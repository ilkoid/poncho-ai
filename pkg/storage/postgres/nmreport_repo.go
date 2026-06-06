package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/nmreport"
)

// Compile-time assertion: PgNmReportRepo implements nmreport.NmReportWriter.
var _ nmreport.NmReportWriter = (*PgNmReportRepo)(nil)

// PgNmReportRepo implements nmreport.NmReportWriter for PostgreSQL.
type PgNmReportRepo struct {
	pool *pgxpool.Pool
}

// NewPgNmReportRepo creates a new PostgreSQL nmreport repository.
func NewPgNmReportRepo(pool *pgxpool.Pool) *PgNmReportRepo {
	return &PgNmReportRepo{pool: pool}
}

// InitSchema creates nmreport tables if they don't exist.
func (r *PgNmReportRepo) InitSchema(ctx context.Context) error {
	return initNmReportSchema(ctx, r.pool)
}

// ============================================================================
// Writer methods
// ============================================================================

const pgNmReportChunkSize = 500

// GetNmReport looks up an existing report record by type and date range.
func (r *PgNmReportRepo) GetNmReport(ctx context.Context, reportType, startDate, endDate string) (*nmreport.NmReportRecord, error) {
	var rec nmreport.NmReportRecord
	var completedAt *string // nullable

	err := r.pool.QueryRow(ctx,
		"SELECT id, report_type, start_date, end_date, status, file_size, rows_count, created_at, completed_at "+
			"FROM nm_report_downloads WHERE report_type = $1 AND start_date = $2 AND end_date = $3 "+
			"ORDER BY created_at DESC LIMIT 1",
		reportType, startDate, endDate,
	).Scan(&rec.ID, &rec.ReportType, &rec.StartDate, &rec.EndDate, &rec.Status,
		&rec.FileSize, &rec.RowsCount, &rec.CreatedAt, &completedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get nm report: %w", err)
	}

	if completedAt != nil {
		rec.CompletedAt = *completedAt
	}
	return &rec, nil
}

// SaveNmReport inserts or updates a report tracking record.
func (r *PgNmReportRepo) SaveNmReport(ctx context.Context, record nmreport.NmReportRecord) error {
	_, err := r.pool.Exec(ctx, insertNmReportFullChunkSQL,
		record.ID, record.ReportType, record.StartDate, record.EndDate,
		record.Status, record.FileSize, record.RowsCount, record.CreatedAt, record.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("save nm report: %w", err)
	}
	return nil
}

// UpdateNmReportStatus updates the status and row count of a report.
func (r *PgNmReportRepo) UpdateNmReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error {
	completedAt := ""
	if status == "SUCCESS" {
		completedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE nm_report_downloads SET status = $1, rows_count = $2, completed_at = $3 WHERE id = $4",
		status, rowsCount, completedAt, downloadID,
	)
	if err != nil {
		return fmt.Errorf("update nm report status: %w", err)
	}
	return nil
}

// SaveFunnelMetricsDetail saves DETAIL funnel rows using refresh window pattern.
// Recent dates (within refreshDays) -> ON CONFLICT DO UPDATE, old dates -> ON CONFLICT DO NOTHING.
// Chunks rows into batches of 500 per transaction.
func (r *PgNmReportRepo) SaveFunnelMetricsDetail(ctx context.Context, rows []nmreport.FunnelDetailRow, refreshDays int) error {
	if len(rows) == 0 {
		return nil
	}

	cutoffDate := time.Now().AddDate(0, 0, -refreshDays)

	for i := 0; i < len(rows); i += pgNmReportChunkSize {
		end := min(i+pgNmReportChunkSize, len(rows))
		chunk := rows[i:end]

		// Partition chunk into recent (replace) and old (ignore) sub-slices.
		var recentRows, oldRows []nmreport.FunnelDetailRow
		for _, row := range chunk {
			metricDate, err := time.Parse("2006-01-02", row.MetricDate)
			if err != nil {
				return fmt.Errorf("parse date %s: %w", row.MetricDate, err)
			}

			if refreshDays <= 0 || metricDate.After(cutoffDate) || metricDate.Equal(cutoffDate) {
				recentRows = append(recentRows, row)
			} else {
				oldRows = append(oldRows, row)
			}
		}

		if err := r.saveDetailChunk(ctx, recentRows, oldRows); err != nil {
			return fmt.Errorf("save detail chunk at offset %d: %w", i, err)
		}
	}

	return nil
}

