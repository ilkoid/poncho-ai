package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// funnelAggProductsSchemaSQL defines the products table (shared with cards/funnel).
	// Includes tags column (JSON array) — needed by funnel-agg but not by funnel-daily.
	// CREATE TABLE IF NOT EXISTS for safety (shared table).
	funnelAggProductsSchemaSQL = `
CREATE TABLE IF NOT EXISTS products (
    nm_id BIGINT PRIMARY KEY,

    -- Product identification
    vendor_code TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    brand_name TEXT NOT NULL DEFAULT '',

    -- Category hierarchy
    subject_id BIGINT NOT NULL DEFAULT 0,
    subject_name TEXT NOT NULL DEFAULT '',

    -- Quality metrics
    product_rating DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedback_rating DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Stock levels
    stock_wb BIGINT NOT NULL DEFAULT 0,
    stock_mp BIGINT NOT NULL DEFAULT 0,
    stock_balance_sum BIGINT NOT NULL DEFAULT 0,

    -- Tags (JSON array)
    tags TEXT NOT NULL DEFAULT '',

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE INDEX IF NOT EXISTS idx_products_subject_id ON products(subject_id);
CREATE INDEX IF NOT EXISTS idx_products_brand_name ON products(brand_name);
CREATE INDEX IF NOT EXISTS idx_products_updated_at ON products(updated_at);
`

	// funnelAggSchemaSQL defines the funnel_metrics_aggregated table.
	//
	// Translated from pkg/storage/sqlite/schema.go (funnel_metrics_aggregated table):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - 90 data columns + id + created_at
	funnelAggSchemaSQL = `
CREATE TABLE IF NOT EXISTS funnel_metrics_aggregated (
    id BIGSERIAL PRIMARY KEY,

    -- Natural key for upsert
    nm_id BIGINT NOT NULL DEFAULT 0,
    period_start TEXT NOT NULL DEFAULT '',
    period_end TEXT NOT NULL DEFAULT '',

    -- Selected period metrics (NOT NULL — always present)
    selected_open_count BIGINT NOT NULL DEFAULT 0,
    selected_cart_count BIGINT NOT NULL DEFAULT 0,
    selected_order_count BIGINT NOT NULL DEFAULT 0,
    selected_order_sum BIGINT NOT NULL DEFAULT 0,
    selected_buyout_count BIGINT NOT NULL DEFAULT 0,
    selected_buyout_sum BIGINT NOT NULL DEFAULT 0,
    selected_cancel_count BIGINT NOT NULL DEFAULT 0,
    selected_cancel_sum BIGINT NOT NULL DEFAULT 0,
    selected_avg_price BIGINT NOT NULL DEFAULT 0,
    selected_avg_orders_count_per_day DOUBLE PRECISION,
    selected_share_order_percent DOUBLE PRECISION,
    selected_add_to_wishlist BIGINT NOT NULL DEFAULT 0,
    selected_localization_percent DOUBLE PRECISION,
    selected_time_to_ready_days INTEGER NOT NULL DEFAULT 0,
    selected_time_to_ready_hours INTEGER NOT NULL DEFAULT 0,
    selected_time_to_ready_mins INTEGER NOT NULL DEFAULT 0,

    -- Selected WB Club metrics
    selected_wb_club_order_count BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_order_sum BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_buyout_count BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_buyout_sum BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_cancel_count BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_cancel_sum BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_avg_price BIGINT NOT NULL DEFAULT 0,
    selected_wb_club_buyout_percent DOUBLE PRECISION,
    selected_wb_club_avg_order_count_per_day DOUBLE PRECISION,

    -- Selected Conversions
    selected_conversion_add_to_cart DOUBLE PRECISION,
    selected_conversion_cart_to_order DOUBLE PRECISION,
    selected_conversion_buyout DOUBLE PRECISION,

    -- Past period metrics (nullable — only present when past period is requested)
    past_period_start TEXT,
    past_period_end TEXT,
    past_open_count BIGINT,
    past_cart_count BIGINT,
    past_order_count BIGINT,
    past_order_sum BIGINT,
    past_buyout_count BIGINT,
    past_buyout_sum BIGINT,
    past_cancel_count BIGINT,
    past_cancel_sum BIGINT,
    past_avg_price BIGINT,
    past_avg_orders_count_per_day DOUBLE PRECISION,
    past_share_order_percent DOUBLE PRECISION,
    past_add_to_wishlist BIGINT,
    past_localization_percent DOUBLE PRECISION,
    past_time_to_ready_days INTEGER,
    past_time_to_ready_hours INTEGER,
    past_time_to_ready_mins INTEGER,

    -- Past WB Club metrics
    past_wb_club_order_count BIGINT,
    past_wb_club_order_sum BIGINT,
    past_wb_club_buyout_count BIGINT,
    past_wb_club_buyout_sum BIGINT,
    past_wb_club_cancel_count BIGINT,
    past_wb_club_cancel_sum BIGINT,
    past_wb_club_avg_price BIGINT,
    past_wb_club_buyout_percent DOUBLE PRECISION,
    past_wb_club_avg_order_count_per_day DOUBLE PRECISION,

    -- Past Conversions
    past_conversion_add_to_cart DOUBLE PRECISION,
    past_conversion_cart_to_order DOUBLE PRECISION,
    past_conversion_buyout DOUBLE PRECISION,

    -- Comparison metrics (nullable)
    comparison_open_count_dynamic BIGINT,
    comparison_cart_count_dynamic BIGINT,
    comparison_order_count_dynamic BIGINT,
    comparison_order_sum_dynamic BIGINT,
    comparison_buyout_count_dynamic BIGINT,
    comparison_buyout_sum_dynamic BIGINT,
    comparison_cancel_count_dynamic BIGINT,
    comparison_cancel_sum_dynamic BIGINT,
    comparison_avg_orders_count_per_day_dynamic DOUBLE PRECISION,
    comparison_avg_price_dynamic BIGINT,
    comparison_share_order_percent_dynamic DOUBLE PRECISION,
    comparison_add_to_wishlist_dynamic BIGINT,
    comparison_localization_percent_dynamic DOUBLE PRECISION,
    comparison_time_to_ready_days INTEGER,
    comparison_time_to_ready_hours INTEGER,
    comparison_time_to_ready_mins INTEGER,

    -- Comparison WB Club metrics
    comparison_wb_club_order_count BIGINT,
    comparison_wb_club_order_sum BIGINT,
    comparison_wb_club_buyout_count BIGINT,
    comparison_wb_club_buyout_sum BIGINT,
    comparison_wb_club_cancel_count BIGINT,
    comparison_wb_club_cancel_sum BIGINT,
    comparison_wb_club_avg_price BIGINT,
    comparison_wb_club_buyout_percent DOUBLE PRECISION,
    comparison_wb_club_avg_order_count_per_day DOUBLE PRECISION,

    -- Comparison Conversions
    comparison_conversion_add_to_cart DOUBLE PRECISION,
    comparison_conversion_cart_to_order DOUBLE PRECISION,
    comparison_conversion_buyout DOUBLE PRECISION,

    -- Metadata
    currency TEXT DEFAULT 'RUB',
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(nm_id, period_start, period_end)
);

CREATE INDEX IF NOT EXISTS idx_funnel_agg_product_period
    ON funnel_metrics_aggregated(nm_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_funnel_agg_period
    ON funnel_metrics_aggregated(period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_funnel_agg_orders
    ON funnel_metrics_aggregated(period_start, selected_order_count);
CREATE INDEX IF NOT EXISTS idx_funnel_agg_conversion
    ON funnel_metrics_aggregated(period_start, selected_conversion_buyout);
`
)

