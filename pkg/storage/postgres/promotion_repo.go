package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/promotion"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertions: PgPromotionRepo implements promotion.Writer and promotion.Reader.
var (
	_ promotion.Writer = (*PgPromotionRepo)(nil)
	_ promotion.Reader = (*PgPromotionRepo)(nil)
)

const pgPromoChunkSize = 500

// dedupExpenses removes duplicate (advert_id, upd_num) entries (last-write-wins).
// PG ON CONFLICT forbids affecting the same row twice in one multi-row INSERT (SQLSTATE 21000).
func dedupExpenses(rows []wb.ExpenseRow) []wb.ExpenseRow {
	type key struct{ a, b int }
	seen := make(map[key]int, len(rows))
	for i, r := range rows {
		k := key{r.AdvertID, r.UpdNum}
		if prev, ok := seen[k]; ok {
			rows[prev] = r // overwrite with later entry
		} else {
			seen[k] = i
		}
	}
	if len(seen) == len(rows) {
		return rows // no duplicates
	}
	out := make([]wb.ExpenseRow, 0, len(seen))
	for i, r := range rows {
		k := key{r.AdvertID, r.UpdNum}
		if seen[k] == i {
			out = append(out, r)
		}
	}
	return out
}

// dedupCampaignBids removes duplicate (advert_id, nm_id) entries (last-write-wins).
// PG ON CONFLICT forbids affecting the same row twice in one multi-row INSERT (SQLSTATE 21000).
func dedupCampaignBids(rows []wb.CampaignBidRow) []wb.CampaignBidRow {
	type key struct{ a, b int }
	seen := make(map[key]int, len(rows))
	for i, r := range rows {
		k := key{r.AdvertID, r.NmID}
		if prev, ok := seen[k]; ok {
			rows[prev] = r
		} else {
			seen[k] = i
		}
	}
	if len(seen) == len(rows) {
		return rows
	}
	out := make([]wb.CampaignBidRow, 0, len(seen))
	for i, r := range rows {
		k := key{r.AdvertID, r.NmID}
		if seen[k] == i {
			out = append(out, r)
		}
	}
	return out
}

// PgPromotionRepo implements promotion.Writer + promotion.Reader for PostgreSQL.
type PgPromotionRepo struct {
	pool *pgxpool.Pool
}

// NewPgPromotionRepo creates a new PostgreSQL promotion repository.
func NewPgPromotionRepo(pool *pgxpool.Pool) *PgPromotionRepo {
	return &PgPromotionRepo{pool: pool}
}

// InitSchema creates promotion V2 tables if they don't exist.
func (r *PgPromotionRepo) InitSchema(ctx context.Context) error {
	return initPromotionSchema(ctx, r.pool)
}

// ============================================================================
// Writer — 14 save methods
// ============================================================================

// SaveCampaignBids saves campaign bid snapshots using ON CONFLICT upsert.
func (r *PgPromotionRepo) SaveCampaignBids(ctx context.Context, rows []wb.CampaignBidRow) error {
	if len(rows) == 0 {
		return nil
	}
	rows = dedupCampaignBids(rows)
	nowUTC := time.Now().UTC().Format("2006-01-02 15:04:05")

	for i := 0; i < len(rows); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoCampaignBidsCols)
		for _, row := range chunk {
			args = append(args,
				row.AdvertID, row.NmID, row.SubjectID, row.SubjectName,
				row.BidSearch, row.BidReco, nowUTC)
		}

		query := insertPromoCampaignBidsFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoCampaignBidsPrefixSQL, insertPromoCampaignBidsOnConflictSQL, len(chunk), insertPromoCampaignBidsCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert campaign_bids batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit campaign_bids: %w", err)
		}
	}
	return nil
}

