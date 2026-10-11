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

    -- Row classification (2026-10-11): WB кладёт в warehouses[] не только
    -- реальные склады, но и служебные строки-итоги («Всего находится на
    -- складах», «В пути до получателей», «В пути возвраты на склад WB» —
    -- пример ответа 12-reports.yaml:685-689). Псевдостроки пишутся с меткой:
    -- warehouse | total | in_transit_out | in_transit_return.
    -- «Складские» выборки обязаны фильтровать row_kind='warehouse', иначе
    -- SUM(quantity) считает товар дважды (итог + склады + «в пути»).
    row_kind TEXT NOT NULL DEFAULT 'warehouse',

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

// whRemainsMigrations накатывает row_kind на существующие таблицы. До
// 2026-10-11 строки писались без классификации, включая служебные
// «Всего/В пути» (они и есть источник двойного счёта) — DEFAULT 'warehouse'
// пометит их стартово «складами»; доразмечает историю ретро-скрипт из
// dev_wb_stocks_data_notes.md (этап 3 плана).
const whRemainsMigrations = `
ALTER TABLE warehouse_remains ADD COLUMN IF NOT EXISTS row_kind TEXT NOT NULL DEFAULT 'warehouse';
`

// initWhRemainsSchema creates warehouse_remains table in PostgreSQL.
func initWhRemainsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, whRemainsSchemaSQL); err != nil {
		return fmt.Errorf("warehouse_remains schema: %w", err)
	}
	if _, err := pool.Exec(ctx, whRemainsMigrations); err != nil {
		return fmt.Errorf("warehouse_remains migrations (row_kind): %w", err)
	}
	// Частичный индекс — отдельным Exec: pgx не парсит WHERE в multi-statement
	// блоках (гатч AGENTS.md). Каноническая выборка остатков — row_kind='warehouse'.
	if _, err := pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_wr_warehouse_rows
		ON warehouse_remains (snapshot_date, nm_id)
		WHERE row_kind = 'warehouse'`); err != nil {
		return fmt.Errorf("warehouse_remains partial index: %w", err)
	}
	return nil
}
