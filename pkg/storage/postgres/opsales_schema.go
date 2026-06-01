package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// opsalesSchemaSQL defines PostgreSQL table for WB Statistics API operational sales.
	//
	// Translated from pkg/storage/sqlite/opsales_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - INTEGER boolean fields → BOOLEAN
	//   - downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP → TEXT DEFAULT TO_CHAR(...)
	opsalesSchemaSQL = `
-- ============================================================================
-- OPERATIONAL SALES (WB Statistics API — /api/v1/supplier/sales)
-- Single flat table: 1 row per sale/return event (sale_id is unique)
-- ============================================================================

CREATE TABLE IF NOT EXISTS operational_sales (
    id BIGSERIAL PRIMARY KEY,

    -- Unique sale/return identifier (primary business key)
    sale_id TEXT UNIQUE NOT NULL,

    -- Timestamps (Moscow UTC+3 strings from API)
    sale_date TEXT NOT NULL DEFAULT '',
    last_change_date TEXT NOT NULL DEFAULT '',

    -- Location
    warehouse_name TEXT NOT NULL DEFAULT '',
    warehouse_type TEXT NOT NULL DEFAULT '',
    country_name TEXT NOT NULL DEFAULT '',
    oblast_okrug_name TEXT NOT NULL DEFAULT '',
    region_name TEXT NOT NULL DEFAULT '',

    -- Product identification
    supplier_article TEXT NOT NULL DEFAULT '',
    nm_id INTEGER NOT NULL DEFAULT 0,
    barcode TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    brand TEXT NOT NULL DEFAULT '',
    tech_size TEXT NOT NULL DEFAULT '',

    -- Supply info
    income_id INTEGER NOT NULL DEFAULT 0,
    is_supply BOOLEAN NOT NULL DEFAULT FALSE,
    is_realization BOOLEAN NOT NULL DEFAULT FALSE,

    -- Pricing
    total_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    discount_percent INTEGER NOT NULL DEFAULT 0,
    spp DOUBLE PRECISION NOT NULL DEFAULT 0,
    payment_sale_amount INTEGER NOT NULL DEFAULT 0,
    for_pay DOUBLE PRECISION NOT NULL DEFAULT 0,
    finished_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    price_with_disc DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Grouping
    sticker TEXT NOT NULL DEFAULT '',
    g_number TEXT NOT NULL DEFAULT '',

    -- WB internal ID
    srid TEXT NOT NULL DEFAULT '',

    -- Download metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_opsales_sale_id ON operational_sales(sale_id);
CREATE INDEX IF NOT EXISTS idx_opsales_nm_id ON operational_sales(nm_id);
CREATE INDEX IF NOT EXISTS idx_opsales_sale_date ON operational_sales(sale_date);
CREATE INDEX IF NOT EXISTS idx_opsales_g_number ON operational_sales(g_number);
CREATE INDEX IF NOT EXISTS idx_opsales_supplier_article ON operational_sales(supplier_article);
CREATE INDEX IF NOT EXISTS idx_opsales_last_change_date ON operational_sales(last_change_date);
`
)

// initOpsalesSchema creates operational_sales table in the PostgreSQL database.
func initOpsalesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, opsalesSchemaSQL)
	if err != nil {
		return fmt.Errorf("opsales schema: %w", err)
	}
	return nil
}
