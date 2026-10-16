package sqlite

const (
	// ItemRatingSchemaSQL defines SQLite tables for WB item rating report
	// («Оценка товара», POST /api/analytics/v2/item-rating).
	//
	// RAW layer: every spec field gets a column; raw_item/raw_seller/raw_fb_increase
	// keep the exact JSON as returned by WB (fields added between spec refreshs survive).
	//
	// Grain: item_ratings UNIQUE(nm_id, period_start, period_end);
	//        item_rating_seller UNIQUE(period_start, period_end).
	ItemRatingSchemaSQL = `
-- ============================================================================
-- ITEM RATINGS (WB Seller Analytics API — /api/analytics/v2/item-rating)
-- One row per item per period window.
-- ============================================================================

CREATE TABLE IF NOT EXISTS item_ratings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Period window (request echo; items do not repeat it in the response)
    period_start TEXT NOT NULL DEFAULT '',
    period_end   TEXT NOT NULL DEFAULT '',

    -- Identification
    nm_id        INTEGER NOT NULL,
    vendor_code  TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    subject_id   INTEGER NOT NULL DEFAULT 0,
    subject_name TEXT NOT NULL DEFAULT '',
    brand_name   TEXT NOT NULL DEFAULT '',
    tag_id       INTEGER NOT NULL DEFAULT 0,
    tag_name     TEXT NOT NULL DEFAULT '',

    -- Flags & scores
    pinned_feedback INTEGER NOT NULL DEFAULT 0,   -- bool (SQLite: 0/1)
    card_rating     REAL NOT NULL DEFAULT 0,      -- WB rating: «Рейтинг карточки товара» (не звёзды)

    -- Star rating: current + dynamics vs past period + percentile in subject
    feedback_rating_current    REAL NOT NULL DEFAULT 0,
    feedback_rating_dynamics   REAL NOT NULL DEFAULT 0,
    feedback_rating_percentile REAL, -- nullable per spec

    -- Feedback counts: period increment + dynamics
    feedback_count_inc      INTEGER NOT NULL DEFAULT 0,
    feedback_count_dynamics INTEGER NOT NULL DEFAULT 0,

    -- Per-star period increments (+ dynamics)
    five_star_inc        INTEGER NOT NULL DEFAULT 0,
    five_star_dynamics   INTEGER NOT NULL DEFAULT 0,
    four_star_inc        INTEGER NOT NULL DEFAULT 0,
    four_star_dynamics   INTEGER NOT NULL DEFAULT 0,
    three_star_inc       INTEGER NOT NULL DEFAULT 0,
    three_star_dynamics  INTEGER NOT NULL DEFAULT 0,
    two_star_inc         INTEGER NOT NULL DEFAULT 0,
    two_star_dynamics    INTEGER NOT NULL DEFAULT 0,
    one_star_inc         INTEGER NOT NULL DEFAULT 0,
    one_star_dynamics    INTEGER NOT NULL DEFAULT 0,

    -- WB-side quality signals
    disqualified INTEGER NOT NULL DEFAULT 0,  -- отзывы, исключённые WB из рейтинга
    is_shadowed  INTEGER NOT NULL DEFAULT 0,  -- товар скрыт из каталога (0/1)

    -- Exact JSON of the item as returned by WB (drift protection)
    raw_item TEXT NOT NULL DEFAULT '{}',

    downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(nm_id, period_start, period_end)
);

CREATE INDEX IF NOT EXISTS idx_ir_period ON item_ratings(period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_ir_nm ON item_ratings(nm_id);
CREATE INDEX IF NOT EXISTS idx_ir_shadowed ON item_ratings(period_start) WHERE is_shadowed = 1;

-- ============================================================================
-- ITEM RATING SELLER (account-level blocks of the same response)
-- One row per period window.
-- ============================================================================

CREATE TABLE IF NOT EXISTS item_rating_seller (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    period_start TEXT NOT NULL DEFAULT '',
    period_end   TEXT NOT NULL DEFAULT '',

    -- sellerRating block
    seller_rating_current  REAL NOT NULL DEFAULT 0,
    seller_rating_dynamics REAL NOT NULL DEFAULT 0,

    -- feedbackIncrease block: totals
    fb_increase_current  INTEGER NOT NULL DEFAULT 0,
    fb_increase_total    INTEGER NOT NULL DEFAULT 0,
    fb_increase_dynamics INTEGER NOT NULL DEFAULT 0,

    -- feedbackIncrease block: per-star {inc, dynamics, total}
    five_star_inc        INTEGER NOT NULL DEFAULT 0,
    five_star_dynamics   INTEGER NOT NULL DEFAULT 0,
    five_star_total      INTEGER NOT NULL DEFAULT 0,
    four_star_inc        INTEGER NOT NULL DEFAULT 0,
    four_star_dynamics   INTEGER NOT NULL DEFAULT 0,
    four_star_total      INTEGER NOT NULL DEFAULT 0,
    three_star_inc       INTEGER NOT NULL DEFAULT 0,
    three_star_dynamics  INTEGER NOT NULL DEFAULT 0,
    three_star_total     INTEGER NOT NULL DEFAULT 0,
    two_star_inc         INTEGER NOT NULL DEFAULT 0,
    two_star_dynamics    INTEGER NOT NULL DEFAULT 0,
    two_star_total       INTEGER NOT NULL DEFAULT 0,
    one_star_inc         INTEGER NOT NULL DEFAULT 0,
    one_star_dynamics    INTEGER NOT NULL DEFAULT 0,
    one_star_total       INTEGER NOT NULL DEFAULT 0,

    -- Exact JSON of both blocks as returned by WB
    raw_seller      TEXT NOT NULL DEFAULT '{}',
    raw_fb_increase TEXT NOT NULL DEFAULT '{}',

    downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(period_start, period_end)
);
`
)

// GetItemRatingSchemaSQL returns the item rating tables schema.
func GetItemRatingSchemaSQL() string {
	return ItemRatingSchemaSQL
}
