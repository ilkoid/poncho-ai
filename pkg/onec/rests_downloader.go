package onec

import (
	"context"
	"fmt"
	"time"
)

// RestsDownloader orchestrates the 1C RESTs download (single step).
// All fields are interfaces — no concrete types from storage or API packages.
type RestsDownloader struct {
	source RestsSource
	writer RestsWriter
	opts   RestsDownloadOptions
}

// NewRestsDownloader creates a RestsDownloader with the given source, writer, and options.
func NewRestsDownloader(source RestsSource, writer RestsWriter, opts RestsDownloadOptions) *RestsDownloader {
	return &RestsDownloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the rests download: clean (optional) → fetch+save → purge old snapshots.
// Returns the result with counts and duration.
func (d *RestsDownloader) Run(ctx context.Context) (*RestsDownloadResult, error) {
	start := time.Now()
	result := &RestsDownloadResult{}

	// Clean table if requested
	if d.opts.Clean && !d.opts.DryRun {
		if err := d.writer.CleanRests(ctx); err != nil {
			return nil, fmt.Errorf("clean rests: %w", err)
		}
		d.progress("Table cleaned")
	}

	// For DryRun: use DiscardWriter so streaming decode still runs but nothing is persisted.
	// The real writer is used for CountRests at the end (count from existing data).
	writer := d.writer
	if d.opts.DryRun {
		writer = NewRestsDiscardWriter()
	}

	// Fetch + stream-save
	goodsCount, totalSaved, filteredOut, err := d.source.FetchRests(
		ctx, d.opts.RestURL, d.opts.StorageFilter, writer, d.opts.SnapshotDate)
	if err != nil {
		return nil, fmt.Errorf("fetch rests: %w", err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	result.GoodsCount = goodsCount
	result.TotalSaved = totalSaved
	result.FilteredOut = filteredOut

	d.progress(fmt.Sprintf("Saved %d rows from %d goods (filtered out: %d)",
		totalSaved, goodsCount, filteredOut))

	// Purge old snapshots (skip after clean — table is already empty)
	if d.opts.RetentionDays > 0 && !d.opts.Clean && !d.opts.DryRun {
		purged, err := d.writer.PurgeOldRestsSnapshots(ctx, d.opts.RetentionDays)
		if err != nil {
			return nil, fmt.Errorf("purge old snapshots: %w", err)
		}
		if purged > 0 {
			d.progress(fmt.Sprintf("Purged %d old snapshot rows (retention: %d days)", purged, d.opts.RetentionDays))
		}
	}

	// Final count
	totalInDB, err := d.writer.CountRests(ctx)
	if err != nil {
		return nil, fmt.Errorf("count rests: %w", err)
	}
	result.TotalInDB = totalInDB

	result.Duration = time.Since(start)
	return result, nil
}

// progress calls the OnProgress callback if set.
func (d *RestsDownloader) progress(msg string) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(msg)
	}
}