// SaveNormqueryStatsBatch saves normquery statistics using ON CONFLICT upsert.
func (r *PgPromotionRepo) SaveNormqueryStatsBatch(ctx context.Context, groups []wb.NormqueryStatsGroup, date string) error {
	if len(groups) == 0 {
		return nil
	}

	// Flatten nested groups into flat stat rows for batching.
	type statFlat struct {
		advertID  int
		nmID      int
		normQuery string
		views     int
		clicks    int
		ctr       float64
		cpc       float64
		cpm       float64
		avgPos    float64
		orders    int
		shks      int
		atbs      int
		spend     float64
	}
	var flat []statFlat
	for _, g := range groups {
		for _, row := range g.Stats {
			var views int
			if row.Views != nil {
				views = *row.Views
			}
			var ctr float64
			if row.CTR != nil {
				ctr = *row.CTR
			}
			var cpm float64
			if row.CPM != nil {
				cpm = *row.CPM
			}
			flat = append(flat, statFlat{
				advertID:  g.AdvertID,
				nmID:      g.NmID,
				normQuery: row.NormQuery,
				views:     views,
				clicks:    row.Clicks,
				ctr:       ctr,
				cpc:       row.CPC,
				cpm:       cpm,
				avgPos:    row.AvgPos,
				orders:    row.Orders,
				shks:      row.SHKS,
				atbs:      row.Atbs,
				spend:     row.Spend,
			})
		}
	}

	for i := 0; i < len(flat); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(flat))
		chunk := flat[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoNormqueryStatsCols)
		for _, s := range chunk {
			args = append(args,
				s.advertID, s.nmID, date, s.normQuery,
				s.views, s.clicks, s.ctr, s.cpc, s.cpm, s.avgPos,
				s.orders, s.shks, s.atbs, s.spend)
		}

		query := insertPromoNormqueryStatsFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoNormqueryStatsPrefixSQL, insertPromoNormqueryStatsOnConflictSQL, len(chunk), insertPromoNormqueryStatsCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert normquery_stats batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit normquery_stats: %w", err)
		}
	}
	return nil
}

// SaveNormqueryBids saves current bid snapshot per (advert_id, nm_id).
// DELETE+INSERT pattern: replaces all bids for each (advert_id, nm_id).
func (r *PgPromotionRepo) SaveNormqueryBids(ctx context.Context, items []wb.NormqueryBidItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, item := range items {
		_, err := tx.Exec(ctx, "DELETE FROM normquery_bids WHERE advert_id = $1 AND nm_id = $2",
			item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_bids advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
		}
		_, err = tx.Exec(ctx, pgInsertNormqueryBidsSQL,
			item.AdvertID, item.NmID, item.NormQuery, item.Bid)
		if err != nil {
			return fmt.Errorf("insert normquery_bids advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
		}
	}
	return tx.Commit(ctx)
}

// SaveNormqueryMinus saves minus phrases per (advert_id, nm_id).
// DELETE+INSERT pattern: replaces all minus phrases for each (advert_id, nm_id).
func (r *PgPromotionRepo) SaveNormqueryMinus(ctx context.Context, items []wb.NormqueryMinusItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, item := range items {
		_, err := tx.Exec(ctx, "DELETE FROM normquery_minus WHERE advert_id = $1 AND nm_id = $2",
			item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_minus advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
		}
		for _, q := range item.NormQueries {
			_, err := tx.Exec(ctx, pgInsertNormqueryMinusSQL,
				item.AdvertID, item.NmID, q)
			if err != nil {
				return fmt.Errorf("insert normquery_minus advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
			}
		}
	}
	return tx.Commit(ctx)
}

// SaveNormqueryClusters saves active/excluded clusters per (advert_id, nm_id).
// DELETE+INSERT pattern: replaces all clusters for each (advert_id, nm_id).
func (r *PgPromotionRepo) SaveNormqueryClusters(ctx context.Context, items []wb.NormqueryListItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, item := range items {
		_, err := tx.Exec(ctx, "DELETE FROM normquery_clusters WHERE advert_id = $1 AND nm_id = $2",
			item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_clusters advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
		}
		for _, q := range item.NormQueries.Active {
			_, err := tx.Exec(ctx, pgInsertNormqueryClustersSQL,
				item.AdvertID, item.NmID, q, false)
			if err != nil {
				return fmt.Errorf("insert normquery_clusters active advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
			}
		}
		for _, q := range item.NormQueries.Excluded {
			_, err := tx.Exec(ctx, pgInsertNormqueryClustersSQL,
				item.AdvertID, item.NmID, q, true)
			if err != nil {
				return fmt.Errorf("insert normquery_clusters excluded advert=%d nm=%d: %w", item.AdvertID, item.NmID, err)
			}
		}
	}
	return tx.Commit(ctx)
}

// SaveBidRecommendations saves bid recommendations (base + per-cluster).
func (r *PgPromotionRepo) SaveBidRecommendations(ctx context.Context, recs []wb.BidRecommendationsResponse, snapshotDate string) error {
	if len(recs) == 0 {
		return nil
	}

	// Separate base rows and nq rows for independent multi-row inserts.
	type baseRow struct {
		nmID, advertID, competitiveBid, leadersBid, top2 int
	}
	type nqRow struct {
		nmID                     int
		normQuery                string
		reachMin, reachMed, reachMax int
	}
	var bases []baseRow
	var nqs []nqRow
	for _, rec := range recs {
		bases = append(bases, baseRow{
			nmID:           rec.NmID,
			advertID:       rec.AdvertID,
			competitiveBid: rec.Base.CompetitiveBid.BidKopecks,
			leadersBid:     rec.Base.LeadersBid.BidKopecks,
			top2:           rec.Base.Top2.BidKopecks,
		})
		for _, nq := range rec.NormQueries {
			nqs = append(nqs, nqRow{
				nmID:      rec.NmID,
				normQuery: nq.NormQuery,
				reachMin:  nq.ReachMin.BidKopecks,
				reachMed:  nq.ReachMedium.BidKopecks,
				reachMax:  nq.ReachMax.BidKopecks,
			})
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Batch insert base rows.
	for i := 0; i < len(bases); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(bases))
		chunk := bases[i:end]

		args := make([]any, 0, len(chunk)*insertPromoBidRecBaseCols)
		for _, b := range chunk {
			args = append(args, b.nmID, b.advertID, snapshotDate, b.competitiveBid, b.leadersBid, b.top2)
		}

		query := insertPromoBidRecBaseFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoBidRecBasePrefixSQL, insertPromoBidRecBaseOnConflictSQL, len(chunk), insertPromoBidRecBaseCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("upsert bid_recommendations batch (size %d): %w", len(chunk), err)
		}
	}

	// Batch insert nq rows.
	for i := 0; i < len(nqs); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(nqs))
		chunk := nqs[i:end]

		args := make([]any, 0, len(chunk)*insertPromoBidRecNqCols)
		for _, n := range chunk {
			args = append(args, n.nmID, n.normQuery, snapshotDate, n.reachMin, n.reachMed, n.reachMax)
		}

		query := insertPromoBidRecNqFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoBidRecNqPrefixSQL, insertPromoBidRecNqOnConflictSQL, len(chunk), insertPromoBidRecNqCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("upsert bid_recommendations_nq batch (size %d): %w", len(chunk), err)
		}
	}

	return tx.Commit(ctx)
}

