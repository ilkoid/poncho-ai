package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// pricesSchemaSQL defines PostgreSQL table for WB Discounts-Prices API data.
	//
	// Translated from pkg/storage/sqlite/prices_schema.go:
	//   - REAL → DOUBLE PRECISION
	//   - Added editable_size_price BOOLEAN (present in wb.ProductPrice but not in SQLite schema)
	//   - created_at → downloaded_at with TO_CHAR(NOW()...)
	pricesSchemaSQL = `
-- ============================================================================
-- PRODUCT_PRICES (WB Discounts-Prices API — /api/v2/list/goods/filter)
-- Grain: one row per (nm_id, snapshot_date) — current prices at point in time.
-- ============================================================================

CREATE TABLE IF NOT EXISTS product_prices (
    nm_id                 BIGINT NOT NULL,
    snapshot_date         TEXT    NOT NULL,

    -- Pricing
    price                 BIGINT NOT NULL DEFAULT 0,
    discounted_price      DOUBLE PRECISION NOT NULL DEFAULT 0,
    club_discounted_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    discount              INTEGER NOT NULL DEFAULT 0,
    club_discount         INTEGER NOT NULL DEFAULT 0,

    -- Product identification
    vendor_code           TEXT    NOT NULL DEFAULT '',
    currency              TEXT    NOT NULL DEFAULT 'RUB',
    editable_size_price   BOOLEAN NOT NULL DEFAULT FALSE,

    -- Download metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    PRIMARY KEY (nm_id, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_prices_snapshot_date ON product_prices(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_prices_vendor_code ON product_prices(vendor_code);
`
)

const pricesMigrations = `
ALTER TABLE product_prices ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE product_prices ALTER COLUMN price TYPE BIGINT;
`

// initPricesSchema creates product_prices table in the PostgreSQL database.
func initPricesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, pricesSchemaSQL)
	if err != nil {
		return fmt.Errorf("prices schema: %w", err)
	}
	if _, err := pool.Exec(ctx, pricesMigrations); err != nil {
		return fmt.Errorf("prices migrations (int4→bigint): %w", err)
	}
	return nil
}
