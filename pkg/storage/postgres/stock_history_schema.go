package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// stockHistorySchemaSQL defines PostgreSQL tables for WB Stock History CSV reports.
	//
	// 3 tables: stock_history_reports, stock_history_metrics, stock_history_daily.
	//
	// Translated from pkg/storage/sqlite/stock_history_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY (except reports: TEXT PK)
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	//   - INSERT OR REPLACE → ON CONFLICT ... DO UPDATE SET ... = EXCLUDED
	stockHistorySchemaSQL = `
-- ============================================================================
-- STOCK HISTORY REPORTS — async CSV report lifecycle tracking
-- Grain: one row per report request (id = UUID from WB API)
-- UNIQUE on (report_type, start_date, end_date, stock_type) for resume lookups
-- ============================================================================

CREATE TABLE IF NOT EXISTS stock_history_reports (
    id TEXT PRIMARY KEY,
    report_type TEXT NOT NULL DEFAULT '',
    start_date TEXT NOT NULL DEFAULT '',
    end_date TEXT NOT NULL DEFAULT '',
    stock_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    downloaded_at TEXT NOT NULL DEFAULT '',
    rows_count BIGINT NOT NULL DEFAULT 0,
    UNIQUE(report_type, start_date, end_date, stock_type)
);

CREATE INDEX IF NOT EXISTS idx_shr_dates ON stock_history_reports(start_date, end_date);

-- ============================================================================
-- STOCK HISTORY METRICS — metrics with monthly dynamic columns
-- (WB Analytics — STOCK_HISTORY_REPORT_CSV export)
-- Grain: (report_id, nm_id, chrt_id) per report
-- ============================================================================

CREATE TABLE IF NOT EXISTS stock_history_metrics (
    id BIGSERIAL PRIMARY KEY,
    report_id TEXT NOT NULL REFERENCES stock_history_reports(id),
    vendor_code TEXT,
    name TEXT,
    nm_id BIGINT NOT NULL,
    subject_name TEXT,
    brand_name TEXT,
    size_name TEXT,
    chrt_id BIGINT,
    region_name TEXT,
    office_name TEXT,
    availability TEXT,
    orders_count INTEGER,
    orders_sum INTEGER,
    buyout_count INTEGER,
    buyout_sum INTEGER,
    buyout_percent INTEGER,
    avg_orders DOUBLE PRECISION,
    stock_count INTEGER,
    stock_sum INTEGER,
    sale_rate INTEGER,
    avg_stock_turnover INTEGER,
    to_client_count INTEGER,
    from_client_count INTEGER,
    price INTEGER,
    office_missing_time INTEGER,
    lost_orders_count DOUBLE PRECISION,
    lost_orders_sum DOUBLE PRECISION,
    lost_buyouts_count DOUBLE PRECISION,
    lost_buyouts_sum DOUBLE PRECISION,
    monthly_data TEXT,
    currency TEXT,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(report_id, nm_id, chrt_id)
);

CREATE INDEX IF NOT EXISTS idx_shm_nm_id ON stock_history_metrics(nm_id);
CREATE INDEX IF NOT EXISTS idx_shm_report_id ON stock_history_metrics(report_id);
CREATE INDEX IF NOT EXISTS idx_shm_office ON stock_history_metrics(office_name);

-- ============================================================================
-- STOCK HISTORY DAILY — daily stock levels with date dynamic columns
-- (WB Analytics — STOCK_HISTORY_DAILY_CSV export)
-- Grain: (report_id, nm_id, chrt_id) per report
-- ============================================================================

CREATE TABLE IF NOT EXISTS stock_history_daily (
    id BIGSERIAL PRIMARY KEY,
    report_id TEXT NOT NULL REFERENCES stock_history_reports(id),
    vendor_code TEXT,
    name TEXT,
    nm_id BIGINT NOT NULL,
    subject_name TEXT,
    brand_name TEXT,
    size_name TEXT,
    chrt_id BIGINT,
    office_name TEXT,
    daily_data TEXT,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(report_id, nm_id, chrt_id)
);

CREATE INDEX IF NOT EXISTS idx_shd_nm_id ON stock_history_daily(nm_id);
CREATE INDEX IF NOT EXISTS idx_shd_report_id ON stock_history_daily(report_id);
CREATE INDEX IF NOT EXISTS idx_shd_office ON stock_history_daily(office_name);
`
)

// initStockHistorySchema creates stock history tables in the PostgreSQL database.
func initStockHistorySchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, stockHistorySchemaSQL)
	if err != nil {
		return fmt.Errorf("stock history schema: %w", err)
	}
	return nil
}