// SaveExpenses saves campaign write-off history using ON CONFLICT upsert.
// Deduplicates by (advert_id, upd_num) before chunking — PG ON CONFLICT forbids
// affecting the same row twice within a single multi-row INSERT (SQLSTATE 21000).
func (r *PgPromotionRepo) SaveExpenses(ctx context.Context, rows []wb.ExpenseRow) error {
	if len(rows) == 0 {
		return nil
	}
	rows = dedupExpenses(rows)
	for i := 0; i < len(rows); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoExpensesCols)
		for _, row := range chunk {
			args = append(args,
				row.AdvertID, row.UpdNum, row.UpdTime, row.UpdSum,
				row.CampName, row.AdvertType, row.PaymentType, row.AdvertStatus)
		}

		query := insertPromoExpensesFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoExpensesPrefixSQL, insertPromoExpensesOnConflictSQL, len(chunk), insertPromoExpensesCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert expenses batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit expenses: %w", err)
		}
	}
	return nil
}

// SaveBalance saves account balance snapshot + cashbacks.
// Upserts balance, DELETE+INSERT cashbacks for the same snapshot_date.
func (r *PgPromotionRepo) SaveBalance(ctx context.Context, balance wb.BalanceResponse, snapshotDate string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Single-row multi-row insert for balance (consistent pattern).
	args := []any{snapshotDate, balance.Balance, balance.Net, balance.Bonus}
	query := BuildMultiRowInsert(insertPromoBalancePrefixSQL, insertPromoBalanceOnConflictSQL, 1, insertPromoBalanceCols)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("upsert promotion_balance: %w", err)
	}

	// Clear and replace cashbacks for this date
	_, err = tx.Exec(ctx, "DELETE FROM promotion_balance_cashbacks WHERE snapshot_date = $1", snapshotDate)
	if err != nil {
		return fmt.Errorf("delete promotion_balance_cashbacks: %w", err)
	}
	for _, cb := range balance.Cashbacks {
		_, err := tx.Exec(ctx, pgInsertCashbackSQL, snapshotDate, cb.Sum, cb.Percent, cb.ExpirationDate)
		if err != nil {
			return fmt.Errorf("insert promotion_balance_cashbacks: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// SavePayments saves payment history using ON CONFLICT upsert.
func (r *PgPromotionRepo) SavePayments(ctx context.Context, rows []wb.PaymentRow) error {
	if len(rows) == 0 {
		return nil
	}
	for i := 0; i < len(rows); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoPaymentsCols)
		for _, row := range chunk {
			args = append(args,
				row.ID, row.Sum, row.Date, row.Type, row.StatusID, row.CardStatus)
		}

		query := insertPromoPaymentsFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoPaymentsPrefixSQL, insertPromoPaymentsOnConflictSQL, len(chunk), insertPromoPaymentsCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert promotion_payments batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit promotion_payments: %w", err)
		}
	}
	return nil
}

