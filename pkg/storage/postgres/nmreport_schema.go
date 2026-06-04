package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// nmreportSchemaSQL defines PostgreSQL tables for WB nm-report funnel CSV data.
	//
	// 3 tables: nm_report_downloads, funnel_metrics_daily, funnel_metrics_grouped_daily.
	//
	// Translated from pkg/storage/sqlite/schema.go (nmreport section):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY (except nm_report_downloads: TEXT PK)
	//   - REAL → DOUBLE PRECISION
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	//   - INSERT OR REPLACE → ON CONFLICT ... DO UPDATE SET ... = EXCLUDED
	nmreportSchemaSQL = `
-- ============================================================================
-- NM REPORT DOWNLOADS — async CSV report lifecycle tracking
-- Grain: one row per report request (id = UUID from WB API)
-- ============================================================================

CREATE TABLE IF NOT EXISTS nm_report_downloads (
    id TEXT PRIMARY KEY,
    report_type TEXT NOT NULL DEFAULT '',
    start_date TEXT NOT NULL DEFAULT '',
    end_date TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0,
    rows_count BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    completed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_nrd_type_dates ON nm_report_downloads(report_type, start_date, end_date);

-- ============================================================================
-- FUNNEL METRICS DAILY — detail funnel per nmID per day
-- (WB Analytics — DETAIL_HISTORY_REPORT CSV export)
-- Grain: (nm_id, metric_date)
-- ============================================================================

CREATE TABLE IF NOT EXISTS funnel_metrics_daily (
    id BIGSERIAL PRIMARY KEY,
    nm_id INTEGER NOT NULL,
    metric_date TEXT NOT NULL DEFAULT '',
    open_count INTEGER NOT NULL DEFAULT 0,
    cart_count INTEGER NOT NULL DEFAULT 0,
    order_count INTEGER NOT NULL DEFAULT 0,
    buyout_count INTEGER NOT NULL DEFAULT 0,
    add_to_wishlist INTEGER NOT NULL DEFAULT 0,
    order_sum INTEGER NOT NULL DEFAULT 0,
    buyout_sum INTEGER NOT NULL DEFAULT 0,
    cancel_count INTEGER NOT NULL DEFAULT 0,
    cancel_sum_rub INTEGER NOT NULL DEFAULT 0,
    conversion_add_to_cart DOUBLE PRECISION NOT NULL DEFAULT 0,
    conversion_cart_to_order DOUBLE PRECISION NOT NULL DEFAULT 0,
    conversion_buyout DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(nm_id, metric_date)
);

CREATE INDEX IF NOT EXISTS idx_fmd_nm_date ON funnel_metrics_daily(nm_id, metric_date);
CREATE INDEX IF NOT EXISTS idx_fmd_date ON funnel_metrics_daily(metric_date);

-- ============================================================================
-- FUNNEL METRICS GROUPED DAILY — aggregated funnel per day
-- (WB Analytics — GROUPED_HISTORY_REPORT CSV export)
-- Grain: (metric_date)
-- ============================================================================

CREATE TABLE IF NOT EXISTS funnel_metrics_grouped_daily (
    id BIGSERIAL PRIMARY KEY,
    metric_date TEXT NOT NULL DEFAULT '',
    open_card_count INTEGER NOT NULL DEFAULT 0,
    add_to_cart_count INTEGER NOT NULL DEFAULT 0,
    orders_count INTEGER NOT NULL DEFAULT 0,
    orders_sum_rub INTEGER NOT NULL DEFAULT 0,
    buyouts_count INTEGER NOT NULL DEFAULT 0,
    buyouts_sum_rub INTEGER NOT NULL DEFAULT 0,
    cancel_count INTEGER NOT NULL DEFAULT 0,
    cancel_sum_rub INTEGER NOT NULL DEFAULT 0,
    conversion_add_to_cart DOUBLE PRECISION NOT NULL DEFAULT 0,
    conversion_cart_to_order DOUBLE PRECISION NOT NULL DEFAULT 0,
    conversion_buyout DOUBLE PRECISION NOT NULL DEFAULT 0,
    add_to_wishlist INTEGER NOT NULL DEFAULT 0,
    UNIQUE(metric_date)
);
`
)

// initNmReportSchema creates nmreport tables in the PostgreSQL database.
func initNmReportSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, nmreportSchemaSQL)
	if err != nil {
		return fmt.Errorf("nmreport schema: %w", err)
	}
	return nil
}
