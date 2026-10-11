// download-api-specs обновляет OpenAPI/Swagger спеки WB и Ozon в docs/.
//
// Каналы:
//   - HTTP: прямой GET YAML-файлов dev.wildberries.ru (браузерный UA,
//     ретраи; при антибот-челлендже файл уходит в браузерную очередь);
//   - CDP: настоящий браузер (Edge) с постоянным профилем — проходит
//     челленджи и перехватывает спеку, которую docs.ozon.ru грузит сам.
//
// Утилита только обновляет файлы и печатает отчёт. Git-коммит — руками.
//
// Usage:
//
//	go run .                                    # HTTP → фолбэк CDP
//	go run . --http-only                        # только прямой HTTP
//	go run . --cdp-only                         # только браузер (проверка канала)
//	go run . --dry-run                          # скачать, не записывать
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ilkoid/poncho-ai/pkg/apispecs"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dryRun := flag.Bool("dry-run", false, "Скачать и показать отчёт, файлы не записывать")
	httpOnly := flag.Bool("http-only", false, "Только прямой HTTP, без браузера")
	cdpOnly := flag.Bool("cdp-only", false, "Только браузерный канал (форс-проверка CDP)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var cfg apispecs.Config
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg = cfg.GetDefaults()

	repoRoot, err := findRepoRoot(*configPath)
	if err != nil {
		log.Fatalf("repo root: %v", err)
	}

	dllog.PrintHeader("API Specs Downloader",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "RepoRoot", Value: repoRoot},
		dllog.HeaderField{Key: "Targets", Value: fmt.Sprintf("%d", len(cfg.Targets))},
		dllog.HeaderField{Key: "Mode", Value: modeName(*httpOnly, *cdpOnly, *dryRun)},
	)

	runner := &apispecs.Runner{
		HTTP:     apispecs.NewHTTPFetcher(cfg.HTTP),
		RepoRoot: repoRoot,
		DryRun:   *dryRun,
		HTTPOnly: *httpOnly,
		CDPOnly:  *cdpOnly,
		Logf:     dllog.Log,
		CDP: func() (apispecs.FileGetter, apispecs.Capturer, func(), error) {
			f := apispecs.NewCDPFetcher(cfg.CDP)
			if err := f.Start(ctx); err != nil {
				return nil, nil, nil, err
			}
			return f, f, f.Close, nil
		},
	}

	report, err := runner.Run(ctx, cfg.Targets)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	printResults(report)
	if failed := report.Failed(); failed > 0 {
		dllog.Error("%d файл(ов) не обновлено — см. лог выше", failed)
		os.Exit(1)
	}
	dllog.Done(report.Duration, "%d updated, %d всего",
		report.Updated(), len(report.Results))
}

func printResults(report apispecs.Report) {
	for _, res := range report.Results {
		note := ""
		if res.Err != nil {
			note = fmt.Sprintf(" (%v)", res.Err)
		}
		switch res.Status {
		case apispecs.StatusUpdated:
			dllog.Log("✔ %s/%s: %d → %d байт%s", res.Target, res.Name, res.OldSize, res.NewSize, note)
		case apispecs.StatusUnchanged:
			dllog.Log("= %s/%s: без изменений (%d байт)", res.Target, res.Name, res.OldSize)
		case apispecs.StatusSkipped:
			dllog.Log("– %s/%s: пропущено%s", res.Target, res.Name, note)
		default:
			dllog.Error("✘ %s/%s%s", res.Target, res.Name, note)
		}
	}
}

func modeName(httpOnly, cdpOnly, dryRun bool) string {
	switch {
	case httpOnly:
		return "http-only"
	case cdpOnly:
		return "cdp-only"
	case dryRun:
		return "dry-run"
	default:
		return "http+cdp"
	}
}

// findRepoRoot идёт вверх от каталога конфига до каталога с go.mod —
// относительные пути outputs в конфиге считаются от корня репо.
func findRepoRoot(configPath string) (string, error) {
	dir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", configPath)
		}
		dir = parent
	}
}
