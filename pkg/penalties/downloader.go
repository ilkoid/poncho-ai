package penalties

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable measurement penalties downloader.
// Depends on PenaltiesSource (WB API) and PenaltiesWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := penalties.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source PenaltiesSource
	writer PenaltiesWriter
	opts   DownloadOptions
}

// NewDownloader creates a penalties downloader from source, writer, and options.
func NewDownloader(source PenaltiesSource, writer PenaltiesWriter, opts DownloadOptions) *Downloader {
	if opts.Days <= 0 {
		opts.Days = 90
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run downloads measurement penalties for the configured date range.
// Single pass: offset pagination handled by PenaltiesIterator.
// After download: optionally cleans up penalties older than retention window.
//
// Continue-on-error: individual page failures are retried by MeasurementPenaltiesIterator.
// Context cancellation is checked on each page request.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	dateFrom, dateTo := d.resolveDateRange()

	d.progress("downloading penalties from %s to %s (retention: %d days)", dateFrom, dateTo, d.opts.Days)

	// Optional: rewrite mode — delete old penalties before downloading
	if d.opts.Rewrite && !d.opts.DryRun {
		cutoff := time.Now().AddDate(0, 0, -d.opts.Days)
		deleted, err := d.writer.DeletePenaltiesOlderThan(ctx, cutoff)
		if err != nil {
			d.progress("warning: cleanup failed: %v", err)
		} else if deleted > 0 {
			d.progress("cleaned up %d penalties older than %s", deleted, cutoff.Format("2006-01-02"))
		}
	}

	// Download via iterator — callback processes each page
	totalCount, err := d.source.MeasurementPenaltiesIterator(ctx, dateFrom, dateTo, 1, 1, func(items []wb.MeasurementPenaltyItem, total int) error {
		result.TotalPages++

		if len(items) == 0 {
			return nil
		}

		// Apply client-side filter
		filtered := d.applyFilter(items)

		if len(filtered) == 0 {
			d.progress("page %d: 0 penalties after filter (from %d)", result.TotalPages, len(items))
			return nil
		}

		if d.opts.DryRun {
			d.progress("page %d: %d penalties skipped — dry-run (total: %d)", result.TotalPages, len(filtered), total)
			result.TotalPenalties += len(filtered)
			return nil
		}

		n, err := d.writer.SavePenalties(ctx, filtered)
		if err != nil {
			return fmt.Errorf("save penalties page %d: %w", result.TotalPages, err)
		}
		result.TotalPenalties += n
		d.progress("page %d: %d penalties saved (total: %d)", result.TotalPages, n, total)
		return nil
	})

	if err != nil {
		result.Duration = time.Since(start)
		return result, fmt.Errorf("penalties download: %w", err)
	}

	_ = totalCount

	result.Duration = time.Since(start)
	return result, nil
}

// resolveDateRange computes dateFrom and dateTo from options.
// Returns YYYY-MM-DD strings suitable for query params.
func (d *Downloader) resolveDateRange() (string, string) {
	var dateFrom, dateTo string

	if d.opts.From != "" {
		dateFrom = d.opts.From
	} else {
		t := time.Now().AddDate(0, 0, -d.opts.Days)
		dateFrom = t.Format("2006-01-02")
	}

	if d.opts.To != "" {
		dateTo = d.opts.To
	} else {
		dateTo = time.Now().Format("2006-01-02")
	}

	return dateFrom, dateTo
}

// applyFilter removes penalties that don't match PenaltiesFilterConfig.
// Filters by nm_ids (set lookup), subject (case-insensitive contains), is_valid (exact match).
func (d *Downloader) applyFilter(items []wb.MeasurementPenaltyItem) []wb.MeasurementPenaltyItem {
	f := d.opts.Filter
	if len(f.NmIds) == 0 && f.Subject == "" && f.IsValid == nil {
		return items
	}

	// Build nm_id lookup set
	nmSet := make(map[int]bool, len(f.NmIds))
	for _, id := range f.NmIds {
		nmSet[id] = true
	}

	subjectLower := strings.ToLower(f.Subject)

	filtered := make([]wb.MeasurementPenaltyItem, 0, len(items))
	for _, p := range items {
		// Filter by nm_ids
		if len(nmSet) > 0 && !nmSet[p.NmId] {
			continue
		}

		// Filter by subject (case-insensitive contains)
		if subjectLower != "" && !strings.Contains(strings.ToLower(p.SubjectName), subjectLower) {
			continue
		}

		// Filter by is_valid
		if f.IsValid != nil && p.IsValid != *f.IsValid {
			continue
		}

		filtered = append(filtered, p)
	}
	return filtered
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
