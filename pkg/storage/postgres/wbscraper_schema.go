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
    gender    TEXT NOT NULL DEFAULT '',
    season    TEXT NOT NULL DEFAULT '',
    age       TEXT NOT NULL DEFAULT '',
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
`
)

// initWbscraperSchema creates the wb-scraper tables in PostgreSQL.
func initWbscraperSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, wbscraperSchemaSQL); err != nil {
		return fmt.Errorf("wbscraper schema: %w", err)
	}
	return nil
}
