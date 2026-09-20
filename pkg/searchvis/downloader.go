package searchvis

import (
	"context"
	"fmt"
	"time"
)

// Downloader is a reusable search visibility downloader.
// Depends on Source (WB API) and Writer (persistence) — both are interfaces.
// CLI resolves nmIDs via Reader before creating Downloader.
//
// Usage:
//
//	dl := searchvis.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates a search visibility downloader from source, writer, and options.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the 2-phase search visibility download:
//
//	Phase 1: Positions — POST /api/v2/search-report/report (batch 100 nmIDs) → SaveSearchPositions
//	Phase 2: Queries  — POST /api/v2/search-report/product/search-texts (batch 50 nmIDs) → SaveSearchQueries
//
// Continue-on-error: individual batch failures increment result.Errors.
// Context cancellation is checked before each batch.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	if len(d.opts.NmIDs) == 0 {
		d.progress("no nmIDs provided, nothing to download")
		result.Duration = time.Since(start)
		return result, nil
	}

	d.progress("downloading %d products, period %s → %s", len(d.opts.NmIDs), d.opts.BeginDate, d.opts.EndDate)

	// ── Phase 1: Search Positions ────────────────────────────────────
	if !d.opts.SkipPositions {
		if err := d.runPositionsPhase(ctx, result); err != nil {
			result.Duration = time.Since(start)
			return result, err
		}
	} else {
		d.progress("skipping positions phase")
	}

	// ── Phase 2: Search Queries ──────────────────────────────────────
	if !d.opts.SkipQueries {
		if err := d.runQueriesPhase(ctx, result); err != nil {
			result.Duration = time.Since(start)
			return result, err
		}
	} else {
		d.progress("skipping queries phase")
	}

	result.Duration = time.Since(start)
	return result, nil
}

// runPositionsPhase downloads search position snapshots in batches of 100.
//
// Each batch is saved immediately: a run interrupted mid-phase keeps everything
// already fetched (upserts make re-runs idempotent), instead of losing the
// whole phase like the former buffer-then-save-at-end design.
//
// Batches that exhaust the client's inner retries are not retried in place —
// they are collected and refetched by rescueLoop after the main sweep.
func (d *Downloader) runPositionsPhase(ctx context.Context, result *DownloadResult) error {
	totalBatches := (len(d.opts.NmIDs) + PositionsBatchSize - 1) / PositionsBatchSize
	d.progress("phase 1: search positions (%d batches)", totalBatches)

	var failedStarts []int

	for i := 0; i < len(d.opts.NmIDs); i += PositionsBatchSize {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		failed, err := d.processPositionsBatch(ctx, i, result)
		if err != nil {
			return err
		}
		if failed {
			failedStarts = append(failedStarts, i)
		}
	}

	lost, err := d.rescueLoop(ctx, "positions", failedStarts, PositionsBatchSize,
		func(ctx context.Context, start int) (bool, error) {
			return d.processPositionsBatch(ctx, start, result)
		})
	result.Errors += lost
	if err != nil {
		return err
	}

	d.progress("positions done: %d rows, %d errors", result.PositionRows, result.Errors)
	return nil
}

// processPositionsBatch fetches and saves one positions batch (start — index in opts.NmIDs).
// Returns failed=true on fetch error (batch goes to rescue); err != nil is a
// fatal save error that stops the phase.
func (d *Downloader) processPositionsBatch(ctx context.Context, start int, result *DownloadResult) (bool, error) {
	end := min(start+PositionsBatchSize, len(d.opts.NmIDs))
	batch := d.opts.NmIDs[start:end]
	batchNum := start/PositionsBatchSize + 1
	totalBatches := (len(d.opts.NmIDs) + PositionsBatchSize - 1) / PositionsBatchSize

	rows, err := d.source.FetchPositions(ctx, PositionsRequest{
		NmIDs: batch,
		Begin: d.opts.BeginDate,
		End:   d.opts.EndDate,
	})
	if err != nil {
		d.progress("error: positions batch %d/%d (nmIDs %d-%d): %v", batchNum, totalBatches, start+1, end, err)
		return true, nil
	}

	if d.opts.DryRun {
		result.PositionRows += len(rows)
	} else if len(rows) > 0 {
		saved, err := d.writer.SaveSearchPositions(ctx, rows)
		if err != nil {
			return false, fmt.Errorf("save positions (batch %d/%d): %w", batchNum, totalBatches, err)
		}
		result.PositionRows += saved
	}

	d.progress("positions: batch %d/%d, %d rows (saved so far: %d)", batchNum, totalBatches, len(rows), result.PositionRows)
	return false, nil
}

