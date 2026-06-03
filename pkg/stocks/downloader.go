package stocks

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxStocksPageSize is the maximum items per page from the stocks warehouse API.
	MaxStocksPageSize = 250000

	// ToolID matches the rate limiter key in pkg/wb.
	ToolID = "get_stocks_warehouses"
)

// Downloader is a reusable stock snapshot downloader.
// Depends on StocksSource (WB API) and StocksWriter (persistence) — both are interfaces.
type Downloader struct {
	source StocksSource
	writer StocksWriter
	opts   DownloadOptions
}

// NewDownloader creates a stock snapshot downloader from a StocksSource and StocksWriter.
func NewDownloader(source StocksSource, writer StocksWriter, opts DownloadOptions) *Downloader {
	if opts.RateLimit <= 0 {
		opts.RateLimit = 3
	}
	if opts.Burst <= 0 {
		opts.Burst = 1
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full stock snapshot download pipeline.
// Downloads all pages via offset-based pagination and saves to the writer.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	snapshotDate := d.opts.SnapshotDate
	if snapshotDate == "" {
		snapshotDate = time.Now().Format("2006-01-02")
	}
	d.progress("📅 Snapshot date: %s", snapshotDate)

	// Gap detection (informational — does not backfill)
	if d.opts.FirstDate != "" {
		d.detectGaps(ctx, d.opts.FirstDate)
	}

	// Download all pages
	total := 0
	offset := 0

	for {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		items, err := d.source.GetStockWarehouses(ctx, MaxStocksPageSize, offset, d.opts.RateLimit, d.opts.Burst)
		if err != nil {
			d.progress("❌ Error at offset=%d: %v", offset, err)
			return result, fmt.Errorf("download page offset=%d: %w", offset, err)
		}

		if len(items) == 0 {
			break
		}

		if d.opts.DryRun {
			result.TotalRows += len(items)
			result.Pages++
			d.progress("  [dry-run] offset=%d: %d items", offset, len(items))
		} else {
			n, err := d.writer.SaveStocks(ctx, snapshotDate, items)
			if err != nil {
				d.progress("❌ Save at offset=%d: %v", offset, err)
				return result, fmt.Errorf("save stocks offset=%d: %w", offset, err)
			}
			total += n
			result.Pages++
			d.progress("  offset=%d: %d items (saved %d)", offset, len(items), n)
		}

		// Less than full page → last page
		if len(items) < MaxStocksPageSize {
			break
		}
		offset += MaxStocksPageSize
	}

	if !d.opts.DryRun {
		result.TotalRows = total
	}

	result.Duration = time.Since(start)
	return result, nil
}

// detectGaps checks for missing snapshot dates between firstDate and yesterday.
// Purely informational — does NOT backfill. Reports gaps via OnProgress.
func (d *Downloader) detectGaps(ctx context.Context, firstDate string) {
	existingDates, err := d.writer.GetDistinctSnapshotDates(ctx)
	if err != nil {
		d.progress("⚠️  Gap detection failed: %v", err)
		return
	}

	if len(existingDates) == 0 {
		return
	}

	// Build set of existing dates
	existSet := make(map[string]struct{}, len(existingDates))
	for _, dt := range existingDates {
		existSet[dt] = struct{}{}
	}

	// Generate expected dates from firstDate to yesterday
	start, err := time.Parse("2006-01-02", firstDate)
	if err != nil {
		d.progress("⚠️  Invalid first_date %q: %v", firstDate, err)
		return
	}
	yesterday := time.Now().AddDate(0, 0, -1)

	var gaps []string
	for dt := start; !dt.After(yesterday); dt = dt.AddDate(0, 0, 1) {
		ds := dt.Format("2006-01-02")
		if _, ok := existSet[ds]; !ok {
			gaps = append(gaps, ds)
		}
	}

	if len(gaps) == 0 {
		d.progress("  ✅ No gaps detected")
		return
	}

	d.progress("  ⚠️  %d missing snapshot dates", len(gaps))
	switch {
	case len(gaps) <= 15:
		d.progress("    %s", strings.Join(gaps, ", "))
	default:
		d.progress("    %s", strings.Join(gaps[:10], ", "))
		d.progress("    ... (%d more) ...", len(gaps)-15)
		d.progress("    %s", strings.Join(gaps[len(gaps)-5:], ", "))
	}
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
