// Package main provides V2 promotion download phases.
package main

import (
	"context"
	"fmt"
	"time"

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
	GetCalendarPromotions(ctx context.Context, rateLimit, burst int) (*wb.CalendarPromotionsResponse, error)
	GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*wb.CalendarPromotionDetailsResponse, error)
	GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*wb.CalendarPromotionNomsResponse, error)
	GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*wb.BudgetResponse, error)
	GetMinBids(ctx context.Context, req wb.MinBidsRequest, rateLimit, burst int) (*wb.MinBidsResponse, error)
}

// DownloadCampaignBids extracts bid data from AdvertDetails and saves to campaign_bids table.
func DownloadCampaignBids(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	// Collect unique advert IDs
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
		endIdx := i + batchSize
		if endIdx > len(advertIDs) {
			endIdx = len(advertIDs)
		}
		batch := advertIDs[i:endIdx]

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
		fmt.Printf("   [%d/%d] %d bids saved\n", (i/batchSize)+1, totalBatches, len(rows))
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
		endIdx := i + batchSize
		if endIdx > len(productIDs) {
			endIdx = len(productIDs)
		}
		batch := productIDs[i:endIdx]

		req := wb.NormqueryStatsRequest{
			From:  beginDate,
			To:    endDate,
			Items: batch,
		}
		resp, err := client.GetNormqueryStats(ctx, req, rateLimit, burst)
		if err != nil {
			fmt.Printf("   Error batch %d-%d: %v\n", i+1, endIdx, err)
			continue
		}

		for _, group := range resp.Stats {
			if len(group.Stats) == 0 {
				continue
			}
			// Use beginDate as stats_date (API returns aggregated stats for the period)
			if err := repo.SaveNormqueryStats(ctx, group.AdvertID, group.NmID, beginDate, group.Stats); err != nil {
				fmt.Printf("   Error saving stats advert=%d nm=%d: %v\n", group.AdvertID, group.NmID, err)
			}
			totalStats += len(group.Stats)
		}
		fmt.Printf("   [%d/%d] %d clusters\n", (i/batchSize)+1, totalBatches, totalStats)
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
func DownloadBidRecommendations(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, productIDs []wb.NormqueryItem, rateLimit, burst int) error {
	t0 := time.Now()
	total := 0
	snapshotDate := time.Now().Format("2006-01-02")

	for i, p := range productIDs {
		resp, err := client.GetBidRecommendations(ctx, p.NmID, p.AdvertID, rateLimit, burst)
		if err != nil {
			fmt.Printf("   Error nm=%d advert=%d: %v\n", p.NmID, p.AdvertID, err)
			continue
		}
		if err := repo.SaveBidRecommendations(ctx, []wb.BidRecommendationsResponse{*resp}, snapshotDate); err != nil {
			fmt.Printf("   Error saving: %v\n", err)
			continue
		}
		total++
		if (i+1)%100 == 0 {
			elapsed := time.Since(t0).Truncate(time.Second)
			fmt.Printf("   [%d/%d] %d recommendations (%s)\n", i+1, len(productIDs), total, elapsed)
		}
	}

	fmt.Printf("   Done: %d recommendations (%s)\n", total, time.Since(t0).Truncate(time.Second))
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
	if err := repo.SavePayments(ctx, payments); err != nil {
		return fmt.Errorf("save payments: %w", err)
	}
	fmt.Printf("   Done: %d payment records (%s)\n", len(payments), time.Since(t0).Truncate(time.Second))
	return nil
}

// DownloadCalendarPromotions downloads WB promotions calendar.
func DownloadCalendarPromotions(ctx context.Context, client V2Client, repo *sqlite.SQLiteSalesRepository, rateLimit, burst int) error {
	t0 := time.Now()
	promos, err := client.GetCalendarPromotions(ctx, rateLimit, burst)
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
	t0 := time.Now()
	totalDetails := 0

	for i := 0; i < len(ids); i += batchSize {
		endIdx := i + batchSize
		if endIdx > len(ids) {
			endIdx = len(ids)
		}
		batch := ids[i:endIdx]

		resp, err := client.GetCalendarPromotionDetails(ctx, batch, rateLimit, burst)
		if err != nil {
			fmt.Printf("   Error batch %d-%d: %v\n", i+1, endIdx, err)
			continue
		}

		if err := repo.SaveCalendarPromotionDetails(ctx, resp.Data.Promotions); err != nil {
			fmt.Printf("   Error saving details: %v\n", err)
			continue
		}
		totalDetails += len(resp.Data.Promotions)
		fmt.Printf("   [%d/%d] %d details saved\n", (i/batchSize)+1, (len(ids)+batchSize-1)/batchSize, totalDetails)
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

	for _, promoID := range ids {
		var allNoms []wb.CalendarPromotionNom
		for _, inAction := range []bool{false, true} {
			offset := 0
			for {
				const limit = 1000
				resp, err := client.GetCalendarPromotionNomenclatures(ctx, promoID, inAction, limit, offset, rateLimit, burst)
				if err != nil {
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
		fmt.Printf("   Promo %d: done\n", promoID)
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
		endIdx := i + batchSize
		if endIdx > len(productIDs) {
			endIdx = len(productIDs)
		}
		batch := productIDs[i:endIdx]

		count, err := fn(ctx, batch)
		if err != nil {
			fmt.Printf("   Error batch %d-%d: %v\n", i+1, endIdx, err)
			continue
		}
		total += count
		fmt.Printf("   [%d/%d] %d %s\n", (i/batchSize)+1, totalBatches, count, label)
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

	for _, id := range advertIDs {
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

	for advertID, nmIDs := range byAdvert {
		// Batch nm_ids (max 100 per request)
		for i := 0; i < len(nmIDs); i += 100 {
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
	}

	fmt.Printf("   Done: %d min-bid entries from %d campaigns (%s)\n", totalBids, len(byAdvert), time.Since(t0).Truncate(time.Second))
	return nil
}
