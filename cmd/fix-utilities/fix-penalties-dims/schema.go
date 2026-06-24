package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stagingSchemaDDL defines the PG staging table for the penalties-dims fixer.
//
// Working state only (current status per nm_id); the append-only CSV audit log
// is the durable history. No weight columns — penalties data has no weight and
// the fixer preserves the card's existing WeightBrutto without logging it.
const stagingSchemaDDL = `
CREATE TABLE IF NOT EXISTS fix_penalties_dims_staging (
    nm_id        BIGINT PRIMARY KEY,
    vendor_code  TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    dim_id       BIGINT NOT NULL,        -- source measurement (measurement_penalties.dim_id)
    dt_bonus     TEXT NOT NULL DEFAULT '',-- source measurement date
    old_length   DOUBLE PRECISION NOT NULL DEFAULT 0,
    old_width    DOUBLE PRECISION NOT NULL DEFAULT 0,
    old_height   DOUBLE PRECISION NOT NULL DEFAULT 0,
    new_length   DOUBLE PRECISION NOT NULL DEFAULT 0,
    new_width    DOUBLE PRECISION NOT NULL DEFAULT 0,
    new_height   DOUBLE PRECISION NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'pending', -- pending|applied|skipped|error
    error_msg    TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT '',
    applied_at   TEXT NOT NULL DEFAULT ''
);
`

// initStagingSchema creates the staging table if it does not exist.
func initStagingSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, stagingSchemaDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	return nil
}
