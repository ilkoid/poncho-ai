package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// funnelProductsSchemaSQL defines the products table for product metadata.
	//
	// Translated from pkg/storage/sqlite/schema.go (products table):
	//   - INTEGER PRIMARY KEY → INTEGER PRIMARY KEY (natural key nm_id)
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - Shared with cards/funnel downloaders — CREATE IF NOT EXISTS for safety.
	funnelProductsSchemaSQL = `
CREATE TABLE IF NOT EXISTS products (
    nm_id INTEGER PRIMARY KEY,

    -- Product identification
    vendor_code TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    brand_name TEXT NOT NULL DEFAULT '',

    -- Category hierarchy
    subject_id INTEGER NOT NULL DEFAULT 0,
    subject_name TEXT NOT NULL DEFAULT '',

    -- Quality metrics
    product_rating DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedback_rating DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Stock levels
    stock_wb INTEGER NOT NULL DEFAULT 0,
    stock_mp INTEGER NOT NULL DEFAULT 0,
    stock_balance_sum INTEGER NOT NULL DEFAULT 0,

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

	// funnelMetricsSchemaSQL defines daily funnel metrics table.
	//
	// Translated from pkg/storage/sqlite/schema.go (funnel_metrics_daily table):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - UNIQUE(nm_id, metric_date) preserved for upsert logic
	funnelMetricsSchemaSQL = `
CREATE TABLE IF NOT EXISTS funnel_metrics_daily (
    id BIGSERIAL PRIMARY KEY,

    -- Natural key for upsert
    nm_id INTEGER NOT NULL DEFAULT 0,
    metric_date TEXT NOT NULL DEFAULT '',

    -- Funnel counts
    open_count INTEGER NOT NULL DEFAULT 0,
    cart_count INTEGER NOT NULL DEFAULT 0,
    order_count INTEGER NOT NULL DEFAULT 0,
    buyout_count INTEGER NOT NULL DEFAULT 0,
    add_to_wishlist INTEGER NOT NULL DEFAULT 0,

    -- Financial metrics
    order_sum INTEGER NOT NULL DEFAULT 0,
    buyout_sum INTEGER NOT NULL DEFAULT 0,

    -- Conversion rates
    conversion_add_to_cart DOUBLE PRECISION,
    conversion_cart_to_order DOUBLE PRECISION,
    conversion_buyout DOUBLE PRECISION,

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(nm_id, metric_date)
);

CREATE INDEX IF NOT EXISTS idx_funnel_product_date ON funnel_metrics_daily(nm_id, metric_date);
CREATE INDEX IF NOT EXISTS idx_funnel_date_product ON funnel_metrics_daily(metric_date, nm_id);
CREATE INDEX IF NOT EXISTS idx_funnel_orders ON funnel_metrics_daily(metric_date, order_count);
CREATE INDEX IF NOT EXISTS idx_funnel_conversion ON funnel_metrics_daily(metric_date, conversion_buyout);
CREATE INDEX IF NOT EXISTS idx_funnel_nm_id_created ON funnel_metrics_daily(nm_id, created_at);
`
)

// initFunnelSchema creates products and funnel_metrics_daily tables in PostgreSQL.
func initFunnelSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, funnelProductsSchemaSQL); err != nil {
		return fmt.Errorf("funnel products schema: %w", err)
	}
	if _, err := pool.Exec(ctx, funnelMetricsSchemaSQL); err != nil {
		return fmt.Errorf("funnel metrics schema: %w", err)
	}
	return nil
}
