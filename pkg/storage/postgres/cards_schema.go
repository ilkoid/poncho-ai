package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// cardsSchemaSQL defines PostgreSQL tables for WB Content API product cards.
	//
	// Translated from pkg/storage/sqlite/cards_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - INSERT OR REPLACE → ON CONFLICT ... DO UPDATE SET ... = EXCLUDED ...
	//   - REAL → DOUBLE PRECISION
	//   - INTEGER boolean fields → BOOLEAN
	//   - downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP → TEXT DEFAULT TO_CHAR(...)
	cardsSchemaSQL = `
-- ============================================================================
-- CONTENT CARDS (WB Content API — /content/v2/get/cards/list)
-- Multi-table schema: 1 card → N photos, N sizes, N characteristics, N tags
-- ============================================================================

-- Main cards table (1 row per nmID)
CREATE TABLE IF NOT EXISTS cards (
    nm_id BIGINT PRIMARY KEY,

    -- Category identifiers
    imt_id BIGINT NOT NULL DEFAULT 0,
    nm_uuid TEXT NOT NULL DEFAULT '',
    subject_id BIGINT NOT NULL DEFAULT 0,
    subject_name TEXT NOT NULL DEFAULT '',

    -- Product info
    vendor_code TEXT NOT NULL DEFAULT '',
    brand TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',

    -- Flags and media
    need_kiz BOOLEAN NOT NULL DEFAULT FALSE,
    video TEXT NOT NULL DEFAULT '',

    -- Wholesale (flattened from object)
    wholesale_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    wholesale_quantum BIGINT NOT NULL DEFAULT 0,

    -- Dimensions (flattened from object)
    dim_length DOUBLE PRECISION NOT NULL DEFAULT 0,
    dim_width DOUBLE PRECISION NOT NULL DEFAULT 0,
    dim_height DOUBLE PRECISION NOT NULL DEFAULT 0,
    dim_weight_brutto DOUBLE PRECISION NOT NULL DEFAULT 0,
    dim_is_valid BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',

    -- Download metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Photos table (1 row per photo per card)
CREATE TABLE IF NOT EXISTS card_photos (
    id BIGSERIAL PRIMARY KEY,
    nm_id BIGINT NOT NULL,

    big TEXT NOT NULL DEFAULT '',
    c246x328 TEXT NOT NULL DEFAULT '',
    c516x688 TEXT NOT NULL DEFAULT '',
    square TEXT NOT NULL DEFAULT '',
    tm TEXT NOT NULL DEFAULT '',

    UNIQUE(nm_id, big),
    FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Sizes table (1 row per size variant)
CREATE TABLE IF NOT EXISTS card_sizes (
    chrt_id BIGINT PRIMARY KEY,
    nm_id BIGINT NOT NULL,
    tech_size TEXT NOT NULL DEFAULT '',
    wb_size TEXT NOT NULL DEFAULT '',
    skus_json TEXT NOT NULL DEFAULT '[]',
    FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Characteristics table (1 row per characteristic per card)
CREATE TABLE IF NOT EXISTS card_characteristics (
    id BIGSERIAL PRIMARY KEY,
    nm_id BIGINT NOT NULL,
    char_id BIGINT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    json_value TEXT NOT NULL DEFAULT '[]',
    UNIQUE(nm_id, char_id),
    FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Tags table (1 row per tag per card)
CREATE TABLE IF NOT EXISTS card_tags (
    id BIGSERIAL PRIMARY KEY,
    nm_id BIGINT NOT NULL,
    tag_id BIGINT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    UNIQUE(nm_id, tag_id),
    FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_cards_vendor_code ON cards(vendor_code);
CREATE INDEX IF NOT EXISTS idx_cards_brand ON cards(brand);
CREATE INDEX IF NOT EXISTS idx_cards_subject_id ON cards(subject_id);
CREATE INDEX IF NOT EXISTS idx_cards_updated_at ON cards(updated_at);
CREATE INDEX IF NOT EXISTS idx_card_photos_nm_id ON card_photos(nm_id);
CREATE INDEX IF NOT EXISTS idx_card_sizes_nm_id ON card_sizes(nm_id);
CREATE INDEX IF NOT EXISTS idx_card_characteristics_nm_id ON card_characteristics(nm_id);
CREATE INDEX IF NOT EXISTS idx_card_tags_nm_id ON card_tags(nm_id);
CREATE INDEX IF NOT EXISTS idx_cards_subject_name ON cards(subject_name);
CREATE INDEX IF NOT EXISTS idx_card_characteristics_char_id ON card_characteristics(char_id);
`
)

const cardsMigrations = `
ALTER TABLE cards ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE cards ALTER COLUMN imt_id TYPE BIGINT;
ALTER TABLE cards ALTER COLUMN subject_id TYPE BIGINT;
ALTER TABLE cards ALTER COLUMN wholesale_quantum TYPE BIGINT;
ALTER TABLE card_photos ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE card_sizes ALTER COLUMN chrt_id TYPE BIGINT;
ALTER TABLE card_sizes ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE card_characteristics ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE card_characteristics ALTER COLUMN char_id TYPE BIGINT;
ALTER TABLE card_tags ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE card_tags ALTER COLUMN tag_id TYPE BIGINT;
`

// initCardsSchema creates cards tables in the PostgreSQL database.
func initCardsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, cardsSchemaSQL)
	if err != nil {
		return fmt.Errorf("cards schema: %w", err)
	}
	if _, err := pool.Exec(ctx, cardsMigrations); err != nil {
		return fmt.Errorf("cards migrations (int4→bigint): %w", err)
	}
	return nil
}
