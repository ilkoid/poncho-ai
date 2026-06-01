package opsales

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const statsAPIURL = "https://statistics-api.wildberries.ru"

// Downloader is a reusable operational sales downloader.
// Depends on OpsalesSource (WB API) and OpsalesWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := opsales.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source OpsalesSource
	writer OpsalesWriter
	opts   DownloadOptions
	filter config.FunnelFilterConfig
}

// NewDownloader creates an operational sales downloader from source, writer, options, and filter.
func NewDownloader(source OpsalesSource, writer OpsalesWriter, opts DownloadOptions, filter config.FunnelFilterConfig) *Downloader {
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

// Run downloads operational sales from (today - Days) to now.
// Single pass: the WB Statistics API handles pagination internally.
// After download: optionally cleans up sales older than retention window.
//
// Continue-on-error: individual page failures are retried by SalesIterator.
// Context cancellation is checked on each page request.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Resolve date range
	dateFrom := d.resolveDateFrom()

	d.progress("downloading operational sales from %s (retention: %d days)", dateFrom, d.opts.Days)

	// Optional: rewrite mode — delete old sales before downloading
	if d.opts.Rewrite && !d.opts.DryRun {
		cutoff := time.Now().AddDate(0, 0, -d.opts.Days)
		deleted, err := d.writer.DeleteSalesOlderThan(ctx, cutoff)
		if err != nil {
			d.progress("warning: cleanup failed: %v", err)
		} else if deleted > 0 {
			d.progress("cleaned up %d sales older than %s", deleted, cutoff.Format("2006-01-02"))
		}
	}

	// Download via iterator — callback processes each page
	totalCount, err := d.source.SalesIterator(ctx, statsAPIURL, 1, 10, dateFrom, func(sales []wb.SalesItem) error {
		result.TotalPages++

		// Apply article-based filter
		filtered := d.applyFilter(sales)

		if len(filtered) == 0 {
			d.progress("page %d: 0 sales after filter (from %d)", result.TotalPages, len(sales))
			return nil
		}

		if d.opts.DryRun {
			d.progress("page %d: %d sales skipped — dry-run", result.TotalPages, len(filtered))
			result.TotalSales += len(filtered)
			return nil
		}

		n, err := d.writer.SaveSales(ctx, filtered)
		if err != nil {
			return fmt.Errorf("save sales page %d: %w", result.TotalPages, err)
		}
		result.TotalSales += n
		d.progress("page %d: %d sales saved", result.TotalPages, n)
		return nil
	})

	if err != nil {
		result.Duration = time.Since(start)
		return result, fmt.Errorf("opsales download: %w", err)
	}

	// totalCount from iterator may include filtered-out sales
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

// applyFilter removes sales that don't match FunnelFilterConfig.
// Filters by article length (ExcludeLengths) and year from article (AllowedYears).
func (d *Downloader) applyFilter(sales []wb.SalesItem) []wb.SalesItem {
	if len(d.filter.ExcludeLengths) == 0 && len(d.filter.AllowedYears) == 0 {
		return sales
	}

	excludeLenSet := make(map[int]bool, len(d.filter.ExcludeLengths))
	for _, l := range d.filter.ExcludeLengths {
		excludeLenSet[l] = true
	}

	yearSet := make(map[int]bool, len(d.filter.AllowedYears))
	for _, y := range d.filter.AllowedYears {
		yearSet[y] = true
	}

	filtered := make([]wb.SalesItem, 0, len(sales))
	for _, s := range sales {
		vc := s.SupplierArticle

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

		filtered = append(filtered, s)
	}
	return filtered
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