// saveDetailChunk saves a partitioned set of detail rows in a single transaction.
// recentRows use REPLACE (upsert), oldRows use IGNORE (insert-if-missing).
func (r *PgNmReportRepo) saveDetailChunk(ctx context.Context, recentRows, oldRows []nmreport.FunnelDetailRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Upsert recent rows (full refresh).
	if len(recentRows) > 0 {
		args := make([]any, 0, len(recentRows)*insertNmFunnelDailyCols)
		for _, row := range recentRows {
			args = append(args,
				row.NmID, row.MetricDate,
				row.OpenCardCount, row.AddToCartCount, row.OrdersCount, row.BuyoutsCount, row.AddToWishlist,
				row.OrdersSumRub, row.BuyoutsSumRub, row.CancelCount, row.CancelSumRub,
				row.AddToCartConversion, row.CartToOrderConversion, row.BuyoutPercent,
			)
		}

		query := insertNmFunnelDailyReplaceFullSQL
		if len(recentRows) < pgNmReportChunkSize {
			query = BuildMultiRowInsert(insertNmFunnelDailyPrefixSQL, insertNmFunnelDailyOnConflictReplaceSQL, len(recentRows), insertNmFunnelDailyCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("save recent detail rows (count %d): %w", len(recentRows), err)
		}
	}

	// Insert-if-missing old rows (preserve historical data).
	if len(oldRows) > 0 {
		args := make([]any, 0, len(oldRows)*insertNmFunnelDailyCols)
		for _, row := range oldRows {
			args = append(args,
				row.NmID, row.MetricDate,
				row.OpenCardCount, row.AddToCartCount, row.OrdersCount, row.BuyoutsCount, row.AddToWishlist,
				row.OrdersSumRub, row.BuyoutsSumRub, row.CancelCount, row.CancelSumRub,
				row.AddToCartConversion, row.CartToOrderConversion, row.BuyoutPercent,
			)
		}

		query := insertNmFunnelDailyIgnoreFullSQL
		if len(oldRows) < pgNmReportChunkSize {
			query = BuildMultiRowInsert(insertNmFunnelDailyPrefixSQL, insertNmFunnelDailyOnConflictIgnoreSQL, len(oldRows), insertNmFunnelDailyCols)
		}

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("save old detail rows (count %d): %w", len(oldRows), err)
		}
	}

	return tx.Commit(ctx)
}

// SaveFunnelMetricsGrouped saves GROUPED funnel rows via ON CONFLICT upsert.
// Chunks rows into batches of 500 per transaction.
func (r *PgNmReportRepo) SaveFunnelMetricsGrouped(ctx context.Context, rows []nmreport.FunnelGroupedRow) error {
	if len(rows) == 0 {
		return nil
	}

	for i := 0; i < len(rows); i += pgNmReportChunkSize {
		end := min(i+pgNmReportChunkSize, len(rows))
		chunk := rows[i:end]

		if err := r.saveGroupedChunk(ctx, chunk); err != nil {
			return fmt.Errorf("save grouped chunk at offset %d: %w", i, err)
		}
	}

	return nil
}

