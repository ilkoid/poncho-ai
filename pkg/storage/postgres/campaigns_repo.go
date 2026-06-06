package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/campaigns"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgCampaignsRepo implements campaigns.CampaignsWriter.
var _ campaigns.CampaignsWriter = (*PgCampaignsRepo)(nil)

// PgCampaignsRepo implements campaigns.CampaignsWriter for PostgreSQL.
// Focused repository (ISP) — only campaign persistence methods.
type PgCampaignsRepo struct {
	pool *pgxpool.Pool
}

// NewPgCampaignsRepo creates a new PostgreSQL campaigns repository.
func NewPgCampaignsRepo(pool *pgxpool.Pool) *PgCampaignsRepo {
	return &PgCampaignsRepo{pool: pool}
}

// InitSchema creates campaign tables if they don't exist.
func (r *PgCampaignsRepo) InitSchema(ctx context.Context) error {
	return initCampaignsSchema(ctx, r.pool)
}

// ============================================================================
// SaveCampaigns — upsert from promotion/count
// ============================================================================

const pgCampaignsChunkSize = 500

// SaveCampaigns saves batch of campaign metadata using multi-row ON CONFLICT upsert.
func (r *PgCampaignsRepo) SaveCampaigns(ctx context.Context, groups []wb.PromotionAdvertGroup) error {
	if len(groups) == 0 {
		return nil
	}

	// Flatten groups → individual adverts.
	var adverts []struct {
		advertID    int
		campType    int
		status      int
		changeTime  string
	}
	for _, g := range groups {
		for _, a := range g.AdvertList {
			adverts = append(adverts, struct {
				advertID    int
				campType    int
				status      int
				changeTime  string
			}{a.AdvertID, g.Type, g.Status, a.ChangeTime})
		}
	}
	if len(adverts) == 0 {
		return nil
	}

	for i := 0; i < len(adverts); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(adverts))
		chunk := adverts[i:end]

		if err := r.saveCampaignsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("save campaigns chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// saveCampaignsChunk saves up to 500 campaigns using a single multi-row INSERT.
func (r *PgCampaignsRepo) saveCampaignsChunk(ctx context.Context, chunk []struct {
	advertID   int
	campType   int
	status     int
	changeTime string
}) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	args := make([]any, 0, len(chunk)*insertCampaignCols)
	for _, a := range chunk {
		args = append(args, a.advertID, a.campType, a.status, a.changeTime, now)
	}

	query := insertCampaignFullChunkSQL
	if len(chunk) < pgCampaignsChunkSize {
		query = BuildMultiRowInsert(insertCampaignPrefixSQL, insertCampaignOnConflictSQL, len(chunk), insertCampaignCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save campaigns batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// ============================================================================
// SaveCampaignDetails — UPDATE from adverts endpoint
// ============================================================================

// SaveCampaignDetails updates campaign metadata from /api/advert/v2/adverts.
// Uses per-row UPDATE because rows already exist from SaveCampaigns().
func (r *PgCampaignsRepo) SaveCampaignDetails(ctx context.Context, details []wb.AdvertDetail) error {
	if len(details) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, d := range details {
		_, err := tx.Exec(ctx, pgUpdateCampaignDetailsSQL,
			d.Settings.Name,
			d.Settings.PaymentType,
			d.BidType,
			d.Settings.Placements.Search,
			d.Settings.Placements.Recommendations,
			d.Timestamps.Created,
			d.Timestamps.Started,
			d.Timestamps.Deleted,
			d.ID,
		)
		if err != nil {
			return fmt.Errorf("update campaign details advert_id=%d: %w", d.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ============================================================================
// SaveFullstats — aggregate method for 4 stat tables
// ============================================================================

// SaveFullstats saves all 4 stat tables from flattened fullstats data.
// Each table is saved in chunked transactions (500 rows each).
func (r *PgCampaignsRepo) SaveFullstats(ctx context.Context, flat wb.FlattenAllResult) error {
	if err := r.saveDailyStats(ctx, flat.Daily); err != nil {
		return fmt.Errorf("save daily stats: %w", err)
	}
	if err := r.saveAppStats(ctx, flat.App); err != nil {
		return fmt.Errorf("save app stats: %w", err)
	}
	if err := r.saveNmStats(ctx, flat.Nm); err != nil {
		return fmt.Errorf("save nm stats: %w", err)
	}
	if err := r.saveBoosterStats(ctx, flat.Booster); err != nil {
		return fmt.Errorf("save booster stats: %w", err)
	}
	return nil
}

func (r *PgCampaignsRepo) saveDailyStats(ctx context.Context, rows []wb.CampaignDailyStats) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		if err := r.saveDailyStatsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("daily stats chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// saveDailyStatsChunk saves up to 500 daily stats rows using a single multi-row INSERT.
func (r *PgCampaignsRepo) saveDailyStatsChunk(ctx context.Context, chunk []wb.CampaignDailyStats) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertCampStatsDailyCols)
	for _, row := range chunk {
		args = append(args,
			row.AdvertID, row.StatsDate,
			row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
			row.Orders, row.Shks, row.Atbs, row.Canceled,
			row.Sum, row.SumPrice,
		)
	}

	query := insertCampStatsDailyFullChunkSQL
	if len(chunk) < pgCampaignsChunkSize {
		query = BuildMultiRowInsert(insertCampStatsDailyPrefixSQL, insertCampStatsDailyOnConflictSQL, len(chunk), insertCampStatsDailyCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save daily batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

func (r *PgCampaignsRepo) saveAppStats(ctx context.Context, rows []wb.CampaignAppStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		if err := r.saveAppStatsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("app stats chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// saveAppStatsChunk saves up to 500 app stats rows using a single multi-row INSERT.
func (r *PgCampaignsRepo) saveAppStatsChunk(ctx context.Context, chunk []wb.CampaignAppStatsRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertCampStatsAppCols)
	for _, row := range chunk {
		args = append(args,
			row.AdvertID, row.StatsDate, row.AppType,
			row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
			row.Orders, row.Shks, row.Atbs, row.Canceled,
			row.Sum, row.SumPrice,
		)
	}

	query := insertCampStatsAppFullChunkSQL
	if len(chunk) < pgCampaignsChunkSize {
		query = BuildMultiRowInsert(insertCampStatsAppPrefixSQL, insertCampStatsAppOnConflictSQL, len(chunk), insertCampStatsAppCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save app batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

func (r *PgCampaignsRepo) saveNmStats(ctx context.Context, rows []wb.CampaignNmStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		if err := r.saveNmStatsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("nm stats chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// saveNmStatsChunk saves up to 500 nm stats rows using a single multi-row INSERT.
func (r *PgCampaignsRepo) saveNmStatsChunk(ctx context.Context, chunk []wb.CampaignNmStatsRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertCampStatsNmCols)
	for _, row := range chunk {
		args = append(args,
			row.AdvertID, row.StatsDate, row.AppType, row.NmID, row.NmName,
			row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
			row.Orders, row.Shks, row.Atbs, row.Canceled,
			row.Sum, row.SumPrice,
		)
	}

	query := insertCampStatsNmFullChunkSQL
	if len(chunk) < pgCampaignsChunkSize {
		query = BuildMultiRowInsert(insertCampStatsNmPrefixSQL, insertCampStatsNmOnConflictSQL, len(chunk), insertCampStatsNmCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save nm batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

func (r *PgCampaignsRepo) saveBoosterStats(ctx context.Context, rows []wb.CampaignBoosterStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		if err := r.saveBoosterStatsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("booster stats chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// saveBoosterStatsChunk saves up to 500 booster stats rows using a single multi-row INSERT.
func (r *PgCampaignsRepo) saveBoosterStatsChunk(ctx context.Context, chunk []wb.CampaignBoosterStatsRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertCampBoosterCols)
	for _, row := range chunk {
		args = append(args, row.AdvertID, row.StatsDate, row.NmID, row.AvgPosition)
	}

	query := insertCampBoosterFullChunkSQL
	if len(chunk) < pgCampaignsChunkSize {
		query = BuildMultiRowInsert(insertCampBoosterPrefixSQL, insertCampBoosterOnConflictSQL, len(chunk), insertCampBoosterCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save booster batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// ============================================================================
// GetLastCampaignStatsDateAll — for resume mode
// ============================================================================

// GetLastCampaignStatsDateAll returns the most recent stats_date per campaign.
// Uses pointer scan (*string) for nullable MAX() aggregate.
func (r *PgCampaignsRepo) GetLastCampaignStatsDateAll(ctx context.Context) (map[int]time.Time, error) {
	rows, err := r.pool.Query(ctx, pgGetLastStatsDateAllSQL)
	if err != nil {
		return nil, fmt.Errorf("query last stats dates: %w", err)
	}
	defer rows.Close()

	result := make(map[int]time.Time)
	for rows.Next() {
		var advertID int
		var lastDate *string
		if err := rows.Scan(&advertID, &lastDate); err != nil {
			return nil, fmt.Errorf("scan last date: %w", err)
		}
		if lastDate != nil && *lastDate != "" {
			t, err := time.Parse("2006-01-02", *lastDate)
			if err != nil {
				continue
			}
			result[advertID] = t
		}
	}
	return result, rows.Err()
}

// ============================================================================
// PopulateCampaignProducts — rebuild materialized view
// ============================================================================

// PopulateCampaignProducts rebuilds the campaign_products materialized view
// from campaign_stats_nm data. Uses DELETE + INSERT for full refresh.
func (r *PgCampaignsRepo) PopulateCampaignProducts(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, pgPopulateCampaignProductsSQL)
	if err != nil {
		return fmt.Errorf("populate campaign_products: %w", err)
	}
	return nil
}

// ============================================================================
// Multi-row INSERT SQL fragments
// ============================================================================

const (
	// campaigns table: 5 columns. updated_at uses Go-formatted timestamp (not TO_CHAR).
	insertCampaignCols = 5

	insertCampaignPrefixSQL = `INSERT INTO campaigns (advert_id, campaign_type, status, change_time, updated_at) VALUES `

	insertCampaignOnConflictSQL = `
	ON CONFLICT (advert_id) DO UPDATE SET
	    campaign_type = EXCLUDED.campaign_type,
	    status = EXCLUDED.status,
	    change_time = EXCLUDED.change_time,
	    updated_at = EXCLUDED.updated_at`

	// campaign_stats_daily: 13 columns.
	insertCampStatsDailyCols = 13

	insertCampStatsDailyPrefixSQL = `INSERT INTO campaign_stats_daily (advert_id, stats_date, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price) VALUES `

	insertCampStatsDailyOnConflictSQL = `
	ON CONFLICT (advert_id, stats_date) DO UPDATE SET
	    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
	    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
	    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// campaign_stats_app: 14 columns.
	insertCampStatsAppCols = 14

	insertCampStatsAppPrefixSQL = `INSERT INTO campaign_stats_app (advert_id, stats_date, app_type, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price) VALUES `

	insertCampStatsAppOnConflictSQL = `
	ON CONFLICT (advert_id, stats_date, app_type) DO UPDATE SET
	    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
	    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
	    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// campaign_stats_nm: 16 columns.
	insertCampStatsNmCols = 16

	insertCampStatsNmPrefixSQL = `INSERT INTO campaign_stats_nm (advert_id, stats_date, app_type, nm_id, nm_name, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price) VALUES `

	insertCampStatsNmOnConflictSQL = `
	ON CONFLICT (advert_id, stats_date, app_type, nm_id) DO UPDATE SET
	    nm_name = EXCLUDED.nm_name,
	    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
	    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
	    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// campaign_booster_stats: 4 columns.
	insertCampBoosterCols = 4

	insertCampBoosterPrefixSQL = `INSERT INTO campaign_booster_stats (advert_id, stats_date, nm_id, avg_position) VALUES `

	insertCampBoosterOnConflictSQL = `
	ON CONFLICT (advert_id, stats_date, nm_id) DO UPDATE SET
	    avg_position = EXCLUDED.avg_position`
)

// Pre-built queries for full chunks (500 rows). Last chunk rebuilt with actual size.
var (
	insertCampaignFullChunkSQL        = BuildMultiRowInsert(insertCampaignPrefixSQL, insertCampaignOnConflictSQL, pgCampaignsChunkSize, insertCampaignCols)
	insertCampStatsDailyFullChunkSQL  = BuildMultiRowInsert(insertCampStatsDailyPrefixSQL, insertCampStatsDailyOnConflictSQL, pgCampaignsChunkSize, insertCampStatsDailyCols)
	insertCampStatsAppFullChunkSQL    = BuildMultiRowInsert(insertCampStatsAppPrefixSQL, insertCampStatsAppOnConflictSQL, pgCampaignsChunkSize, insertCampStatsAppCols)
	insertCampStatsNmFullChunkSQL     = BuildMultiRowInsert(insertCampStatsNmPrefixSQL, insertCampStatsNmOnConflictSQL, pgCampaignsChunkSize, insertCampStatsNmCols)
	insertCampBoosterFullChunkSQL     = BuildMultiRowInsert(insertCampBoosterPrefixSQL, insertCampBoosterOnConflictSQL, pgCampaignsChunkSize, insertCampBoosterCols)
)

// ============================================================================
// Non-multi-row SQL statements
// ============================================================================

var (
	// Update campaign details from adverts endpoint (per-row UPDATE, not INSERT).
	pgUpdateCampaignDetailsSQL = `
UPDATE campaigns SET
    name = $1,
    payment_type = $2,
    bid_type = $3,
    placement_search = $4,
    placement_reco = $5,
    ts_created = $6,
    ts_started = $7,
    ts_deleted = $8,
    updated_at = TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
WHERE advert_id = $9`

	// Get last stats date per campaign
	pgGetLastStatsDateAllSQL = `SELECT advert_id, MAX(stats_date) FROM campaign_stats_daily GROUP BY advert_id`

	// Rebuild campaign_products materialized view
	pgPopulateCampaignProductsSQL = `
DELETE FROM campaign_products;
INSERT INTO campaign_products (advert_id, nm_id, product_name, total_views, total_clicks, total_orders, total_sum)
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
ON CONFLICT (advert_id, nm_id) DO UPDATE SET
    product_name = EXCLUDED.product_name,
    total_views = EXCLUDED.total_views,
    total_clicks = EXCLUDED.total_clicks,
    total_orders = EXCLUDED.total_orders,
    total_sum = EXCLUDED.total_sum`
)
