package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// onecSchemaSQL defines PostgreSQL tables for 1C/PIM product data.
	//
	// Translated from pkg/storage/sqlite/onec_schema.go:
	//   - INTEGER (boolean) → BOOLEAN NOT NULL DEFAULT FALSE
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	//   - INSERT OR REPLACE → ON CONFLICT (...) DO UPDATE SET ... = EXCLUDED ...
	onecSchemaSQL = `
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
    type                TEXT    DEFAULT '',
    category            TEXT    DEFAULT '',
    category_level1     TEXT    DEFAULT '',
    category_level2     TEXT    DEFAULT '',
    sex                 TEXT    DEFAULT '',
    season              TEXT    DEFAULT '',
    composition         TEXT    DEFAULT '',
    composition_lining  TEXT    DEFAULT '',
    color               TEXT    DEFAULT '',
    collection          TEXT    DEFAULT '',
    country_of_origin   TEXT    DEFAULT '',
    weight              DOUBLE PRECISION DEFAULT 0,
    size_range          TEXT    DEFAULT '',
    tnved_codes         TEXT    DEFAULT '',
    business_line       TEXT    DEFAULT '',
    is_sale             BOOLEAN NOT NULL DEFAULT FALSE,
    is_new              BOOLEAN NOT NULL DEFAULT FALSE,
    model_status        TEXT    DEFAULT '',
    date                TEXT    DEFAULT '',
    downloaded_at       TEXT    DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    -- Dimensions & Weight (cm / grams)
    length              DOUBLE PRECISION DEFAULT 0,
    wideness            DOUBLE PRECISION DEFAULT 0,
    height              DOUBLE PRECISION DEFAULT 0,
    weight_sku_g        DOUBLE PRECISION DEFAULT 0,
    -- Certificate
    certificate         TEXT    DEFAULT '',
    certificate_type    TEXT    DEFAULT '',
    has_certificate     BOOLEAN NOT NULL DEFAULT FALSE,
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
    quantity_bar_code   BIGINT DEFAULT 0,
    -- Boolean flags
    is_adult            BOOLEAN NOT NULL DEFAULT FALSE,
    is_article_blocked  BOOLEAN NOT NULL DEFAULT FALSE,
    is_exclude_from_site BOOLEAN NOT NULL DEFAULT FALSE,
    is_exclusive        BOOLEAN NOT NULL DEFAULT FALSE,
    is_genuine_leather  BOOLEAN NOT NULL DEFAULT FALSE,
    is_model_cancelled  BOOLEAN NOT NULL DEFAULT FALSE,
    is_new_collection   BOOLEAN NOT NULL DEFAULT FALSE,
    is_not_require_ironing BOOLEAN NOT NULL DEFAULT FALSE,
    is_pps              BOOLEAN NOT NULL DEFAULT FALSE,
    is_ya_price_list_opt BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_onec_goods_article ON onec_goods(article);
CREATE INDEX IF NOT EXISTS idx_onec_goods_brand ON onec_goods(brand);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category ON onec_goods(category);
CREATE INDEX IF NOT EXISTS idx_onec_goods_downloaded_at ON onec_goods(downloaded_at);
CREATE INDEX IF NOT EXISTS idx_onec_goods_type ON onec_goods(type);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category_level1 ON onec_goods(category_level1);
CREATE INDEX IF NOT EXISTS idx_onec_goods_category_level2 ON onec_goods(category_level2);
CREATE INDEX IF NOT EXISTS idx_onec_goods_active ON onec_goods(is_article_blocked) WHERE is_article_blocked;

-- ============================================================================
-- 1C GOODS SKU (barcode + size variants)
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_goods_sku (
    sku_guid        TEXT    NOT NULL,
    guid            TEXT    NOT NULL DEFAULT '',
    barcode         TEXT    DEFAULT '',
    size            TEXT    DEFAULT '',
    nds             INTEGER DEFAULT 0,
    length          DOUBLE PRECISION DEFAULT 0,
    wideness        DOUBLE PRECISION DEFAULT 0,
    height          DOUBLE PRECISION DEFAULT 0,
    weight_sku_g    DOUBLE PRECISION DEFAULT 0,
    PRIMARY KEY (sku_guid, guid),
    FOREIGN KEY (guid) REFERENCES onec_goods(guid) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_onec_goods_sku_guid ON onec_goods_sku(guid);
CREATE INDEX IF NOT EXISTS idx_onec_goods_sku_barcode ON onec_goods_sku(barcode);

-- ============================================================================
-- 1C PRICES (price types per product, snapshot-based)
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_prices (
    good_guid       TEXT    NOT NULL,
    snapshot_date   TEXT    NOT NULL,
    type_guid       TEXT    NOT NULL DEFAULT '',
    type_name       TEXT    NOT NULL DEFAULT '',
    price           DOUBLE PRECISION DEFAULT 0,
    spec_price      DOUBLE PRECISION DEFAULT 0,
    PRIMARY KEY (good_guid, snapshot_date, type_guid)
);

CREATE INDEX IF NOT EXISTS idx_onec_prices_snapshot ON onec_prices(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_onec_prices_type_name ON onec_prices(type_name);
CREATE INDEX IF NOT EXISTS idx_onec_prices_good_guid ON onec_prices(good_guid);

-- ============================================================================
-- PIM GOODS (validated product attributes from PIM system)
-- ============================================================================
CREATE TABLE IF NOT EXISTS pim_goods (
    identifier          TEXT PRIMARY KEY,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    family              TEXT    DEFAULT '',
    categories          TEXT    DEFAULT '',
    product_type        TEXT    DEFAULT '',
    sex                 TEXT    DEFAULT '',
    season              TEXT    DEFAULT '',
    color               TEXT    DEFAULT '',
    filter_color        TEXT    DEFAULT '',
    wb_nm_id            BIGINT DEFAULT 0,
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
    wildberries_length  DOUBLE PRECISION DEFAULT 0,
    wildberries_width   DOUBLE PRECISION DEFAULT 0,
    wildberries_height  DOUBLE PRECISION DEFAULT 0,
    downloaded_at       TEXT    DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE INDEX IF NOT EXISTS idx_pim_goods_wb_nm_id ON pim_goods(wb_nm_id);
CREATE INDEX IF NOT EXISTS idx_pim_goods_product_type ON pim_goods(product_type);
CREATE INDEX IF NOT EXISTS idx_pim_goods_family ON pim_goods(family);
CREATE INDEX IF NOT EXISTS idx_pim_goods_downloaded_at ON pim_goods(downloaded_at);

-- ============================================================================
-- 1C DIMENSIONS (per-SKU weight-dimension data)
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_dimensions (
    good_guid       TEXT NOT NULL,
    sku_guid        TEXT NOT NULL,
    good_name       TEXT NOT NULL DEFAULT '',
    size_name       TEXT NOT NULL DEFAULT '',
    length_dm       DOUBLE PRECISION NOT NULL DEFAULT 0,
    width_dm        DOUBLE PRECISION NOT NULL DEFAULT 0,
    height_dm       DOUBLE PRECISION NOT NULL DEFAULT 0,
    weight_kg       DOUBLE PRECISION NOT NULL DEFAULT 0,
    volume_cm3      DOUBLE PRECISION NOT NULL DEFAULT 0,
    source          TEXT NOT NULL DEFAULT 'xls',
    created_at      TEXT NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (good_guid, sku_guid)
);

CREATE INDEX IF NOT EXISTS idx_onec_dimensions_good_guid ON onec_dimensions(good_guid);
`
)

// onecMigrations widens INTEGER columns to BIGINT for ID and quantity fields.
// Safe: INTEGER→BIGINT is a widening conversion — no data loss.
// nds (VAT 0-20) and year_collection (year) remain INTEGER (bounded values).
const onecMigrations = `
ALTER TABLE onec_goods ALTER COLUMN quantity_bar_code TYPE BIGINT;
ALTER TABLE pim_goods ALTER COLUMN wb_nm_id TYPE BIGINT;
`

// initOneCSchema creates 1C/PIM tables in the PostgreSQL database.
func initOneCSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, onecSchemaSQL)
	if err != nil {
		return fmt.Errorf("onec schema: %w", err)
	}
	if _, err := pool.Exec(ctx, onecMigrations); err != nil {
		return fmt.Errorf("onec migrations (int4→bigint): %w", err)
	}
	return nil
}
