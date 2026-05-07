// Package main provides V2 promotion download phases.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// formatProgress returns a progress string with percentage and ETA.
// Returns "done/total" when done==0 (no baseline for ETA).
func formatProgress(done, total int, start time.Time) string {
	if total == 0 || done == 0 {
		return fmt.Sprintf("%d/%d", done, total)
	}
	pct := float64(done) * 100 / float64(total)
	elapsed := time.Since(start)
	avg := elapsed / time.Duration(done)
	remaining := time.Duration(total-done) * avg
	return fmt.Sprintf("%d/%d %.0f%% ETA %s", done, total, pct, formatDur(remaining))
}

// formatDur formats a duration as human-readable string.
func formatDur(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("~%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("~%dm %ds", m, s)
	default:
		return fmt.Sprintf("~%ds", s)
	}
}

// V2Client is the interface for V2 Promotion API operations.
// Defined in cmd/ per Rule 6 (consumer's interface).
type V2Client interface {
	GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error)
	GetNormqueryStats(ctx context.Context, req wb.NormqueryStatsRequest, rateLimit, burst int) (*wb.NormqueryStatsResponse, error)
	GetNormqueryList(ctx context.Context, req wb.NormqueryListRequest, rateLimit, burst int) (*wb.NormqueryListResponse, error)
	GetNormqueryBids(ctx context.Context, req wb.NormqueryBidsRequest, rateLimit, burst int) (*wb.NormqueryBidsResponse, error)
	GetNormqueryMinus(ctx context.Context, req wb.NormqueryMinusRequest, rateLimit, burst int) (*wb.NormqueryMinusResponse, error)
	GetBidRecommendations(ctx context.Context, nmID, advertID int, rateLimit, burst int) (*wb.BidRecommendationsResponse, error)
	GetExpenses(ctx context.Context, from, to string, rateLimit, burst int) (wb.ExpensesResponse, error)
	GetBalance(ctx context.Context, rateLimit, burst int) (*wb.BalanceResponse, error)
	GetPayments(ctx context.Context, from, to string, rateLimit, burst int) (wb.PaymentsResponse, error)
	GetCalendarPromotions(ctx context.Context, start, end string, allPromo bool, rateLimit, burst int) (*wb.CalendarPromotionsResponse, error)
	GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*wb.CalendarPromotionDetailsResponse, error)
	GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*wb.CalendarPromotionNomsResponse, error)
	GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*wb.BudgetResponse, error)
	GetMinBids(ctx context.Context, req wb.MinBidsRequest, rateLimit, burst int) (*wb.MinBidsResponse, error)
}

