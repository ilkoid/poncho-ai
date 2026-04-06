package sqlite

// PricesSchemaSQL creates the product_prices table for WB Discounts-Prices API data.
// Grain: one row per (nm_id, snapshot_date) — current prices at point in time.
const PricesSchemaSQL = `
CREATE TABLE IF NOT EXISTS product_prices (
    nm_id                INTEGER NOT NULL,
    snapshot_date        TEXT    NOT NULL,
    price                INTEGER DEFAULT 0,
    discounted_price     REAL    DEFAULT 0,
    club_discounted_price REAL   DEFAULT 0,
    discount             INTEGER DEFAULT 0,
    club_discount        INTEGER DEFAULT 0,
    vendor_code          TEXT    DEFAULT '',
    currency             TEXT    DEFAULT 'RUB',
    created_at           TEXT    DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (nm_id, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_prices_snapshot_date ON product_prices(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_prices_vendor_code ON product_prices(vendor_code);
`

// GetPricesSchemaSQL returns the SQL for product_prices table creation.
func GetPricesSchemaSQL() string {
	return PricesSchemaSQL
}
