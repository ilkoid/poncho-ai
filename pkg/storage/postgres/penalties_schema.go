package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// penaltiesSchemaSQL defines PostgreSQL table for WB measurement penalties.
	//
	// Translated from pkg/storage/sqlite/penalties_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - INTEGER boolean fields → BOOLEAN
	//   - downloaded_at TEXT DEFAULT CURRENT_TIMESTAMP → TEXT DEFAULT TO_CHAR(...)
	penaltiesSchemaSQL = `
-- ============================================================================
-- MEASUREMENT PENALTIES (WB Seller Analytics API — /api/analytics/v1/measurement-penalties)
-- One row per dimension penalty (dim_id is unique).
-- ============================================================================

CREATE TABLE IF NOT EXISTS measurement_penalties (
    id BIGSERIAL PRIMARY KEY,

    -- Identification
    dim_id BIGINT UNIQUE NOT NULL,         -- Measurement ID (natural key)
    nm_id BIGINT NOT NULL DEFAULT 0,       -- WB article ID
    subject_name TEXT NOT NULL DEFAULT '',  -- Product subject

    -- Dimension difference
    prc_over DOUBLE PRECISION NOT NULL DEFAULT 0, -- % real volume > declared

    -- Actual warehouse measurements (физические замеры на складе WB)
    volume DOUBLE PRECISION NOT NULL DEFAULT 0,   -- litres
    width INTEGER NOT NULL DEFAULT 0,             -- cm
    length INTEGER NOT NULL DEFAULT 0,            -- cm
    height INTEGER NOT NULL DEFAULT 0,            -- cm

    -- Declared product card dimensions (заявленные продавцом)
    volume_sup DOUBLE PRECISION NOT NULL DEFAULT 0, -- litres
    width_sup INTEGER NOT NULL DEFAULT 0,           -- cm
    length_sup INTEGER NOT NULL DEFAULT 0,          -- cm
    height_sup INTEGER NOT NULL DEFAULT 0,          -- cm

    -- Evidence & dates
    photo_urls TEXT NOT NULL DEFAULT '[]',  -- JSON array of measurement photo URLs
    dt_bonus TEXT NOT NULL DEFAULT '',      -- Penalty date

    -- Status: confirmed / cancelled
    is_valid BOOLEAN NOT NULL DEFAULT TRUE, -- TRUE=confirmed, FALSE=cancelled
    is_valid_dt TEXT NOT NULL DEFAULT '',    -- Confirmation/cancellation date

    -- Money
    reversal_amount DOUBLE PRECISION NOT NULL DEFAULT 0, -- Refund if cancelled
    penalty_amount DOUBLE PRECISION NOT NULL DEFAULT 0,  -- Penalty amount, ₽

    -- Download metadata
    downloaded_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_mp_nm_id ON measurement_penalties(nm_id);
CREATE INDEX IF NOT EXISTS idx_mp_dt_bonus ON measurement_penalties(dt_bonus);
CREATE INDEX IF NOT EXISTS idx_mp_is_valid ON measurement_penalties(is_valid);
`
)

// initPenaltiesSchema creates measurement_penalties table in the PostgreSQL database.
func initPenaltiesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, penaltiesSchemaSQL)
	if err != nil {
		return fmt.Errorf("penalties schema: %w", err)
	}
	return nil
}
