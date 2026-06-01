package sqlite

const (
	// OpsalesSchemaSQL defines the operational_sales table for WB Statistics API sales.
	//
	// WB API endpoint: GET /api/v1/supplier/sales
	// Pagination by lastChangeDate string, max 80,000 rows per page.
	// Retention: 90 days. Updates every 30 minutes.
	//
	// Schema design: single flat table (no child records).
	// Primary key: sale_id ("S***" = sale, "R***" = return).
	// Table name: operational_sales (avoids collision with `sales` table for financial reports).
	OpsalesSchemaSQL = `
-- ============================================================================
-- OPERATIONAL SALES (WB Statistics API — /api/v1/supplier/sales)
-- Single flat table: 1 row per sale/return event (sale_id is unique)
-- ============================================================================

CREATE TABLE IF NOT EXISTS operational_sales (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

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
    is_supply INTEGER NOT NULL DEFAULT 0,       -- boolean
    is_realization INTEGER NOT NULL DEFAULT 0,   -- boolean

    -- Pricing
    total_price REAL NOT NULL DEFAULT 0,
    discount_percent INTEGER NOT NULL DEFAULT 0,
    spp REAL NOT NULL DEFAULT 0,
    payment_sale_amount INTEGER NOT NULL DEFAULT 0,
    for_pay REAL NOT NULL DEFAULT 0,
    finished_price REAL NOT NULL DEFAULT 0,
    price_with_disc REAL NOT NULL DEFAULT 0,

    -- Grouping
    sticker TEXT NOT NULL DEFAULT '',
    g_number TEXT NOT NULL DEFAULT '',            -- cart ID

    -- WB internal ID
    srid TEXT NOT NULL DEFAULT '',

    -- Download metadata
    downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_opsales_sale_id
    ON operational_sales(sale_id);

CREATE INDEX IF NOT EXISTS idx_opsales_nm_id
    ON operational_sales(nm_id);

CREATE INDEX IF NOT EXISTS idx_opsales_sale_date
    ON operational_sales(sale_date);

CREATE INDEX IF NOT EXISTS idx_opsales_g_number
    ON operational_sales(g_number);

CREATE INDEX IF NOT EXISTS idx_opsales_supplier_article
    ON operational_sales(supplier_article);

CREATE INDEX IF NOT EXISTS idx_opsales_last_change_date
    ON operational_sales(last_change_date);
`
)

// GetOpsalesSchemaSQL returns the operational_sales table schema.
func GetOpsalesSchemaSQL() string {
	return OpsalesSchemaSQL
}
