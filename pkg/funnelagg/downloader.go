package funnelagg

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable aggregated funnel downloader.
// Depends on Source (WB API) and Writer (persistence) — both are interfaces.
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates an aggregated funnel downloader from a Source and Writer.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	if opts.PageSize <= 0 {
		opts.PageSize = 100
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = 3
	}
	if opts.Burst <= 0 {
		opts.Burst = 3
	}
	if opts.MaxPageRetries <= 0 {
		opts.MaxPageRetries = 3
	}
	if opts.PageRetryBaseSleep <= 0 {
		opts.PageRetryBaseSleep = 2 * time.Minute
	}
	return &Downloader{source: source, writer: writer, opts: opts}
}

// Run executes the full aggregated funnel download pipeline.
//
// Algorithm:
//  1. Estimate progress via writer.GetDistinctNmIDCount() (best-effort).
//  2. Offset resume: skip already-downloaded pages via writer.GetFunnelAggregatedCount().
//  3. Pagination loop: offset += pageSize, page retry on API errors.
//  4. Exit on empty response OR len(products) < pageSize.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Progress estimation (best-effort — may be 0 if no sales data).
	totalProducts := 0
	if count, err := d.writer.GetDistinctNmIDCount(ctx); err == nil && count > 0 {
		totalProducts = count
	}

	d.progress("📅 Period: %s → %s", d.opts.SelectedStart, d.opts.SelectedEnd)
	if d.opts.PastStart != "" && d.opts.PastEnd != "" {
		d.progress("📅 Past:   %s → %s", d.opts.PastStart, d.opts.PastEnd)
	}

	// Offset resume: skip already-downloaded pages.
	offset := 0
	existingCount, resumeErr := d.writer.GetFunnelAggregatedCount(ctx, d.opts.SelectedStart, d.opts.SelectedEnd)
	if resumeErr == nil && existingCount > 0 {
		offset = max(0, existingCount-d.opts.PageSize) // overlap by one page for safety
		d.progress("📎 Resume: %d products already in DB, starting at offset=%d", existingCount, offset)
	}

	totalLoaded := offset

	for {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		req := d.buildRequest(offset)
		pageNum := offset/d.opts.PageSize + 1

		// Progress info
		if totalProducts > 0 {
			percent := float64(totalLoaded) / float64(totalProducts) * 100
			d.progress("📄 Page %d (offset=%d, %d/%d: %.1f%%)",
				pageNum, offset, totalLoaded, totalProducts, percent)
		} else {
			d.progress("📄 Page %d (offset=%d, loaded=%d)", pageNum, offset, totalLoaded)
		}

		// Fetch with page-level retry for global limiter recovery.
		products, currency, err := d.fetchWithRetry(ctx, req)
		if err != nil {
			d.progress("❌ API error after %d retries: %v", d.opts.MaxPageRetries, err)
			break
		}

		apiCount := len(products)
		if apiCount == 0 {
			if pageNum == 1 {
				d.progress("ℹ️  No data for the specified period")
			} else {
				d.progress("✅ All data loaded")
			}
			break
		}

		// Save batch.
		saved := apiCount
		if !d.opts.DryRun {
			saved, err = d.writer.SaveFunnelAggregatedBatch(ctx, products,
				d.opts.SelectedStart, d.opts.SelectedEnd, currency)
			if err != nil {
				d.progress("❌ Save error: %v", err)
				break
			}
		}

		totalLoaded += saved
		result.ProductsLoaded = totalLoaded
		result.PagesLoaded++

		if saved != apiCount {
			d.progress("⚠️  API: %d, saved: %d", apiCount, saved)
		}

		// Last page check.
		if apiCount < d.opts.PageSize {
			d.progress("✅ Last page loaded (%d products)", apiCount)
			break
		}

		offset += d.opts.PageSize
	}

	result.Duration = time.Since(start)
	return result, nil
}

// buildRequest constructs the API request for the given offset.
func (d *Downloader) buildRequest(offset int) wb.FunnelAggregatedRequest {
	req := wb.FunnelAggregatedRequest{
		SelectedPeriod: wb.PeriodRange{
			Start: d.opts.SelectedStart,
			End:   d.opts.SelectedEnd,
		},
		NmIDs:         d.opts.NmIDs,
		BrandNames:    d.opts.BrandNames,
		SubjectIDs:    d.opts.SubjectIDs,
		TagIDs:        d.opts.TagIDs,
		SkipDeletedNm: d.opts.SkipDeletedNm,
		Limit:         d.opts.PageSize,
		Offset:        offset,
	}

	if d.opts.PastStart != "" && d.opts.PastEnd != "" {
		req.PastPeriod = &wb.PeriodRange{
			Start: d.opts.PastStart,
			End:   d.opts.PastEnd,
		}
	}

	if d.opts.OrderByField != "" {
		req.OrderBy = &wb.OrderBy{
			Field: d.opts.OrderByField,
			Mode:  d.opts.OrderByMode,
		}
	}

	return req
}

// fetchWithRetry fetches one page with retries on API errors.
func (d *Downloader) fetchWithRetry(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	var products []wb.FunnelAggregatedProduct
	var currency string
	var lastErr error

	for retry := range d.opts.MaxPageRetries {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		products, currency, lastErr = d.source.LoadAggregatedPage(ctx, req)
		if lastErr == nil {
			return products, currency, nil
		}

		if retry < d.opts.MaxPageRetries-1 {
			sleepDur := d.opts.PageRetryBaseSleep * time.Duration(retry+1)
			d.progress("⏳ Retry %d/%d after %v: %v", retry+2, d.opts.MaxPageRetries, sleepDur, lastErr)

			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(sleepDur):
			}
		}
	}

	return nil, "", fmt.Errorf("all %d retries exhausted: %w", d.opts.MaxPageRetries, lastErr)
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
