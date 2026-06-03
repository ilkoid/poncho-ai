package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// stocksSchemaSQL defines the stocks_daily_warehouses table for PostgreSQL.
	//
	// Translated from pkg/storage/sqlite/schema.go (stocks_daily_warehouses table):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - UNIQUE constraint preserved for upsert logic
	stocksSchemaSQL = `
CREATE TABLE IF NOT EXISTS stocks_daily_warehouses (
    id BIGSERIAL PRIMARY KEY,

    -- Snapshot identification
    snapshot_date TEXT NOT NULL,

    -- Product and warehouse keys (part of UNIQUE constraint)
    nm_id BIGINT NOT NULL,
    chrt_id BIGINT NOT NULL,
    warehouse_id BIGINT NOT NULL,

    -- Warehouse metadata
    warehouse_name TEXT NOT NULL DEFAULT '',
    region_name TEXT NOT NULL DEFAULT '',

    -- Stock quantities
    quantity BIGINT NOT NULL DEFAULT 0,
    in_way_to_client BIGINT NOT NULL DEFAULT 0,
    in_way_from_client BIGINT NOT NULL DEFAULT 0,

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    UNIQUE(snapshot_date, nm_id, chrt_id, warehouse_id)
);

-- Composite index for time-series queries (product-focused: stock trends per warehouse)
CREATE INDEX IF NOT EXISTS idx_stocks_nm_warehouse_date
    ON stocks_daily_warehouses(nm_id, warehouse_id, snapshot_date);

-- Index for date-focused queries (gap detection, date filtering)
CREATE INDEX IF NOT EXISTS idx_stocks_date
    ON stocks_daily_warehouses(snapshot_date);

-- Index for warehouse-level aggregation queries
CREATE INDEX IF NOT EXISTS idx_stocks_warehouse
    ON stocks_daily_warehouses(warehouse_id, snapshot_date);
`
)

// initStocksSchema creates stocks_daily_warehouses table in PostgreSQL.
func initStocksSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, stocksSchemaSQL); err != nil {
		return fmt.Errorf("stocks schema: %w", err)
	}
	return nil
}
