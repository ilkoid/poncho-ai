package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// wbscraperSchemaSQL defines the PostgreSQL tables for pkg/wbscraper.
	//
	// Translated from pkg/storage/sqlite/wbscraper_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - INTEGER (ids/prices/kopecks, int64) → BIGINT
	//   - INTEGER (small counts: page, position, feedbacks, …) → INTEGER
	//   - REAL → DOUBLE PRECISION
	//   - created_at TEXT DEFAULT CURRENT_TIMESTAMP → TEXT DEFAULT TO_CHAR(...)
	//
	// Same two-group design: search_queries DIMENSION (upsert by UNIQUE query) +
	// append-only FACT tables keyed by snapshot_ts. query_id is BIGINT, nullable in
	// the competitor_* / vitrine_ads tables (a capture may have no search query) and
	// NOT NULL in search_positions.
	wbscraperSchemaSQL = `
-- ============================================================================
-- WB-SCRAPER (browser extension captures → pkg/wbscraper collector)
-- ============================================================================

-- search_queries: DIMENSION (one row per unique query text; upsert, NOT append-only)
CREATE TABLE IF NOT EXISTS search_queries (
    query_id  BIGSERIAL PRIMARY KEY,
    query     TEXT UNIQUE NOT NULL,
    subject   TEXT NOT NULL DEFAULT '',
    brand     TEXT NOT NULL DEFAULT '',   -- v2 cartesian-axis provenance
    gender    TEXT NOT NULL DEFAULT '',
    season    TEXT NOT NULL DEFAULT '',
    age       TEXT NOT NULL DEFAULT '',
    material  TEXT NOT NULL DEFAULT '',   -- v2 cartesian-axis provenance
    purpose   TEXT NOT NULL DEFAULT '',   -- v2 cartesian-axis provenance
    comment   TEXT NOT NULL DEFAULT '',   -- v2 free-text query suffix
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- search_positions: append-only fact. query_id NOT NULL (position always from a query).
CREATE TABLE IF NOT EXISTS search_positions (
    snapshot_ts   TEXT NOT NULL,
    query_id      BIGINT NOT NULL,
    region_dest   INTEGER,
    page          INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    nm_id         BIGINT NOT NULL DEFAULT 0,
    brand         TEXT NOT NULL DEFAULT '',
    supplier_id   BIGINT,
    panel_promo_id BIGINT,
    price_basic   BIGINT NOT NULL DEFAULT 0,    -- kopecks
    price_product BIGINT NOT NULL DEFAULT 0,    -- kopecks
    rating        DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedbacks     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sp_query_ts        ON search_positions(query_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_sp_nm_ts           ON search_positions(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_sp_query_region_ts ON search_positions(query_id, region_dest, snapshot_ts);

-- vitrine_ads: append-only fact (banner advertisements). query_id nullable.
CREATE TABLE IF NOT EXISTS vitrine_ads (
    snapshot_ts     TEXT NOT NULL,
    query_id        BIGINT,
    advertiser_name TEXT NOT NULL DEFAULT '',
    advertiser_inn  TEXT NOT NULL DEFAULT '',
    erid            TEXT NOT NULL DEFAULT '',
    promo_id        BIGINT,
    banner_type     TEXT NOT NULL DEFAULT '',
    creative_url    TEXT NOT NULL DEFAULT '',
    landing_href    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_va_query_ts ON vitrine_ads(query_id, snapshot_ts);

-- competitor_cards: append-only fact (core card from /list and /detail subset).
CREATE TABLE IF NOT EXISTS competitor_cards (
    snapshot_ts   TEXT NOT NULL,
    query_id      BIGINT,
    nm_id         BIGINT NOT NULL DEFAULT 0,
    brand         TEXT NOT NULL DEFAULT '',
    supplier      TEXT NOT NULL DEFAULT '',
    supplier_id   BIGINT,
    rating        DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedbacks     INTEGER NOT NULL DEFAULT 0,
    pics          INTEGER NOT NULL DEFAULT 0,
    weight        DOUBLE PRECISION NOT NULL DEFAULT 0,
    volume        DOUBLE PRECISION NOT NULL DEFAULT 0,
    colors        TEXT NOT NULL DEFAULT '',
    subject_id    BIGINT,
    panel_promo_id BIGINT
);
CREATE INDEX IF NOT EXISTS idx_cc_nm_ts    ON competitor_cards(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_cc_query_ts ON competitor_cards(query_id, snapshot_ts);

-- competitor_card_prices: append-only fact (per-size price rows from /list).
CREATE TABLE IF NOT EXISTS competitor_card_prices (
    snapshot_ts   TEXT NOT NULL,
    query_id      BIGINT,
    nm_id         BIGINT NOT NULL DEFAULT 0,
    size_name     TEXT NOT NULL DEFAULT '',
    price_basic   BIGINT NOT NULL DEFAULT 0,    -- kopecks
    price_product BIGINT NOT NULL DEFAULT 0,    -- kopecks
    wh_id         BIGINT,
    delivery_days INTEGER
);
CREATE INDEX IF NOT EXISTS idx_ccp_nm_ts ON competitor_card_prices(nm_id, snapshot_ts);

-- competitor_card_details: append-only fact (/detail-exclusive aggregates).
-- promotions is a JSON text blob (variable-shape array).
CREATE TABLE IF NOT EXISTS competitor_card_details (
    snapshot_ts    TEXT NOT NULL,
    query_id       BIGINT,
    nm_id          BIGINT NOT NULL DEFAULT 0,
    total_quantity INTEGER NOT NULL DEFAULT 0,
    promotions     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ccd_nm_ts ON competitor_card_details(nm_id, snapshot_ts);

-- competitor_card_stocks: append-only fact (per-warehouse stock from /detail).
CREATE TABLE IF NOT EXISTS competitor_card_stocks (
    snapshot_ts TEXT NOT NULL,
    query_id    BIGINT,
    nm_id       BIGINT NOT NULL DEFAULT 0,
    size_name   TEXT NOT NULL DEFAULT '',
    wh_id       BIGINT,
    qty         INTEGER NOT NULL DEFAULT 0,
    time1       INTEGER,
    time2       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_ccs_nm_ts ON competitor_card_stocks(nm_id, snapshot_ts);

-- ============================================================================
-- card.json CONTENT tables (Этап A/B — v2-only; v1 /capture never populates these)
-- ============================================================================

-- competitor_card_meta: per-nm scalar content of one card.json (1 row per nm per
-- snapshot). Captures EVERY known scalar: title (imt_name), brand (selling.*), media,
-- subject ids (data.*), colors name, contents, kinds. description = markdown form.
-- Nullable numerics (imt_id/supplier_id/subject_id/subject_root_id) → BIGINT NULL.
CREATE TABLE IF NOT EXISTS competitor_card_meta (
    snapshot_ts        TEXT NOT NULL,
    query_id           BIGINT,
    nm_id              BIGINT NOT NULL DEFAULT 0,
    vendor_code        TEXT NOT NULL DEFAULT '',
    subj_name          TEXT NOT NULL DEFAULT '',
    subj_root_name     TEXT NOT NULL DEFAULT '',
    description        TEXT NOT NULL DEFAULT '',
    need_kiz           INTEGER NOT NULL DEFAULT 0,
    create_date        TEXT NOT NULL DEFAULT '',
    update_date        TEXT NOT NULL DEFAULT '',
    imt_id             BIGINT,
    imt_name           TEXT NOT NULL DEFAULT '',
    slug               TEXT NOT NULL DEFAULT '',
    brand_name         TEXT NOT NULL DEFAULT '',
    brand_hash         TEXT NOT NULL DEFAULT '',
    supplier_id        BIGINT,
    photo_count        INTEGER NOT NULL DEFAULT 0,
    has_video          INTEGER NOT NULL DEFAULT 0,
    subject_id         BIGINT,
    subject_root_id    BIGINT,
    nm_colors_names    TEXT NOT NULL DEFAULT '',
    contents           TEXT NOT NULL DEFAULT '',
    has_seller_recommendations INTEGER NOT NULL DEFAULT 0,
    user_flags         INTEGER NOT NULL DEFAULT 0,
    kinds              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ccm_nm_ts    ON competitor_card_meta(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_ccm_query_ts ON competitor_card_meta(query_id, snapshot_ts);

-- competitor_card_options: EAV — one product characteristic per row (Состав / Цвет / …).
-- group_name = section from grouped_options[]; variable_values = JSON-text array ('' if none).
CREATE TABLE IF NOT EXISTS competitor_card_options (
    snapshot_ts     TEXT NOT NULL,
    query_id        BIGINT,
    nm_id           BIGINT NOT NULL DEFAULT 0,
    char_name       TEXT NOT NULL DEFAULT '',
    char_value      TEXT NOT NULL DEFAULT '',
    charc_type      INTEGER NOT NULL DEFAULT 0,
    is_variable     INTEGER NOT NULL DEFAULT 0,
    variable_values TEXT NOT NULL DEFAULT '',
    group_name      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_cco_nm_ts       ON competitor_card_options(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_cco_nm_char_ts  ON competitor_card_options(nm_id, char_name, snapshot_ts);

-- competitor_card_compositions: material components from compositions[] (хлопок 60% …).
CREATE TABLE IF NOT EXISTS competitor_card_compositions (
    snapshot_ts TEXT NOT NULL,
    query_id    BIGINT,
    nm_id       BIGINT NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    ord         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cccp_nm_ts ON competitor_card_compositions(nm_id, snapshot_ts);

-- competitor_card_sizes: one cell of the size grid (a single measurement for one
-- tech_size). Built by zipping details_props[i] × details[i]; empty cells skipped.
CREATE TABLE IF NOT EXISTS competitor_card_sizes (
    snapshot_ts TEXT NOT NULL,
    query_id    BIGINT,
    nm_id       BIGINT NOT NULL DEFAULT 0,
    tech_size   TEXT NOT NULL DEFAULT '',
    chrt_id     BIGINT,
    prop_name   TEXT NOT NULL DEFAULT '',
    prop_value  TEXT NOT NULL DEFAULT '',
    prop_order  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_ccsz_nm_ts      ON competitor_card_sizes(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_ccsz_nm_size_ts ON competitor_card_sizes(nm_id, tech_size, snapshot_ts);

-- competitor_card_colors: color-variant nm_ids from colors[]/full_colors[].
CREATE TABLE IF NOT EXISTS competitor_card_colors (
    snapshot_ts  TEXT NOT NULL,
    query_id     BIGINT,
    nm_id        BIGINT NOT NULL DEFAULT 0,
    color_nm_id  BIGINT NOT NULL DEFAULT 0,
    ord          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cccl_nm_ts ON competitor_card_colors(nm_id, snapshot_ts);
`
)

