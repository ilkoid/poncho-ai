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
func (d *Downloader) runPositionsPhase(ctx context.Context, result *DownloadResult) error {
	d.progress("phase 1: search positions (%d batches)", (len(d.opts.NmIDs)+PositionsBatchSize-1)/PositionsBatchSize)

	totalBatches := (len(d.opts.NmIDs) + PositionsBatchSize - 1) / PositionsBatchSize

	for i := 0; i < len(d.opts.NmIDs); i += PositionsBatchSize {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := min(i+PositionsBatchSize, len(d.opts.NmIDs))
		batch := d.opts.NmIDs[i:end]
		batchNum := i/PositionsBatchSize + 1

		rows, err := d.source.FetchPositions(ctx, PositionsRequest{
			NmIDs: batch,
			Begin: d.opts.BeginDate,
			End:   d.opts.EndDate,
		})
		if err != nil {
			d.progress("error: positions batch %d/%d (nmIDs %d-%d): %v", batchNum, totalBatches, i+1, end, err)
			result.Errors++
			continue
		}

		if d.opts.DryRun {
			result.PositionRows += len(rows)
		} else if len(rows) > 0 {
			saved, err := d.writer.SaveSearchPositions(ctx, rows)
			if err != nil {
				return fmt.Errorf("save positions (batch %d/%d): %w", batchNum, totalBatches, err)
			}
			result.PositionRows += saved
		}

		d.progress("positions: batch %d/%d, %d rows (saved so far: %d)", batchNum, totalBatches, len(rows), result.PositionRows)
	}

	d.progress("positions done: %d rows, %d errors", result.PositionRows, result.Errors)
	return nil
}

// runQueriesPhase downloads search query snapshots in batches of 50.
// Like the positions phase, each batch is saved immediately.
func (d *Downloader) runQueriesPhase(ctx context.Context, result *DownloadResult) error {
	d.progress("phase 2: search queries (%d batches, limit=%d)", (len(d.opts.NmIDs)+QueryBatchSize-1)/QueryBatchSize, d.opts.QueryLimit)

	totalBatches := (len(d.opts.NmIDs) + QueryBatchSize - 1) / QueryBatchSize

	for i := 0; i < len(d.opts.NmIDs); i += QueryBatchSize {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := min(i+QueryBatchSize, len(d.opts.NmIDs))
		batch := d.opts.NmIDs[i:end]
		batchNum := i/QueryBatchSize + 1

		rows, err := d.source.FetchSearchTexts(ctx, TextsRequest{
			NmIDs: batch,
			Begin: d.opts.BeginDate,
			End:   d.opts.EndDate,
			Limit: d.opts.QueryLimit,
		})
		if err != nil {
			d.progress("error: queries batch %d/%d (nmIDs %d-%d): %v", batchNum, totalBatches, i+1, end, err)
			result.Errors++
			continue
		}

		if d.opts.DryRun {
			result.QueryRows += len(rows)
		} else if len(rows) > 0 {
			saved, err := d.writer.SaveSearchQueries(ctx, rows)
			if err != nil {
				return fmt.Errorf("save queries (batch %d/%d): %w", batchNum, totalBatches, err)
			}
			result.QueryRows += saved
		}

		d.progress("queries: batch %d/%d, %d rows (saved so far: %d)", batchNum, totalBatches, len(rows), result.QueryRows)
	}

	d.progress("queries done: %d rows, %d errors", result.QueryRows, result.Errors)
	return nil
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
