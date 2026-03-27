// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// Campaign Repository Methods
// ============================================================================

// SaveCampaigns saves batch of campaign metadata.
// Uses INSERT ... ON CONFLICT DO UPDATE to preserve aggregate columns.
func (r *SQLiteSalesRepository) SaveCampaigns(ctx context.Context, groups []wb.PromotionAdvertGroup) error {
	if len(groups) == 0 {
		return nil
	}

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

// SaveCampaignDetails updates campaign metadata from /api/advert/v2/adverts.
// Uses UPDATE because campaigns already exist from SaveCampaigns().
func (r *SQLiteSalesRepository) SaveCampaignDetails(ctx context.Context, details []wb.AdvertDetail) error {
	if len(details) == 0 {
		return nil
	}

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
