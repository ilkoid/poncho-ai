// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/campaigns"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: SQLiteSalesRepository implements campaigns.CampaignsWriter.
var _ campaigns.CampaignsWriter = (*SQLiteSalesRepository)(nil)

// ============================================================================
// Campaign Repository Methods
// ============================================================================

// SaveCampaigns saves batch of campaign metadata.
// Uses INSERT ... ON CONFLICT DO UPDATE to preserve aggregate columns.
func (r *SQLiteSalesRepository) SaveCampaigns(ctx context.Context, groups []wb.PromotionAdvertGroup) error {
	if len(groups) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO campaigns (
			advert_id, campaign_type, status, change_time, updated_at
		) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(advert_id) DO UPDATE SET
			campaign_type = excluded.campaign_type,
			status = excluded.status,
			change_time = excluded.change_time,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, group := range groups {
		for _, advert := range group.AdvertList {
			_, err := stmt.ExecContext(ctx,
				advert.AdvertID,
				group.Type,
				group.Status,
				advert.ChangeTime,
			)
			if err != nil {
				return fmt.Errorf("insert campaign advert_id=%d: %w", advert.AdvertID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SaveCampaignStats saves batch of daily campaign statistics.
// Uses INSERT OR REPLACE for upsert by (advert_id, stats_date) UNIQUE constraint.
func (r *SQLiteSalesRepository) SaveCampaignStats(ctx context.Context, stats []wb.CampaignDailyStats) error {
	if len(stats) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO campaign_stats_daily (
			advert_id, stats_date,
			views, clicks, ctr, cpc, cr,
			orders, shks, atbs, canceled,
			sum, sum_price
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range stats {
		_, err := stmt.ExecContext(ctx,
			row.AdvertID,
			row.StatsDate,
			row.Views,
			row.Clicks,
			row.CTR,
			row.CPC,
			row.CR,
			row.Orders,
			row.Shks,
			row.Atbs,
			row.Canceled,
			row.Sum,
			row.SumPrice,
		)
		if err != nil {
			return fmt.Errorf("insert stats advert_id=%d date=%s: %w", row.AdvertID, row.StatsDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SaveCampaignProducts saves batch of campaign-product relationships.
// Uses INSERT OR REPLACE for upsert by (advert_id, nm_id) UNIQUE constraint.
func (r *SQLiteSalesRepository) SaveCampaignProducts(ctx context.Context, products []wb.CampaignProduct) error {
	if len(products) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO campaign_products (
			advert_id, nm_id, product_name,
			total_views, total_clicks, total_orders, total_sum
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, p := range products {
		_, err := stmt.ExecContext(ctx,
			p.AdvertID,
			p.NmID,
			p.Name,
			p.Views,
			p.Clicks,
			p.Orders,
			p.Sum,
		)
		if err != nil {
			return fmt.Errorf("insert campaign_product advert=%d nm=%d: %w", p.AdvertID, p.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetCampaignIDs returns all campaign IDs from database.
func (r *SQLiteSalesRepository) GetCampaignIDs(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT advert_id FROM campaigns ORDER BY advert_id")
	if err != nil {
		return nil, fmt.Errorf("query campaign ids: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan advert_id: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}

// GetCampaignIDsByStatus returns campaign IDs filtered by status.
func (r *SQLiteSalesRepository) GetCampaignIDsByStatus(ctx context.Context, statuses []int) ([]int, error) {
	if len(statuses) == 0 {
		return r.GetCampaignIDs(ctx)
	}

	query := "SELECT advert_id FROM campaigns WHERE status IN ("
	args := make([]any, len(statuses))
	for i, s := range statuses {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = s
	}
	query += ") ORDER BY advert_id"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query campaign ids by status: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan advert_id: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}

// GetLastCampaignStatsDate returns the most recent stats_date for a campaign.
// Returns zero time if no stats exist.
func (r *SQLiteSalesRepository) GetLastCampaignStatsDate(ctx context.Context, advertID int) (time.Time, error) {
	var lastDate sql.NullString
	err := r.db.QueryRowContext(ctx,
		"SELECT MAX(stats_date) FROM campaign_stats_daily WHERE advert_id = ?",
		advertID,
	).Scan(&lastDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("get last stats date: %w", err)
	}
	if !lastDate.Valid || lastDate.String == "" {
		return time.Time{}, nil
	}

	// Parse date (YYYY-MM-DD format)
	return time.Parse("2006-01-02", lastDate.String)
}

// GetLastCampaignStatsDateAll returns the most recent stats_date for all campaigns.
// Returns map[advertID]lastDate.
func (r *SQLiteSalesRepository) GetLastCampaignStatsDateAll(ctx context.Context) (map[int]time.Time, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT advert_id, MAX(stats_date) FROM campaign_stats_daily GROUP BY advert_id",
	)
	if err != nil {
		return nil, fmt.Errorf("query last stats dates: %w", err)
	}
	defer rows.Close()

	result := make(map[int]time.Time)
	for rows.Next() {
		var advertID int
		var lastDate sql.NullString
		if err := rows.Scan(&advertID, &lastDate); err != nil {
			return nil, fmt.Errorf("scan last date: %w", err)
		}
		if lastDate.Valid && lastDate.String != "" {
			t, err := time.Parse("2006-01-02", lastDate.String)
			if err != nil {
				continue // Skip invalid dates
			}
			result[advertID] = t
		}
	}

	return result, rows.Err()
}

// CountCampaigns returns total number of campaigns in database.
func (r *SQLiteSalesRepository) CountCampaigns(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaigns").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaigns: %w", err)
	}
	return count, nil
}

// CountCampaignStats returns total number of campaign stats records.
func (r *SQLiteSalesRepository) CountCampaignStats(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaign_stats_daily").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaign_stats_daily: %w", err)
	}
	return count, nil
}

// CountCampaignProducts returns total number of campaign-product relationships.
func (r *SQLiteSalesRepository) CountCampaignProducts(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaign_products").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaign_products: %w", err)
	}
	return count, nil
}

// UpdateCampaignAggregates updates total_views, total_clicks, etc. in campaigns table.
// Called after loading stats to keep summary data in sync.
func (r *SQLiteSalesRepository) UpdateCampaignAggregates(ctx context.Context, advertID int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE campaigns SET
			total_views = (SELECT COALESCE(SUM(views), 0) FROM campaign_stats_daily WHERE advert_id = ?),
			total_clicks = (SELECT COALESCE(SUM(clicks), 0) FROM campaign_stats_daily WHERE advert_id = ?),
			total_orders = (SELECT COALESCE(SUM(orders), 0) FROM campaign_stats_daily WHERE advert_id = ?),
			total_sum = (SELECT COALESCE(SUM(sum), 0) FROM campaign_stats_daily WHERE advert_id = ?),
			total_sum_price = (SELECT COALESCE(SUM(sum_price), 0) FROM campaign_stats_daily WHERE advert_id = ?),
			updated_at = CURRENT_TIMESTAMP
		WHERE advert_id = ?
	`, advertID, advertID, advertID, advertID, advertID, advertID)
	if err != nil {
		return fmt.Errorf("update campaign aggregates advert_id=%d: %w", advertID, err)
	}
	return nil
}

// ============================================================================
// Campaign Fullstats Repository Methods (API v3 - /adv/v3/fullstats)
// ============================================================================

// SaveCampaignAppStats saves batch of platform-level daily stats.
// Grain: (advert_id, stats_date, app_type).
// Uses INSERT OR REPLACE for upsert.
func (r *SQLiteSalesRepository) SaveCampaignAppStats(ctx context.Context, rows []wb.CampaignAppStatsRow) error {
	if len(rows) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO campaign_stats_app (
			advert_id, stats_date, app_type,
			views, clicks, ctr, cpc, cr,
			orders, shks, atbs, canceled,
			sum, sum_price
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.AdvertID, row.StatsDate, row.AppType,
			row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
			row.Orders, row.Shks, row.Atbs, row.Canceled,
			row.Sum, row.SumPrice,
		)
		if err != nil {
			return fmt.Errorf("insert app stats advert=%d date=%s app=%d: %w", row.AdvertID, row.StatsDate, row.AppType, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// SaveCampaignNmStats saves batch of product-level daily stats per platform.
// Grain: (advert_id, stats_date, app_type, nm_id).
// Uses INSERT OR REPLACE for upsert.
func (r *SQLiteSalesRepository) SaveCampaignNmStats(ctx context.Context, rows []wb.CampaignNmStatsRow) error {
	if len(rows) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO campaign_stats_nm (
			advert_id, stats_date, app_type, nm_id, nm_name,
			views, clicks, ctr, cpc, cr,
			orders, shks, atbs, canceled,
			sum, sum_price
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.AdvertID, row.StatsDate, row.AppType, row.NmID, row.NmName,
			row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
			row.Orders, row.Shks, row.Atbs, row.Canceled,
			row.Sum, row.SumPrice,
		)
		if err != nil {
			return fmt.Errorf("insert nm stats advert=%d date=%s app=%d nm=%d: %w", row.AdvertID, row.StatsDate, row.AppType, row.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// SaveCampaignBoosterStats saves batch of booster-specific stats.
// Grain: (advert_id, stats_date, nm_id).
// Uses INSERT OR REPLACE for upsert.
func (r *SQLiteSalesRepository) SaveCampaignBoosterStats(ctx context.Context, rows []wb.CampaignBoosterStatsRow) error {
	if len(rows) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO campaign_booster_stats (
			advert_id, stats_date, nm_id, avg_position
		) VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.AdvertID, row.StatsDate, row.NmID, row.AvgPosition,
		)
		if err != nil {
			return fmt.Errorf("insert booster stats advert=%d date=%s nm=%d: %w", row.AdvertID, row.StatsDate, row.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// SaveFullstats saves all 4 stat tables from a flattened fullstats result.
// Aggregate method implementing campaigns.CampaignsWriter.
func (r *SQLiteSalesRepository) SaveFullstats(ctx context.Context, flat wb.FlattenAllResult) error {
	if err := r.SaveCampaignStats(ctx, flat.Daily); err != nil {
		return fmt.Errorf("save daily stats: %w", err)
	}
	if err := r.SaveCampaignAppStats(ctx, flat.App); err != nil {
		return fmt.Errorf("save app stats: %w", err)
	}
	if err := r.SaveCampaignNmStats(ctx, flat.Nm); err != nil {
		return fmt.Errorf("save nm stats: %w", err)
	}
	if err := r.SaveCampaignBoosterStats(ctx, flat.Booster); err != nil {
		return fmt.Errorf("save booster stats: %w", err)
	}
	return nil
}

// SaveCampaignDetails updates campaign metadata from /api/advert/v2/adverts.
// Uses UPDATE because campaigns already exist from SaveCampaigns().
func (r *SQLiteSalesRepository) SaveCampaignDetails(ctx context.Context, details []wb.AdvertDetail) error {
	if len(details) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE campaigns SET
			name = ?,
			payment_type = ?,
			bid_type = ?,
			placement_search = ?,
			placement_reco = ?,
			ts_created = ?,
			ts_started = ?,
			ts_deleted = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE advert_id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, d := range details {
		placementSearch := 0
		if d.Settings.Placements.Search {
			placementSearch = 1
		}
		placementReco := 0
		if d.Settings.Placements.Recommendations {
			placementReco = 1
		}

		_, err := stmt.ExecContext(ctx,
			d.Settings.Name,
			d.Settings.PaymentType,
			d.BidType,
			placementSearch,
			placementReco,
			d.Timestamps.Created,
			d.Timestamps.Started,
			d.Timestamps.Deleted,
			d.ID,
		)
		if err != nil {
			return fmt.Errorf("update campaign details advert_id=%d: %w", d.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// PopulateCampaignProducts rebuilds the campaign_products materialized view
// from campaign_stats_nm data. Uses DELETE + INSERT for full refresh.
// This is the single source of truth for campaign-product relationships.
func (r *SQLiteSalesRepository) PopulateCampaignProducts(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM campaign_products;

		INSERT OR REPLACE INTO campaign_products (
			advert_id, nm_id, product_name,
			total_views, total_clicks, total_orders, total_sum
		)
		SELECT
			advert_id,
			nm_id,
			MAX(nm_name) AS product_name,
			SUM(views)   AS total_views,
			SUM(clicks)  AS total_clicks,
			SUM(orders)  AS total_orders,
			SUM(sum)     AS total_sum
		FROM campaign_stats_nm
		GROUP BY advert_id, nm_id
	`)
	if err != nil {
		return fmt.Errorf("populate campaign_products: %w", err)
	}
	return nil
}

// CountCampaignAppStats returns total number of platform-level stats records.
func (r *SQLiteSalesRepository) CountCampaignAppStats(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaign_stats_app").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaign_stats_app: %w", err)
	}
	return count, nil
}

// CountCampaignNmStats returns total number of product-level stats records.
func (r *SQLiteSalesRepository) CountCampaignNmStats(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaign_stats_nm").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaign_stats_nm: %w", err)
	}
	return count, nil
}

// CountCampaignBoosterStats returns total number of booster stats records.
func (r *SQLiteSalesRepository) CountCampaignBoosterStats(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM campaign_booster_stats").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count campaign_booster_stats: %w", err)
	}
	return count, nil
}

// ============================================================================
// V2 Promotion Repository Methods (normquery, bid recommendations, finance)
// ============================================================================

// SaveCampaignBids saves campaign bid snapshots from AdvertDetail.NmSettings.
func (r *SQLiteSalesRepository) SaveCampaignBids(ctx context.Context, rows []wb.CampaignBidRow) error {
	if len(rows) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO campaign_bids (advert_id, nm_id, subject_id, subject_name, bid_search, bid_reco, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(advert_id, nm_id) DO UPDATE SET
			subject_id = excluded.subject_id,
			subject_name = excluded.subject_name,
			bid_search = excluded.bid_search,
			bid_reco = excluded.bid_reco,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx, row.AdvertID, row.NmID, row.SubjectID, row.SubjectName, row.BidSearch, row.BidReco)
		if err != nil {
			return fmt.Errorf("insert campaign_bids advert_id=%d nm_id=%d: %w", row.AdvertID, row.NmID, err)
		}
	}
	return tx.Commit()
}

// SaveNormqueryStats saves normquery statistics for one (advert_id, nm_id, date).
// Deletes existing rows for this key first (full replacement).
func (r *SQLiteSalesRepository) SaveNormqueryStats(ctx context.Context, advertID, nmID int, date string, rows []wb.NormqueryStatRow) error {
	if len(rows) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM normquery_stats WHERE advert_id = ? AND nm_id = ? AND stats_date = ?", advertID, nmID, date)
	if err != nil {
		return fmt.Errorf("delete normquery_stats: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO normquery_stats (advert_id, nm_id, stats_date, normquery, views, clicks, ctr, cpc, cpm, avg_pos, orders, shks, atbs, spend)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
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
		_, err := stmt.ExecContext(ctx, advertID, nmID, date, row.NormQuery, views, row.Clicks, ctr, row.CPC, cpm, row.AvgPos, row.Orders, row.SHKS, row.Atbs, row.Spend)
		if err != nil {
			return fmt.Errorf("insert normquery_stats: %w", err)
		}
	}
	return tx.Commit()
}

// SaveNormqueryStatsBatch saves normquery statistics for multiple (advert_id, nm_id) groups
// in a single transaction. Uses INSERT OR REPLACE to avoid separate DELETE per group.
// Much faster than per-group SaveNormqueryStats on WSL2 /mnt/d mounts where fsync is slow.
func (r *SQLiteSalesRepository) SaveNormqueryStatsBatch(ctx context.Context, groups []wb.NormqueryStatsGroup, date string) error {
	if len(groups) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO normquery_stats
		(advert_id, nm_id, stats_date, normquery, views, clicks, ctr, cpc, cpm, avg_pos, orders, shks, atbs, spend)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, group := range groups {
		for _, row := range group.Stats {
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
			_, err := stmt.ExecContext(ctx, group.AdvertID, group.NmID, date, row.NormQuery, views, row.Clicks, ctr, row.CPC, cpm, row.AvgPos, row.Orders, row.SHKS, row.Atbs, row.Spend)
			if err != nil {
				return fmt.Errorf("insert normquery_stats advert=%d nm=%d cluster=%s: %w", group.AdvertID, group.NmID, row.NormQuery, err)
			}
		}
	}
	return tx.Commit()
}

// SaveNormqueryBids saves current bid snapshot per (advert_id, nm_id, normquery).
func (r *SQLiteSalesRepository) SaveNormqueryBids(ctx context.Context, items []wb.NormqueryBidItem) error {
	if len(items) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO normquery_bids (advert_id, nm_id, normquery, bid)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		// Delete existing rows for this (advert_id, nm_id) before inserting new ones
		_, err := tx.ExecContext(ctx, "DELETE FROM normquery_bids WHERE advert_id = ? AND nm_id = ?", item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_bids: %w", err)
		}
		_, err = stmt.ExecContext(ctx, item.AdvertID, item.NmID, item.NormQuery, item.Bid)
		if err != nil {
			return fmt.Errorf("insert normquery_bids: %w", err)
		}
	}
	return tx.Commit()
}

// SaveNormqueryMinus saves minus phrases per (advert_id, nm_id).
// Deletes existing rows for each (advert_id, nm_id) pair first.
func (r *SQLiteSalesRepository) SaveNormqueryMinus(ctx context.Context, items []wb.NormqueryMinusItem) error {
	if len(items) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO normquery_minus (advert_id, nm_id, minus_query)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		// Delete existing rows for this (advert_id, nm_id)
		_, err := tx.ExecContext(ctx, "DELETE FROM normquery_minus WHERE advert_id = ? AND nm_id = ?", item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_minus: %w", err)
		}
		for _, q := range item.NormQueries {
			_, err := stmt.ExecContext(ctx, item.AdvertID, item.NmID, q)
			if err != nil {
				return fmt.Errorf("insert normquery_minus: %w", err)
			}
		}
	}
	return tx.Commit()
}

// SaveNormqueryClusters saves active/excluded clusters per (advert_id, nm_id).
func (r *SQLiteSalesRepository) SaveNormqueryClusters(ctx context.Context, items []wb.NormqueryListItem) error {
	if len(items) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO normquery_clusters (advert_id, nm_id, normquery, is_excluded)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		// Delete existing rows for this (advert_id, nm_id)
		_, err := tx.ExecContext(ctx, "DELETE FROM normquery_clusters WHERE advert_id = ? AND nm_id = ?", item.AdvertID, item.NmID)
		if err != nil {
			return fmt.Errorf("delete normquery_clusters: %w", err)
		}
		for _, q := range item.NormQueries.Active {
			_, err := stmt.ExecContext(ctx, item.AdvertID, item.NmID, q, 0)
			if err != nil {
				return fmt.Errorf("insert normquery_clusters: %w", err)
			}
		}
		for _, q := range item.NormQueries.Excluded {
			_, err := stmt.ExecContext(ctx, item.AdvertID, item.NmID, q, 1)
			if err != nil {
				return fmt.Errorf("insert normquery_clusters: %w", err)
			}
		}
	}
	return tx.Commit()
}

// SaveBidRecommendations saves bid recommendations for multiple products.
func (r *SQLiteSalesRepository) SaveBidRecommendations(ctx context.Context, recs []wb.BidRecommendationsResponse, snapshotDate string) error {
	if len(recs) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	baseStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bid_recommendations (nm_id, advert_id, snapshot_date, competitive_bid, leaders_bid, top2)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(nm_id, advert_id, snapshot_date) DO UPDATE SET
			competitive_bid = excluded.competitive_bid,
			leaders_bid = excluded.leaders_bid,
			top2 = excluded.top2
	`)
	if err != nil {
		return fmt.Errorf("prepare base statement: %w", err)
	}
	defer baseStmt.Close()

	nqStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bid_recommendations_nq (nm_id, normquery, snapshot_date, reach_min_bid, reach_medium_bid, reach_max_bid)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(nm_id, normquery, snapshot_date) DO UPDATE SET
			reach_min_bid = excluded.reach_min_bid,
			reach_medium_bid = excluded.reach_medium_bid,
			reach_max_bid = excluded.reach_max_bid
	`)
	if err != nil {
		return fmt.Errorf("prepare nq statement: %w", err)
	}
	defer nqStmt.Close()

	for _, rec := range recs {
		_, err := baseStmt.ExecContext(ctx,
			rec.NmID, rec.AdvertID, snapshotDate,
			rec.Base.CompetitiveBid.BidKopecks,
			rec.Base.LeadersBid.BidKopecks,
			rec.Base.Top2.BidKopecks,
		)
		if err != nil {
			return fmt.Errorf("insert bid_recommendations nm_id=%d: %w", rec.NmID, err)
		}
		for _, nq := range rec.NormQueries {
			_, err := nqStmt.ExecContext(ctx,
				rec.NmID, nq.NormQuery, snapshotDate,
				nq.ReachMin.BidKopecks,
				nq.ReachMedium.BidKopecks,
				nq.ReachMax.BidKopecks,
			)
			if err != nil {
				return fmt.Errorf("insert bid_recommendations_nq nm_id=%d: %w", rec.NmID, err)
			}
		}
	}
	return tx.Commit()
}

