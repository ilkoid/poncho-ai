// Package main provides promotion download logic.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// PromotionClient is the interface for Promotion API operations.
// Defined in cmd/ per Rule 6 (consumer's interface).
type PromotionClient interface {
	// GetPromotionCount returns list of campaigns (despite the name).
	GetPromotionCount(ctx context.Context) (*wb.PromotionCountResponse, error)

	// GetCampaignFullstats returns full campaign statistics with 4-level hierarchy.
	// Returns raw API response: Campaign → Day → App → Nm.
	// rateLimit and burst control the client-side rate limiter.
	GetCampaignFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string, rateLimit, burst int) ([]wb.CampaignFullstatsResponse, error)

	// GetAdvertDetails returns campaign metadata (name, payment_type, timestamps).
	// NOTE: v2 may not return details for all campaign types (e.g. type=8 legacy).
	GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error)
}

// DownloadCampaigns downloads campaign list from API and saves to DB.
// Returns (allIDs, filteredIDs). allIDs — for campaign details, filteredIDs — for stats.
func DownloadCampaigns(ctx context.Context, client PromotionClient, repo *sqlite.SQLiteSalesRepository, statuses []int) (allIDs, filteredIDs []int, err error) {
	// Get campaigns from API
	resp, err := client.GetPromotionCount(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get promotion count: %w", err)
	}

	// Save to database (all campaigns, no status filter)
	if err := repo.SaveCampaigns(ctx, resp.Adverts); err != nil {
		return nil, nil, fmt.Errorf("save campaigns: %w", err)
	}

	// Extract IDs
	for _, group := range resp.Adverts {
		for _, advert := range group.AdvertList {
			allIDs = append(allIDs, advert.AdvertID)
		}
		// Filter by status for stats download
		if len(statuses) > 0 && !containsInt(statuses, group.Status) {
			continue
		}
		for _, advert := range group.AdvertList {
			filteredIDs = append(filteredIDs, advert.AdvertID)
		}
	}

	return allIDs, filteredIDs, nil
}

