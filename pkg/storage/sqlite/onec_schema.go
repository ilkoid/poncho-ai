package sqlite

// OneCSchemaSQL creates tables for 1C/PIM product data.
//
// Four tables store raw data from three APIs:
//   - onec_goods:     product dictionary from /feeds/ones/goods/ (26,968 rows)
//   - onec_goods_sku: SKU variants (barcode, size) per good (~140K rows)
//   - onec_prices:    25 price types per product from /feeds/ones/prices/ (~660K rows)
//   - pim_goods:      validated PIM attributes from /feeds/pim/goods/ (25,737 rows)
//
// JOIN chain: onec_prices.good_guid → onec_goods.guid → onec_goods.article
//
//	→ cards.vendor_code → cards.nm_id
const OneCSchemaSQL = `
-- ============================================================================
-- 1C GOODS (product dictionary from accounting system)
-- Grain: one row per guid
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_goods (
    guid                TEXT PRIMARY KEY,
    article             TEXT    NOT NULL DEFAULT '',
    name                TEXT    DEFAULT '',
    name_im             TEXT    DEFAULT '',
    description         TEXT    DEFAULT '',
    brand               TEXT    DEFAULT '',
    type                TEXT    DEFAULT '',       -- Обувь, Одежда, Аксессуары
    category            TEXT    DEFAULT '',       -- Сандалии, Футболки...
    category_level1     TEXT    DEFAULT '',
    category_level2     TEXT    DEFAULT '',
    sex                 TEXT    DEFAULT '',
    season              TEXT    DEFAULT '',
    composition         TEXT    DEFAULT '',
    composition_lining  TEXT    DEFAULT '',
    color               TEXT    DEFAULT '',
    collection          TEXT    DEFAULT '',
    country_of_origin   TEXT    DEFAULT '',
    weight              REAL    DEFAULT 0,
    size_range          TEXT    DEFAULT '',
    tnved_codes         TEXT    DEFAULT '',       -- JSON array: ["6402993900"]
    business_line       TEXT    DEFAULT '',       -- JSON array
    is_sale             INTEGER DEFAULT 0,
    is_new              INTEGER DEFAULT 0,
    model_status        TEXT    DEFAULT '',
    date                TEXT    DEFAULT '',
    downloaded_at       TEXT    DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_onec_goods_article ON onec_goods(article);
CREATE INDEX IF NOT EXISTS idx_onec_goods_brand ON onec_goods(brand);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category ON onec_goods(category);

-- ============================================================================
-- 1C GOODS SKU (barcode + size variants)
-- Grain: one row per (sku_guid, guid) — sku_guid is NOT globally unique,
-- it represents a size template shared across many products.
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_goods_sku (
    sku_guid    TEXT    NOT NULL,
    guid        TEXT    NOT NULL DEFAULT '',  -- FK → onec_goods.guid
    barcode     TEXT    DEFAULT '',
    size        TEXT    DEFAULT '',
    nds         INTEGER DEFAULT 0,
    PRIMARY KEY (sku_guid, guid),
    FOREIGN KEY (guid) REFERENCES onec_goods(guid)
);

CREATE INDEX IF NOT EXISTS idx_onec_goods_sku_guid ON onec_goods_sku(guid);
CREATE INDEX IF NOT EXISTS idx_onec_goods_sku_barcode ON onec_goods_sku(barcode);

-- ============================================================================
-- 1C PRICES (price types per product, snapshot-based)
-- Grain: one row per (good_guid, snapshot_date, type_guid)
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_prices (
    good_guid       TEXT    NOT NULL,
    snapshot_date   TEXT    NOT NULL,
    type_guid       TEXT    NOT NULL DEFAULT '',
    type_name       TEXT    NOT NULL DEFAULT '',
    price           REAL    DEFAULT 0,
    spec_price      REAL    DEFAULT 0,
    PRIMARY KEY (good_guid, snapshot_date, type_guid)
);

CREATE INDEX IF NOT EXISTS idx_onec_prices_snapshot ON onec_prices(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_onec_prices_type_name ON onec_prices(type_name);
CREATE INDEX IF NOT EXISTS idx_onec_prices_good_guid ON onec_prices(good_guid);

-- ============================================================================
-- PIM GOODS (validated product attributes from PIM system)
-- Grain: one row per identifier (=article)
-- 24 dedicated columns for high-value attributes + values_json for the rest
-- ============================================================================
CREATE TABLE IF NOT EXISTS pim_goods (
    identifier          TEXT PRIMARY KEY,
    enabled             INTEGER DEFAULT 1,
    family              TEXT    DEFAULT '',
    categories          TEXT    DEFAULT '',           -- JSON array of category paths
    product_type        TEXT    DEFAULT '',
    sex                 TEXT    DEFAULT '',
    season              TEXT    DEFAULT '',
    color               TEXT    DEFAULT '',
    filter_color        TEXT    DEFAULT '',
    wb_nm_id            INTEGER DEFAULT 0,           -- wildberries field = nmID
    year_collection     INTEGER DEFAULT 0,
    menu_product_type   TEXT    DEFAULT '',
    menu_age            TEXT    DEFAULT '',
    age_category        TEXT    DEFAULT '',
    composition         TEXT    DEFAULT '',
    naznacenie          TEXT    DEFAULT '',
    minicollection      TEXT    DEFAULT '',
    brand_country       TEXT    DEFAULT '',
    country_manufacture TEXT    DEFAULT '',
    size_table          TEXT    DEFAULT '',
    features_care       TEXT    DEFAULT '',
    description         TEXT    DEFAULT '',
    name                TEXT    DEFAULT '',
    updated             TEXT    DEFAULT '',
    values_json         TEXT    DEFAULT '',           -- Full values dict as JSON blob
    downloaded_at       TEXT    DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pim_goods_wb_nm_id ON pim_goods(wb_nm_id);
CREATE INDEX IF NOT EXISTS idx_pim_goods_product_type ON pim_goods(product_type);
CREATE INDEX IF NOT EXISTS idx_pim_goods_family ON pim_goods(family);
`

// GetOneCSchemaSQL returns the SQL for 1C/PIM tables creation.
func GetOneCSchemaSQL() string {
	return OneCSchemaSQL
}
