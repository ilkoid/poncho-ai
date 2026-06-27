package main

import (
	"context"
	"database/sql"
	"fmt"
)

// stagingSchemaDDL defines the SQLite staging table for the penalties-dims fixer.
//
// Working state only (current status per nm_id); the append-only CSV audit log
// is the durable history. No weight columns — penalties data has no weight and
// the fixer preserves the card's existing WeightBrutto without logging it.
//
// SQLite types: BIGINT→INTEGER, DOUBLE PRECISION→REAL (no BIGSERIAL — nm_id is the
// natural PK). The cards/measurement_penalties tables are created by the downloaders
// (download-wb-cards, download-wb-penalties-v2) in the same fixer.db; this fixer owns
// only its staging table.
const stagingSchemaDDL = `
CREATE TABLE IF NOT EXISTS fix_penalties_dims_staging (
    nm_id        INTEGER PRIMARY KEY,
    vendor_code  TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    dim_id       INTEGER NOT NULL,         -- source measurement (measurement_penalties.dim_id)
    dt_bonus     TEXT NOT NULL DEFAULT '', -- source measurement date
    old_length   REAL NOT NULL DEFAULT 0,
    old_width    REAL NOT NULL DEFAULT 0,
    old_height   REAL NOT NULL DEFAULT 0,
    new_length   REAL NOT NULL DEFAULT 0,
    new_width    REAL NOT NULL DEFAULT 0,
    new_height   REAL NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'pending', -- pending|applied|skipped|error
    error_msg    TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT '',
    applied_at   TEXT NOT NULL DEFAULT ''
);
`

// initStagingSchema creates the staging table in the SQLite database if absent.
func initStagingSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, stagingSchemaDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	return nil
}
