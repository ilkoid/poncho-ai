package apispecs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Статусы файла в отчёте.
type Status string

const (
	StatusUpdated   Status = "updated"
	StatusUnchanged Status = "unchanged"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped"
)

// FileResult — итог по одному выходному файлу.
type FileResult struct {
	Target  string
	Name    string
	Path    string
	Status  Status
	OldSize int64
	NewSize int64
	Err     error
}

// Report — сводка прогона.
type Report struct {
	Results  []FileResult
	Duration time.Duration
}

func (r Report) Updated() (n int) {
	for _, res := range r.Results {
		if res.Status == StatusUpdated {
			n++
		}
	}
	return n
}

func (r Report) Failed() (n int) {
	for _, res := range r.Results {
		if res.Status == StatusFailed {
			n++
		}
	}
	return n
}

// CDPFactory создаёт браузерный канал лениво — только когда он понадобился.
// Возвращает getter (файлы), capturer (перехват спек) и cleanup.
// В тестах подставляется заглушка.
type CDPFactory func() (FileGetter, Capturer, func(), error)

// Runner — оркестрация: сперва HTTP по всем файлам, челленджи и capture
// добираются одним подъёмом браузера. Запись атомарная; при ошибке старый
// файл сохраняется; git не трогается.
type Runner struct {
	HTTP     *HTTPFetcher
	CDP      CDPFactory
	RepoRoot string
	DryRun   bool
	HTTPOnly bool
	CDPOnly  bool
	Logf     func(format string, args ...any)
}

// cdpTask — элемент очереди браузерного этапа: либо файл, либо capture.
type cdpTask struct {
	target  Target
	file    *FileSpec
	capture *CaptureSpec
}

// Run выполняет все targets из конфига.
func (r *Runner) Run(ctx context.Context, targets []Target) (Report, error) {
	if r.Logf == nil {
		r.Logf = func(string, ...any) {}
	}
	logf := r.Logf
	start := time.Now()
	report := Report{}
	var queue []cdpTask

	httpFetcher := r.HTTP
	if r.CDPOnly {
		httpFetcher = nil // браузерный канал форсируется целиком
	}

	for _, target := range targets {
		for i := range target.Files {
			file := target.Files[i]
			if httpFetcher == nil {
				queue = append(queue, cdpTask{target: target, file: &file})
				continue
			}
			res := FileResult{Target: target.Name, Name: filepath.Base(file.Path), Path: file.Path}
			body, err := httpFetcher.Get(ctx, file.URL)
			switch {
			case errors.Is(err, ErrChallenge):
				logf("apispecs: %s: HTTP попал на челлендж — в очередь CDP", file.URL)
				queue = append(queue, cdpTask{target: target, file: &file})
				continue
			case err != nil:
				res.Status = StatusFailed
				res.Err = err
			case r.DryRun:
				res.Status = StatusSkipped
				res.NewSize = int64(len(body))
				res.Err = errors.New("dry-run: файл не записан")
			default:
				res.NewSize = int64(len(body))
				if serr := r.save(file.Path, body, &res); serr != nil {
					res.Status = StatusFailed
					res.Err = serr
				}
			}
			report.Results = append(report.Results, res)
			sleepCtx(ctx, httpFetcher.Pause)
		}

		if capSpec := target.Capture; capSpec != nil {
			res := FileResult{Target: target.Name, Name: filepath.Base(capSpec.Outputs[0]), Path: capSpec.Outputs[0]}
			switch {
			case r.HTTPOnly:
				res.Status = StatusSkipped
				res.Err = errors.New("--http-only: capture требует браузер")
			case r.DryRun:
				res.Status = StatusSkipped
				res.Err = errors.New("dry-run: capture не выполняется")
			default:
				queue = append(queue, cdpTask{target: target, capture: capSpec})
				continue
			}
			report.Results = append(report.Results, res)
		}
	}

	// Браузерный этап: один подъём на все хвосты.
	if len(queue) > 0 && !r.HTTPOnly && !r.DryRun {
		getter, capturer, cleanup, err := r.CDP()
		if err != nil {
			for _, task := range queue {
				report.Results = append(report.Results, r.failedRows(task, fmt.Errorf("cdp: %w", err))...)
			}
		} else {
			for _, task := range queue {
				for _, res := range r.runCDPTask(ctx, getter, capturer, task) {
					report.Results = append(report.Results, res)
				}
			}
			cleanup()
		}
	} else if len(queue) > 0 {
		// Очередь не обрабатывалась (--http-only / dry-run) — не теряем строки отчёта.
		reason := "--http-only: браузерный канал отключён"
		if r.DryRun {
			reason = "dry-run"
		}
		for _, task := range queue {
			for _, res := range r.failedRows(task, errors.New(reason)) {
				res.Status = StatusSkipped
				report.Results = append(report.Results, res)
			}
		}
	}

	report.Duration = time.Since(start)
	return report, nil
}

// runCDPTask добирает один элемент браузерным каналом.
func (r *Runner) runCDPTask(ctx context.Context, getter FileGetter, capturer Capturer, task cdpTask) []FileResult {
	if task.file != nil {
		res := FileResult{Target: task.target.Name, Name: filepath.Base(task.file.Path), Path: task.file.Path}
		body, err := getter.Get(ctx, task.file.URL)
		if err != nil {
			res.Status = StatusFailed
			res.Err = err
			return []FileResult{res}
		}
		res.NewSize = int64(len(body))
		if err := r.save(task.file.Path, body, &res); err != nil {
			res.Status = StatusFailed
			res.Err = err
		}
		return []FileResult{res}
	}

	// Capture: одно скачивание → N одинаковых копий.
	cap := task.capture
	body, err := capturer.Capture(ctx, *cap, r.Logf)
	if err != nil {
		return r.failedRows(task, err)
	}
	results := make([]FileResult, 0, len(cap.Outputs))
	for _, out := range cap.Outputs {
		res := FileResult{Target: task.target.Name, Name: filepath.Base(out), Path: out, NewSize: int64(len(body))}
		if err := r.save(out, body, &res); err != nil {
			res.Status = StatusFailed
			res.Err = err
		}
		results = append(results, res)
	}
	return results
}

// failedRows — строки отчёта для задачи, упавшей целиком.
func (r *Runner) failedRows(task cdpTask, err error) []FileResult {
	mk := func(name, path string) FileResult {
		return FileResult{Target: task.target.Name, Name: name, Path: path, Status: StatusFailed, Err: err}
	}
	if task.file != nil {
		return []FileResult{mk(filepath.Base(task.file.Path), task.file.Path)}
	}
	rows := make([]FileResult, 0, len(task.capture.Outputs))
	for _, out := range task.capture.Outputs {
		rows = append(rows, mk(filepath.Base(out), out))
	}
	return rows
}

// save атомарно пишет body в path (относительные пути — от RepoRoot).
// Если содержимое не изменилось — StatusUnchanged, файл не трогается.
func (r *Runner) save(path string, body []byte, res *FileResult) error {
	res.Path = path
	abs := r.resolve(path)

	if old, err := os.ReadFile(abs); err == nil {
		res.OldSize = int64(len(old))
		res.NewSize = int64(len(body))
		if sha256.Sum256(old) == sha256.Sum256(body) {
			res.Status = StatusUnchanged
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".spec-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close: %w", err)
	}
	// CreateTemp даёт 0600 — выравниваем к обычным правам репо.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	res.Status = StatusUpdated
	return nil
}

func (r *Runner) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(r.RepoRoot, path)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