// wbscraperMigrations extends existing search_queries rows with the v2 cartesian-axis
// columns (a fresh DB gets them via CREATE TABLE above; an existing DB does not —
// CREATE TABLE IF NOT EXISTS leaves it untouched). Idempotent at startup
// (ADD COLUMN IF NOT EXISTS), mirroring cardsMigrations (cards_schema.go).
const wbscraperMigrations = `
ALTER TABLE search_queries ADD COLUMN IF NOT EXISTS brand    TEXT NOT NULL DEFAULT '';
ALTER TABLE search_queries ADD COLUMN IF NOT EXISTS material TEXT NOT NULL DEFAULT '';
ALTER TABLE search_queries ADD COLUMN IF NOT EXISTS purpose  TEXT NOT NULL DEFAULT '';
ALTER TABLE search_queries ADD COLUMN IF NOT EXISTS comment  TEXT NOT NULL DEFAULT '';
`

// initWbscraperSchema creates the wb-scraper tables in PostgreSQL, then runs the
// idempotent v2 migrations (search_queries cartesian-axis columns for existing DBs).
func initWbscraperSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, wbscraperSchemaSQL); err != nil {
		return fmt.Errorf("wbscraper schema: %w", err)
	}
	if _, err := pool.Exec(ctx, wbscraperMigrations); err != nil {
		return fmt.Errorf("wbscraper migrations: %w", err)
	}
	return nil
}