// saveGroupedChunk saves up to pgNmReportChunkSize grouped rows using a single multi-row INSERT.
func (r *PgNmReportRepo) saveGroupedChunk(ctx context.Context, chunk []nmreport.FunnelGroupedRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertNmGroupedDailyCols)
	for _, row := range chunk {
		args = append(args,
			row.MetricDate,
			row.OpenCardCount, row.AddToCartCount, row.OrdersCount, row.OrdersSumRub,
			row.BuyoutsCount, row.BuyoutsSumRub, row.CancelCount, row.CancelSumRub,
			row.AddToCartConversion, row.CartToOrderConversion, row.BuyoutPercent,
			row.AddToWishlist,
		)
	}

	query := insertNmGroupedDailyFullChunkSQL
	if len(chunk) < pgNmReportChunkSize {
		query = BuildMultiRowInsert(insertNmGroupedDailyPrefixSQL, insertNmGroupedDailyOnConflictSQL, len(chunk), insertNmGroupedDailyCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save grouped batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// ============================================================================
// Multi-row INSERT SQL fragments
// ============================================================================

// nm_report_downloads — 9 columns ($1-$9).
// PK: id (TEXT, natural key). All non-PK columns in DO UPDATE SET.
// Single-row insert (report tracking, not bulk).
const (
	insertNmReportCols = 9

	insertNmReportPrefixSQL = `INSERT INTO nm_report_downloads (
    id, report_type, start_date, end_date, status,
    file_size, rows_count, created_at, completed_at
) VALUES `

	insertNmReportOnConflictSQL = `
ON CONFLICT (id) DO UPDATE SET
    report_type = EXCLUDED.report_type,
    start_date = EXCLUDED.start_date,
    end_date = EXCLUDED.end_date,
    status = EXCLUDED.status,
    file_size = EXCLUDED.file_size,
    rows_count = EXCLUDED.rows_count,
    created_at = EXCLUDED.created_at,
    completed_at = EXCLUDED.completed_at`
)

var insertNmReportFullChunkSQL = BuildMultiRowInsert(insertNmReportPrefixSQL, insertNmReportOnConflictSQL, 1, insertNmReportCols)

// funnel_metrics_daily — 14 columns ($1-$14).
// PK: (nm_id, metric_date). Two variants: REPLACE (recent) and IGNORE (old).
const (
	insertNmFunnelDailyCols = 14

	insertNmFunnelDailyPrefixSQL = `INSERT INTO funnel_metrics_daily (
    nm_id, metric_date,
    open_count, cart_count, order_count, buyout_count, add_to_wishlist,
    order_sum, buyout_sum, cancel_count, cancel_sum_rub,
    conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
) VALUES `

	insertNmFunnelDailyOnConflictReplaceSQL = `
ON CONFLICT (nm_id, metric_date) DO UPDATE SET
    open_count = EXCLUDED.open_count,
    cart_count = EXCLUDED.cart_count,
    order_count = EXCLUDED.order_count,
    buyout_count = EXCLUDED.buyout_count,
    add_to_wishlist = EXCLUDED.add_to_wishlist,
    order_sum = EXCLUDED.order_sum,
    buyout_sum = EXCLUDED.buyout_sum,
    cancel_count = EXCLUDED.cancel_count,
    cancel_sum_rub = EXCLUDED.cancel_sum_rub,
    conversion_add_to_cart = EXCLUDED.conversion_add_to_cart,
    conversion_cart_to_order = EXCLUDED.conversion_cart_to_order,
    conversion_buyout = EXCLUDED.conversion_buyout`

	insertNmFunnelDailyOnConflictIgnoreSQL = `
ON CONFLICT (nm_id, metric_date) DO NOTHING`
)

var (
	insertNmFunnelDailyReplaceFullSQL = BuildMultiRowInsert(insertNmFunnelDailyPrefixSQL, insertNmFunnelDailyOnConflictReplaceSQL, pgNmReportChunkSize, insertNmFunnelDailyCols)
	insertNmFunnelDailyIgnoreFullSQL  = BuildMultiRowInsert(insertNmFunnelDailyPrefixSQL, insertNmFunnelDailyOnConflictIgnoreSQL, pgNmReportChunkSize, insertNmFunnelDailyCols)
)

// funnel_metrics_grouped_daily — 13 columns ($1-$13).
// PK: (metric_date). All non-PK columns in DO UPDATE SET.
const (
	insertNmGroupedDailyCols = 13

	insertNmGroupedDailyPrefixSQL = `INSERT INTO funnel_metrics_grouped_daily (
    metric_date,
    open_card_count, add_to_cart_count, orders_count, orders_sum_rub,
    buyouts_count, buyouts_sum_rub, cancel_count, cancel_sum_rub,
    conversion_add_to_cart, conversion_cart_to_order, conversion_buyout,
    add_to_wishlist
) VALUES `

	insertNmGroupedDailyOnConflictSQL = `
ON CONFLICT (metric_date) DO UPDATE SET
    open_card_count = EXCLUDED.open_card_count,
    add_to_cart_count = EXCLUDED.add_to_cart_count,
    orders_count = EXCLUDED.orders_count,
    orders_sum_rub = EXCLUDED.orders_sum_rub,
    buyouts_count = EXCLUDED.buyouts_count,
    buyouts_sum_rub = EXCLUDED.buyouts_sum_rub,
    cancel_count = EXCLUDED.cancel_count,
    cancel_sum_rub = EXCLUDED.cancel_sum_rub,
    conversion_add_to_cart = EXCLUDED.conversion_add_to_cart,
    conversion_cart_to_order = EXCLUDED.conversion_cart_to_order,
    conversion_buyout = EXCLUDED.conversion_buyout,
    add_to_wishlist = EXCLUDED.add_to_wishlist`
)

var insertNmGroupedDailyFullChunkSQL = BuildMultiRowInsert(insertNmGroupedDailyPrefixSQL, insertNmGroupedDailyOnConflictSQL, pgNmReportChunkSize, insertNmGroupedDailyCols)
