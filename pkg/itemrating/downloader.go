package itemrating

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable item rating downloader.
// Depends on Source (WB API) and Writer (persistence) — both are interfaces.
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates an item rating downloader from a Source and Writer.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	if opts.PageSize <= 0 || opts.PageSize > PageSizeMax {
		opts.PageSize = PageSizeMax
	}
	if opts.OrderByField == "" {
		opts.OrderByField = DefaultOrderByField
	}
	if opts.OrderByMode == "" {
		opts.OrderByMode = DefaultOrderByMode
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = 3 // swagger: 3 req/min
	}
	if opts.Burst <= 0 {
		opts.Burst = 3
	}
	return &Downloader{source: source, writer: writer, opts: opts}
}

// Run executes the full item rating download pipeline.
//
// Algorithm:
//  1. Offset pagination loop over the whole catalog (no filters — full capture).
//  2. Save each page immediately (upserts make re-runs idempotent).
//  3. Exit on empty response OR len(items) < pageSize.
//  4. Post-run completeness check: stored row count vs loaded count
//     (mismatch is informational — re-runs and hourly drift collapse into upserts).
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	d.progress("📅 Period: %s → %s", d.opts.PeriodStart, d.opts.PeriodEnd)
	if d.opts.PastStart != "" {
		d.progress("📅 Past:   %s → %s", d.opts.PastStart, d.opts.PastEnd)
	}

	var fatalErr error

	for offset := 0; ; offset += d.opts.PageSize {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		pageNum := offset/d.opts.PageSize + 1
		d.progress("📄 Page %d (offset=%d, loaded=%d)", pageNum, offset, result.ItemsLoaded)

		page, err := d.source.FetchPage(ctx, d.buildRequest(offset))
		if err != nil {
			d.progress("❌ API error: %v", err)
			fatalErr = fmt.Errorf("fetch page %d: %w", pageNum, err)
			result.Errors++
			break
		}

		if page.APICount == 0 {
			if pageNum == 1 {
				d.progress("ℹ️  No data for the specified period")
			} else {
				d.progress("✅ All data loaded")
			}
			break
		}

		saved := page.APICount
		if !d.opts.DryRun {
			saved, err = d.writer.SaveItemRatings(ctx, page.Items)
			if err != nil {
				d.progress("❌ Save error: %v", err)
				fatalErr = fmt.Errorf("save items page %d: %w", pageNum, err)
				result.Errors++
				break
			}
			if page.Seller != nil {
				if _, err := d.writer.SaveSellerRating(ctx, *page.Seller); err != nil {
					d.progress("❌ Seller block save error: %v", err)
					fatalErr = fmt.Errorf("save seller block page %d: %w", pageNum, err)
					result.Errors++
					break
				}
			}
		}

		result.ItemsLoaded += saved
		result.PagesLoaded++
		if saved != page.APICount {
			d.progress("⚠️  API: %d, saved: %d", page.APICount, saved)
		}

		if page.APICount < d.opts.PageSize {
			d.progress("✅ Last page loaded (%d items)", page.APICount)
			break
		}
	}

	// Completeness check (informational, not a failure signal).
	if fatalErr == nil && !d.opts.DryRun {
		if n, err := d.writer.CountItemRatings(ctx, d.opts.PeriodStart, d.opts.PeriodEnd); err == nil {
			result.DBCount = n
			if n != result.ItemsLoaded {
				d.progress("⚠️  Completeness: loaded %d, stored distinct %d (re-run/drift upserts)", result.ItemsLoaded, n)
			}
		}
	}

	result.Duration = time.Since(start)
	return result, fatalErr
}

// buildRequest constructs the API request for the given offset.
// No filters: isNotIncludeNmsWithoutSales=false, onlyShadowedNms=false —
// full catalog including items without sales and shadowed ones.
func (d *Downloader) buildRequest(offset int) wb.ItemRatingRequest {
	req := wb.ItemRatingRequest{
		CurrentPeriod: wb.PeriodRange{Start: d.opts.PeriodStart, End: d.opts.PeriodEnd},
		OrderBy:       wb.OrderBy{Field: d.opts.OrderByField, Mode: d.opts.OrderByMode},
		Limit:         d.opts.PageSize,
		Offset:        offset,
	}
	if d.opts.PastStart != "" && d.opts.PastEnd != "" {
		req.PastPeriod = &wb.PeriodRange{Start: d.opts.PastStart, End: d.opts.PastEnd}
	}
	return req
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
