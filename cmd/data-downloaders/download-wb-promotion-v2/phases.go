// Package main provides V2 promotion download phases.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

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
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
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
			dllog.Error("batch %d-%d: %v", i+1, endIdx, err)
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
			dllog.Error("saving bids: %v", err)
			continue
		}
		totalBids += len(rows)
		dllog.Progress(batchNum, totalBatches, "campaign-bids", fmt.Sprintf("%d saved", len(rows)), t0)
	}

	dllog.Done(time.Since(t0), "%d bids from %d campaigns", totalBids, len(advertIDs))
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
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
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
		tAPIStart := time.Now()
		resp, err := client.GetNormqueryStats(ctx, req, rateLimit, burst)
		apiDur := time.Since(tAPIStart)
		if err != nil {
			dllog.Error("batch %d: %v (api=%.1fs)", batchNum, err, apiDur.Seconds())
			continue
		}

		tDBStart := time.Now()
		if len(resp.Stats) > 0 {
			if err := repo.SaveNormqueryStatsBatch(ctx, resp.Stats, beginDate); err != nil {
				dllog.Error("batch save: %v", err)
			}
			for _, group := range resp.Stats {
				totalStats += len(group.Stats)
			}
		}
		dbDur := time.Since(tDBStart)
		dllog.Progress(batchNum, totalBatches, "normquery-stats",
			fmt.Sprintf("%d clusters (api=%.1fs, db=%.1fs)", totalStats, apiDur.Seconds(), dbDur.Seconds()), t0)
	}

	dllog.Done(time.Since(t0), "%d stat rows from %d products", totalStats, len(productIDs))
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
	return downloadBatched(ctx, client, repo, productIDs, rateLimit, burst, "minus-phrases", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
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
			dllog.Log("interrupted [%d/%d]", i, len(productIDs))
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
				dllog.Log("stale: nm=%d no longer in advert=%d (skipping remaining nms)", p.NmID, p.AdvertID)
				continue
			}
			dllog.Error("nm=%d advert=%d: %v", p.NmID, p.AdvertID, err)
			continue
		}
		if err := repo.SaveBidRecommendations(ctx, []wb.BidRecommendationsResponse{*resp}, snapshotDate); err != nil {
			dllog.Error("saving bid recs: %v", err)
			continue
		}
		total++
		if (i+1)%reportInterval == 0 {
			dllog.Progress(i+1, len(productIDs), "bid-recs", fmt.Sprintf("%d ok, %d stale", total, staleCount), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d recommendations, %d stale (%d campaigns)", total, staleCount, len(staleAdverts))
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
	dllog.Done(time.Since(t0), "%d expense records", len(expenses))
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
	dllog.Done(time.Since(t0), "balance=%d, net=%d, bonus=%d", balance.Balance, balance.Net, balance.Bonus)
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
		dllog.Log("no payments in period")
		return nil
	}
	if err := repo.SavePayments(ctx, payments); err != nil {
		return fmt.Errorf("save payments: %w", err)
	}
	dllog.Done(time.Since(t0), "%d payment records", len(payments))
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
	dllog.Done(time.Since(t0), "%d promotions", len(promos.Data.Promotions))
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
		dllog.Log("no calendar promotions found — run Calendar phase first")
		return nil
	}

	const batchSize = 100
	totalBatches := (len(ids) + batchSize - 1) / batchSize
	t0 := time.Now()
	totalDetails := 0

	for i := 0; i < len(ids); i += batchSize {
		if ctx.Err() != nil {
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
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
			dllog.Error("details batch %d: %v", batchNum, err)
			continue
		}

		if err := repo.SaveCalendarPromotionDetails(ctx, resp.Data.Promotions); err != nil {
			dllog.Error("saving details: %v", err)
			continue
		}
		totalDetails += len(resp.Data.Promotions)
		dllog.Progress(batchNum, totalBatches, "calendar-details", fmt.Sprintf("%d saved", totalDetails), t0)
	}

	dllog.Done(time.Since(t0), "%d promotion details", totalDetails)
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
		dllog.Log("no non-auto promotions found")
		return nil
	}

	snapshotDate := time.Now().Format("2006-01-02")
	t0 := time.Now()
	totalNoms := 0
	total := len(ids)
	var errCount, emptyCount, done422 int

	for i, promoID := range ids {
		if ctx.Err() != nil {
			dllog.Log("interrupted [%d/%d]", i, total)
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
						done422++
						break // promotion completed or not applicable
					}
					errCount++
					dllog.Error("promo=%d inAction=%v offset=%d: %v", promoID, inAction, offset, err)
					break
				}
				if len(resp.Data.Nomenclatures) == 0 {
					emptyCount++
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
				dllog.Error("saving noms promo=%d: %v", promoID, err)
			}
		}
		if (i+1)%10 == 0 || i+1 == total {
			dllog.Progress(i+1, total, "calendar-noms", fmt.Sprintf("%d noms", totalNoms), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d noms from %d promos (422=%d, empty=%d, errors=%d)",
		totalNoms, len(ids), done422, emptyCount, errCount)

	if totalNoms == 0 && (errCount > 0 || len(ids) > 0) {
		return fmt.Errorf("calendar nomenclatures: 0 results from %d promotions (errors=%d, empty=%d, 422=%d) — check API key and calendar endpoint access", len(ids), errCount, emptyCount, done422)
	}
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
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
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
			dllog.Error("batch %d: %v", batchNum, err)
			continue
		}
		total += count
		dllog.Progress(batchNum, totalBatches, label, fmt.Sprintf("%d %s", count, label), t0)
	}

	dllog.Done(time.Since(t0), "%d %s from %d products", total, label, len(productIDs))
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
			dllog.Log("interrupted [%d/%d]", i, totalAds)
			return ctx.Err()
		}
		budget, err := client.GetCampaignBudget(ctx, id, rateLimit, burst)
		if err != nil {
			dllog.Error("advert=%d: %v", id, err)
			continue
		}
		if err := repo.SaveCampaignBudget(ctx, id, *budget, snapshotDate); err != nil {
			dllog.Error("saving budget advert=%d: %v", id, err)
			continue
		}
		total++
		if (i+1)%100 == 0 || i+1 == totalAds {
			dllog.Progress(i+1, totalAds, "budgets", fmt.Sprintf("%d saved", total), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d campaign budgets", total)
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
			dllog.Log("interrupted [%d/%d]", ai, totalAds)
			return ctx.Err()
		}
		nmIDs := byAdvert[advertID]
		// Batch nm_ids (max 100 per request)
		for i := 0; i < len(nmIDs); i += 100 {
			if ctx.Err() != nil {
				dllog.Log("interrupted [%d/%d]", ai, totalAds)
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
				dllog.Error("advert=%d nm=%d-%d: %v", advertID, batch[0], batch[len(batch)-1], err)
				continue
			}
			if err := repo.SaveMinBids(ctx, advertID, resp.Bids, snapshotDate); err != nil {
				dllog.Error("saving min_bids advert=%d: %v", advertID, err)
				continue
			}
			for _, item := range resp.Bids {
				totalBids += len(item.Bids)
			}
		}
		if (ai+1)%100 == 0 || ai+1 == totalAds {
			dllog.Progress(ai+1, totalAds, "min-bids", fmt.Sprintf("%d bids from %d campaigns", totalBids, ai+1), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d min-bid entries from %d campaigns", totalBids, len(byAdvert))
	return nil
}
