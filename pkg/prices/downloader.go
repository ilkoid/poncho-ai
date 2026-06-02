package prices

import (
	"context"
	"fmt"
	"time"
)

const defaultPageSize = 1000 // WB Discounts-Prices API max per page

// Downloader is a reusable prices downloader.
// Depends on PricesSource (WB API) and PricesWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := prices.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source PricesSource
	writer PricesWriter
	opts   DownloadOptions
}

// NewDownloader creates a prices downloader from a PricesSource and PricesWriter.
func NewDownloader(source PricesSource, writer PricesWriter, opts DownloadOptions) *Downloader {
	if opts.PageSize <= 0 {
		opts.PageSize = defaultPageSize
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run downloads product prices using offset-based pagination.
//
// Pagination loop:
//  1. Start with offset=0, pageSize=1000
//  2. source.GetPrices(ctx, pageSize, offset)
//  3. writer.SavePrices(ctx, prices, snapshotDate) — unless dry-run
//  4. offset += len(prices)
//  5. Break when len(prices) < pageSize (last page)
//
// Continue-on-error: fetch failures abort the run (prices are a snapshot,
// partial data is worse than retry). Context cancellation is checked each page.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	offset := 0

	for {
		// Check cancellation
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		result.Requests++
		pageNum := result.Requests
		tStart := time.Now()

		// Fetch page from API
		prices, count, err := d.source.GetPrices(ctx, d.opts.PageSize, offset)
		if err != nil {
			result.Duration = time.Since(start)
			return result, fmt.Errorf("fetch prices page %d (offset %d): %w", pageNum, offset, err)
		}

		elapsed := time.Since(tStart).Round(time.Millisecond)

		if len(prices) == 0 {
			break
		}

		// Save to database (unless dry-run)
		if !d.opts.DryRun {
			n, err := d.writer.SavePrices(ctx, prices, d.opts.SnapshotDate)
			if err != nil {
				result.Duration = time.Since(start)
				return result, fmt.Errorf("save prices page %d: %w", pageNum, err)
			}
			result.TotalProducts += n
			d.progress("%d prices saved, offset %d (%s)", n, offset, elapsed)
		} else {
			result.TotalProducts += count
			d.progress("%d prices skipped — dry-run, offset %d (%s)", count, offset, elapsed)
		}

		result.Pages++

		// Last page — less than pageSize means we're done
		if count < d.opts.PageSize {
			break
		}

		offset += count
	}

	result.Duration = time.Since(start)
	return result, nil
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
