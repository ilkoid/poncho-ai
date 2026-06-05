package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// whRemainsSchemaSQL defines the warehouse_remains table for PostgreSQL.
	//
	// Translated from pkg/storage/sqlite/schema.go (warehouse_remains table):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - UNIQUE constraint preserved for upsert logic
	whRemainsSchemaSQL = `
CREATE TABLE IF NOT EXISTS warehouse_remains (
    id BIGSERIAL PRIMARY KEY,

    -- Snapshot identification
    snapshot_date TEXT NOT NULL,

    -- Product keys (part of UNIQUE constraint)
    nm_id BIGINT NOT NULL,
    barcode TEXT NOT NULL DEFAULT '',
    tech_size TEXT NOT NULL DEFAULT '0',
    warehouse_name TEXT NOT NULL DEFAULT '',

    -- Product metadata
    brand TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    vendor_code TEXT NOT NULL DEFAULT '',
    volume DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Stock quantity
    quantity BIGINT NOT NULL DEFAULT 0,

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(snapshot_date, nm_id, tech_size, warehouse_name)
);

-- Index for product-focused time-series queries
CREATE INDEX IF NOT EXISTS idx_wr_nm_date
    ON warehouse_remains(nm_id, snapshot_date);

-- Index for warehouse-focused queries
CREATE INDEX IF NOT EXISTS idx_wr_warehouse_date
    ON warehouse_remains(warehouse_name, snapshot_date);
`
)

// initWhRemainsSchema creates warehouse_remains table in PostgreSQL.
func initWhRemainsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, whRemainsSchemaSQL); err != nil {
		return fmt.Errorf("warehouse_remains schema: %w", err)
	}
	return nil
}
