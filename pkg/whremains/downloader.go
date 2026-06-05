package whremains

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader handles the async warehouse remains report lifecycle.
// Depends on WhRemainsSource (API) and WhRemainsWriter (persistence) — both are interfaces.
type Downloader struct {
	source WhRemainsSource
	writer WhRemainsWriter
	opts   DownloadOptions
}

// NewDownloader creates a warehouse remains downloader from source and writer.
func NewDownloader(source WhRemainsSource, writer WhRemainsWriter, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full async warehouse remains download pipeline:
//  1. Resolve snapshot date
//  2. Check resume (skip if today already has data)
//  3. Create report task
//  4. Poll until ready
//  5. Download data
//  6. Flatten nested items → flat rows
//  7. Save to DB (or dry-run)
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	snapshotDate := d.opts.SnapshotDate
	if snapshotDate == "" {
		snapshotDate = time.Now().Format("2006-01-02")
	}
	d.progress("📅 Snapshot date: %s", snapshotDate)

	// Resume: skip if data already exists for today
	count, err := d.writer.CountRemainsForDate(ctx, snapshotDate)
	if err != nil {
		d.progress("⚠️  Resume check failed: %v", err)
	} else if count > 0 {
		d.progress("✅ Resumed: %d rows already exist for %s", count, snapshotDate)
		result.Status = "RESUMED"
		result.TotalRows = count
		result.Duration = time.Since(start)
		return result, nil
	}

	// Step 1: Create report
	taskID, err := d.source.CreateReport(ctx, d.opts.Params)
	if err != nil {
		return result, fmt.Errorf("create report: %w", err)
	}
	result.TaskID = taskID
	d.progress("📋 Report created: %s", taskID)

	// Step 2: Poll until ready
	d.progress("⏳ Polling status (interval=%ds, timeout=%dm)...", d.opts.PollIntervalSec, d.opts.PollTimeoutMin)
	status, err := d.poll(ctx, taskID)
	if err != nil {
		return result, fmt.Errorf("poll report %s: %w", taskID, err)
	}
	d.progress("✅ Report ready: status=%s", status)

	// Step 3: Download data
	items, err := d.source.DownloadReport(ctx, taskID)
	if err != nil {
		return result, fmt.Errorf("download report %s: %w", taskID, err)
	}
	d.progress("📦 Downloaded: %d items", len(items))

	// Step 4: Flatten nested warehouses
	flatRows := flattenRemains(items)
	d.progress("🔀 Flattened: %d items → %d rows", len(items), len(flatRows))

	// Step 5: Save or dry-run
	if d.opts.DryRun {
		result.Status = "SUCCESS"
		result.TotalRows = len(flatRows)
		result.Duration = time.Since(start)
		return result, nil
	}

	saved, err := d.writer.SaveRemains(ctx, snapshotDate, flatRows)
	if err != nil {
		return result, fmt.Errorf("save remains: %w", err)
	}

	result.Status = "SUCCESS"
	result.TotalRows = saved
	result.Duration = time.Since(start)
	return result, nil
}

// poll waits for the report to reach a terminal state.
// Terminal states: done (success), purged/canceled (error).
// Non-terminal: new, processing (sleep and retry).
func (d *Downloader) poll(ctx context.Context, taskID string) (string, error) {
	interval := time.Duration(d.opts.PollIntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := time.Duration(d.opts.PollTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		status, err := d.source.PollStatus(ctx, taskID)
		if err != nil {
			return "", fmt.Errorf("poll status: %w", err)
		}

		switch status {
		case wb.WrStatusDone:
			return status, nil
		case wb.WrStatusPurged:
			return "", fmt.Errorf("report purged")
		case wb.WrStatusCanceled:
			return "", fmt.Errorf("report canceled")
		case wb.WrStatusNew, wb.WrStatusProcessing:
			d.progress("  ⏳ Status: %s", status)
		default:
			d.progress("  ⚠️  Unknown status: %s", status)
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("poll timeout after %v", timeout)
		}

		time.Sleep(interval)
	}
}

// flattenRemains transforms nested API items into flat storage rows.
// Each item has warehouses[] → one flat row per (item × warehouse).
// Items with no warehouses are skipped.
func flattenRemains(items []wb.WarehouseRemainsItem) []WhRemainsFlatRow {
	// Pre-allocate: most items have 2-5 warehouses
	rows := make([]WhRemainsFlatRow, 0, len(items)*3)
	for _, item := range items {
		for _, wh := range item.Warehouses {
			rows = append(rows, WhRemainsFlatRow{
				Brand:         item.Brand,
				SubjectName:   item.SubjectName,
				VendorCode:    item.VendorCode,
				NmID:          item.NmID,
				Barcode:       item.Barcode,
				TechSize:      item.TechSize,
				Volume:        item.Volume,
				WarehouseName: wh.WarehouseName,
				Quantity:      wh.Quantity,
			})
		}
	}
	return rows
}

// progress emits a progress message via the OnProgress callback.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