// SaveExpenses saves promotion expense history.
func (r *SQLiteSalesRepository) SaveExpenses(ctx context.Context, rows []wb.ExpenseRow) error {
	if len(rows) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO promotion_expenses (advert_id, upd_num, upd_time, upd_sum, camp_name, advert_type, payment_type, advert_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(advert_id, upd_num) DO UPDATE SET
			upd_time = excluded.upd_time, upd_sum = excluded.upd_sum, camp_name = excluded.camp_name,
			advert_type = excluded.advert_type, payment_type = excluded.payment_type, advert_status = excluded.advert_status
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx, row.AdvertID, row.UpdNum, row.UpdTime, row.UpdSum, row.CampName, row.AdvertType, row.PaymentType, row.AdvertStatus)
		if err != nil {
			return fmt.Errorf("insert promotion_expenses advert_id=%d: %w", row.AdvertID, err)
		}
	}
	return tx.Commit()
}

// SaveBalance saves account balance snapshot.
func (r *SQLiteSalesRepository) SaveBalance(ctx context.Context, balance wb.BalanceResponse, snapshotDate string) error {
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO promotion_balance (snapshot_date, balance, net, bonus)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(snapshot_date) DO UPDATE SET balance = excluded.balance, net = excluded.net, bonus = excluded.bonus
	`, snapshotDate, balance.Balance, balance.Net, balance.Bonus)
	if err != nil {
		return fmt.Errorf("insert promotion_balance: %w", err)
	}

	// Clear and replace cashbacks for this date
	_, err = tx.ExecContext(ctx, "DELETE FROM promotion_balance_cashbacks WHERE snapshot_date = ?", snapshotDate)
	if err != nil {
		return fmt.Errorf("delete promotion_balance_cashbacks: %w", err)
	}
	cbStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO promotion_balance_cashbacks (snapshot_date, sum_val, percent_val, expiration_date)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare cashback statement: %w", err)
	}
	defer cbStmt.Close()

	for _, cb := range balance.Cashbacks {
		_, err := cbStmt.ExecContext(ctx, snapshotDate, cb.Sum, cb.Percent, cb.ExpirationDate)
		if err != nil {
			return fmt.Errorf("insert promotion_balance_cashbacks: %w", err)
		}
	}
	return tx.Commit()
}

// SavePayments saves payment history.
func (r *SQLiteSalesRepository) SavePayments(ctx context.Context, rows []wb.PaymentRow) error {
	if len(rows) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO promotion_payments (payment_id, sum_val, payment_date, type_val, status_id, card_status)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(payment_id) DO UPDATE SET
			sum_val = excluded.sum_val, payment_date = excluded.payment_date,
			type_val = excluded.type_val, status_id = excluded.status_id, card_status = excluded.card_status
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx, row.ID, row.Sum, row.Date, row.Type, row.StatusID, row.CardStatus)
		if err != nil {
			return fmt.Errorf("insert promotion_payments id=%d: %w", row.ID, err)
		}
	}
	return tx.Commit()
}

// SaveCalendarPromotions saves WB promotions calendar.
func (r *SQLiteSalesRepository) SaveCalendarPromotions(ctx context.Context, promos []wb.CalendarPromotion) error {
	if len(promos) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wb_calendar_promotions (promotion_id, name, start_date, end_date, type)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(promotion_id) DO UPDATE SET
			name = excluded.name, start_date = excluded.start_date, end_date = excluded.end_date, type = excluded.type
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, p := range promos {
		_, err := stmt.ExecContext(ctx, p.ID, p.Name, p.Start, p.End, p.Type)
		if err != nil {
			return fmt.Errorf("insert wb_calendar_promotions id=%d: %w", p.ID, err)
		}
	}
	return tx.Commit()
}

// GetCampaignProductIDs returns distinct (advert_id, nm_id) pairs for active/paused campaigns.
// Used by V2 downloader to build normquery batch requests.
// If changedSince is non-empty, only campaigns with change_time >= changedSince are included.
func (r *SQLiteSalesRepository) GetCampaignProductIDs(ctx context.Context, statuses []int, changedSince string) ([]wb.NormqueryItem, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]interface{}, 0, len(statuses)+1)
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, s)
	}
	query := fmt.Sprintf(
		"SELECT DISTINCT advert_id, nm_id FROM campaign_products WHERE advert_id IN (SELECT advert_id FROM campaigns WHERE status IN (%s)",
		strings.Join(placeholders, ","),
	)
	if changedSince != "" {
		query += " AND change_time >= ?"
		args = append(args, changedSince)
	}
	query += ")"
	rows, err := r.db.QueryContext(ctx, query, args...)
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

// GetNormqueryLastRun returns the timestamp of the last normquery_stats download (MAX(created_at)).
// Returns empty string if no data exists.
func (r *SQLiteSalesRepository) GetNormqueryLastRun(ctx context.Context) (string, error) {
	var ts sql.NullString
	err := r.db.QueryRowContext(ctx, "SELECT MAX(created_at) FROM normquery_stats").Scan(&ts)
	if err != nil {
		return "", fmt.Errorf("query normquery_last_run: %w", err)
	}
	return ts.String, nil
}

// SaveCampaignBudget saves budget snapshot for one campaign.
func (r *SQLiteSalesRepository) SaveCampaignBudget(ctx context.Context, advertID int, budget wb.BudgetResponse, snapshotDate string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO campaign_budget (advert_id, snapshot_date, total_budget)
		VALUES (?, ?, ?)
		ON CONFLICT(advert_id, snapshot_date) DO UPDATE SET total_budget = excluded.total_budget
	`, advertID, snapshotDate, budget.Total)
	if err != nil {
		return fmt.Errorf("insert campaign_budget advert_id=%d: %w", advertID, err)
	}
	return nil
}

