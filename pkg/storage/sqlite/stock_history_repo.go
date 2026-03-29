// Package sqlite provides SQLite storage implementation for stock history data.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// StockHistoryReport represents metadata for a stock history CSV report.
type StockHistoryReport struct {
	ID         string
	ReportType string
	StartDate  string
	EndDate    string
	StockType  string
	Status     string
	FileSize   int64
	CreatedAt  string
	DownloadAt *string
	RowsCount  int
}

// SaveStockHistoryReport saves report metadata to database.
// Uses INSERT OR REPLACE for idempotency (resume mode).
func (r *SQLiteSalesRepository) SaveStockHistoryReport(ctx context.Context, report *StockHistoryReport) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO stock_history_reports
		(id, report_type, start_date, end_date, stock_type, status, file_size, created_at, downloaded_at, rows_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	var downloadAt interface{}
	if report.DownloadAt != nil {
		downloadAt = *report.DownloadAt
	} else {
		downloadAt = nil
	}

	_, err = stmt.ExecContext(ctx,
		report.ID, report.ReportType, report.StartDate, report.EndDate, report.StockType,
		report.Status, report.FileSize, report.CreatedAt, downloadAt, report.RowsCount,
	)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	return tx.Commit()
}

// GetStockHistoryReport retrieves report by parameters.
func (r *SQLiteSalesRepository) GetStockHistoryReport(ctx context.Context, reportType, startDate, endDate, stockType string) (*StockHistoryReport, error) {
	var report StockHistoryReport

	err := r.db.QueryRowContext(ctx, `
		SELECT id, report_type, start_date, end_date, stock_type, status, file_size, created_at, downloaded_at, rows_count
		FROM stock_history_reports
		WHERE report_type = ? AND start_date = ? AND end_date = ? AND stock_type = ?
	`, reportType, startDate, endDate, stockType).Scan(
		&report.ID, &report.ReportType, &report.StartDate, &report.EndDate, &report.StockType,
		&report.Status, &report.FileSize, &report.CreatedAt, &report.DownloadAt, &report.RowsCount,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	return &report, nil
}

// StockHistoryMetricRow represents a row from stock_history_metrics table.
type StockHistoryMetricRow struct {
	ReportID        string
	VendorCode      *string
	Name            *string
	NmID            int64
	SubjectName     *string
	BrandName       *string
	SizeName        *string
	ChrtID          *int
	RegionName      *string
	OfficeName      *string
	Availability    *string
	OrdersCount     *int
	OrdersSum       *int
	BuyoutCount     *int
	BuyoutSum       *int
	BuyoutPercent   *int
	AvgOrders       *float64
	StockCount      *int
	StockSum        *int
	SaleRate        *int
	AvgStockTurnover *int
	ToClientCount   *int
	FromClientCount *int
	Price           *int
	OfficeMissingTime *int
	LostOrdersCount *float64
	LostOrdersSum   *float64
	LostBuyoutsCount *float64
	LostBuyoutsSum  *float64
	MonthlyData     *string // JSON
	Currency        *string
}

// SaveStockHistoryMetrics saves metrics rows to database.
func (r *SQLiteSalesRepository) SaveStockHistoryMetrics(ctx context.Context, rows []StockHistoryMetricRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO stock_history_metrics
		(report_id, vendor_code, name, nm_id, subject_name, brand_name, size_name, chrt_id,
		 region_name, office_name, availability,
		 orders_count, orders_sum, buyout_count, buyout_sum, buyout_percent,
		 avg_orders, stock_count, stock_sum, sale_rate, avg_stock_turnover,
		 to_client_count, from_client_count, price, office_missing_time,
		 lost_orders_count, lost_orders_sum, lost_buyouts_count, lost_buyouts_sum,
		 monthly_data, currency)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err = stmt.ExecContext(ctx,
			row.ReportID, row.VendorCode, row.Name, row.NmID, row.SubjectName, row.BrandName,
			row.SizeName, row.ChrtID, row.RegionName, row.OfficeName, row.Availability,
			row.OrdersCount, row.OrdersSum, row.BuyoutCount, row.BuyoutSum, row.BuyoutPercent,
			row.AvgOrders, row.StockCount, row.StockSum, row.SaleRate, row.AvgStockTurnover,
			row.ToClientCount, row.FromClientCount, row.Price, row.OfficeMissingTime,
			row.LostOrdersCount, row.LostOrdersSum, row.LostBuyoutsCount, row.LostBuyoutsSum,
			row.MonthlyData, row.Currency,
		)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	return tx.Commit()
}

// StockHistoryDailyRow represents a row from stock_history_daily table.
type StockHistoryDailyRow struct {
	ReportID    string
	VendorCode  *string
	Name        *string
	NmID        int64
	SubjectName *string
	BrandName   *string
	SizeName    *string
	ChrtID      *int
	OfficeName  *string
	DailyData   *string // JSON
}

// SaveStockHistoryDaily saves daily rows to database.
func (r *SQLiteSalesRepository) SaveStockHistoryDaily(ctx context.Context, rows []StockHistoryDailyRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO stock_history_daily
		(report_id, vendor_code, name, nm_id, subject_name, brand_name, size_name, chrt_id, office_name, daily_data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err = stmt.ExecContext(ctx,
			row.ReportID, row.VendorCode, row.Name, row.NmID, row.SubjectName,
			row.BrandName, row.SizeName, row.ChrtID, row.OfficeName, row.DailyData,
		)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateStockHistoryReportStatus updates report status and metadata.
func (r *SQLiteSalesRepository) UpdateStockHistoryReportStatus(ctx context.Context, reportID, status string, rowsCount int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE stock_history_reports
		SET status = ?, rows_count = ?, downloaded_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, rowsCount, reportID)

	return err
}

// ListStockHistoryReports lists reports by type and date range.
func (r *SQLiteSalesRepository) ListStockHistoryReports(ctx context.Context, reportType string, startDate, endDate string) ([]StockHistoryReport, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, report_type, start_date, end_date, stock_type, status, file_size, created_at, downloaded_at, rows_count
		FROM stock_history_reports
		WHERE report_type = ? AND start_date >= ? AND end_date <= ?
		ORDER BY created_at DESC
	`, reportType, startDate, endDate)

	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var reports []StockHistoryReport
	for rows.Next() {
		var r StockHistoryReport
		err := rows.Scan(&r.ID, &r.ReportType, &r.StartDate, &r.EndDate, &r.StockType,
			&r.Status, &r.FileSize, &r.CreatedAt, &r.DownloadAt, &r.RowsCount)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		reports = append(reports, r)
	}

	return reports, nil
}

// CountStockHistoryMetrics counts metrics rows for a report.
func (r *SQLiteSalesRepository) CountStockHistoryMetrics(ctx context.Context, reportID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stock_history_metrics WHERE report_id = ?
	`, reportID).Scan(&count)

	return count, err
}

// CountStockHistoryDaily counts daily rows for a report.
func (r *SQLiteSalesRepository) CountStockHistoryDaily(ctx context.Context, reportID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stock_history_daily WHERE report_id = ?
	`, reportID).Scan(&count)

	return count, err
}

// ParseMonthlyDataJSON parses JSON string into map[string]float64.
func ParseMonthlyDataJSON(data *string) map[string]float64 {
	if data == nil || *data == "" {
		return nil
	}

	var result map[string]float64
	if err := json.Unmarshal([]byte(*data), &result); err != nil {
		return nil
	}

	return result
}

// ParseDailyDataJSON parses JSON string into map[string]int64.
func ParseDailyDataJSON(data *string) map[string]int64 {
	if data == nil || *data == "" {
		return nil
	}

	var result map[string]int64
	if err := json.Unmarshal([]byte(*data), &result); err != nil {
		return nil
	}

	return result
}