const funnelAggMigrations = `
ALTER TABLE products ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE products ALTER COLUMN subject_id TYPE BIGINT;
ALTER TABLE products ALTER COLUMN stock_wb TYPE BIGINT;
ALTER TABLE products ALTER COLUMN stock_mp TYPE BIGINT;
ALTER TABLE products ALTER COLUMN stock_balance_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_open_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_cart_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_order_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_order_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_buyout_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_buyout_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_cancel_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_cancel_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_avg_price TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_add_to_wishlist TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_order_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_order_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_buyout_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_buyout_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_cancel_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_cancel_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN selected_wb_club_avg_price TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_open_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_cart_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_order_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_order_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_buyout_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_buyout_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_cancel_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_cancel_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_avg_price TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_add_to_wishlist TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_order_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_order_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_buyout_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_buyout_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_cancel_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_cancel_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN past_wb_club_avg_price TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_open_count_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_cart_count_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_order_count_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_order_sum_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_buyout_count_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_buyout_sum_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_cancel_count_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_cancel_sum_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_avg_price_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_add_to_wishlist_dynamic TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_order_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_order_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_buyout_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_buyout_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_cancel_count TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_cancel_sum TYPE BIGINT;
ALTER TABLE funnel_metrics_aggregated ALTER COLUMN comparison_wb_club_avg_price TYPE BIGINT;
`

// initFunnelAggSchema creates products and funnel_metrics_aggregated tables in PostgreSQL.
func initFunnelAggSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, funnelAggProductsSchemaSQL); err != nil {
		return fmt.Errorf("funnel-agg products schema: %w", err)
	}
	if _, err := pool.Exec(ctx, funnelAggSchemaSQL); err != nil {
		return fmt.Errorf("funnel-agg schema: %w", err)
	}
	if _, err := pool.Exec(ctx, funnelAggMigrations); err != nil {
		return fmt.Errorf("funnel-agg migrations (int4→bigint): %w", err)
	}
	return nil
}
