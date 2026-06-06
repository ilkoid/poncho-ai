package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// ordersSchemaSQL defines PostgreSQL table for WB Statistics API orders.
	//
	// Translated from pkg/storage/sqlite/orders_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - INTEGER boolean fields → BOOLEAN
	//   - downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP → TEXT DEFAULT TO_CHAR(...)
	ordersSchemaSQL = `
-- ============================================================================
-- ORDERS (WB Statistics API — /api/v1/supplier/orders)
-- Single flat table: 1 row per order (srid is unique)
-- ============================================================================

CREATE TABLE IF NOT EXISTS orders (
    id BIGSERIAL PRIMARY KEY,

    -- Unique order identifier (primary business key)
    srid TEXT UNIQUE NOT NULL,

    -- Timestamps (Moscow UTC+3 strings from API)
    order_date TEXT NOT NULL DEFAULT '',
    last_change_date TEXT NOT NULL DEFAULT '',

    -- Location
    warehouse_name TEXT NOT NULL DEFAULT '',
    warehouse_type TEXT NOT NULL DEFAULT '',
    country_name TEXT NOT NULL DEFAULT '',
    oblast_okrug_name TEXT NOT NULL DEFAULT '',
    region_name TEXT NOT NULL DEFAULT '',

    -- Product identification
    supplier_article TEXT NOT NULL DEFAULT '',
    nm_id BIGINT NOT NULL DEFAULT 0,
    barcode TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    brand TEXT NOT NULL DEFAULT '',
    tech_size TEXT NOT NULL DEFAULT '',

    -- Supply info
    income_id BIGINT NOT NULL DEFAULT 0,
    is_supply BOOLEAN NOT NULL DEFAULT FALSE,
    is_realization BOOLEAN NOT NULL DEFAULT FALSE,

    -- Pricing
    total_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    discount_percent INTEGER NOT NULL DEFAULT 0,
    spp DOUBLE PRECISION NOT NULL DEFAULT 0,
    finished_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    price_with_disc DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Cancellation
    is_cancel BOOLEAN NOT NULL DEFAULT FALSE,
    cancel_date TEXT NOT NULL DEFAULT '',

    -- Grouping
    sticker TEXT NOT NULL DEFAULT '',
    g_number TEXT NOT NULL DEFAULT '',

    -- Download metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_orders_nm_id ON orders(nm_id);
CREATE INDEX IF NOT EXISTS idx_orders_order_date ON orders(order_date);
CREATE INDEX IF NOT EXISTS idx_orders_g_number ON orders(g_number);
CREATE INDEX IF NOT EXISTS idx_orders_supplier_article ON orders(supplier_article);
CREATE INDEX IF NOT EXISTS idx_orders_last_change_date ON orders(last_change_date);
CREATE INDEX IF NOT EXISTS idx_orders_is_cancel ON orders(is_cancel);
`
)

// ordersMigrations widens INTEGER columns to BIGINT for ID fields.
// Safe: INTEGER→BIGINT is a widening conversion — no data loss.
const ordersMigrations = `
ALTER TABLE orders ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE orders ALTER COLUMN income_id TYPE BIGINT;
`

// initOrdersSchema creates orders table in the PostgreSQL database.
func initOrdersSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, ordersSchemaSQL)
	if err != nil {
		return fmt.Errorf("orders schema: %w", err)
	}
	if _, err := pool.Exec(ctx, ordersMigrations); err != nil {
		return fmt.Errorf("orders migrations (int4→bigint): %w", err)
	}
	return nil
}
