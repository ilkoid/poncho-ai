package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// searchvisSchemaSQL defines PostgreSQL tables for WB Seller Analytics search visibility.
	//
	// 2 tables: search_positions_daily, search_queries_daily.
	//
	// Translated from pkg/storage/sqlite/schema.go (search visibility section):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	//   - ON CONFLICT ... DO UPDATE SET ... = EXCLUDED for upsert
	searchvisSchemaSQL = `
-- ============================================================================
-- SEARCH POSITIONS DAILY (WB Seller Analytics — POST /api/v2/search-report/report)
-- Grain: (nm_id, snapshot_date, period_start)
-- ============================================================================

CREATE TABLE IF NOT EXISTS search_positions_daily (
    id BIGSERIAL PRIMARY KEY,
    nm_id INTEGER NOT NULL,
    snapshot_date TEXT NOT NULL DEFAULT '',
    avg_position DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_position_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    median_position DOUBLE PRECISION NOT NULL DEFAULT 0,
    visibility DOUBLE PRECISION NOT NULL DEFAULT 0,
    visibility_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    open_card INTEGER NOT NULL DEFAULT 0,
    open_card_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    cluster_first_hundred INTEGER NOT NULL DEFAULT 0,
    cluster_second_hundred INTEGER NOT NULL DEFAULT 0,
    cluster_below INTEGER NOT NULL DEFAULT 0,
    period_start TEXT NOT NULL DEFAULT '',
    period_end TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(nm_id, snapshot_date, period_start)
);

CREATE INDEX IF NOT EXISTS idx_spd_nm_date ON search_positions_daily(nm_id, snapshot_date);
CREATE INDEX IF NOT EXISTS idx_spd_date ON search_positions_daily(snapshot_date);

-- ============================================================================
-- SEARCH QUERIES DAILY (WB Seller Analytics — POST /api/v2/search-report/product/search-texts)
-- Grain: (nm_id, search_text, snapshot_date)
-- ============================================================================

CREATE TABLE IF NOT EXISTS search_queries_daily (
    id BIGSERIAL PRIMARY KEY,
    nm_id INTEGER NOT NULL,
    snapshot_date TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    frequency INTEGER NOT NULL DEFAULT 0,
    frequency_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    week_frequency INTEGER NOT NULL DEFAULT 0,
    avg_position DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_position_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    median_position DOUBLE PRECISION NOT NULL DEFAULT 0,
    median_position_dynamics DOUBLE PRECISION NOT NULL DEFAULT 0,
    visibility DOUBLE PRECISION NOT NULL DEFAULT 0,
    open_card INTEGER NOT NULL DEFAULT 0,
    add_to_cart INTEGER NOT NULL DEFAULT 0,
    orders INTEGER NOT NULL DEFAULT 0,
    open_to_cart DOUBLE PRECISION NOT NULL DEFAULT 0,
    cart_to_order DOUBLE PRECISION NOT NULL DEFAULT 0,
    vendor_code TEXT NOT NULL DEFAULT '',
    brand_name TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    period_start TEXT NOT NULL DEFAULT '',
    period_end TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(nm_id, search_text, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_sqd_nm_date ON search_queries_daily(nm_id, snapshot_date);
CREATE INDEX IF NOT EXISTS idx_sqd_text ON search_queries_daily(search_text);
CREATE INDEX IF NOT EXISTS idx_sqd_nm_text ON search_queries_daily(nm_id, search_text);
`
)

// initSearchvisSchema creates search visibility tables in the PostgreSQL database.
func initSearchvisSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, searchvisSchemaSQL)
	if err != nil {
		return fmt.Errorf("searchvis schema: %w", err)
	}
	return nil
}
