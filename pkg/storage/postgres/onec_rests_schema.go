package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// onecRestsSchemaSQL defines PostgreSQL table for 1C warehouse stock levels.
	//
	// Translated from pkg/storage/sqlite/onec_schema.go:
	//   - INTEGER (boolean) → BOOLEAN NOT NULL DEFAULT FALSE
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	//   - INSERT OR REPLACE → ON CONFLICT (...) DO UPDATE SET ... = EXCLUDED ...
	onecRestsSchemaSQL = `
-- ============================================================================
-- 1C RESTS (warehouse stock levels, snapshot-based)
-- Grain: one row per (good_guid, sku_guid, storage_guid, snapshot_date)
-- ============================================================================
CREATE TABLE IF NOT EXISTS onec_rests (
    good_guid       TEXT    NOT NULL,
    sku_guid        TEXT    NOT NULL DEFAULT '',
    storage_guid    TEXT    NOT NULL,
    snapshot_date   TEXT    NOT NULL,
    storage_name    TEXT    DEFAULT '',
    stock           INTEGER DEFAULT 0,
    reserv          INTEGER DEFAULT 0,
    free            INTEGER DEFAULT 0,
    first_stage     BOOLEAN NOT NULL DEFAULT FALSE,
    downloaded_at   TEXT    DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (good_guid, sku_guid, storage_guid, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_onec_rests_snapshot ON onec_rests(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_onec_rests_good_guid ON onec_rests(good_guid);
CREATE INDEX IF NOT EXISTS idx_onec_rests_storage_guid ON onec_rests(storage_guid);
`
)

// initOneCRestsSchema creates onec_rests table in the PostgreSQL database.
func initOneCRestsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, onecRestsSchemaSQL)
	if err != nil {
		return fmt.Errorf("onec_rests schema: %w", err)
	}
	return nil
}
