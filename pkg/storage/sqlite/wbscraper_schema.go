package sqlite

const (
	// WbscraperSchemaSQL defines the tables populated by the wb-scraper browser
	// extension via the Go collector (pkg/wbscraper). Data source is external — the
	// extension captures WB storefront API responses; the collector never calls WB.
	//
	// Two table groups:
	//   - search_queries: a DIMENSION. One row per unique query text with a stable
	//     autoincrement id (UNIQUE(query)). Upserted (NOT append-only): re-running a
	//     session reuses the same ids, so cross-session analysis joins cleanly.
	//   - *_positions / *_ads / competitor_* : FACT tables, append-only by snapshot_ts
	//     (pattern: stock_history). Fact rows store query_id (FK by convention to
	//     search_queries.query_id), NEVER the query text — text lives only in the
	//     dimension, so "data by season=зима" is a column filter, not a LIKE.
	//
	// query_id is NULLABLE in vitrine_ads / competitor_* (a capture may come from a
	// direct nmId/url target with no search query → wbscraper.NoQuery → NULL), and
	// NOT NULL in search_positions (a position always comes from a query).
	//
	// Prices are INTEGER kopecks (WB search/list responses are already kopecks).
	WbscraperSchemaSQL = `
-- ============================================================================
-- WB-SCRAPER (browser extension captures → pkg/wbscraper collector)
-- ============================================================================

-- search_queries: DIMENSION (one row per unique query text; upsert, NOT append-only)
CREATE TABLE IF NOT EXISTS search_queries (
    query_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    query     TEXT UNIQUE NOT NULL,
    subject   TEXT NOT NULL DEFAULT '',
    gender    TEXT NOT NULL DEFAULT '',
    season    TEXT NOT NULL DEFAULT '',
    age       TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- search_positions: append-only fact. query_id NOT NULL (position always from a query).
CREATE TABLE IF NOT EXISTS search_positions (
    snapshot_ts   TEXT NOT NULL,
    query_id      INTEGER NOT NULL,
    region_dest   INTEGER,
    page          INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    nm_id         INTEGER NOT NULL DEFAULT 0,
    brand         TEXT NOT NULL DEFAULT '',
    supplier_id   INTEGER,
    panel_promo_id INTEGER,
    price_basic   INTEGER NOT NULL DEFAULT 0,   -- kopecks
    price_product INTEGER NOT NULL DEFAULT 0,   -- kopecks
    rating        REAL NOT NULL DEFAULT 0,
    feedbacks     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sp_query_ts    ON search_positions(query_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_sp_nm_ts       ON search_positions(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_sp_query_region_ts ON search_positions(query_id, region_dest, snapshot_ts);

-- vitrine_ads: append-only fact (banner advertisements). query_id nullable.
CREATE TABLE IF NOT EXISTS vitrine_ads (
    snapshot_ts     TEXT NOT NULL,
    query_id        INTEGER,
    advertiser_name TEXT NOT NULL DEFAULT '',
    advertiser_inn  TEXT NOT NULL DEFAULT '',
    erid            TEXT NOT NULL DEFAULT '',
    promo_id        INTEGER,
    banner_type     TEXT NOT NULL DEFAULT '',
    creative_url    TEXT NOT NULL DEFAULT '',
    landing_href    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_va_query_ts ON vitrine_ads(query_id, snapshot_ts);

-- competitor_cards: append-only fact (core card from /list and /detail subset).
CREATE TABLE IF NOT EXISTS competitor_cards (
    snapshot_ts   TEXT NOT NULL,
    query_id      INTEGER,
    nm_id         INTEGER NOT NULL DEFAULT 0,
    brand         TEXT NOT NULL DEFAULT '',
    supplier      TEXT NOT NULL DEFAULT '',
    supplier_id   INTEGER,
    rating        REAL NOT NULL DEFAULT 0,
    feedbacks     INTEGER NOT NULL DEFAULT 0,
    pics          INTEGER NOT NULL DEFAULT 0,
    weight        INTEGER NOT NULL DEFAULT 0,
    volume        INTEGER NOT NULL DEFAULT 0,
    colors        TEXT NOT NULL DEFAULT '',
    subject_id    INTEGER,
    panel_promo_id INTEGER
);
CREATE INDEX IF NOT EXISTS idx_cc_nm_ts    ON competitor_cards(nm_id, snapshot_ts);
CREATE INDEX IF NOT EXISTS idx_cc_query_ts ON competitor_cards(query_id, snapshot_ts);

-- competitor_card_prices: append-only fact (per-size price rows from /list).
CREATE TABLE IF NOT EXISTS competitor_card_prices (
    snapshot_ts   TEXT NOT NULL,
    query_id      INTEGER,
    nm_id         INTEGER NOT NULL DEFAULT 0,
    size_name     TEXT NOT NULL DEFAULT '',
    price_basic   INTEGER NOT NULL DEFAULT 0,   -- kopecks
    price_product INTEGER NOT NULL DEFAULT 0,   -- kopecks
    wh_id         INTEGER,
    delivery_days INTEGER
);
CREATE INDEX IF NOT EXISTS idx_ccp_nm_ts ON competitor_card_prices(nm_id, snapshot_ts);

-- competitor_card_details: append-only fact (/detail-exclusive aggregates).
-- promotions is a JSON text blob (variable-shape array).
CREATE TABLE IF NOT EXISTS competitor_card_details (
    snapshot_ts    TEXT NOT NULL,
    query_id       INTEGER,
    nm_id          INTEGER NOT NULL DEFAULT 0,
    total_quantity INTEGER NOT NULL DEFAULT 0,
    promotions     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ccd_nm_ts ON competitor_card_details(nm_id, snapshot_ts);

-- competitor_card_stocks: append-only fact (per-warehouse stock from /detail).
CREATE TABLE IF NOT EXISTS competitor_card_stocks (
    snapshot_ts TEXT NOT NULL,
    query_id    INTEGER,
    nm_id       INTEGER NOT NULL DEFAULT 0,
    size_name   TEXT NOT NULL DEFAULT '',
    wh_id       INTEGER,
    qty         INTEGER NOT NULL DEFAULT 0,
    time1       INTEGER,
    time2       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_ccs_nm_ts ON competitor_card_stocks(nm_id, snapshot_ts);
`
)

// GetWbscraperSchemaSQL returns the wb-scraper tables schema.
func GetWbscraperSchemaSQL() string { return WbscraperSchemaSQL }