// SaveCalendarPromotions saves WB promotion calendar using ON CONFLICT upsert.
func (r *PgPromotionRepo) SaveCalendarPromotions(ctx context.Context, promos []wb.CalendarPromotion) error {
	if len(promos) == 0 {
		return nil
	}
	for i := 0; i < len(promos); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(promos))
		chunk := promos[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoCalPromosCols)
		for _, p := range chunk {
			args = append(args, p.ID, p.Name, p.Start, p.End, p.Type)
		}

		query := insertPromoCalPromosFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoCalPromosPrefixSQL, insertPromoCalPromosOnConflictSQL, len(chunk), insertPromoCalPromosCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert wb_calendar_promotions batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit wb_calendar_promotions: %w", err)
		}
	}
	return nil
}

// SaveCalendarPromotionDetails saves promotion details, advantages, and ranging.
// Upserts details via multi-row INSERT, DELETE+INSERT advantages and ranging per promotion_id.
func (r *PgPromotionRepo) SaveCalendarPromotionDetails(ctx context.Context, details []wb.CalendarPromotionDetail) error {
	if len(details) == 0 {
		return nil
	}
	nowUTC := time.Now().UTC().Format("2006-01-02 15:04:05")

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Batch upsert details.
	args := make([]any, 0, len(details)*insertPromoCalDetailsCols)
	for _, d := range details {
		args = append(args,
			d.ID, d.Description,
			d.InPromoActionLeftovers, d.InPromoActionTotal,
			d.NotInPromoActionLeftovers, d.NotInPromoActionTotal,
			d.ParticipationPercentage, d.ExceptionProductsCount, nowUTC)
	}

	query := BuildMultiRowInsert(insertPromoCalDetailsPrefixSQL, insertPromoCalDetailsOnConflictSQL, len(details), insertPromoCalDetailsCols)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("upsert wb_calendar_promotion_details batch (size %d): %w", len(details), err)
	}

	// Per-promotion DELETE+INSERT for advantages and ranging.
	for _, d := range details {
		// Delete+insert advantages
		_, err = tx.Exec(ctx, "DELETE FROM wb_calendar_promotion_advantages WHERE promotion_id = $1", d.ID)
		if err != nil {
			return fmt.Errorf("delete advantages id=%d: %w", d.ID, err)
		}
		for _, a := range d.Advantages {
			_, err := tx.Exec(ctx, pgInsertAdvantageSQL, d.ID, a)
			if err != nil {
				return fmt.Errorf("insert advantage id=%d: %w", d.ID, err)
			}
		}

		// Delete+insert ranging
		_, err = tx.Exec(ctx, "DELETE FROM wb_calendar_promotion_ranging WHERE promotion_id = $1", d.ID)
		if err != nil {
			return fmt.Errorf("delete ranging id=%d: %w", d.ID, err)
		}
		for _, rng := range d.Ranging {
			_, err := tx.Exec(ctx, pgUpsertCalendarRangingSQL, d.ID, rng.Condition, rng.ParticipationRate, rng.Boost)
			if err != nil {
				return fmt.Errorf("insert ranging id=%d: %w", d.ID, err)
			}
		}
	}
	return tx.Commit(ctx)
}

// SaveCalendarPromotionNomenclatures saves eligible products per promotion.
// DELETE+INSERT pattern: replaces all noms for (promotion_id, snapshot_date).
func (r *PgPromotionRepo) SaveCalendarPromotionNomenclatures(ctx context.Context, promotionID int, noms []wb.CalendarPromotionNom, snapshotDate string) error {
	if len(noms) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "DELETE FROM wb_calendar_promotion_nomenclatures WHERE promotion_id = $1 AND snapshot_date = $2",
		promotionID, snapshotDate)
	if err != nil {
		return fmt.Errorf("delete nomenclatures promo=%d: %w", promotionID, err)
	}

	for _, n := range noms {
		_, err := tx.Exec(ctx, pgInsertCalendarNomsSQL,
			promotionID, n.ID, n.InAction, n.Price, n.PlanPrice,
			n.Discount, n.PlanDiscount, n.CurrencyCode, snapshotDate)
		if err != nil {
			return fmt.Errorf("insert nomenclature nm=%d promo=%d: %w", n.ID, promotionID, err)
		}
	}
	return tx.Commit(ctx)
}

