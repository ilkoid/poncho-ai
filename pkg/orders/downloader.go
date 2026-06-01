package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const statsAPIURL = "https://statistics-api.wildberries.ru"

// Downloader is a reusable orders downloader.
// Depends on OrdersSource (WB API) and OrdersWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := orders.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source OrdersSource
	writer OrdersWriter
	opts   DownloadOptions
	filter config.FunnelFilterConfig
}

// NewDownloader creates an orders downloader from source, writer, options, and filter.
func NewDownloader(source OrdersSource, writer OrdersWriter, opts DownloadOptions, filter config.FunnelFilterConfig) *Downloader {
	if opts.Days <= 0 {
		opts.Days = 90
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
		filter: filter,
	}
}

// Run downloads orders from (today - Days) to now.
// Single pass: the WB Statistics API handles pagination internally.
// After download: optionally cleans up orders older than retention window.
//
// Continue-on-error: individual page failures are retried by OrdersIterator.
// Context cancellation is checked on each page request.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Resolve date range
	dateFrom := d.resolveDateFrom()

	d.progress("downloading orders from %s (retention: %d days)", dateFrom, d.opts.Days)

	// Optional: rewrite mode — delete old orders before downloading
	if d.opts.Rewrite && !d.opts.DryRun {
		cutoff := time.Now().AddDate(0, 0, -d.opts.Days)
		deleted, err := d.writer.DeleteOrdersOlderThan(ctx, cutoff)
		if err != nil {
			d.progress("warning: cleanup failed: %v", err)
		} else if deleted > 0 {
			d.progress("cleaned up %d orders older than %s", deleted, cutoff.Format("2006-01-02"))
		}
	}

	// Download via iterator — callback processes each page
	totalCount, err := d.source.OrdersIterator(ctx, statsAPIURL, 1, 10, dateFrom, func(orders []wb.OrdersItem) error {
		result.TotalPages++

		// Apply article-based filter
		filtered := d.applyFilter(orders)

		if len(filtered) == 0 {
			d.progress("page %d: 0 orders after filter (from %d)", result.TotalPages, len(orders))
			return nil
		}

		if d.opts.DryRun {
			d.progress("page %d: %d orders skipped — dry-run", result.TotalPages, len(filtered))
			result.TotalOrders += len(filtered)
			return nil
		}

		n, err := d.writer.SaveOrders(ctx, filtered)
		if err != nil {
			return fmt.Errorf("save orders page %d: %w", result.TotalPages, err)
		}
		result.TotalOrders += n
		d.progress("page %d: %d orders saved", result.TotalPages, n)
		return nil
	})

	if err != nil {
		result.Duration = time.Since(start)
		return result, fmt.Errorf("orders download: %w", err)
	}

	// totalCount from iterator may include filtered-out orders
	_ = totalCount

	result.Duration = time.Since(start)
	return result, nil
}

// resolveDateFrom computes the start date from opts.From or opts.Days.
func (d *Downloader) resolveDateFrom() string {
	if d.opts.From != "" {
		return d.opts.From + "T00:00:00"
	}
	t := time.Now().AddDate(0, 0, -d.opts.Days)
	return t.Format("2006-01-02") + "T00:00:00"
}

// applyFilter removes orders that don't match FunnelFilterConfig.
// Filters by article length (ExcludeLengths) and year from article (AllowedYears).
func (d *Downloader) applyFilter(orders []wb.OrdersItem) []wb.OrdersItem {
	if len(d.filter.ExcludeLengths) == 0 && len(d.filter.AllowedYears) == 0 {
		return orders
	}

	excludeLenSet := make(map[int]bool, len(d.filter.ExcludeLengths))
	for _, l := range d.filter.ExcludeLengths {
		excludeLenSet[l] = true
	}

	yearSet := make(map[int]bool, len(d.filter.AllowedYears))
	for _, y := range d.filter.AllowedYears {
		yearSet[y] = true
	}

	filtered := make([]wb.OrdersItem, 0, len(orders))
	for _, o := range orders {
		vc := o.SupplierArticle

		// Exclude by length
		if excludeLenSet[len(vc)] {
			continue
		}

		// Filter by year (from 2nd-3rd digits of article)
		if len(yearSet) > 0 {
			if len(vc) < 3 {
				continue // can't extract year, skip
			}
			year := int(vc[1]-'0')*10 + int(vc[2]-'0')
			if !yearSet[year] {
				continue
			}
		}

		filtered = append(filtered, o)
	}
	return filtered
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
