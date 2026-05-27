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
    downloaded_at       TEXT    DEFAULT CURRENT_TIMESTAMP,
    -- Dimensions & Weight (cm / grams)
    length              REAL    DEFAULT 0,
    wideness            REAL    DEFAULT 0,
    height              REAL    DEFAULT 0,
    weight_sku_g        REAL    DEFAULT 0,       -- grams! _g suffix — divide by 1000 for WB kg
    -- Certificate
    certificate         TEXT    DEFAULT '',
    certificate_type    TEXT    DEFAULT '',
    has_certificate     INTEGER DEFAULT 0,
    certificate_begin   TEXT    DEFAULT '',
    certificate_end     TEXT    DEFAULT '',
    certificate_number  TEXT    DEFAULT '',
    -- Dates
    approval_date       TEXT    DEFAULT '',
    date_of_production  TEXT    DEFAULT '',
    date_of_receipt     TEXT    DEFAULT '',
    pps_date            TEXT    DEFAULT '',
    -- Seasons & Collections
    collection_season   TEXT    DEFAULT '',
    collection_year     TEXT    DEFAULT '',
    look_season         TEXT    DEFAULT '',
    opt_collection_season TEXT  DEFAULT '',
    opt_collection_year TEXT    DEFAULT '',
    production_season   TEXT    DEFAULT '',
    production_year     TEXT    DEFAULT '',
    -- Categories
    category_level1_name TEXT   DEFAULT '',
    category_level2_name TEXT   DEFAULT '',
    -- Product attributes
    age                 TEXT    DEFAULT '',
    figure_features     TEXT    DEFAULT '',
    licensor            TEXT    DEFAULT '',
    main_capture        TEXT    DEFAULT '',
    markirovka          TEXT    DEFAULT '',
    model_height        TEXT    DEFAULT '',
    ratio_heat          TEXT    DEFAULT '',
    recommendations     TEXT    DEFAULT '',
    size_on_model       TEXT    DEFAULT '',
    tag                 TEXT    DEFAULT '',
    quantity_bar_code   INTEGER DEFAULT 0,
    -- Boolean flags
    is_adult            INTEGER DEFAULT 0,
    is_article_blocked  INTEGER DEFAULT 0,
    is_exclude_from_site INTEGER DEFAULT 0,
    is_exclusive        INTEGER DEFAULT 0,
    is_genuine_leather  INTEGER DEFAULT 0,
    is_model_cancelled  INTEGER DEFAULT 0,
    is_new_collection   INTEGER DEFAULT 0,
    is_not_require_ironing INTEGER DEFAULT 0,
    is_pps              INTEGER DEFAULT 0,
    is_ya_price_list_opt INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_onec_goods_article ON onec_goods(article);
CREATE INDEX IF NOT EXISTS idx_onec_goods_brand ON onec_goods(brand);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category ON onec_goods(category);

-- Index for freshness checks
CREATE INDEX IF NOT EXISTS idx_onec_goods_downloaded_at ON onec_goods(downloaded_at);

-- Indexes for pkg/filter.BuildSQL() and category analytics
CREATE INDEX IF NOT EXISTS idx_onec_goods_type ON onec_goods(type);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category_level1 ON onec_goods(category_level1);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category_level2 ON onec_goods(category_level2);
CREATE INDEX IF NOT EXISTS idx_onec_goods_active ON onec_goods(is_article_blocked) WHERE is_article_blocked = 1;

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
    -- Per-SKU dimensions (cm / grams)
    length      REAL    DEFAULT 0,
    wideness    REAL    DEFAULT 0,
    height      REAL    DEFAULT 0,
    weight_sku_g REAL   DEFAULT 0,
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
-- 27 dedicated columns for high-value attributes
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
    wildberries_length  REAL    DEFAULT 0,            -- cm, from PIM attribute
    wildberries_width   REAL    DEFAULT 0,            -- cm, from PIM attribute
    wildberries_height  REAL    DEFAULT 0,            -- cm, from PIM attribute
    downloaded_at       TEXT    DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pim_goods_wb_nm_id ON pim_goods(wb_nm_id);
CREATE INDEX IF NOT EXISTS idx_pim_goods_product_type ON pim_goods(product_type);
CREATE INDEX IF NOT EXISTS idx_pim_goods_family ON pim_goods(family);

-- Index for freshness checks
CREATE INDEX IF NOT EXISTS idx_pim_goods_downloaded_at ON pim_goods(downloaded_at);

-- ============================================================================
-- 1C RESTS (warehouse stock levels / остатки)
-- Grain: one row per (good_guid, sku_guid, storage_guid, snapshot_date)
-- JOIN chain: onec_rests.good_guid → onec_goods.guid → onec_goods.article
--
--	→ cards.vendor_code → cards.nm_id
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_rests (
    good_guid       TEXT    NOT NULL,
    sku_guid        TEXT    NOT NULL DEFAULT '',
    storage_guid    TEXT    NOT NULL,
    snapshot_date   TEXT    NOT NULL,
    storage_name    TEXT    DEFAULT '',
    stock           INTEGER DEFAULT 0,
    reserv          INTEGER DEFAULT 0,
    free            INTEGER DEFAULT 0,
    first_stage     INTEGER DEFAULT 0,
    downloaded_at   TEXT    DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (good_guid, sku_guid, storage_guid, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_onec_rests_snapshot ON onec_rests(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_onec_rests_good_guid ON onec_rests(good_guid);
CREATE INDEX IF NOT EXISTS idx_onec_rests_storage_guid ON onec_rests(storage_guid);

-- ============================================================================
-- 1C DIMENSIONS (weight-dimension data per SKU from 1C WMS)
-- Grain: one row per (good_guid, sku_guid) — per-SKU measurements.
-- WB aggregates to card level (MAX), but raw per-SKU data kept for Ozon/Yandex.
-- JOIN chain: onec_dimensions.good_guid → onec_goods.guid → onec_goods.article
--
--	→ cards.vendor_code → cards.nm_id
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_dimensions (
    good_guid    TEXT NOT NULL,
    sku_guid     TEXT NOT NULL,
    good_name    TEXT NOT NULL DEFAULT '',
    size_name    TEXT NOT NULL DEFAULT '',
    length_dm    REAL NOT NULL DEFAULT 0,
    width_dm     REAL NOT NULL DEFAULT 0,
    height_dm    REAL NOT NULL DEFAULT 0,
    weight_kg    REAL NOT NULL DEFAULT 0,
    volume_cm3   REAL NOT NULL DEFAULT 0,
    source       TEXT NOT NULL DEFAULT 'xls',
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (good_guid, sku_guid)
);

CREATE INDEX IF NOT EXISTS idx_onec_dimensions_good_guid ON onec_dimensions(good_guid);
`

// GetOneCSchemaSQL returns the SQL for 1C/PIM tables creation.
func GetOneCSchemaSQL() string {
	return OneCSchemaSQL
}