// SaveCampaignBudget saves per-campaign budget snapshot using ON CONFLICT upsert.
func (r *PgPromotionRepo) SaveCampaignBudget(ctx context.Context, advertID int, budget wb.BudgetResponse, snapshotDate string) error {
	// Single-row multi-row insert (consistent pattern).
	args := []any{advertID, snapshotDate, budget.Total}
	query := BuildMultiRowInsert(insertPromoCampaignBudgetPrefixSQL, insertPromoCampaignBudgetOnConflictSQL, 1, insertPromoCampaignBudgetCols)
	if _, err := r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("upsert campaign_budget advert=%d: %w", advertID, err)
	}
	return nil
}

// SaveMinBids saves minimum bid snapshots using ON CONFLICT upsert.
func (r *PgPromotionRepo) SaveMinBids(ctx context.Context, advertID int, items []wb.MinBidItem, snapshotDate string) error {
	if len(items) == 0 {
		return nil
	}

	// Flatten nested items into individual bid rows.
	type bidFlat struct {
		nmID, bid int
		placement string
	}
	var flat []bidFlat
	for _, item := range items {
		for _, b := range item.Bids {
			flat = append(flat, bidFlat{nmID: item.NmID, bid: b.Bid, placement: b.Placement})
		}
	}


		// Deduplicate by (nmID, placement) — PG ON CONFLICT (nm_id, advert_id, placement_type, snapshot_date)
		// forbids affecting the same row twice (SQLSTATE 21000). advertID and snapshotDate are constant per call.
		type dedupKey struct{ nmID int; placement string }
		seen := make(map[dedupKey]int, len(flat))
		for i, f := range flat {
			k := dedupKey{f.nmID, f.placement}
			if prev, ok := seen[k]; ok {
				flat[prev] = f // last-write-wins
			} else {
				seen[k] = i
			}
		}
		if len(seen) < len(flat) {
			deduped := make([]bidFlat, 0, len(seen))
			for i, f := range flat {
				k := dedupKey{f.nmID, f.placement}
				if seen[k] == i {
					deduped = append(deduped, f)
				}
			}
			flat = deduped
		}
	for i := 0; i < len(flat); i += pgPromoChunkSize {
		end := min(i+pgPromoChunkSize, len(flat))
		chunk := flat[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		args := make([]any, 0, len(chunk)*insertPromoMinBidsCols)
		for _, b := range chunk {
			args = append(args, b.nmID, advertID, b.placement, b.bid, snapshotDate)
		}

		query := insertPromoMinBidsFullChunkSQL
		if len(chunk) < pgPromoChunkSize {
			query = BuildMultiRowInsert(insertPromoMinBidsPrefixSQL, insertPromoMinBidsOnConflictSQL, len(chunk), insertPromoMinBidsCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("upsert min_bids batch (size %d): %w", len(chunk), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit min_bids: %w", err)
		}
	}
	return nil
}

// ============================================================================
// Reader — 4 cross-domain read methods
// ============================================================================

// GetCampaignProductIDs returns (advert_id, nm_id) pairs for active/paused campaigns.
// Dynamic IN clause: generates $1, $2, ... from statuses slice.
func (r *PgPromotionRepo) GetCampaignProductIDs(ctx context.Context, statuses []int, changedSince string) ([]wb.NormqueryItem, error) {
	if len(statuses) == 0 {
		return nil, nil
	}

	// Build dynamic IN clause: $1, $2, $3, ...
	placeholders := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+1)
	for i, s := range statuses {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, s)
	}

	query := fmt.Sprintf(
		"SELECT DISTINCT advert_id, nm_id FROM campaign_products WHERE advert_id IN (SELECT advert_id FROM campaigns WHERE status IN (%s)",
		strings.Join(placeholders, ","),
	)
	if changedSince != "" {
		nextIdx := len(statuses) + 1
		query += fmt.Sprintf(" AND change_time >= $%d", nextIdx)
		args = append(args, changedSince)
	}
	query += ")"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query campaign_product_ids: %w", err)
	}
	defer rows.Close()

	var items []wb.NormqueryItem
	for rows.Next() {
		var item wb.NormqueryItem
		if err := rows.Scan(&item.AdvertID, &item.NmID); err != nil {
			return nil, fmt.Errorf("scan campaign_product_id: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetNormqueryLastRun returns the most recent created_at from normquery_stats.
// Uses *string scan for nullable MAX() aggregate.
func (r *PgPromotionRepo) GetNormqueryLastRun(ctx context.Context) (string, error) {
	var ts *string
	err := r.pool.QueryRow(ctx, "SELECT MAX(created_at) FROM normquery_stats").Scan(&ts)
	if err != nil {
		return "", fmt.Errorf("query normquery_last_run: %w", err)
	}
	if ts == nil {
		return "", nil
	}
	return *ts, nil
}

// GetCalendarPromotionIDs returns all promotion IDs from wb_calendar_promotions.
func (r *PgPromotionRepo) GetCalendarPromotionIDs(ctx context.Context) ([]int, error) {
	rows, err := r.pool.Query(ctx, "SELECT promotion_id FROM wb_calendar_promotions ORDER BY promotion_id")
	if err != nil {
		return nil, fmt.Errorf("query calendar promotion ids: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan promotion_id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetCalendarPromotionIDsByType returns promotion IDs excluding a specific type.
func (r *PgPromotionRepo) GetCalendarPromotionIDsByType(ctx context.Context, excludeType string) ([]int, error) {
	rows, err := r.pool.Query(ctx, "SELECT promotion_id FROM wb_calendar_promotions WHERE type != $1 ORDER BY promotion_id", excludeType)
	if err != nil {
		return nil, fmt.Errorf("query calendar promotion ids by type: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan promotion_id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ============================================================================
// SQL statements — multi-row INSERT fragments for 10 pure upsert tables
// ============================================================================

// --- 1. campaign_bids ---
const (
	insertPromoCampaignBidsCols = 7 // $1-$7 (including updated_at as Go-computed timestamp)

	insertPromoCampaignBidsPrefixSQL = `INSERT INTO campaign_bids (advert_id, nm_id, subject_id, subject_name, bid_search, bid_reco, updated_at) VALUES `

	insertPromoCampaignBidsOnConflictSQL = `
ON CONFLICT (advert_id, nm_id) DO UPDATE SET
    subject_id = EXCLUDED.subject_id,
    subject_name = EXCLUDED.subject_name,
    bid_search = EXCLUDED.bid_search,
    bid_reco = EXCLUDED.bid_reco,
    updated_at = EXCLUDED.updated_at`
)

var insertPromoCampaignBidsFullChunkSQL = BuildMultiRowInsert(insertPromoCampaignBidsPrefixSQL, insertPromoCampaignBidsOnConflictSQL, pgPromoChunkSize, insertPromoCampaignBidsCols)

// --- 2. normquery_stats ---
const (
	insertPromoNormqueryStatsCols = 14 // $1-$14

	insertPromoNormqueryStatsPrefixSQL = `INSERT INTO normquery_stats (advert_id, nm_id, stats_date, normquery, views, clicks, ctr, cpc, cpm, avg_pos, orders, shks, atbs, spend) VALUES `

	insertPromoNormqueryStatsOnConflictSQL = `
ON CONFLICT (advert_id, nm_id, stats_date, normquery) DO UPDATE SET
    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc,
    cpm = EXCLUDED.cpm, avg_pos = EXCLUDED.avg_pos, orders = EXCLUDED.orders, shks = EXCLUDED.shks,
    atbs = EXCLUDED.atbs, spend = EXCLUDED.spend`
)

var insertPromoNormqueryStatsFullChunkSQL = BuildMultiRowInsert(insertPromoNormqueryStatsPrefixSQL, insertPromoNormqueryStatsOnConflictSQL, pgPromoChunkSize, insertPromoNormqueryStatsCols)

// --- 3. bid_recommendations ---
const (
	insertPromoBidRecBaseCols = 6 // $1-$6

	insertPromoBidRecBasePrefixSQL = `INSERT INTO bid_recommendations (nm_id, advert_id, snapshot_date, competitive_bid, leaders_bid, top2) VALUES `

	insertPromoBidRecBaseOnConflictSQL = `
ON CONFLICT (nm_id, advert_id, snapshot_date) DO UPDATE SET
    competitive_bid = EXCLUDED.competitive_bid,
    leaders_bid = EXCLUDED.leaders_bid,
    top2 = EXCLUDED.top2`
)

var insertPromoBidRecBaseFullChunkSQL = BuildMultiRowInsert(insertPromoBidRecBasePrefixSQL, insertPromoBidRecBaseOnConflictSQL, pgPromoChunkSize, insertPromoBidRecBaseCols)

// --- 4. bid_recommendations_nq ---
const (
	insertPromoBidRecNqCols = 6 // $1-$6

	insertPromoBidRecNqPrefixSQL = `INSERT INTO bid_recommendations_nq (nm_id, normquery, snapshot_date, reach_min_bid, reach_medium_bid, reach_max_bid) VALUES `

	insertPromoBidRecNqOnConflictSQL = `
ON CONFLICT (nm_id, normquery, snapshot_date) DO UPDATE SET
    reach_min_bid = EXCLUDED.reach_min_bid,
    reach_medium_bid = EXCLUDED.reach_medium_bid,
    reach_max_bid = EXCLUDED.reach_max_bid`
)

var insertPromoBidRecNqFullChunkSQL = BuildMultiRowInsert(insertPromoBidRecNqPrefixSQL, insertPromoBidRecNqOnConflictSQL, pgPromoChunkSize, insertPromoBidRecNqCols)

// --- 5. promotion_expenses ---
const (
	insertPromoExpensesCols = 8 // $1-$8

	insertPromoExpensesPrefixSQL = `INSERT INTO promotion_expenses (advert_id, upd_num, upd_time, upd_sum, camp_name, advert_type, payment_type, advert_status) VALUES `

	insertPromoExpensesOnConflictSQL = `
ON CONFLICT (advert_id, upd_num) DO UPDATE SET
    upd_time = EXCLUDED.upd_time, upd_sum = EXCLUDED.upd_sum, camp_name = EXCLUDED.camp_name,
    advert_type = EXCLUDED.advert_type, payment_type = EXCLUDED.payment_type, advert_status = EXCLUDED.advert_status`
)

var insertPromoExpensesFullChunkSQL = BuildMultiRowInsert(insertPromoExpensesPrefixSQL, insertPromoExpensesOnConflictSQL, pgPromoChunkSize, insertPromoExpensesCols)

// --- 6. promotion_balance (single-row upsert, PRIMARY KEY snapshot_date) ---
const (
	insertPromoBalanceCols = 4 // $1-$4

	insertPromoBalancePrefixSQL = `INSERT INTO promotion_balance (snapshot_date, balance, net, bonus) VALUES `

	insertPromoBalanceOnConflictSQL = `
ON CONFLICT (snapshot_date) DO UPDATE SET
    balance = EXCLUDED.balance, net = EXCLUDED.net, bonus = EXCLUDED.bonus`
)

// --- 7. promotion_payments ---
const (
	insertPromoPaymentsCols = 6 // $1-$6

	insertPromoPaymentsPrefixSQL = `INSERT INTO promotion_payments (payment_id, sum_val, payment_date, type_val, status_id, card_status) VALUES `

	insertPromoPaymentsOnConflictSQL = `
ON CONFLICT (payment_id) DO UPDATE SET
    sum_val = EXCLUDED.sum_val, payment_date = EXCLUDED.payment_date,
    type_val = EXCLUDED.type_val, status_id = EXCLUDED.status_id, card_status = EXCLUDED.card_status`
)

var insertPromoPaymentsFullChunkSQL = BuildMultiRowInsert(insertPromoPaymentsPrefixSQL, insertPromoPaymentsOnConflictSQL, pgPromoChunkSize, insertPromoPaymentsCols)

// --- 8. wb_calendar_promotions (PRIMARY KEY promotion_id) ---
const (
	insertPromoCalPromosCols = 5 // $1-$5

	insertPromoCalPromosPrefixSQL = `INSERT INTO wb_calendar_promotions (promotion_id, name, start_date, end_date, type) VALUES `

	insertPromoCalPromosOnConflictSQL = `
ON CONFLICT (promotion_id) DO UPDATE SET
    name = EXCLUDED.name, start_date = EXCLUDED.start_date, end_date = EXCLUDED.end_date, type = EXCLUDED.type`
)

var insertPromoCalPromosFullChunkSQL = BuildMultiRowInsert(insertPromoCalPromosPrefixSQL, insertPromoCalPromosOnConflictSQL, pgPromoChunkSize, insertPromoCalPromosCols)

// --- 9. wb_calendar_promotion_details (PRIMARY KEY promotion_id) ---
const (
	insertPromoCalDetailsCols = 9 // $1-$9 (including updated_at as Go-computed timestamp)

	insertPromoCalDetailsPrefixSQL = `INSERT INTO wb_calendar_promotion_details (
    promotion_id, description, in_promo_action_leftovers, in_promo_action_total,
    not_in_promo_action_leftovers, not_in_promo_action_total,
    participation_percentage, exception_products_count, updated_at) VALUES `

	insertPromoCalDetailsOnConflictSQL = `
ON CONFLICT (promotion_id) DO UPDATE SET
    description = EXCLUDED.description,
    in_promo_action_leftovers = EXCLUDED.in_promo_action_leftovers,
    in_promo_action_total = EXCLUDED.in_promo_action_total,
    not_in_promo_action_leftovers = EXCLUDED.not_in_promo_action_leftovers,
    not_in_promo_action_total = EXCLUDED.not_in_promo_action_total,
    participation_percentage = EXCLUDED.participation_percentage,
    exception_products_count = EXCLUDED.exception_products_count,
    updated_at = EXCLUDED.updated_at`
)

// --- 10. campaign_budget ---
const (
	insertPromoCampaignBudgetCols = 3 // $1-$3

	insertPromoCampaignBudgetPrefixSQL = `INSERT INTO campaign_budget (advert_id, snapshot_date, total_budget) VALUES `

	insertPromoCampaignBudgetOnConflictSQL = `
ON CONFLICT (advert_id, snapshot_date) DO UPDATE SET
    total_budget = EXCLUDED.total_budget`
)

// --- 11. min_bids (bonus: also migrated) ---
const (
	insertPromoMinBidsCols = 5 // $1-$5

	insertPromoMinBidsPrefixSQL = `INSERT INTO min_bids (nm_id, advert_id, placement_type, min_bid, snapshot_date) VALUES `

	insertPromoMinBidsOnConflictSQL = `
ON CONFLICT (nm_id, advert_id, placement_type, snapshot_date) DO UPDATE SET
    min_bid = EXCLUDED.min_bid`
)

var insertPromoMinBidsFullChunkSQL = BuildMultiRowInsert(insertPromoMinBidsPrefixSQL, insertPromoMinBidsOnConflictSQL, pgPromoChunkSize, insertPromoMinBidsCols)

// ============================================================================
// SQL statements — DELETE+INSERT tables (unchanged per-row INSERT)
// ============================================================================

var (
	// Normquery Bids — simple insert (DELETE done separately)
	pgInsertNormqueryBidsSQL = `
INSERT INTO normquery_bids (advert_id, nm_id, normquery, bid)
VALUES ($1, $2, $3, $4)`

	// Normquery Minus — simple insert
	pgInsertNormqueryMinusSQL = `
INSERT INTO normquery_minus (advert_id, nm_id, minus_query)
VALUES ($1, $2, $3)`

	// Normquery Clusters — simple insert with BOOLEAN
	pgInsertNormqueryClustersSQL = `
INSERT INTO normquery_clusters (advert_id, nm_id, normquery, is_excluded)
VALUES ($1, $2, $3, $4)`

	// Cashback — simple insert (DELETE done separately)
	pgInsertCashbackSQL = `
INSERT INTO promotion_balance_cashbacks (snapshot_date, sum_val, percent_val, expiration_date)
VALUES ($1, $2, $3, $4)`

	// Calendar Advantage — simple insert (DELETE done separately)
	pgInsertAdvantageSQL = `
INSERT INTO wb_calendar_promotion_advantages (promotion_id, advantage)
VALUES ($1, $2)`

	// Calendar Ranging — upsert (DELETE done separately)
	pgUpsertCalendarRangingSQL = `
INSERT INTO wb_calendar_promotion_ranging (promotion_id, condition, participation_rate, boost)
VALUES ($1, $2, $3, $4)
ON CONFLICT (promotion_id, condition) DO UPDATE SET
    participation_rate = EXCLUDED.participation_rate, boost = EXCLUDED.boost`

	// Calendar Nomenclatures — simple insert (DELETE done separately, BOOLEAN for in_action)
	pgInsertCalendarNomsSQL = `
INSERT INTO wb_calendar_promotion_nomenclatures (
    promotion_id, nm_id, in_action, price, plan_price, discount, plan_discount, currency_code, snapshot_date
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
)

// Suppress unused import (sql.NullString used by reference implementations).
var _ sql.NullString
