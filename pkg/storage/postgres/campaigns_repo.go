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

// SaveCampaigns saves batch of campaign metadata using ON CONFLICT upsert.
func (r *PgCampaignsRepo) SaveCampaigns(ctx context.Context, groups []wb.PromotionAdvertGroup) error {
	if len(groups) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, group := range groups {
		for _, advert := range group.AdvertList {
			_, err := tx.Exec(ctx, pgUpsertCampaignSQL,
				advert.AdvertID,
				group.Type,
				group.Status,
				advert.ChangeTime,
			)
			if err != nil {
				return fmt.Errorf("upsert campaign advert_id=%d: %w", advert.AdvertID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ============================================================================
// SaveCampaignDetails — UPDATE from adverts endpoint
// ============================================================================

// SaveCampaignDetails updates campaign metadata from /api/advert/v2/adverts.
// Uses UPDATE because rows already exist from SaveCampaigns().
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

const pgCampaignsChunkSize = 500

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

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertDailySQL,
				row.AdvertID, row.StatsDate,
				row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
				row.Orders, row.Shks, row.Atbs, row.Canceled,
				row.Sum, row.SumPrice,
			)
			if err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("upsert daily advert=%d date=%s: %w", row.AdvertID, row.StatsDate, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit daily: %w", err)
		}
	}
	return nil
}

func (r *PgCampaignsRepo) saveAppStats(ctx context.Context, rows []wb.CampaignAppStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertAppSQL,
				row.AdvertID, row.StatsDate, row.AppType,
				row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
				row.Orders, row.Shks, row.Atbs, row.Canceled,
				row.Sum, row.SumPrice,
			)
			if err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("upsert app advert=%d date=%s app=%d: %w", row.AdvertID, row.StatsDate, row.AppType, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit app: %w", err)
		}
	}
	return nil
}

func (r *PgCampaignsRepo) saveNmStats(ctx context.Context, rows []wb.CampaignNmStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertNmSQL,
				row.AdvertID, row.StatsDate, row.AppType, row.NmID, row.NmName,
				row.Views, row.Clicks, row.CTR, row.CPC, row.CR,
				row.Orders, row.Shks, row.Atbs, row.Canceled,
				row.Sum, row.SumPrice,
			)
			if err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("upsert nm advert=%d date=%s app=%d nm=%d: %w", row.AdvertID, row.StatsDate, row.AppType, row.NmID, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit nm: %w", err)
		}
	}
	return nil
}

func (r *PgCampaignsRepo) saveBoosterStats(ctx context.Context, rows []wb.CampaignBoosterStatsRow) error {
	for i := 0; i < len(rows); i += pgCampaignsChunkSize {
		end := min(i+pgCampaignsChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertBoosterSQL,
				row.AdvertID, row.StatsDate, row.NmID, row.AvgPosition,
			)
			if err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("upsert booster advert=%d date=%s nm=%d: %w", row.AdvertID, row.StatsDate, row.NmID, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit booster: %w", err)
		}
	}
	return nil
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
// SQL statements
// ============================================================================

var (
	// Upsert campaign from promotion/count
	pgUpsertCampaignSQL = `
INSERT INTO campaigns (advert_id, campaign_type, status, change_time, updated_at)
VALUES ($1, $2, $3, $4, TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
ON CONFLICT (advert_id) DO UPDATE SET
    campaign_type = EXCLUDED.campaign_type,
    status = EXCLUDED.status,
    change_time = EXCLUDED.change_time,
    updated_at = EXCLUDED.updated_at`

	// Update campaign details from adverts endpoint
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

	// Upsert daily stats
	pgUpsertDailySQL = `
INSERT INTO campaign_stats_daily (advert_id, stats_date, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (advert_id, stats_date) DO UPDATE SET
    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// Upsert app stats
	pgUpsertAppSQL = `
INSERT INTO campaign_stats_app (advert_id, stats_date, app_type, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (advert_id, stats_date, app_type) DO UPDATE SET
    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// Upsert nm stats
	pgUpsertNmSQL = `
INSERT INTO campaign_stats_nm (advert_id, stats_date, app_type, nm_id, nm_name, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (advert_id, stats_date, app_type, nm_id) DO UPDATE SET
    nm_name = EXCLUDED.nm_name,
    views = EXCLUDED.views, clicks = EXCLUDED.clicks, ctr = EXCLUDED.ctr, cpc = EXCLUDED.cpc, cr = EXCLUDED.cr,
    orders = EXCLUDED.orders, shks = EXCLUDED.shks, atbs = EXCLUDED.atbs, canceled = EXCLUDED.canceled,
    sum = EXCLUDED.sum, sum_price = EXCLUDED.sum_price`

	// Upsert booster stats
	pgUpsertBoosterSQL = `
INSERT INTO campaign_booster_stats (advert_id, stats_date, nm_id, avg_position)
VALUES ($1,$2,$3,$4)
ON CONFLICT (advert_id, stats_date, nm_id) DO UPDATE SET
    avg_position = EXCLUDED.avg_position`

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
