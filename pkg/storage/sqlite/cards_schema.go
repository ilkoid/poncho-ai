// Package sqlite provides SQLite storage implementation.
package sqlite

const (
	// CardsSchemaSQL defines tables for WB Content API product cards.
	//
	// WB API endpoint: POST /content/v2/get/cards/list
	// Cursor-based pagination (updatedAt + nmID), max 100 cards per page.
	// All fields from API response stored for analysis.
	//
	// Schema design: multi-table (6 tables) for queryability.
	// Child records use DELETE+INSERT pattern for atomicity.
	CardsSchemaSQL = `
-- ============================================================================
-- CONTENT CARDS (WB Content API — /content/v2/get/cards/list)
-- Multi-table schema: 1 card → N photos, N sizes, N characteristics, N tags
-- ============================================================================

-- Main cards table (1 row per nmID)
CREATE TABLE IF NOT EXISTS cards (
	nm_id INTEGER PRIMARY KEY,

	-- Category identifiers
	imt_id INTEGER NOT NULL DEFAULT 0,
	nm_uuid TEXT NOT NULL DEFAULT '',
	subject_id INTEGER NOT NULL DEFAULT 0,
	subject_name TEXT NOT NULL DEFAULT '',

	-- Product info
	vendor_code TEXT NOT NULL DEFAULT '',
	brand TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',

	-- Flags and media
	need_kiz INTEGER NOT NULL DEFAULT 0,  -- boolean
	video TEXT NOT NULL DEFAULT '',

	-- Wholesale (flattened from object)
	wholesale_enabled INTEGER NOT NULL DEFAULT 0,  -- boolean
	wholesale_quantum INTEGER NOT NULL DEFAULT 0,

	-- Dimensions (flattened from object)
	dim_length REAL NOT NULL DEFAULT 0,
	dim_width REAL NOT NULL DEFAULT 0,
	dim_height REAL NOT NULL DEFAULT 0,
	dim_weight_brutto REAL NOT NULL DEFAULT 0,
	dim_is_valid INTEGER NOT NULL DEFAULT 0,  -- boolean

	-- Timestamps
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',

	-- Download metadata
	downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Photos table (1 row per photo per card)
CREATE TABLE IF NOT EXISTS card_photos (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	nm_id INTEGER NOT NULL,

	-- Photo URLs (all variants from API)
	big TEXT NOT NULL DEFAULT '',
	c246x328 TEXT NOT NULL DEFAULT '',
	c516x688 TEXT NOT NULL DEFAULT '',
	square TEXT NOT NULL DEFAULT '',
	tm TEXT NOT NULL DEFAULT '',

	UNIQUE(nm_id, big),

	FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Sizes table (1 row per size variant)
-- chrt_id is globally unique across all products → PRIMARY KEY
CREATE TABLE IF NOT EXISTS card_sizes (
	chrt_id INTEGER PRIMARY KEY,

	nm_id INTEGER NOT NULL,
	tech_size TEXT NOT NULL DEFAULT '',
	wb_size TEXT NOT NULL DEFAULT '',
	skus_json TEXT NOT NULL DEFAULT '[]',  -- JSON array

	FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Characteristics table (1 row per characteristic per card)
CREATE TABLE IF NOT EXISTS card_characteristics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	nm_id INTEGER NOT NULL,

	char_id INTEGER NOT NULL,  -- characteristic ID from API
	name TEXT NOT NULL DEFAULT '',
	json_value TEXT NOT NULL DEFAULT '[]',  -- JSON array (Value is []string)

	UNIQUE(nm_id, char_id),

	FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Tags table (1 row per tag per card)
CREATE TABLE IF NOT EXISTS card_tags (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	nm_id INTEGER NOT NULL,

	tag_id INTEGER NOT NULL,  -- tag ID from API
	name TEXT NOT NULL DEFAULT '',
	color TEXT NOT NULL DEFAULT '',

	UNIQUE(nm_id, tag_id),

	FOREIGN KEY (nm_id) REFERENCES cards(nm_id) ON DELETE CASCADE
);

-- Download metadata table (key-value for cursor persistence)
CREATE TABLE IF NOT EXISTS cards_download_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_cards_vendor_code
	ON cards(vendor_code);

CREATE INDEX IF NOT EXISTS idx_cards_brand
	ON cards(brand);

CREATE INDEX IF NOT EXISTS idx_cards_subject_id
	ON cards(subject_id);

CREATE INDEX IF NOT EXISTS idx_cards_updated_at
	ON cards(updated_at);

CREATE INDEX IF NOT EXISTS idx_card_photos_nm_id
	ON card_photos(nm_id);

CREATE INDEX IF NOT EXISTS idx_card_sizes_nm_id
	ON card_sizes(nm_id);

CREATE INDEX IF NOT EXISTS idx_card_characteristics_nm_id
	ON card_characteristics(nm_id);

CREATE INDEX IF NOT EXISTS idx_card_tags_nm_id
	ON card_tags(nm_id);
`
)

// GetCardsSchemaSQL returns the cards tables schema.
func GetCardsSchemaSQL() string {
	return CardsSchemaSQL
}
