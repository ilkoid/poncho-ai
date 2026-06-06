package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// regionSalesSchemaSQL defines the region_sales table for PostgreSQL.
	//
	// Translated from pkg/storage/sqlite/region_sales_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - UNIQUE constraint preserved for upsert logic (6-column business key)
	regionSalesSchemaSQL = `
CREATE TABLE IF NOT EXISTS region_sales (
    id BIGSERIAL PRIMARY KEY,

    -- Product identification
    nm_id BIGINT NOT NULL,
    sa TEXT NOT NULL DEFAULT '',

    -- Geography hierarchy
    country_name TEXT NOT NULL DEFAULT '',
    fo_name TEXT NOT NULL DEFAULT '',
    region_name TEXT NOT NULL DEFAULT '',
    city_name TEXT NOT NULL DEFAULT '',

    -- Period (matches API request params)
    date_from TEXT NOT NULL,
    date_to TEXT NOT NULL,

    -- Sales metrics
    sale_invoice_cost_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    sale_invoice_cost_price_perc DOUBLE PRECISION NOT NULL DEFAULT 0,
    sale_item_invoice_qty BIGINT NOT NULL DEFAULT 0,

    -- Metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(nm_id, region_name, city_name, country_name, date_from, date_to)
);

-- Composite index for product + period lookups
CREATE INDEX IF NOT EXISTS idx_region_sales_nm_period
    ON region_sales(nm_id, date_from, date_to);

-- Index for region-level aggregation
CREATE INDEX IF NOT EXISTS idx_region_sales_region_date
    ON region_sales(region_name, date_from);

-- Index for country-level aggregation
CREATE INDEX IF NOT EXISTS idx_region_sales_country_date
    ON region_sales(country_name, date_from);

-- Index for date range filtering
CREATE INDEX IF NOT EXISTS idx_region_sales_dates
    ON region_sales(date_from, date_to);
`
)

const regionSalesMigrations = `
ALTER TABLE region_sales ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE region_sales ALTER COLUMN sale_item_invoice_qty TYPE BIGINT;
`

// initRegionSalesSchema creates region_sales table in PostgreSQL.
func initRegionSalesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, regionSalesSchemaSQL); err != nil {
		return fmt.Errorf("region_sales schema: %w", err)
	}
	if _, err := pool.Exec(ctx, regionSalesMigrations); err != nil {
		return fmt.Errorf("region_sales migrations (int4→bigint): %w", err)
	}
	return nil
}