// SaveMinBids saves minimum bid snapshots for products.
func (r *SQLiteSalesRepository) SaveMinBids(ctx context.Context, advertID int, items []wb.MinBidItem, snapshotDate string) error {
	if len(items) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO min_bids (nm_id, advert_id, placement_type, min_bid, snapshot_date)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(nm_id, advert_id, placement_type, snapshot_date) DO UPDATE SET min_bid = excluded.min_bid
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		for _, bid := range item.Bids {
			_, err := stmt.ExecContext(ctx, item.NmID, advertID, bid.Placement, bid.Bid, snapshotDate)
			if err != nil {
				return fmt.Errorf("insert min_bids nm_id=%d: %w", item.NmID, err)
			}
		}
	}
	return tx.Commit()
}

// SaveCalendarPromotionDetails saves details, advantages, and ranging for promotions.
// Upserts wb_calendar_promotion_details, deletes+inserts advantages and ranging per promotion_id.
func (r *SQLiteSalesRepository) SaveCalendarPromotionDetails(ctx context.Context, details []wb.CalendarPromotionDetail) error {
	if len(details) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	detailStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wb_calendar_promotion_details (
			promotion_id, description, in_promo_action_leftovers, in_promo_action_total,
			not_in_promo_action_leftovers, not_in_promo_action_total,
			participation_percentage, exception_products_count, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(promotion_id) DO UPDATE SET
			description = excluded.description,
			in_promo_action_leftovers = excluded.in_promo_action_leftovers,
			in_promo_action_total = excluded.in_promo_action_total,
			not_in_promo_action_leftovers = excluded.not_in_promo_action_leftovers,
			not_in_promo_action_total = excluded.not_in_promo_action_total,
			participation_percentage = excluded.participation_percentage,
			exception_products_count = excluded.exception_products_count,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare detail statement: %w", err)
	}
	defer detailStmt.Close()

	advStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO wb_calendar_promotion_advantages (promotion_id, advantage)
		VALUES (?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare advantage statement: %w", err)
	}
	defer advStmt.Close()

	rangingStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wb_calendar_promotion_ranging (promotion_id, condition, participation_rate, boost)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(promotion_id, condition) DO UPDATE SET
			participation_rate = excluded.participation_rate, boost = excluded.boost
	`)
	if err != nil {
		return fmt.Errorf("prepare ranging statement: %w", err)
	}
	defer rangingStmt.Close()

	for _, d := range details {
		_, err := detailStmt.ExecContext(ctx,
			d.ID, d.Description,
			d.InPromoActionLeftovers, d.InPromoActionTotal,
			d.NotInPromoActionLeftovers, d.NotInPromoActionTotal,
			d.ParticipationPercentage, d.ExceptionProductsCount,
		)
		if err != nil {
			return fmt.Errorf("insert promotion_details id=%d: %w", d.ID, err)
		}

		_, err = tx.ExecContext(ctx, "DELETE FROM wb_calendar_promotion_advantages WHERE promotion_id = ?", d.ID)
		if err != nil {
			return fmt.Errorf("delete advantages id=%d: %w", d.ID, err)
		}
		for _, a := range d.Advantages {
			_, err := advStmt.ExecContext(ctx, d.ID, a)
			if err != nil {
				return fmt.Errorf("insert advantage id=%d: %w", d.ID, err)
			}
		}

		_, err = tx.ExecContext(ctx, "DELETE FROM wb_calendar_promotion_ranging WHERE promotion_id = ?", d.ID)
		if err != nil {
			return fmt.Errorf("delete ranging id=%d: %w", d.ID, err)
		}
		for _, rng := range d.Ranging {
			_, err := rangingStmt.ExecContext(ctx, d.ID, rng.Condition, rng.ParticipationRate, rng.Boost)
			if err != nil {
				return fmt.Errorf("insert ranging id=%d: %w", d.ID, err)
			}
		}
	}
	return tx.Commit()
}

// SaveCalendarPromotionNomenclatures saves product data for a promotion snapshot.
// Deletes existing rows for (promotion_id, snapshot_date) before inserting.
func (r *SQLiteSalesRepository) SaveCalendarPromotionNomenclatures(ctx context.Context, promotionID int, noms []wb.CalendarPromotionNom, snapshotDate string) error {
	if len(noms) == 0 {
		return nil
	}
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM wb_calendar_promotion_nomenclatures WHERE promotion_id = ? AND snapshot_date = ?", promotionID, snapshotDate)
	if err != nil {
		return fmt.Errorf("delete nomenclatures: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO wb_calendar_promotion_nomenclatures (
			promotion_id, nm_id, in_action, price, plan_price, discount, plan_discount, currency_code, snapshot_date
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, n := range noms {
		inAction := 0
		if n.InAction {
			inAction = 1
		}
		_, err := stmt.ExecContext(ctx, promotionID, n.ID, inAction, n.Price, n.PlanPrice, n.Discount, n.PlanDiscount, n.CurrencyCode, snapshotDate)
		if err != nil {
			return fmt.Errorf("insert nomenclature nm_id=%d: %w", n.ID, err)
		}
	}
	return tx.Commit()
}

// GetCalendarPromotionIDs returns all promotion IDs from wb_calendar_promotions.
func (r *SQLiteSalesRepository) GetCalendarPromotionIDs(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT promotion_id FROM wb_calendar_promotions ORDER BY promotion_id")
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
// Used to skip "auto" promotions for /nomenclatures (API returns 422 for them).
func (r *SQLiteSalesRepository) GetCalendarPromotionIDsByType(ctx context.Context, excludeType string) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT promotion_id FROM wb_calendar_promotions WHERE type != ? ORDER BY promotion_id", excludeType)
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
