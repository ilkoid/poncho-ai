package sqlite

const (
	// PenaltiesSchemaSQL defines SQLite table for WB measurement penalties.
	//
	// WB Seller Analytics API: GET /api/analytics/v1/measurement-penalties
	// Штрафы за неверные габариты и вес упаковки.
	//
	// Natural key: dim_id (measurement ID, unique per measurement).
	// Upsert semantics: penalties can be updated (cancelled with isValid=false, reversal amounts changed).
	PenaltiesSchemaSQL = `
-- ============================================================================
-- MEASUREMENT PENALTIES (WB Seller Analytics API — /api/analytics/v1/measurement-penalties)
-- One row per dimension penalty (dim_id is unique).
-- ============================================================================

CREATE TABLE IF NOT EXISTS measurement_penalties (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Identification
    dim_id INTEGER UNIQUE NOT NULL,        -- Measurement ID (natural key)
    nm_id INTEGER NOT NULL DEFAULT 0,      -- WB article ID
    subject_name TEXT NOT NULL DEFAULT '',  -- Product subject

    -- Dimension difference
    prc_over REAL NOT NULL DEFAULT 0,      -- % real volume > declared

    -- Actual warehouse measurements (физические замеры на складе WB)
    volume REAL NOT NULL DEFAULT 0,        -- litres
    width INTEGER NOT NULL DEFAULT 0,      -- cm
    length INTEGER NOT NULL DEFAULT 0,     -- cm
    height INTEGER NOT NULL DEFAULT 0,     -- cm

    -- Declared product card dimensions (заявленные продавцом)
    volume_sup REAL NOT NULL DEFAULT 0,    -- litres
    width_sup INTEGER NOT NULL DEFAULT 0,  -- cm
    length_sup INTEGER NOT NULL DEFAULT 0, -- cm
    height_sup INTEGER NOT NULL DEFAULT 0, -- cm

    -- Evidence & dates
    photo_urls TEXT NOT NULL DEFAULT '[]',  -- JSON array of measurement photo URLs
    dt_bonus TEXT NOT NULL DEFAULT '',      -- Penalty date (YYYY-MM-DDTHH:MM:SS)

    -- Status: confirmed / cancelled
    is_valid INTEGER NOT NULL DEFAULT 1,   -- 1=confirmed, 0=cancelled
    is_valid_dt TEXT NOT NULL DEFAULT '',   -- Confirmation/cancellation date

    -- Money
    reversal_amount REAL NOT NULL DEFAULT 0, -- Refund if cancelled
    penalty_amount REAL NOT NULL DEFAULT 0,  -- Penalty amount, ₽

    -- Download metadata
    downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_mp_nm_id ON measurement_penalties(nm_id);
CREATE INDEX IF NOT EXISTS idx_mp_dt_bonus ON measurement_penalties(dt_bonus);
CREATE INDEX IF NOT EXISTS idx_mp_is_valid ON measurement_penalties(is_valid);
`
)

// GetPenaltiesSchemaSQL returns the measurement penalties table schema.
func GetPenaltiesSchemaSQL() string {
	return PenaltiesSchemaSQL
}
