package campaigns

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Batch sizes and API limits
const (
	batchSize        = 50  // Max IDs per /fullstats request
	maxStatsWindow   = 31  // Max days per /fullstats request (WB API limit)
)

// Downloader is a reusable campaigns downloader.
// Depends on CampaignsSource (WB API) and CampaignsWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := campaigns.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source CampaignsSource
	writer CampaignsWriter
	opts   DownloadOptions
}

// NewDownloader creates a campaigns downloader from source, writer, and options.
func NewDownloader(source CampaignsSource, writer CampaignsWriter, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the 3-phase campaign download:
//
//	Phase 1: campaigns — GET /adv/v1/promotion/count → SaveCampaigns
//	Phase 2: details — GET /api/advert/v2/adverts → SaveCampaignDetails
//	Phase 3: fullstats — GET /adv/v3/fullstats (batch 50, window 31d) → SaveFullstats
//
// Post-run: PopulateCampaignProducts
//
// Continue-on-error: individual batch/window failures increment result.Errors.
// Context cancellation is checked on each API call.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// ── Phase 1: Campaign List ─────────────────────────────────────
	allIDs, filteredIDs, err := d.runCampaignsPhase(ctx, result)
	if err != nil {
		result.Duration = time.Since(start)
		return result, err
	}
	if len(allIDs) == 0 {
		d.progress("no campaigns found")
		result.Duration = time.Since(start)
		return result, nil
	}

	// ── Phase 2: Campaign Details ──────────────────────────────────
	if err := d.runDetailsPhase(ctx, allIDs, result); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// ── Phase 3: Campaign Stats (Fullstats) ────────────────────────
	if err := d.runStatsPhase(ctx, filteredIDs, result); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// ── Post-run: Rebuild campaign_products ────────────────────────
	if !d.opts.DryRun {
		d.progress("rebuilding campaign_products...")
		if err := d.writer.PopulateCampaignProducts(ctx); err != nil {
			return result, fmt.Errorf("populate campaign_products: %w", err)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// runCampaignsPhase downloads campaign list from API and saves to DB.
// Returns (allIDs, filteredIDs, error).
func (d *Downloader) runCampaignsPhase(ctx context.Context, result *DownloadResult) ([]int, []int, error) {
	if d.opts.SkipCampaigns {
		d.progress("skipping campaign list download")
		return nil, nil, nil
	}

	d.progress("downloading campaigns...")

	resp, err := d.source.GetCampaignList(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get campaign list: %w", err)
	}

	if !d.opts.DryRun {
		if err := d.writer.SaveCampaigns(ctx, resp.Adverts); err != nil {
			return nil, nil, fmt.Errorf("save campaigns: %w", err)
		}
	}

	// Extract all IDs and filtered IDs
	var allIDs, filteredIDs []int
	for _, group := range resp.Adverts {
		for _, advert := range group.AdvertList {
			allIDs = append(allIDs, advert.AdvertID)
		}
		// Filter by status for stats download
		if len(d.opts.Statuses) > 0 && !slices.Contains(d.opts.Statuses, group.Status) {
			continue
		}
		for _, advert := range group.AdvertList {
			filteredIDs = append(filteredIDs, advert.AdvertID)
		}
	}

	result.CampaignsTotal = len(allIDs)
	result.CampaignsForStats = len(filteredIDs)
	d.progress("%d campaigns (%d for stats)", len(allIDs), len(filteredIDs))
	return allIDs, filteredIDs, nil
}

// runDetailsPhase downloads campaign details via single API call.
// WB API /api/advert/v2/adverts ignores the id parameter and returns ALL campaigns,
// so one call without IDs fetches everything — no batching needed.
func (d *Downloader) runDetailsPhase(ctx context.Context, allIDs []int, result *DownloadResult) error {
	if d.opts.SkipDetails || len(allIDs) == 0 {
		d.progress("skipping campaign details")
		return nil
	}

	d.progress("downloading campaign details...")

	details, err := d.source.GetAllAdvertDetails(ctx)
	if err != nil {
		return fmt.Errorf("get advert details: %w", err)
	}

	if len(details) > 0 && !d.opts.DryRun {
		if err := d.writer.SaveCampaignDetails(ctx, details); err != nil {
			return fmt.Errorf("save campaign details: %w", err)
		}
	}

	d.progress("details: %d/%d campaigns updated", len(details), len(allIDs))
	result.DetailsLoaded = len(details)
	return nil
}

// runStatsPhase downloads campaign stats in batches with 31-day windowing.
func (d *Downloader) runStatsPhase(ctx context.Context, campaignIDs []int, result *DownloadResult) error {
	if d.opts.SkipStats || len(campaignIDs) == 0 {
		d.progress("skipping stats")
		return nil
	}

	// Get last loaded dates for resume mode
	var lastDates map[int]time.Time
	if d.opts.Resume {
		var err error
		lastDates, err = d.writer.GetLastCampaignStatsDateAll(ctx)
		if err != nil {
			d.progress("warning: could not get last dates: %v", err)
			lastDates = make(map[int]time.Time)
		}
	}

	// Validate date range
	if _, err := time.Parse("2006-01-02", d.opts.Begin); err != nil {
		return fmt.Errorf("parse begin date %q: %w", d.opts.Begin, err)
	}
	end, err := time.Parse("2006-01-02", d.opts.End)
	if err != nil {
		return fmt.Errorf("parse end date %q: %w", d.opts.End, err)
	}

	// Calculate total API calls for progress tracking
	windows := splitDateRanges(d.opts.Begin, d.opts.End)
	totalBatches := (len(campaignIDs) + batchSize - 1) / batchSize
	d.progress("downloading stats (%s -> %s, %d batches x %d windows = %d API calls)",
		d.opts.Begin, d.opts.End, totalBatches, len(windows), totalBatches*len(windows))

	rl := d.opts.FullstatsRate
	burst := d.opts.FullstatsBurst

	for i := 0; i < len(campaignIDs); i += batchSize {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		batchEnd := min(i+batchSize, len(campaignIDs))
		batch := campaignIDs[i:batchEnd]

		// Adjust date range for resume mode
		batchBegin := d.opts.Begin
		if d.opts.Resume && len(lastDates) > 0 {
			earliestLast := end
			for _, id := range batch {
				if lastDate, ok := lastDates[id]; ok && lastDate.Before(earliestLast) {
					earliestLast = lastDate
				}
			}
			if earliestLast.Before(end) {
				batchBegin = earliestLast.AddDate(0, 0, 1).Format("2006-01-02")
				if batchBegin > d.opts.End {
					continue // Already loaded
				}
			}
		}

		// Split into 31-day windows
		batchWindows := splitDateRanges(batchBegin, d.opts.End)

		for _, w := range batchWindows {
			// Check context cancellation between windows
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			responses, err := d.source.GetCampaignFullstats(ctx, batch, w.begin, w.end, rl, burst)
			if err != nil {
				d.progress("error: fullstats batch %d-%d window %s-%s: %v", i+1, batchEnd, w.begin, w.end, err)
				result.Errors++
				continue
			}

			if len(responses) == 0 {
				continue
			}

			// Flatten 4-level hierarchy → flat rows for all tables
			flat := wb.FlattenAll(responses)

			if !d.opts.DryRun {
				if err := d.writer.SaveFullstats(ctx, flat); err != nil {
					d.progress("error: save fullstats: %v", err)
					result.Errors++
					continue
				}
			}

			result.DailyRows += len(flat.Daily)
			result.AppRows += len(flat.App)
			result.NmRows += len(flat.Nm)
			result.BoosterRows += len(flat.Booster)
			result.DateWindows++

			d.progress("window %s-%s: %d daily, %d app, %d nm, %d booster",
				w.begin, w.end, len(flat.Daily), len(flat.App), len(flat.Nm), len(flat.Booster))
		}
	}

	return nil
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}

// dateRange represents a date interval for API requests (YYYY-MM-DD strings).
type dateRange struct {
	begin string
	end   string
}

// splitDateRanges splits a date range into intervals of maxStatsWindow (31) days.
// Returns at least one interval. For ranges <= 31 days, returns a single interval.
func splitDateRanges(beginDate, endDate string) []dateRange {
	begin, _ := time.Parse("2006-01-02", beginDate)
	end, _ := time.Parse("2006-01-02", endDate)

	totalDays := int(end.Sub(begin).Hours()/24) + 1
	if totalDays <= maxStatsWindow {
		return []dateRange{{begin: beginDate, end: endDate}}
	}

	var ranges []dateRange
	current := begin
	for current.Before(end) || current.Equal(end) {
		windowEnd := current.AddDate(0, 0, maxStatsWindow-1)
		if windowEnd.After(end) {
			windowEnd = end
		}
		ranges = append(ranges, dateRange{
			begin: current.Format("2006-01-02"),
			end:   windowEnd.Format("2006-01-02"),
		})
		current = windowEnd.AddDate(0, 0, 1)
	}
	return ranges
}
