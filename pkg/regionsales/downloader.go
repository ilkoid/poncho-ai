package regionsales

import (
	"context"
	"fmt"
	"time"
)

const (
	// maxPeriodDays is the maximum days per API request (WB API limit).
	maxPeriodDays = 31

	// maxDataAvailabilityDays is how far back the API stores data.
	maxDataAvailabilityDays = 31

	// defaultDaysBack is the default number of days to download.
	defaultDaysBack = 7
)

// Downloader is a reusable region sales downloader.
// Depends on RegionSalesSource (WB API) and RegionSalesWriter (persistence) — both are interfaces.
type Downloader struct {
	source RegionSalesSource
	writer RegionSalesWriter
	opts   DownloadOptions
}

// NewDownloader creates a region sales downloader from a source and writer.
func NewDownloader(source RegionSalesSource, writer RegionSalesWriter, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full region sales download pipeline.
//
// Algorithm:
//  1. Resolve date range from options (Date > Begin/End > Days)
//  2. Split into ≤31-day sub-ranges (API limit per request)
//  3. For each sub-range: fetch → save → progress
//  4. Return aggregated result
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// 1. Resolve date range
	begin, end, err := d.resolveDateRange()
	if err != nil {
		return result, fmt.Errorf("resolve date range: %w", err)
	}
	d.progress("📅 Period: %s → %s", begin, end)

	// 2. Data availability warning
	today := time.Now()
	beginDate, _ := time.Parse("2006-01-02", begin)
	if today.Sub(beginDate) > time.Duration(maxDataAvailabilityDays)*24*time.Hour {
		d.progress("⚠️  Data older than %d days may be unavailable", maxDataAvailabilityDays)
	}

	// 3. Split into sub-ranges
	subRanges := splitDateRange(begin, end, maxPeriodDays)
	d.progress("  %d request(s) for %s → %s", len(subRanges), begin, end)

	// 4. Download each sub-range
	for i, sr := range subRanges {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		items, err := d.source.GetRegionSales(ctx, sr[0], sr[1])
		if err != nil {
			d.progress("❌ Error request %d (%s→%s): %v", i+1, sr[0], sr[1], err)
			return result, fmt.Errorf("request %d (%s→%s): %w", i+1, sr[0], sr[1], err)
		}

		result.Requests++

		if len(items) == 0 {
			d.progress("  request %d (%s→%s): empty", i+1, sr[0], sr[1])
			continue
		}

		if d.opts.DryRun {
			result.TotalRows += len(items)
			d.progress("  [dry-run] request %d (%s→%s): %d rows", i+1, sr[0], sr[1], len(items))
			continue
		}

		n, err := d.writer.SaveRegionSales(ctx, sr[0], sr[1], items)
		if err != nil {
			d.progress("❌ Save request %d (%s→%s): %v", i+1, sr[0], sr[1], err)
			return result, fmt.Errorf("save request %d (%s→%s): %w", i+1, sr[0], sr[1], err)
		}
		result.TotalRows += n
		d.progress("  request %d (%s→%s): %d rows saved", i+1, sr[0], sr[1], n)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// resolveDateRange determines the download date range from options.
// Priority: Date > Begin/End > Days (default: 7).
func (d *Downloader) resolveDateRange() (begin, end string, err error) {
	today := time.Now()
	yesterday := today.AddDate(0, 0, -1)

	// Single date mode (highest priority)
	if d.opts.Date != "" {
		if _, parseErr := time.Parse("2006-01-02", d.opts.Date); parseErr != nil {
			return "", "", fmt.Errorf("invalid date %q: %w", d.opts.Date, parseErr)
		}
		return d.opts.Date, d.opts.Date, nil
	}

	// Explicit begin/end
	if d.opts.Begin != "" {
		beginDate, parseErr := time.Parse("2006-01-02", d.opts.Begin)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid begin %q: %w", d.opts.Begin, parseErr)
		}

		endDate := yesterday
		if d.opts.End != "" {
			e, parseErr := time.Parse("2006-01-02", d.opts.End)
			if parseErr != nil {
				return "", "", fmt.Errorf("invalid end %q: %w", d.opts.End, parseErr)
			}
			endDate = e
		}

		return beginDate.Format("2006-01-02"), endDate.Format("2006-01-02"), nil
	}

	// Days back (default)
	days := d.opts.Days
	if days <= 0 {
		days = defaultDaysBack
	}
	beginDate := today.AddDate(0, 0, -days)
	return beginDate.Format("2006-01-02"), yesterday.Format("2006-01-02"), nil
}

// splitDateRange splits [begin, end] into sub-ranges of at most maxDays days each.
// Uses half-open intervals internally to prevent overlaps.
// Returns pairs [][2]string where each pair is {subBegin, subEnd} (both inclusive).
func splitDateRange(begin, end string, maxDays int) [][2]string {
	b, err := time.Parse("2006-01-02", begin)
	if err != nil {
		return [][2]string{{begin, end}}
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return [][2]string{{begin, end}}
	}

	// Total days (inclusive both ends)
	totalDays := int(e.Sub(b).Hours()/24) + 1
	if totalDays <= maxDays {
		return [][2]string{{begin, end}}
	}

	var ranges [][2]string
	cursor := b
	for cursor.Before(e) || cursor.Equal(e) {
		// Sub-range end: cursor + (maxDays - 1) to keep within limit (inclusive)
		subEnd := cursor.AddDate(0, 0, maxDays-1)
		if subEnd.After(e) {
			subEnd = e
		}
		ranges = append(ranges, [2]string{
			cursor.Format("2006-01-02"),
			subEnd.Format("2006-01-02"),
		})
		// Move cursor to day after subEnd
		cursor = subEnd.AddDate(0, 0, 1)
	}

	return ranges
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
