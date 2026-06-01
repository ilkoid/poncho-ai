package sqlite

const (
	// OrdersSchemaSQL defines the orders table for WB Statistics API orders.
	//
	// WB API endpoint: GET /api/v1/supplier/orders
	// Pagination by lastChangeDate string, max 80,000 rows per page.
	// Retention: 90 days.
	//
	// Schema design: single flat table (no child records).
	// Primary key: srid (unique order ID from WB).
	OrdersSchemaSQL = `
-- ============================================================================
-- ORDERS (WB Statistics API — /api/v1/supplier/orders)
-- Single flat table: 1 row per order (srid is unique)
-- ============================================================================

CREATE TABLE IF NOT EXISTS orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

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
    finished_price REAL NOT NULL DEFAULT 0,
    price_with_disc REAL NOT NULL DEFAULT 0,

    -- Cancellation
    is_cancel INTEGER NOT NULL DEFAULT 0,        -- boolean
    cancel_date TEXT NOT NULL DEFAULT '',

    -- Grouping
    sticker TEXT NOT NULL DEFAULT '',
    g_number TEXT NOT NULL DEFAULT '',            -- cart ID

    -- Download metadata
    downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_orders_nm_id
    ON orders(nm_id);

CREATE INDEX IF NOT EXISTS idx_orders_order_date
    ON orders(order_date);

CREATE INDEX IF NOT EXISTS idx_orders_g_number
    ON orders(g_number);

CREATE INDEX IF NOT EXISTS idx_orders_supplier_article
    ON orders(supplier_article);

CREATE INDEX IF NOT EXISTS idx_orders_last_change_date
    ON orders(last_change_date);

CREATE INDEX IF NOT EXISTS idx_orders_is_cancel
    ON orders(is_cancel);
`
)

// GetOrdersSchemaSQL returns the orders table schema.
func GetOrdersSchemaSQL() string {
	return OrdersSchemaSQL
}
