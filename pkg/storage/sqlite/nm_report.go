package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/nmreport"
)

// Compile-time assertion: SQLiteSalesRepository implements nmreport.NmReportWriter.
var _ nmreport.NmReportWriter = (*SQLiteSalesRepository)(nil)

// GetNmReport looks up an existing report record by type and date range.
func (r *SQLiteSalesRepository) GetNmReport(ctx context.Context, reportType, startDate, endDate string) (*nmreport.NmReportRecord, error) {
	var rec nmreport.NmReportRecord
	var completedAt sql.NullString

	err := r.db.QueryRowContext(ctx,
		"SELECT id, report_type, start_date, end_date, status, file_size, rows_count, created_at, completed_at FROM nm_report_downloads WHERE report_type = ? AND start_date = ? AND end_date = ? ORDER BY created_at DESC LIMIT 1",
		reportType, startDate, endDate,
	).Scan(&rec.ID, &rec.ReportType, &rec.StartDate, &rec.EndDate, &rec.Status, &rec.FileSize, &rec.RowsCount, &rec.CreatedAt, &completedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get nm report: %w", err)
	}

	if completedAt.Valid {
		rec.CompletedAt = completedAt.String
	}
	return &rec, nil
}

// SaveNmReport inserts a new report tracking record.
func (r *SQLiteSalesRepository) SaveNmReport(ctx context.Context, record nmreport.NmReportRecord) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO nm_report_downloads (id, report_type, start_date, end_date, status, file_size, rows_count, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		record.ID, record.ReportType, record.StartDate, record.EndDate, record.Status, record.FileSize, record.RowsCount, record.CreatedAt, record.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("save nm report: %w", err)
	}
	return nil
}

// UpdateNmReportStatus updates the status and row count of a report.
func (r *SQLiteSalesRepository) UpdateNmReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error {
	completedAt := ""
	if status == "SUCCESS" {
		completedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE nm_report_downloads SET status = ?, rows_count = ?, completed_at = ? WHERE id = ?",
		status, rowsCount, completedAt, downloadID,
	)
	if err != nil {
		return fmt.Errorf("update nm report status: %w", err)
	}
	return nil
}

// SaveFunnelMetricsDetail saves DETAIL funnel rows using refresh window pattern.
// Recent dates (within refreshDays) → INSERT OR REPLACE, old dates → INSERT OR IGNORE.
func (r *SQLiteSalesRepository) SaveFunnelMetricsDetail(ctx context.Context, rows []nmreport.FunnelDetailRow, refreshDays int) error {
	if len(rows) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	cutoffDate := time.Now().AddDate(0, 0, -refreshDays)

	stmtReplace, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO funnel_metrics_daily (
			nm_id, metric_date,
			open_count, cart_count, order_count, buyout_count, add_to_wishlist,
			order_sum, buyout_sum, cancel_count, cancel_sum_rub,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare replace: %w", err)
	}
	defer stmtReplace.Close()

	stmtIgnore, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO funnel_metrics_daily (
			nm_id, metric_date,
			open_count, cart_count, order_count, buyout_count, add_to_wishlist,
			order_sum, buyout_sum, cancel_count, cancel_sum_rub,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare ignore: %w", err)
	}
	defer stmtIgnore.Close()

	for _, row := range rows {
		metricDate, err := time.Parse("2006-01-02", row.MetricDate)
		if err != nil {
			return fmt.Errorf("parse date %s: %w", row.MetricDate, err)
		}

		var stmt *sql.Stmt
		if refreshDays <= 0 || metricDate.After(cutoffDate) || metricDate.Equal(cutoffDate) {
			stmt = stmtReplace
		} else {
			stmt = stmtIgnore
		}

		_, err = stmt.ExecContext(ctx,
			row.NmID, row.MetricDate,
			row.OpenCardCount, row.AddToCartCount, row.OrdersCount, row.BuyoutsCount, row.AddToWishlist,
			row.OrdersSumRub, row.BuyoutsSumRub, row.CancelCount, row.CancelSumRub,
			row.AddToCartConversion, row.CartToOrderConversion, row.BuyoutPercent,
		)
		if err != nil {
			return fmt.Errorf("save row nm_id=%d date=%s: %w", row.NmID, row.MetricDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SaveFunnelMetricsGrouped saves GROUPED funnel rows via INSERT OR REPLACE.
func (r *SQLiteSalesRepository) SaveFunnelMetricsGrouped(ctx context.Context, rows []nmreport.FunnelGroupedRow) error {
	if len(rows) == 0 {
		return nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO funnel_metrics_grouped_daily (
			metric_date,
			open_card_count, add_to_cart_count, orders_count, orders_sum_rub,
			buyouts_count, buyouts_sum_rub, cancel_count, cancel_sum_rub,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout,
			add_to_wishlist
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err = stmt.ExecContext(ctx,
			row.MetricDate,
			row.OpenCardCount, row.AddToCartCount, row.OrdersCount, row.OrdersSumRub,
			row.BuyoutsCount, row.BuyoutsSumRub, row.CancelCount, row.CancelSumRub,
			row.AddToCartConversion, row.CartToOrderConversion, row.BuyoutPercent,
			row.AddToWishlist,
		)
		if err != nil {
			return fmt.Errorf("save grouped row date=%s: %w", row.MetricDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