// DownloadCampaignBids extracts bid data from AdvertDetails and saves to campaign_bids table.
func DownloadCampaignBids(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	seen := make(map[int]bool)
	var advertIDs []int
	for _, p := range productIDs {
		if !seen[p.AdvertID] {
			seen[p.AdvertID] = true
			advertIDs = append(advertIDs, p.AdvertID)
		}
	}

	const batchSize = 50
	totalBatches := (len(advertIDs) + batchSize - 1) / batchSize
	t0 := time.Now()
	totalBids := 0

	for i := 0; i < len(advertIDs); i += batchSize {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := i + batchSize
		if endIdx > len(advertIDs) {
			endIdx = len(advertIDs)
		}
		batch := advertIDs[i:endIdx]
		batchNum := i/batchSize + 1

		details, err := client.GetAdvertDetails(ctx, batch)
		if err != nil {
			fmt.Printf("   Error batch %d-%d: %v\n", i+1, endIdx, err)
			continue
		}

		var rows []wb.CampaignBidRow
		for _, d := range details {
			for _, nm := range d.NmSettings {
				rows = append(rows, wb.CampaignBidRow{
					AdvertID:    d.ID,
					NmID:        nm.NmID,
					SubjectID:   nm.Subject.ID,
					SubjectName: nm.Subject.Name,
					BidSearch:   nm.BidsKopecks.Search,
					BidReco:     nm.BidsKopecks.Recommendations,
				})
			}
		}

		if err := repo.SaveCampaignBids(ctx, rows); err != nil {
			fmt.Printf("   Error saving bids: %v\n", err)
			continue
		}
		totalBids += len(rows)
		fmt.Printf("   [%s] %d bids saved\n", formatProgress(batchNum, totalBatches, t0), len(rows))
	}

	fmt.Printf("   Done: %d bids from %d campaigns (%s)\n", totalBids, len(advertIDs), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadNormqueryStats downloads search cluster statistics per (advert_id, nm_id, date).
func DownloadNormqueryStats(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, beginDate, endDate string, rateLimit, burst int) error {
	const batchSize = 100
	totalBatches := (len(productIDs) + batchSize - 1) / batchSize
	t0 := time.Now()
	totalStats := 0

	for i := 0; i < len(productIDs); i += batchSize {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := i + batchSize
		if endIdx > len(productIDs) {
			endIdx = len(productIDs)
		}
		batch := productIDs[i:endIdx]
		batchNum := i/batchSize + 1

		req := wb.NormqueryStatsRequest{
			From:  beginDate,
			To:    endDate,
			Items: batch,
		}
		resp, err := client.GetNormqueryStats(ctx, req, rateLimit, burst)
		if err != nil {
			fmt.Printf("   [%s] Error: %v\n", formatProgress(batchNum, totalBatches, t0), err)
			continue
		}

		for _, group := range resp.Stats {
			if len(group.Stats) == 0 {
				continue
			}
			if err := repo.SaveNormqueryStats(ctx, group.AdvertID, group.NmID, beginDate, group.Stats); err != nil {
				fmt.Printf("   Error saving stats advert=%d nm=%d: %v\n", group.AdvertID, group.NmID, err)
			}
			totalStats += len(group.Stats)
		}
		fmt.Printf("   [%s] %d clusters\n", formatProgress(batchNum, totalBatches, t0), totalStats)
	}

	fmt.Printf("   Done: %d stat rows from %d products (%s)\n", totalStats, len(productIDs), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadNormqueryClusters downloads active/excluded search clusters.
func DownloadNormqueryClusters(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	return downloadBatched(ctx, client, repo, productIDs, rateLimit, burst, "clusters", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := client.GetNormqueryList(ctx, wb.NormqueryListRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if err := repo.SaveNormqueryClusters(ctx, resp.Items); err != nil {
			return 0, err
		}
		total := 0
		for _, item := range resp.Items {
			total += len(item.NormQueries.Active) + len(item.NormQueries.Excluded)
		}
		return total, nil
	})
}

// DownloadNormqueryBids downloads current bid per (advert_id, nm_id).
func DownloadNormqueryBids(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	return downloadBatched(ctx, client, repo, productIDs, rateLimit, burst, "bids", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := client.GetNormqueryBids(ctx, wb.NormqueryBidsRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if err := repo.SaveNormqueryBids(ctx, resp.Bids); err != nil {
			return 0, err
		}
		return len(resp.Bids), nil
	})
}

// DownloadNormqueryMinus downloads minus phrases per (advert_id, nm_id).
func DownloadNormqueryMinus(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	return downloadBatched(ctx, client, repo, productIDs, rateLimit, burst, "minus phrases", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := client.GetNormqueryMinus(ctx, wb.NormqueryMinusRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if err := repo.SaveNormqueryMinus(ctx, resp.Items); err != nil {
			return 0, err
		}
		total := 0
		for _, item := range resp.Items {
			total += len(item.NormQueries)
		}
		return total, nil
	})
}

// DownloadBidRecommendations downloads recommended bids per product.
// Rate limit is 5 req/min — very slow. One request per (nm_id, advert_id).
// Pairs are sorted by advert_id to skip all remaining nms for stale campaigns
// (where nm no longer belongs to advert) after the first 400 error.
func DownloadBidRecommendations(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	t0 := time.Now()
	total := 0
	staleCount := 0
	snapshotDate := time.Now().Format("2006-01-02")

	// Sort by advert_id so stale campaigns can be skipped entirely.
	sort.Slice(productIDs, func(i, j int) bool {
		if productIDs[i].AdvertID != productIDs[j].AdvertID {
			return productIDs[i].AdvertID < productIDs[j].AdvertID
		}
		return productIDs[i].NmID < productIDs[j].NmID
	})

	staleAdverts := make(map[int]bool)
	reportInterval := 100
	if len(productIDs) > 10000 {
		reportInterval = 500
	}

	for i, p := range productIDs {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted %s\n", formatProgress(i, len(productIDs), t0))
			return ctx.Err()
		}
		if staleAdverts[p.AdvertID] {
			staleCount++
			continue
		}

		resp, err := client.GetBidRecommendations(ctx, p.NmID, p.AdvertID, rateLimit, burst)
		if err != nil {
			if strings.Contains(err.Error(), "nm_not_belong_to_advert") {
				staleAdverts[p.AdvertID] = true
				staleCount++
				fmt.Printf("   Stale: nm=%d no longer in advert=%d (skipping remaining nms)\n", p.NmID, p.AdvertID)
				continue
			}
			fmt.Printf("   Error nm=%d advert=%d: %v\n", p.NmID, p.AdvertID, err)
			continue
		}
		if err := repo.SaveBidRecommendations(ctx, []wb.BidRecommendationsResponse{*resp}, snapshotDate); err != nil {
			fmt.Printf("   Error saving: %v\n", err)
			continue
		}
		total++
		if (i+1)%reportInterval == 0 {
			fmt.Printf("   [%s] %d ok, %d stale\n", formatProgress(i+1, len(productIDs), t0), total, staleCount)
		}
	}

	fmt.Printf("   Done: %d recommendations, %d stale pairs (%d campaigns) (%s)\n",
		total, staleCount, len(staleAdverts), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadExpenses downloads expense history.
func DownloadExpenses(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, from, to string, rateLimit, burst int) error {
	t0 := time.Now()
	expenses, err := client.GetExpenses(ctx, from, to, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get expenses: %w", err)
	}
	if err := repo.SaveExpenses(ctx, expenses); err != nil {
		return fmt.Errorf("save expenses: %w", err)
	}
	fmt.Printf("   Done: %d expense records (%s)\n", len(expenses), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadBalance downloads and saves account balance snapshot.
func DownloadBalance(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, rateLimit, burst int) error {
	t0 := time.Now()
	balance, err := client.GetBalance(ctx, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get balance: %w", err)
	}
	snapshotDate := time.Now().Format("2006-01-02")
	if err := repo.SaveBalance(ctx, *balance, snapshotDate); err != nil {
		return fmt.Errorf("save balance: %w", err)
	}
	fmt.Printf("   Done: balance=%d, net=%d, bonus=%d (%s)\n", balance.Balance, balance.Net, balance.Bonus, time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadPayments downloads payment history.
func DownloadPayments(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, from, to string, rateLimit, burst int) error {
	t0 := time.Now()
	payments, err := client.GetPayments(ctx, from, to, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get payments: %w", err)
	}
	if payments == nil {
		fmt.Println("   Done: no payments in period")
		return nil
	}
	if err := repo.SavePayments(ctx, payments); err != nil {
		return fmt.Errorf("save payments: %w", err)
	}
	fmt.Printf("   Done: %d payment records (%s)\n", len(payments), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadCalendarPromotions downloads WB promotions calendar.
func DownloadCalendarPromotions(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, beginDate, endDate string, rateLimit, burst int) error {
	t0 := time.Now()
	// Calendar API requires datetime format: YYYY-MM-DDTHH:MM:SSZ
	start := beginDate + "T00:00:00Z"
	end := endDate + "T23:59:59Z"
	promos, err := client.GetCalendarPromotions(ctx, start, end, true, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get calendar: %w", err)
	}
	if err := repo.SaveCalendarPromotions(ctx, promos.Data.Promotions); err != nil {
		return fmt.Errorf("save calendar: %w", err)
	}
	fmt.Printf("   Done: %d promotions (%s)\n", len(promos.Data.Promotions), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadCalendarPromotionDetails downloads detailed info for all calendar promotions.
// Batches up to 100 IDs per request. Saves details, advantages, and ranging.
func DownloadCalendarPromotionDetails(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, rateLimit, burst int) error {
	ids, err := repo.GetCalendarPromotionIDs(ctx)
	if err != nil {
		return fmt.Errorf("get promotion ids: %w", err)
	}
	if len(ids) == 0 {
		fmt.Println("   No calendar promotions found. Run Calendar phase first.")
		return nil
	}

	const batchSize = 100
	totalBatches := (len(ids) + batchSize - 1) / batchSize
	t0 := time.Now()
	totalDetails := 0

	for i := 0; i < len(ids); i += batchSize {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := i + batchSize
		if endIdx > len(ids) {
			endIdx = len(ids)
		}
		batch := ids[i:endIdx]
		batchNum := i/batchSize + 1

		resp, err := client.GetCalendarPromotionDetails(ctx, batch, rateLimit, burst)
		if err != nil {
			fmt.Printf("   [%s] Error: %v\n", formatProgress(batchNum, totalBatches, t0), err)
			continue
		}

		if err := repo.SaveCalendarPromotionDetails(ctx, resp.Data.Promotions); err != nil {
			fmt.Printf("   Error saving details: %v\n", err)
			continue
		}
		totalDetails += len(resp.Data.Promotions)
		fmt.Printf("   [%s] %d details saved\n", formatProgress(batchNum, totalBatches, t0), totalDetails)
	}

	fmt.Printf("   Done: %d promotion details (%s)\n", totalDetails, time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadCalendarPromotionNomenclatures downloads products for each promotion.
// Fetches both inAction=true (participating) and inAction=false (eligible) products.
// Skips auto-promotions (API returns 422 for them). Paginated (1000 per request).
func DownloadCalendarPromotionNomenclatures(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, rateLimit, burst int) error {
	ids, err := repo.GetCalendarPromotionIDsByType(ctx, "auto")
	if err != nil {
		return fmt.Errorf("get promotion ids: %w", err)
	}
	if len(ids) == 0 {
		fmt.Println("   No non-auto promotions found.")
		return nil
	}

	snapshotDate := time.Now().Format("2006-01-02")
	t0 := time.Now()
	totalNoms := 0
	total := len(ids)

	for i, promoID := range ids {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i, total)
			return ctx.Err()
		}
		var allNoms []wb.CalendarPromotionNom
		for _, inAction := range []bool{false, true} {
			offset := 0
			for {
				const limit = 1000
				resp, err := client.GetCalendarPromotionNomenclatures(ctx, promoID, inAction, limit, offset, rateLimit, burst)
				if err != nil {
					if wb.IsHTTPError(err, 422) {
						break // promotion completed or not applicable
					}
					fmt.Printf("   Error promo=%d inAction=%v offset=%d: %v\n", promoID, inAction, offset, err)
					break
				}
				if len(resp.Data.Nomenclatures) == 0 {
					break
				}

				allNoms = append(allNoms, resp.Data.Nomenclatures...)
				totalNoms += len(resp.Data.Nomenclatures)

				if len(resp.Data.Nomenclatures) < limit {
					break
				}
				offset += limit
			}
		}
		if len(allNoms) > 0 {
			if err := repo.SaveCalendarPromotionNomenclatures(ctx, promoID, allNoms, snapshotDate); err != nil {
				fmt.Printf("   Error saving noms promo=%d: %v\n", promoID, err)
			}
		}
		if (i+1)%10 == 0 || i+1 == total {
			fmt.Printf("   [%s] %d noms\n", formatProgress(i+1, total, t0), totalNoms)
		}
	}

	fmt.Printf("   Done: %d nomenclatures from %d promotions (%s)\n", totalNoms, len(ids), time.Since(t0).Truncate(time.Second))
	return nil
}

// downloadBatched is a helper for batched normquery endpoints.
func downloadBatched(
	ctx context.Context,
	client V2Client,
	repo *sqlite.SQLiteSalesRepository,
	productIDs []wb.NormqueryItem,
	rateLimit, burst int,
	label string,
	fn func(ctx context.Context, batch []wb.NormqueryItem) (int, error),
) error {
	const batchSize = 100
	totalBatches := (len(productIDs) + batchSize - 1) / batchSize
	t0 := time.Now()
	total := 0

	for i := 0; i < len(productIDs); i += batchSize {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := i + batchSize
		if endIdx > len(productIDs) {
			endIdx = len(productIDs)
		}
		batch := productIDs[i:endIdx]
		batchNum := i/batchSize + 1

		count, err := fn(ctx, batch)
		if err != nil {
			fmt.Printf("   [%s] Error: %v\n", formatProgress(batchNum, totalBatches, t0), err)
			continue
		}
		total += count
		fmt.Printf("   [%s] %d %s\n", formatProgress(batchNum, totalBatches, t0), count, label)
	}

	fmt.Printf("   Done: %d %s from %d products (%s)\n", total, label, len(productIDs), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadCampaignBudgets downloads budget for each campaign.
func DownloadCampaignBudgets(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	seen := make(map[int]bool)
	var advertIDs []int
	for _, p := range productIDs {
		if !seen[p.AdvertID] {
			seen[p.AdvertID] = true
			advertIDs = append(advertIDs, p.AdvertID)
		}
	}

	t0 := time.Now()
	snapshotDate := time.Now().Format("2006-01-02")
	total := 0
	totalAds := len(advertIDs)

	for i, id := range advertIDs {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", i, totalAds)
			return ctx.Err()
		}
		budget, err := client.GetCampaignBudget(ctx, id, rateLimit, burst)
		if err != nil {
			fmt.Printf("   Error advert=%d: %v\n", id, err)
			continue
		}
		if err := repo.SaveCampaignBudget(ctx, id, *budget, snapshotDate); err != nil {
			fmt.Printf("   Error saving budget advert=%d: %v\n", id, err)
			continue
		}
		total++
		if (i+1)%100 == 0 || i+1 == totalAds {
			fmt.Printf("   [%s] %d budgets saved\n", formatProgress(i+1, totalAds, t0), total)
		}
	}

	fmt.Printf("   Done: %d campaign budgets (%s)\n", total, time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadMinBids downloads minimum bids for each (campaign, product).
func DownloadMinBids(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	// Group nm_ids by advert_id for batched requests
	byAdvert := make(map[int][]int)
	for _, p := range productIDs {
		byAdvert[p.AdvertID] = append(byAdvert[p.AdvertID], p.NmID)
	}

	t0 := time.Now()
	snapshotDate := time.Now().Format("2006-01-02")
	totalBids := 0
	advertList := make([]int, 0, len(byAdvert))
	for id := range byAdvert {
		advertList = append(advertList, id)
	}
	totalAds := len(advertList)

	for ai, advertID := range advertList {
		if ctx.Err() != nil {
			fmt.Printf("   Interrupted [%d/%d]\n", ai, totalAds)
			return ctx.Err()
		}
		nmIDs := byAdvert[advertID]
		// Batch nm_ids (max 100 per request)
		for i := 0; i < len(nmIDs); i += 100 {
			if ctx.Err() != nil {
				fmt.Printf("   Interrupted [%d/%d]\n", ai, totalAds)
				return ctx.Err()
			}
			endIdx := i + 100
			if endIdx > len(nmIDs) {
				endIdx = len(nmIDs)
			}
			batch := nmIDs[i:endIdx]

			req := wb.MinBidsRequest{
				AdvertID:       advertID,
				NmIDs:          batch,
				PaymentType:    "cpm",
				PlacementTypes: []string{"combined", "search", "recommendation"},
			}
			resp, err := client.GetMinBids(ctx, req, rateLimit, burst)
			if err != nil {
				if wb.IsHTTPError(err, 400) {
					continue // nm_ids no longer belong to this campaign
				}
				fmt.Printf("   Error advert=%d nm=%d-%d: %v\n", advertID, batch[0], batch[len(batch)-1], err)
				continue
			}
			if err := repo.SaveMinBids(ctx, advertID, resp.Bids, snapshotDate); err != nil {
				fmt.Printf("   Error saving min_bids advert=%d: %v\n", advertID, err)
				continue
			}
			for _, item := range resp.Bids {
				totalBids += len(item.Bids)
			}
		}
		if (ai+1)%100 == 0 || ai+1 == totalAds {
			fmt.Printf("   [%s] %d bids from %d campaigns\n", formatProgress(ai+1, totalAds, t0), totalBids, ai+1)
		}
	}

	fmt.Printf("   Done: %d min-bid entries from %d campaigns (%s)\n", totalBids, len(byAdvert), time.Since(t0).Truncate(time.Second))
	return nil
}
