package sqlite

const (
	// RegionSalesSchemaSQL defines the region sales table.
	// Stores data from WB Seller Analytics API: GET /api/v1/analytics/region-sale.
	// Grain: one row per (nm_id, region_name, city_name, country_name, date_from, date_to).
	RegionSalesSchemaSQL = `
-- ============================================================================
-- REGION SALES (WB Seller Analytics API — /api/v1/analytics/region-sale)
-- Grain: one row per (nm_id, region_name, city_name, country_name, date_from, date_to)
-- ============================================================================

CREATE TABLE IF NOT EXISTS region_sales (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Product identifiers
    nm_id INTEGER NOT NULL,
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
    sale_invoice_cost_price REAL NOT NULL DEFAULT 0,
    sale_invoice_cost_price_perc REAL NOT NULL DEFAULT 0,
    sale_item_invoice_qty INTEGER NOT NULL DEFAULT 0,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(nm_id, region_name, city_name, country_name, date_from, date_to)
);

-- Product + period lookup
CREATE INDEX IF NOT EXISTS idx_region_sales_nm_period
    ON region_sales(nm_id, date_from, date_to);

-- Region-level aggregation
CREATE INDEX IF NOT EXISTS idx_region_sales_region_date
    ON region_sales(region_name, date_from);

-- Country-level aggregation
CREATE INDEX IF NOT EXISTS idx_region_sales_country_date
    ON region_sales(country_name, date_from);

-- Date range filtering
CREATE INDEX IF NOT EXISTS idx_region_sales_dates
    ON region_sales(date_from, date_to);
`
)

// GetRegionSalesSchemaSQL returns the region sales table schema.
func GetRegionSalesSchemaSQL() string {
	return RegionSalesSchemaSQL
}