// runQueriesPhase downloads search query snapshots in batches of 50.
// Like the positions phase, each batch is saved immediately; failed batches
// go to the end-of-phase rescue loop.
func (d *Downloader) runQueriesPhase(ctx context.Context, result *DownloadResult) error {
	totalBatches := (len(d.opts.NmIDs) + QueryBatchSize - 1) / QueryBatchSize
	d.progress("phase 2: search queries (%d batches, limit=%d)", totalBatches, d.opts.QueryLimit)

	var failedStarts []int

	for i := 0; i < len(d.opts.NmIDs); i += QueryBatchSize {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		failed, err := d.processQueriesBatch(ctx, i, result)
		if err != nil {
			return err
		}
		if failed {
			failedStarts = append(failedStarts, i)
		}
	}

	lost, err := d.rescueLoop(ctx, "queries", failedStarts, QueryBatchSize,
		func(ctx context.Context, start int) (bool, error) {
			return d.processQueriesBatch(ctx, start, result)
		})
	result.Errors += lost
	if err != nil {
		return err
	}

	d.progress("queries done: %d rows, %d errors", result.QueryRows, result.Errors)
	return nil
}

// processQueriesBatch fetches and saves one queries batch (start — index in opts.NmIDs).
// Same contract as processPositionsBatch.
func (d *Downloader) processQueriesBatch(ctx context.Context, start int, result *DownloadResult) (bool, error) {
	end := min(start+QueryBatchSize, len(d.opts.NmIDs))
	batch := d.opts.NmIDs[start:end]
	batchNum := start/QueryBatchSize + 1
	totalBatches := (len(d.opts.NmIDs) + QueryBatchSize - 1) / QueryBatchSize

	rows, err := d.source.FetchSearchTexts(ctx, TextsRequest{
		NmIDs: batch,
		Begin: d.opts.BeginDate,
		End:   d.opts.EndDate,
		Limit: d.opts.QueryLimit,
	})
	if err != nil {
		d.progress("error: queries batch %d/%d (nmIDs %d-%d): %v", batchNum, totalBatches, start+1, end, err)
		return true, nil
	}

	if d.opts.DryRun {
		result.QueryRows += len(rows)
	} else if len(rows) > 0 {
		saved, err := d.writer.SaveSearchQueries(ctx, rows)
		if err != nil {
			return false, fmt.Errorf("save queries (batch %d/%d): %w", batchNum, totalBatches, err)
		}
		result.QueryRows += saved
	}

	d.progress("queries: batch %d/%d, %d rows (saved so far: %d)", batchNum, totalBatches, len(rows), result.QueryRows)
	return false, nil
}

// rescueLoop refetches batches that failed the main sweep, in passes, until
// none remain or the time budget runs out (returning the still-failed count).
//
// Дизайн — проходы по списку неудачников, а не лестница ретраев на каждом
// батче: батчи независимы, а шторм 429 бьёт по всем сразу; лестница на N
// мёртвых батчей дала бы +N×лестница к ночному окну. Оверран ограничен:
// ≤ initial sleep + budget + один цикл батча. RescuePassSleep между проходами
// защищает от hot-loop по «ядовитому» батчу (мгновенный 4xx не ретраится
// клиентом). RescueTimeout == 0 выключает долив — поведение как до него.
func (d *Downloader) rescueLoop(
	ctx context.Context,
	label string,
	failedStarts []int,
	batchSize int,
	attempt func(ctx context.Context, start int) (failed bool, fatal error),
) (int, error) {
	if len(failedStarts) == 0 {
		return 0, nil
	}
	if d.opts.RescueTimeout <= 0 {
		return len(failedStarts), nil
	}

	totalBatches := (len(d.opts.NmIDs) + batchSize - 1) / batchSize
	startTime := time.Now()
	deadline := startTime.Add(d.opts.RescueTimeout)
	d.progress("%s rescue: %d/%d batch(es) failed, refetching (budget %v, initial sleep %v)",
		label, len(failedStarts), totalBatches, d.opts.RescueTimeout, d.opts.RescueInitialSleep)

	if err := sleepCtx(ctx, d.opts.RescueInitialSleep); err != nil {
		return len(failedStarts), err
	}

	for pass := 1; len(failedStarts) > 0; pass++ {
		if err := ctx.Err(); err != nil {
			return len(failedStarts), err
		}
		if !time.Now().Before(deadline) {
			d.progress("%s rescue: budget exhausted, %d batch(es) still failing (elapsed %v)",
				label, len(failedStarts), time.Since(startTime).Round(time.Second))
			break
		}

		var still []int
		for _, bs := range failedStarts {
			// Не стартуем новый батч за дедлайном — иначе бюджет не ограничивает оверран.
			if !time.Now().Before(deadline) {
				still = append(still, bs)
				continue
			}
			if err := ctx.Err(); err != nil {
				return len(failedStarts), err
			}
			failed, fatal := attempt(ctx, bs)
			if fatal != nil {
				return len(failedStarts), fatal
			}
			if failed {
				still = append(still, bs)
			}
		}
		d.progress("%s rescue pass %d: recovered %d, still %d (elapsed %v)",
			label, pass, len(failedStarts)-len(still), len(still), time.Since(startTime).Round(time.Second))
		failedStarts = still

		if len(failedStarts) > 0 {
			if err := sleepCtx(ctx, d.opts.RescuePassSleep); err != nil {
				return len(failedStarts), err
			}
		}
	}

	return len(failedStarts), nil
}

// sleepCtx sleeps for dur, aborting on context cancellation.
func sleepCtx(ctx context.Context, dur time.Duration) error {
	if dur <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(dur):
		return nil
	}
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
