package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// campaignsSchemaSQL defines PostgreSQL tables for WB Promotion API campaigns.
	//
	// 6 tables: campaigns, campaign_stats_daily, campaign_stats_app,
	// campaign_stats_nm, campaign_booster_stats, campaign_products.
	//
	// Translated from pkg/storage/sqlite/cards_schema.go (campaigns section):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - INTEGER boolean fields → BOOLEAN
	//   - INSERT OR REPLACE → ON CONFLICT ... DO UPDATE SET ... = EXCLUDED
	campaignsSchemaSQL = `
-- ============================================================================
-- CAMPAIGNS (WB Promotion API — GET /adv/v1/promotion/count)
-- Master table: 1 row per campaign (advert_id is business key)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaigns (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT UNIQUE NOT NULL,
    campaign_type INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0,
    change_time TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    payment_type TEXT NOT NULL DEFAULT '',
    bid_type TEXT NOT NULL DEFAULT '',
    placement_search BOOLEAN NOT NULL DEFAULT FALSE,
    placement_reco BOOLEAN NOT NULL DEFAULT FALSE,
    ts_created TEXT NOT NULL DEFAULT '',
    ts_started TEXT NOT NULL DEFAULT '',
    ts_deleted TEXT NOT NULL DEFAULT '',
    total_views BIGINT NOT NULL DEFAULT 0,
    total_clicks BIGINT NOT NULL DEFAULT 0,
    total_orders BIGINT NOT NULL DEFAULT 0,
    total_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_sum_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_campaigns_status ON campaigns(status);

-- ============================================================================
-- CAMPAIGN STATS DAILY (WB Promotion API — GET /adv/v3/fullstats days[])
-- Grain: (advert_id, stats_date)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaign_stats_daily (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT NOT NULL,
    stats_date TEXT NOT NULL DEFAULT '',
    views BIGINT NOT NULL DEFAULT 0,
    clicks BIGINT NOT NULL DEFAULT 0,
    ctr DOUBLE PRECISION NOT NULL DEFAULT 0,
    cpc DOUBLE PRECISION NOT NULL DEFAULT 0,
    cr DOUBLE PRECISION NOT NULL DEFAULT 0,
    orders BIGINT NOT NULL DEFAULT 0,
    shks BIGINT NOT NULL DEFAULT 0,
    atbs BIGINT NOT NULL DEFAULT 0,
    canceled BIGINT NOT NULL DEFAULT 0,
    sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    sum_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(advert_id, stats_date)
);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_daily_date ON campaign_stats_daily(stats_date);

-- ============================================================================
-- CAMPAIGN STATS APP (WB Promotion API — GET /adv/v3/fullstats days[].apps[])
-- Grain: (advert_id, stats_date, app_type)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaign_stats_app (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT NOT NULL,
    stats_date TEXT NOT NULL DEFAULT '',
    app_type INTEGER NOT NULL DEFAULT 0,
    views BIGINT NOT NULL DEFAULT 0,
    clicks BIGINT NOT NULL DEFAULT 0,
    ctr DOUBLE PRECISION NOT NULL DEFAULT 0,
    cpc DOUBLE PRECISION NOT NULL DEFAULT 0,
    cr DOUBLE PRECISION NOT NULL DEFAULT 0,
    orders BIGINT NOT NULL DEFAULT 0,
    shks BIGINT NOT NULL DEFAULT 0,
    atbs BIGINT NOT NULL DEFAULT 0,
    canceled BIGINT NOT NULL DEFAULT 0,
    sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    sum_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(advert_id, stats_date, app_type)
);

-- ============================================================================
-- CAMPAIGN STATS NM (WB Promotion API — GET /adv/v3/fullstats days[].apps[].nms[])
-- Grain: (advert_id, stats_date, app_type, nm_id)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaign_stats_nm (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT NOT NULL,
    stats_date TEXT NOT NULL DEFAULT '',
    app_type INTEGER NOT NULL DEFAULT 0,
    nm_id BIGINT NOT NULL DEFAULT 0,
    nm_name TEXT NOT NULL DEFAULT '',
    views BIGINT NOT NULL DEFAULT 0,
    clicks BIGINT NOT NULL DEFAULT 0,
    ctr DOUBLE PRECISION NOT NULL DEFAULT 0,
    cpc DOUBLE PRECISION NOT NULL DEFAULT 0,
    cr DOUBLE PRECISION NOT NULL DEFAULT 0,
    orders BIGINT NOT NULL DEFAULT 0,
    shks BIGINT NOT NULL DEFAULT 0,
    atbs BIGINT NOT NULL DEFAULT 0,
    canceled BIGINT NOT NULL DEFAULT 0,
    sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    sum_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(advert_id, stats_date, app_type, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_nm_nmid ON campaign_stats_nm(nm_id);

-- ============================================================================
-- CAMPAIGN BOOSTER STATS (WB Promotion API — GET /adv/v3/fullstats boosterStats[])
-- Grain: (advert_id, stats_date, nm_id)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaign_booster_stats (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT NOT NULL,
    stats_date TEXT NOT NULL DEFAULT '',
    nm_id BIGINT NOT NULL DEFAULT 0,
    avg_position DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(advert_id, stats_date, nm_id)
);

-- ============================================================================
-- CAMPAIGN PRODUCTS (materialized view from campaign_stats_nm)
-- Grain: (advert_id, nm_id)
-- ============================================================================

CREATE TABLE IF NOT EXISTS campaign_products (
    id BIGSERIAL PRIMARY KEY,
    advert_id BIGINT NOT NULL,
    nm_id BIGINT NOT NULL DEFAULT 0,
    product_name TEXT NOT NULL DEFAULT '',
    total_views BIGINT NOT NULL DEFAULT 0,
    total_clicks BIGINT NOT NULL DEFAULT 0,
    total_orders BIGINT NOT NULL DEFAULT 0,
    total_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(advert_id, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_campaign_products_nmid ON campaign_products(nm_id);
`
)

const campaignsMigrations = `
ALTER TABLE campaigns ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaigns ALTER COLUMN total_views TYPE BIGINT;
ALTER TABLE campaigns ALTER COLUMN total_clicks TYPE BIGINT;
ALTER TABLE campaigns ALTER COLUMN total_orders TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN views TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN clicks TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN orders TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN shks TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN atbs TYPE BIGINT;
ALTER TABLE campaign_stats_daily ALTER COLUMN canceled TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN views TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN clicks TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN orders TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN shks TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN atbs TYPE BIGINT;
ALTER TABLE campaign_stats_app ALTER COLUMN canceled TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN views TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN clicks TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN orders TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN shks TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN atbs TYPE BIGINT;
ALTER TABLE campaign_stats_nm ALTER COLUMN canceled TYPE BIGINT;
ALTER TABLE campaign_booster_stats ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_booster_stats ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE campaign_products ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_products ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE campaign_products ALTER COLUMN total_views TYPE BIGINT;
ALTER TABLE campaign_products ALTER COLUMN total_clicks TYPE BIGINT;
ALTER TABLE campaign_products ALTER COLUMN total_orders TYPE BIGINT;
`

// initCampaignsSchema creates campaign tables in the PostgreSQL database.
func initCampaignsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, campaignsSchemaSQL)
	if err != nil {
		return fmt.Errorf("campaigns schema: %w", err)
	}
	if _, err := pool.Exec(ctx, campaignsMigrations); err != nil {
		return fmt.Errorf("campaigns migrations (int4→bigint): %w", err)
	}
	return nil
}
