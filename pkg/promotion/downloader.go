package promotion

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader orchestrates all 14 promotion V2 download phases.
// Depends only on interfaces (Source, Writer, Reader) — no concrete types.
type Downloader struct {
	source Source
	writer Writer
	reader Reader
	opts   DownloadOptions
}

// NewDownloader creates a new promotion V2 downloader.
func NewDownloader(source Source, writer Writer, reader Reader, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		reader: reader,
		opts:   opts,
	}
}

// Run executes all enabled download phases sequentially.
// Phase errors are logged but do not stop subsequent phases.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	hasV1Data := len(d.opts.ProductIDs) > 0
	if !hasV1Data {
		dllog.Log("no V1 campaign products — skipping V1-dependent phases, running calendar/finance only")
	}

	totalSteps := d.countSteps(hasV1Data)
	if totalSteps == 0 {
		dllog.Log("nothing to do (all steps skipped)")
		return &DownloadResult{}, nil
	}

	result := &DownloadResult{TotalSteps: totalSteps}
	stepNum := 0
	rl := d.opts.RateLimits
	t0 := time.Now()

	// Phase 1: Campaign Bids (from AdvertDetails)
	if hasV1Data && !d.opts.SkipBids {
		stepNum++
		dllog.Log("[%d/%d] Campaign Bids...", stepNum, totalSteps)
		if err := d.downloadCampaignBids(ctx); err != nil {
			dllog.Error("Campaign Bids: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 2-5: Normquery
	if hasV1Data && !d.opts.SkipNormquery {
		stepNum++
		dllog.Log("[%d/%d] Normquery Stats...", stepNum, totalSteps)
		if err := d.downloadNormqueryStats(ctx, rl.NormqueryStats, rl.NormqueryStatsBurst); err != nil {
			dllog.Error("Normquery Stats: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Normquery Clusters...", stepNum, totalSteps)
		if err := d.downloadNormqueryClusters(ctx, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Clusters: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Normquery Bids...", stepNum, totalSteps)
		if err := d.downloadNormqueryBids(ctx, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Bids: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Normquery Minus...", stepNum, totalSteps)
		if err := d.downloadNormqueryMinus(ctx, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Minus: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 6: Bid Recommendations
	if hasV1Data && !d.opts.SkipRecommendations {
		stepNum++
		dllog.Log("[%d/%d] Bid Recommendations...", stepNum, totalSteps)
		if err := d.downloadBidRecommendations(ctx, rl.BidRec, rl.BidRecBurst); err != nil {
			dllog.Error("Bid Recommendations: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 7-9: Finance
	if !d.opts.SkipFinance {
		stepNum++
		dllog.Log("[%d/%d] Expenses...", stepNum, totalSteps)
		if err := d.downloadExpenses(ctx, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Expenses: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Balance...", stepNum, totalSteps)
		if err := d.downloadBalance(ctx, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Balance: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Payments...", stepNum, totalSteps)
		if err := d.downloadPayments(ctx, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Payments: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 10-12: Calendar
	if !d.opts.SkipCalendar {
		stepNum++
		dllog.Log("[%d/%d] Calendar Promotions (%s -> %s)...", stepNum, totalSteps, d.opts.CalendarBegin, d.opts.CalendarEnd)
		if err := d.downloadCalendarPromotions(ctx, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Calendar Details...", stepNum, totalSteps)
		if err := d.downloadCalendarPromotionDetails(ctx, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar Details: %v", err)
			result.Errors++
		}
		result.CompletedSteps++

		stepNum++
		dllog.Log("[%d/%d] Calendar Nomenclatures...", stepNum, totalSteps)
		if err := d.downloadCalendarPromotionNomenclatures(ctx, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar Nomenclatures: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 13: Campaign Budgets
	if hasV1Data && !d.opts.SkipBudgets {
		stepNum++
		dllog.Log("[%d/%d] Campaign Budgets...", stepNum, totalSteps)
		if err := d.downloadCampaignBudgets(ctx, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Campaign Budgets: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	// Phase 14: Minimum Bids
	if hasV1Data && !d.opts.SkipMinBids {
		stepNum++
		dllog.Log("[%d/%d] Minimum Bids...", stepNum, totalSteps)
		if err := d.downloadMinBids(ctx, rl.MinBids, rl.MinBidsBurst); err != nil {
			dllog.Error("Minimum Bids: %v", err)
			result.Errors++
		}
		result.CompletedSteps++
	}

	result.Duration = time.Since(t0)
	return result, nil
}

// countSteps returns the number of phases that will be executed.
func (d *Downloader) countSteps(hasV1Data bool) int {
	total := 0
	if hasV1Data && !d.opts.SkipBids {
		total++
	}
	if hasV1Data && !d.opts.SkipNormquery {
		total += 4 // stats + clusters + bids + minus
	}
	if hasV1Data && !d.opts.SkipRecommendations {
		total++
	}
	if !d.opts.SkipFinance {
		total += 3 // expenses + balance + payments
	}
	if !d.opts.SkipCalendar {
		total += 3 // promotions + details + nomenclatures
	}
	if hasV1Data && !d.opts.SkipBudgets {
		total++
	}
	if hasV1Data && !d.opts.SkipMinBids {
		total++
	}
	return total
}

// ============================================================================
// Phase 1: Campaign Bids
// ============================================================================

func (d *Downloader) downloadCampaignBids(ctx context.Context) error {
	productIDs := d.opts.ProductIDs

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
		endIdx := min(i+batchSize, len(advertIDs))
		batch := advertIDs[i:endIdx]
		batchNum := i/batchSize + 1

		details, err := d.source.GetAdvertDetails(ctx, batch)
		if err != nil {
			dllog.Error("batch %d-%d: %v", i+1, endIdx, err)
			continue
		}

		var rows []wb.CampaignBidRow
		for _, det := range details {
			for _, nm := range det.NmSettings {
				rows = append(rows, wb.CampaignBidRow{
					AdvertID:    det.ID,
					NmID:        nm.NmID,
					SubjectID:   nm.Subject.ID,
					SubjectName: nm.Subject.Name,
					BidSearch:   nm.BidsKopecks.Search,
					BidReco:     nm.BidsKopecks.Recommendations,
				})
			}
		}

		if !d.opts.DryRun {
			if err := d.writer.SaveCampaignBids(ctx, rows); err != nil {
				dllog.Error("saving bids: %v", err)
				continue
			}
		}
		totalBids += len(rows)
		dllog.Progress(batchNum, totalBatches, "campaign-bids", fmt.Sprintf("%d saved", len(rows)), t0)
	}

	dllog.Done(time.Since(t0), "%d bids from %d campaigns", totalBids, len(advertIDs))
	return nil
}

// ============================================================================
// Phase 2: Normquery Stats
// ============================================================================

func (d *Downloader) downloadNormqueryStats(ctx context.Context, rateLimit, burst int) error {
	productIDs := d.opts.ProductIDs
	beginDate := d.opts.BeginDate
	endDate := d.opts.EndDate

	const batchSize = 100
	totalBatches := (len(productIDs) + batchSize - 1) / batchSize
	t0 := time.Now()
	totalStats := 0
	var failedBatches []wb.NormqueryItem

	for i := 0; i < len(productIDs); i += batchSize {
		if ctx.Err() != nil {
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := min(i+batchSize, len(productIDs))
		batch := productIDs[i:endIdx]
		batchNum := i/batchSize + 1

		req := wb.NormqueryStatsRequest{
			From:  beginDate,
			To:    endDate,
			Items: batch,
		}
		tAPIStart := time.Now()
		resp, err := d.source.GetNormqueryStats(ctx, req, rateLimit, burst)
		apiDur := time.Since(tAPIStart)
		if err != nil {
			dllog.Error("batch %d: %v (api=%.1fs)", batchNum, err, apiDur.Seconds())
			failedBatches = append(failedBatches, batch...)
			continue
		}

		tDBStart := time.Now()
		if len(resp.Stats) > 0 && !d.opts.DryRun {
			if err := d.writer.SaveNormqueryStatsBatch(ctx, resp.Stats, beginDate); err != nil {
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

	// Retry failed batches in a second pass
	if len(failedBatches) > 0 {
		retryTotal := (len(failedBatches) + batchSize - 1) / batchSize
		retrySaved := 0
		retrySkipped := 0
		dllog.Log("retrying %d failed batches (%d items)...", retryTotal, len(failedBatches))
		for i := 0; i < len(failedBatches); i += batchSize {
			if ctx.Err() != nil {
				dllog.Log("interrupted during retry")
				return ctx.Err()
			}
			endIdx := min(i+batchSize, len(failedBatches))
			batch := failedBatches[i:endIdx]
			batchNum := i/batchSize + 1

			req := wb.NormqueryStatsRequest{
				From:  beginDate,
				To:    endDate,
				Items: batch,
			}
			resp, err := d.source.GetNormqueryStats(ctx, req, rateLimit, burst)
			if err != nil {
				retrySkipped += len(batch)
				dllog.Error("retry batch %d/%d: %v — skipped %d items", batchNum, retryTotal, err, len(batch))
				continue
			}
			if len(resp.Stats) > 0 && !d.opts.DryRun {
				if err := d.writer.SaveNormqueryStatsBatch(ctx, resp.Stats, beginDate); err != nil {
					dllog.Error("retry batch save: %v", err)
				}
				for _, group := range resp.Stats {
					totalStats += len(group.Stats)
				}
			}
			retrySaved += len(batch)
			dllog.Log("retry batch %d/%d ok", batchNum, retryTotal)
		}
		if retrySaved > 0 {
			dllog.Log("retry recovered %d items", retrySaved)
		}
		if retrySkipped > 0 {
			dllog.Error("%d items skipped (failed all retries)", retrySkipped)
		}
	}

	dllog.Done(time.Since(t0), "%d stat rows from %d products", totalStats, len(productIDs))
	return nil
}

// ============================================================================
// Phase 3: Normquery Clusters
// ============================================================================

func (d *Downloader) downloadNormqueryClusters(ctx context.Context, rateLimit, burst int) error {
	return d.downloadBatched(ctx, "clusters", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := d.source.GetNormqueryList(ctx, wb.NormqueryListRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if !d.opts.DryRun {
			if err := d.writer.SaveNormqueryClusters(ctx, resp.Items); err != nil {
				return 0, err
			}
		}
		total := 0
		for _, item := range resp.Items {
			total += len(item.NormQueries.Active) + len(item.NormQueries.Excluded)
		}
		return total, nil
	})
}

// ============================================================================
// Phase 4: Normquery Bids
// ============================================================================

func (d *Downloader) downloadNormqueryBids(ctx context.Context, rateLimit, burst int) error {
	return d.downloadBatched(ctx, "bids", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := d.source.GetNormqueryBids(ctx, wb.NormqueryBidsRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if !d.opts.DryRun {
			if err := d.writer.SaveNormqueryBids(ctx, resp.Bids); err != nil {
				return 0, err
			}
		}
		return len(resp.Bids), nil
	})
}

// ============================================================================
// Phase 5: Normquery Minus
// ============================================================================

func (d *Downloader) downloadNormqueryMinus(ctx context.Context, rateLimit, burst int) error {
	return d.downloadBatched(ctx, "minus-phrases", func(ctx context.Context, batch []wb.NormqueryItem) (int, error) {
		resp, err := d.source.GetNormqueryMinus(ctx, wb.NormqueryMinusRequest{Items: batch}, rateLimit, burst)
		if err != nil {
			return 0, err
		}
		if !d.opts.DryRun {
			if err := d.writer.SaveNormqueryMinus(ctx, resp.Items); err != nil {
				return 0, err
			}
		}
		total := 0
		for _, item := range resp.Items {
			total += len(item.NormQueries)
		}
		return total, nil
	})
}

// ============================================================================
// Phase 6: Bid Recommendations
// ============================================================================

func (d *Downloader) downloadBidRecommendations(ctx context.Context, rateLimit, burst int) error {
	productIDs := d.opts.ProductIDs
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

		resp, err := d.source.GetBidRecommendations(ctx, p.NmID, p.AdvertID, rateLimit, burst)
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
		if !d.opts.DryRun {
			if err := d.writer.SaveBidRecommendations(ctx, []wb.BidRecommendationsResponse{*resp}, snapshotDate); err != nil {
				dllog.Error("saving bid recs: %v", err)
				continue
			}
		}
		total++
		if (i+1)%reportInterval == 0 {
			dllog.Progress(i+1, len(productIDs), "bid-recs", fmt.Sprintf("%d ok, %d stale", total, staleCount), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d recommendations, %d stale (%d campaigns)", total, staleCount, len(staleAdverts))
	return nil
}

// ============================================================================
// Phase 7: Expenses
// ============================================================================

func (d *Downloader) downloadExpenses(ctx context.Context, rateLimit, burst int) error {
	t0 := time.Now()
	expenses, err := d.source.GetExpenses(ctx, d.opts.BeginDate, d.opts.EndDate, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get expenses: %w", err)
	}
	if !d.opts.DryRun {
		if err := d.writer.SaveExpenses(ctx, expenses); err != nil {
			return fmt.Errorf("save expenses: %w", err)
		}
	}
	dllog.Done(time.Since(t0), "%d expense records", len(expenses))
	return nil
}

// ============================================================================
// Phase 8: Balance
// ============================================================================

func (d *Downloader) downloadBalance(ctx context.Context, rateLimit, burst int) error {
	t0 := time.Now()
	balance, err := d.source.GetBalance(ctx, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get balance: %w", err)
	}
	snapshotDate := time.Now().Format("2006-01-02")
	if !d.opts.DryRun {
		if err := d.writer.SaveBalance(ctx, *balance, snapshotDate); err != nil {
			return fmt.Errorf("save balance: %w", err)
		}
	}
	dllog.Done(time.Since(t0), "balance=%d, net=%d, bonus=%d", balance.Balance, balance.Net, balance.Bonus)
	return nil
}

// ============================================================================
// Phase 9: Payments
// ============================================================================

func (d *Downloader) downloadPayments(ctx context.Context, rateLimit, burst int) error {
	t0 := time.Now()
	payments, err := d.source.GetPayments(ctx, d.opts.BeginDate, d.opts.EndDate, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get payments: %w", err)
	}
	if payments == nil {
		dllog.Log("no payments in period")
		return nil
	}
	if !d.opts.DryRun {
		if err := d.writer.SavePayments(ctx, payments); err != nil {
			return fmt.Errorf("save payments: %w", err)
		}
	}
	dllog.Done(time.Since(t0), "%d payment records", len(payments))
	return nil
}

// ============================================================================
// Phase 10: Calendar Promotions
// ============================================================================

func (d *Downloader) downloadCalendarPromotions(ctx context.Context, rateLimit, burst int) error {
	t0 := time.Now()
	// Calendar API requires datetime format: YYYY-MM-DDTHH:MM:SSZ
	start := d.opts.CalendarBegin + "T00:00:00Z"
	end := d.opts.CalendarEnd + "T23:59:59Z"
	promos, err := d.source.GetCalendarPromotions(ctx, start, end, true, rateLimit, burst)
	if err != nil {
		return fmt.Errorf("get calendar: %w", err)
	}
	if !d.opts.DryRun {
		if err := d.writer.SaveCalendarPromotions(ctx, promos.Data.Promotions); err != nil {
			return fmt.Errorf("save calendar: %w", err)
		}
	}
	dllog.Done(time.Since(t0), "%d promotions", len(promos.Data.Promotions))
	return nil
}

// ============================================================================
// Phase 11: Calendar Promotion Details
// ============================================================================

func (d *Downloader) downloadCalendarPromotionDetails(ctx context.Context, rateLimit, burst int) error {
	ids, err := d.reader.GetCalendarPromotionIDs(ctx)
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
		endIdx := min(i+batchSize, len(ids))
		batch := ids[i:endIdx]
		batchNum := i/batchSize + 1

		resp, err := d.source.GetCalendarPromotionDetails(ctx, batch, rateLimit, burst)
		if err != nil {
			dllog.Error("details batch %d: %v", batchNum, err)
			continue
		}

		if !d.opts.DryRun {
			if err := d.writer.SaveCalendarPromotionDetails(ctx, resp.Data.Promotions); err != nil {
				dllog.Error("saving details: %v", err)
				continue
			}
		}
		totalDetails += len(resp.Data.Promotions)
		dllog.Progress(batchNum, totalBatches, "calendar-details", fmt.Sprintf("%d saved", totalDetails), t0)
	}

	dllog.Done(time.Since(t0), "%d promotion details", totalDetails)
	return nil
}

// ============================================================================
// Phase 12: Calendar Promotion Nomenclatures
// ============================================================================

func (d *Downloader) downloadCalendarPromotionNomenclatures(ctx context.Context, rateLimit, burst int) error {
	ids, err := d.reader.GetCalendarPromotionIDsByType(ctx, "auto")
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
				resp, err := d.source.GetCalendarPromotionNomenclatures(ctx, promoID, inAction, limit, offset, rateLimit, burst)
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
		if len(allNoms) > 0 && !d.opts.DryRun {
			if err := d.writer.SaveCalendarPromotionNomenclatures(ctx, promoID, allNoms, snapshotDate); err != nil {
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

// ============================================================================
// Phase 13: Campaign Budgets
// ============================================================================

func (d *Downloader) downloadCampaignBudgets(ctx context.Context, rateLimit, burst int) error {
	productIDs := d.opts.ProductIDs

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
		budget, err := d.source.GetCampaignBudget(ctx, id, rateLimit, burst)
		if err != nil {
			dllog.Error("advert=%d: %v", id, err)
			continue
		}
		if !d.opts.DryRun {
			if err := d.writer.SaveCampaignBudget(ctx, id, *budget, snapshotDate); err != nil {
				dllog.Error("saving budget advert=%d: %v", id, err)
				continue
			}
		}
		total++
		if (i+1)%100 == 0 || i+1 == totalAds {
			dllog.Progress(i+1, totalAds, "budgets", fmt.Sprintf("%d saved", total), t0)
		}
	}

	dllog.Done(time.Since(t0), "%d campaign budgets", total)
	return nil
}

// ============================================================================
// Phase 14: Minimum Bids
// ============================================================================

func (d *Downloader) downloadMinBids(ctx context.Context, rateLimit, burst int) error {
	productIDs := d.opts.ProductIDs

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
			endIdx := min(i+100, len(nmIDs))
			batch := nmIDs[i:endIdx]

			req := wb.MinBidsRequest{
				AdvertID:       advertID,
				NmIDs:          batch,
				PaymentType:    "cpm",
				PlacementTypes: []string{"combined", "search", "recommendation"},
			}
			resp, err := d.source.GetMinBids(ctx, req, rateLimit, burst)
			if err != nil {
				if wb.IsHTTPError(err, 400) {
					continue // nm_ids no longer belong to this campaign
				}
				dllog.Error("advert=%d nm=%d-%d: %v", advertID, batch[0], batch[len(batch)-1], err)
				continue
			}
			if !d.opts.DryRun {
				if err := d.writer.SaveMinBids(ctx, advertID, resp.Bids, snapshotDate); err != nil {
					dllog.Error("saving min_bids advert=%d: %v", advertID, err)
					continue
				}
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

// ============================================================================
// downloadBatched — generic helper for batched normquery endpoints
// ============================================================================

func (d *Downloader) downloadBatched(
	ctx context.Context,
	label string,
	fn func(ctx context.Context, batch []wb.NormqueryItem) (int, error),
) error {
	const batchSize = 100
	productIDs := d.opts.ProductIDs
	totalBatches := (len(productIDs) + batchSize - 1) / batchSize
	t0 := time.Now()
	total := 0

	for i := 0; i < len(productIDs); i += batchSize {
		if ctx.Err() != nil {
			dllog.Log("interrupted [%d/%d]", i/batchSize+1, totalBatches)
			return ctx.Err()
		}
		endIdx := min(i+batchSize, len(productIDs))
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
