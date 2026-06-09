package stockproducts

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	// MaxPageSize is the maximum items per page from the stock products API.
	MaxPageSize = 1000
)

// Downloader is a reusable stock product metrics downloader.
// Depends on StockProductsSource (WB API) and StockProductsWriter (persistence) — both are interfaces.
type Downloader struct {
	source StockProductsSource
	writer StockProductsWriter
	opts   DownloadOptions
}

// NewDownloader creates a stock product metrics downloader.
func NewDownloader(source StockProductsSource, writer StockProductsWriter, opts DownloadOptions) *Downloader {
	if opts.RateLimit <= 0 {
		opts.RateLimit = 3
	}
	if opts.Burst <= 0 {
		opts.Burst = 3
	}
	if opts.PageSize <= 0 {
		opts.PageSize = MaxPageSize
	}
	if opts.PageSize > MaxPageSize {
		opts.PageSize = MaxPageSize
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full stock product metrics download pipeline.
// Downloads all pages via offset-based pagination and saves to the writer.
// Default date is yesterday — today's data is incomplete (WB API updates hourly).
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Resolve snapshot date: default = yesterday
	snapshotDate := d.opts.SnapshotDate
	if snapshotDate == "" {
		snapshotDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}

	// Period defaults to snapshot date
	periodStart := d.opts.PeriodStart
	if periodStart == "" {
		periodStart = snapshotDate
	}
	periodEnd := d.opts.PeriodEnd
	if periodEnd == "" {
		periodEnd = snapshotDate
	}

	d.progress("📅 Snapshot date: %s (period: %s … %s)", snapshotDate, periodStart, periodEnd)

	// Build base request with required fields (empty filters = all products)
	baseReq := wb.StockProductRequest{
		CurrentPeriod: wb.PeriodInv{
			Start: periodStart,
			End:   periodEnd,
		},
		StockType:           "",        // all warehouses
		SkipDeletedNm:       false,     // include deleted
		OrderBy:             wb.TableOrderBy{Field: "ordersCount", Mode: "desc"},
		AvailabilityFilters: []string{}, // empty array — NOT nil (API rejects null)
		Limit:               d.opts.PageSize,
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

		// Clone base request with current offset
		req := baseReq
		req.Offset = offset

		items, err := d.source.GetStockProducts(ctx, req)
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
			d.progress("  [dry-run] page %d: offset=%d, %d items", result.Pages, offset, len(items))
		} else {
			n, err := d.writer.SaveStockProducts(ctx, snapshotDate, items)
			if err != nil {
				d.progress("❌ Save at offset=%d: %v", offset, err)
				return result, fmt.Errorf("save stock products offset=%d: %w", offset, err)
			}
			total += n
			result.Pages++
			d.progress("  page %d: offset=%d, %d items (saved %d)", result.Pages, offset, len(items), n)
		}

		// Less than full page → last page
		if len(items) < d.opts.PageSize {
			break
		}
		offset += d.opts.PageSize
	}

	if !d.opts.DryRun {
		result.TotalRows = total
	}

	result.Duration = time.Since(start)
	return result, nil
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
