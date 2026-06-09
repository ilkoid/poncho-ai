package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// stockProductsSchemaSQL defines the stock_products table for PostgreSQL.
	//
	// Translated from pkg/storage/sqlite/schema.go (stock_products table):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - INTEGER (quantities/IDs) → BIGINT
	//   - INTEGER (bounded 0-100) → INTEGER
	//   - REAL (floats) → DOUBLE PRECISION
	//   - INTEGER (booleans) → BOOLEAN
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - UNIQUE constraint preserved for upsert logic
	stockProductsSchemaSQL = `
CREATE TABLE IF NOT EXISTS stock_products (
    id BIGSERIAL PRIMARY KEY,

    -- Snapshot identification
    snapshot_date TEXT NOT NULL,

    -- Product key (part of UNIQUE constraint)
    nm_id BIGINT NOT NULL,

    -- Product info
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    subject_name TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    vendor_code TEXT NOT NULL DEFAULT '',
    brand_name TEXT NOT NULL DEFAULT '',
    main_photo TEXT NOT NULL DEFAULT '',
    has_sizes BOOLEAN NOT NULL DEFAULT FALSE,

    -- Counts and sums (Swagger: integer uint64 → BIGINT)
    orders_count BIGINT NOT NULL DEFAULT 0,
    orders_sum BIGINT NOT NULL DEFAULT 0,
    avg_orders DOUBLE PRECISION NOT NULL DEFAULT 0,
    buyout_count BIGINT NOT NULL DEFAULT 0,
    buyout_sum BIGINT NOT NULL DEFAULT 0,
    buyout_percent INTEGER NOT NULL DEFAULT 0,
    stock_count BIGINT NOT NULL DEFAULT 0,
    stock_sum BIGINT NOT NULL DEFAULT 0,
    to_client_count BIGINT NOT NULL DEFAULT 0,
    from_client_count BIGINT NOT NULL DEFAULT 0,

    -- Duration metrics (special negatives: -1=infinite, -2=zero, -3=not calc, -4=absent)
    sale_rate_days INTEGER NOT NULL DEFAULT 0,
    sale_rate_hours INTEGER NOT NULL DEFAULT 0,
    avg_stock_turnover_days INTEGER NOT NULL DEFAULT 0,
    avg_stock_turnover_hours INTEGER NOT NULL DEFAULT 0,
    office_missing_time_days INTEGER NOT NULL DEFAULT 0,
    office_missing_time_hours INTEGER NOT NULL DEFAULT 0,

    -- Lost metrics (Swagger: number float64, special negatives)
    lost_orders_count DOUBLE PRECISION NOT NULL DEFAULT 0,
    lost_orders_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    lost_buyouts_count DOUBLE PRECISION NOT NULL DEFAULT 0,
    lost_buyouts_sum DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Avg orders by month (JSON array, rarely queried)
    avg_orders_by_month TEXT DEFAULT '[]',

    -- Price range (Swagger: integer uint64 → BIGINT — NOT float)
    current_price_min BIGINT NOT NULL DEFAULT 0,
    current_price_max BIGINT NOT NULL DEFAULT 0,

    -- Availability enum
    availability TEXT NOT NULL DEFAULT '',

    -- Metadata (DEFAULT — NOT in INSERT column list, Bug #4/#6)
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(snapshot_date, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_stock_products_date
    ON stock_products(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_stock_products_nm_id
    ON stock_products(nm_id);
CREATE INDEX IF NOT EXISTS idx_stock_products_vendor_code
    ON stock_products(vendor_code);
CREATE INDEX IF NOT EXISTS idx_stock_products_availability
    ON stock_products(availability, snapshot_date);
`
)

// initStockProductsSchema creates stock_products table in PostgreSQL.
func initStockProductsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, stockProductsSchemaSQL); err != nil {
		return fmt.Errorf("stock products schema: %w", err)
	}
	return nil
}