// DownloadCampaignStats downloads daily stats for campaigns.
// Batches requests (max 50 IDs per request for /adv/v3/fullstats).
// Saves to 4 tables: campaign_stats_daily, campaign_stats_app,
// campaign_stats_nm, campaign_booster_stats.
// Then rebuilds campaign_products materialized view.
func DownloadCampaignStats(
	ctx context.Context,
	client PromotionClient,
	repo *sqlite.SQLiteSalesRepository,
	campaignIDs []int,
	beginDate, endDate string,
	resume bool,
	rateLimit, burst int,
) (StatsSummary, error) {
	var summary StatsSummary

	// Get last loaded dates for resume mode
	var lastDates map[int]time.Time
	if resume {
		var err error
		lastDates, err = repo.GetLastCampaignStatsDateAll(ctx)
		if err != nil {
			fmt.Printf("⚠️  Could not get last dates: %v\n", err)
			lastDates = make(map[int]time.Time)
		}
	}

	// Parse date range
	_, err := time.Parse("2006-01-02", beginDate)
	if err != nil {
		return summary, fmt.Errorf("parse begin date: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return summary, fmt.Errorf("parse end date: %w", err)
	}

	// Batch size: max 50 IDs per request
	const batchSize = 50
	totalBatches := (len(campaignIDs) + batchSize - 1) / batchSize
	progress := newProgressTracker(totalBatches)

	for i := 0; i < len(campaignIDs); i += batchSize {
		endIdx := i + batchSize
		if endIdx > len(campaignIDs) {
			endIdx = len(campaignIDs)
		}

		batch := campaignIDs[i:endIdx]
		batchNum := i/batchSize + 1

		eta, elapsed := progress.formatETA(batchNum - 1)
		if eta != "" {
			fmt.Printf("  [%d/%d] 📦 Batch %d-%d (%d campaigns)... ETA %s (%s)\n", batchNum, totalBatches, i+1, endIdx, len(batch), eta, elapsed)
		} else {
			fmt.Printf("  [%d/%d] 📦 Batch %d-%d (%d campaigns)...\n", batchNum, totalBatches, i+1, endIdx, len(batch))
		}

		// Adjust date range for resume mode
		batchBegin := beginDate
		if resume {
			// Find earliest last date in batch
			earliestLast := end
			for _, id := range batch {
				if lastDate, ok := lastDates[id]; ok && lastDate.Before(earliestLast) {
					earliestLast = lastDate
				}
			}
			// Start from day after last loaded
			if earliestLast.Before(end) {
				batchBegin = earliestLast.AddDate(0, 0, 1).Format("2006-01-02")
				if batchBegin > endDate {
					fmt.Printf("     ⏭️  Skipped (already loaded)\n")
					continue
				}
			}
		}

		// Split date range into 31-day windows (API limit)
		windows := splitDateRanges(batchBegin, endDate)
		if len(windows) > 1 {
			fmt.Printf("     📅 %d date windows (max %d days each)\n", len(windows), maxStatsWindowDays)
		}

		for _, w := range windows {
			if len(windows) > 1 {
				fmt.Printf("     📅 %s → %s\n", w.begin, w.end)
			}

			// Get raw stats from API (4-level hierarchy)
			tAPI := time.Now()
			responses, err := client.GetCampaignFullstats(ctx, batch, w.begin, w.end, rateLimit, burst)
			apiDur := time.Since(tAPI)
			if err != nil {
				return summary, fmt.Errorf("get fullstats batch: %w", err)
			}

			if len(responses) == 0 {
				if len(windows) > 1 {
					fmt.Println("        ⏭️  No data")
				} else {
					fmt.Println("     ⏭️  No data")
				}
				continue
			}

			// Flatten hierarchy to flat rows for all tables in one pass
			tFlat := time.Now()
			flat := wb.FlattenAll(responses)
			flatDur := time.Since(tFlat)

			// Save to all tables
			tDB := time.Now()
			if err := repo.SaveCampaignStats(ctx, flat.Daily); err != nil {
				return summary, fmt.Errorf("save daily stats: %w", err)
			}
			if err := repo.SaveCampaignAppStats(ctx, flat.App); err != nil {
				return summary, fmt.Errorf("save app stats: %w", err)
			}
			if err := repo.SaveCampaignNmStats(ctx, flat.Nm); err != nil {
				return summary, fmt.Errorf("save nm stats: %w", err)
			}
			if err := repo.SaveCampaignBoosterStats(ctx, flat.Booster); err != nil {
				return summary, fmt.Errorf("save booster stats: %w", err)
			}
			dbDur := time.Since(tDB)

			summary.DailyRows += len(flat.Daily)
			summary.AppRows += len(flat.App)
			summary.NmRows += len(flat.Nm)
			summary.BoosterRows += len(flat.Booster)
			summary.Campaigns += len(responses)
			summary.DateWindows++

			if len(windows) > 1 {
				fmt.Printf("        ✅ %d daily, %d app, %d nm, %d booster (api %s, flatten %s, db %s)\n",
					len(flat.Daily), len(flat.App), len(flat.Nm), len(flat.Booster),
					apiDur.Truncate(time.Millisecond), flatDur.Truncate(time.Millisecond), dbDur.Truncate(time.Millisecond))
			} else {
				fmt.Printf("     ✅ %d daily, %d app, %d nm, %d booster (api %s, flatten %s, db %s)\n",
					len(flat.Daily), len(flat.App), len(flat.Nm), len(flat.Booster),
					apiDur.Truncate(time.Millisecond), flatDur.Truncate(time.Millisecond), dbDur.Truncate(time.Millisecond))
			}
		}
	}

	return summary, nil
}

// DownloadCampaignDetails downloads campaign metadata from /api/advert/v2/adverts.
// Sends ALL campaign IDs (not filtered) — details are metadata, not status-dependent.
// NOTE: v2 may not return details for all campaign types (e.g. type=8 legacy).
// Campaigns not returned by v2 keep NULL in detail fields — this is expected.
func DownloadCampaignDetails(ctx context.Context, client PromotionClient, repo *sqlite.SQLiteSalesRepository, campaignIDs []int) (int, error) {
	if len(campaignIDs) == 0 {
		return 0, nil
	}

	const batchSize = 50
	totalLoaded := 0
	totalBatches := (len(campaignIDs) + batchSize - 1) / batchSize
	progress := newProgressTracker(totalBatches)

	for i := 0; i < len(campaignIDs); i += batchSize {
		endIdx := i + batchSize
		if endIdx > len(campaignIDs) {
			endIdx = len(campaignIDs)
		}
		batch := campaignIDs[i:endIdx]
		batchNum := i/batchSize + 1

		eta, elapsed := progress.formatETA(batchNum - 1)
		if eta != "" {
			fmt.Printf("  [%d/%d] 📦 Details batch %d-%d (%d IDs)... ETA %s (%s)\n", batchNum, totalBatches, i+1, endIdx, len(batch), eta, elapsed)
		} else {
			fmt.Printf("  [%d/%d] 📦 Details batch %d-%d (%d IDs)...\n", batchNum, totalBatches, i+1, endIdx, len(batch))
		}

		details, err := client.GetAdvertDetails(ctx, batch)
		if err != nil {
			return totalLoaded, fmt.Errorf("get advert details batch: %w", err)
		}

		if len(details) == 0 {
			fmt.Println("     ⏭️  No details returned (unsupported campaign types)")
			continue
		}

		if err := repo.SaveCampaignDetails(ctx, details); err != nil {
			return totalLoaded, fmt.Errorf("save campaign details: %w", err)
		}

		totalLoaded += len(details)
		fmt.Printf("     ✅ %d/%d campaigns updated\n", len(details), len(batch))
	}

	return totalLoaded, nil
}

// StatsSummary holds counts of rows saved across all tables.
type StatsSummary struct {
	Campaigns   int
	DateWindows int // Number of 31-day windows processed
	DailyRows   int
	AppRows     int
	NmRows      int
	BoosterRows int
}

// maxStatsWindowDays is the maximum date range per /adv/v3/fullstats request.
// WB API limits to 31 days maximum (see 08-promotion.yaml).
const maxStatsWindowDays = 31

// dateRange represents a date interval for API requests (YYYY-MM-DD strings).
type dateRange struct {
	begin string
	end   string
}

// splitDateRanges splits a date range into intervals of maxStatsWindowDays days.
// Returns at least one interval. For ranges <= 31 days, returns a single interval.
func splitDateRanges(beginDate, endDate string) []dateRange {
	return splitDateRangesLimit(beginDate, endDate, maxStatsWindowDays)
}

// splitDateRangesLimit splits a date range into intervals of maxDays.
func splitDateRangesLimit(beginDate, endDate string, maxDays int) []dateRange {
	begin, _ := time.Parse("2006-01-02", beginDate)
	end, _ := time.Parse("2006-01-02", endDate)

	totalDays := int(end.Sub(begin).Hours()/24) + 1
	if totalDays <= maxDays {
		return []dateRange{{begin: beginDate, end: endDate}}
	}

	var ranges []dateRange
	current := begin
	for current.Before(end) || current.Equal(end) {
		windowEnd := current.AddDate(0, 0, maxDays-1)
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

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// progressTracker tracks batch progress and estimates remaining time.
type progressTracker struct {
	total int
	start time.Time
}

func newProgressTracker(total int) *progressTracker {
	return &progressTracker{total: total, start: time.Now()}
}

// formatETA returns estimated time remaining and elapsed for the given batch number (0-based).
func (p *progressTracker) formatETA(batchIdx int) (eta string, elapsed time.Duration) {
	elapsed = time.Since(p.start).Truncate(time.Second)
	if batchIdx == 0 {
		return "", elapsed
	}
	avgPerBatch := time.Since(p.start) / time.Duration(batchIdx)
	remaining := time.Duration(p.total-batchIdx) * avgPerBatch
	return "~" + formatDuration(remaining), elapsed
}

// formatDuration formats a duration as human-readable string.
func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
