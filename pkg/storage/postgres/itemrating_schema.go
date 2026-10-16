package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// itemRatingsSchemaSQL defines the item_ratings table (WB «Оценка товара»).
	//
	// Source: POST /api/analytics/v2/item-rating (Seller Analytics API).
	// RAW layer: every spec field gets a column; raw_item keeps the exact JSON
	// of each item as returned by WB (fields added between spec refreshs survive).
	//
	// Grain: UNIQUE(nm_id, period_start, period_end) — one row per item per window.
	itemRatingsSchemaSQL = `
CREATE TABLE IF NOT EXISTS item_ratings (
    id BIGSERIAL PRIMARY KEY,

    -- Period window (request echo; items do not repeat it in the response)
    period_start TEXT NOT NULL DEFAULT '',
    period_end   TEXT NOT NULL DEFAULT '',

    -- Identification
    nm_id        BIGINT NOT NULL,
    vendor_code  TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    subject_id   BIGINT NOT NULL DEFAULT 0,
    subject_name TEXT NOT NULL DEFAULT '',
    brand_name   TEXT NOT NULL DEFAULT '',
    tag_id       BIGINT NOT NULL DEFAULT 0,
    tag_name     TEXT NOT NULL DEFAULT '',

    -- Flags & scores
    pinned_feedback BOOLEAN NOT NULL DEFAULT FALSE,
    card_rating     DOUBLE PRECISION NOT NULL DEFAULT 0, -- WB ` + "`rating`" + `: «Рейтинг карточки товара» (не звёзды)

    -- Star rating: current + dynamics vs past period + percentile in subject
    feedback_rating_current    DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedback_rating_dynamics   DOUBLE PRECISION NOT NULL DEFAULT 0,
    feedback_rating_percentile DOUBLE PRECISION, -- nullable per spec

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
    disqualified INTEGER NOT NULL DEFAULT 0, -- отзывы, исключённые WB из рейтинга
    is_shadowed  BOOLEAN NOT NULL DEFAULT FALSE, -- товар скрыт из каталога

    -- Exact JSON of the item as returned by WB (drift protection)
    raw_item JSONB NOT NULL DEFAULT '{}',

    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(nm_id, period_start, period_end)
);

CREATE INDEX IF NOT EXISTS idx_item_ratings_period ON item_ratings(period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_item_ratings_nm ON item_ratings(nm_id);
`

	// itemRatingShadowedIndexSQL — partial index, отдельным Exec
	// (pgx не парсит WHERE внутри multi-statement блока — гатч AGENTS.md).
	itemRatingShadowedIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_item_ratings_shadowed
    ON item_ratings(period_start) WHERE is_shadowed;
`

	// itemRatingSellerSchemaSQL defines the account-level block table.
	// Grain: UNIQUE(period_start, period_end).
	itemRatingSellerSchemaSQL = `
CREATE TABLE IF NOT EXISTS item_rating_seller (
    id BIGSERIAL PRIMARY KEY,

    period_start TEXT NOT NULL DEFAULT '',
    period_end   TEXT NOT NULL DEFAULT '',

    -- sellerRating block
    seller_rating_current  DOUBLE PRECISION NOT NULL DEFAULT 0,
    seller_rating_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,

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
    raw_seller     JSONB NOT NULL DEFAULT '{}',
    raw_fb_increase JSONB NOT NULL DEFAULT '{}',

    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(period_start, period_end)
);
`
)

// initItemRatingSchema creates item_ratings and item_rating_seller tables in PostgreSQL.
func initItemRatingSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, itemRatingsSchemaSQL); err != nil {
		return fmt.Errorf("item_ratings schema: %w", err)
	}
	if _, err := pool.Exec(ctx, itemRatingShadowedIndexSQL); err != nil {
		return fmt.Errorf("item_ratings shadowed index: %w", err)
	}
	if _, err := pool.Exec(ctx, itemRatingSellerSchemaSQL); err != nil {
		return fmt.Errorf("item_rating_seller schema: %w", err)
	}
	return nil
}
