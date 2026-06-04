// Package sqlite provides SQLite storage implementation for stock history data.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"github.com/ilkoid/poncho-ai/pkg/stockhistory"
)

// Compile-time assertion: SQLiteSalesRepository implements stockhistory.StockHistoryWriter.
var _ stockhistory.StockHistoryWriter = (*SQLiteSalesRepository)(nil)

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
// Deletes child records for existing report before REPLACE to avoid FOREIGN KEY constraint.
// Retries on "database is locked" (concurrent writers from other downloaders).
func (r *SQLiteSalesRepository) SaveStockHistoryReport(ctx context.Context, report *StockHistoryReport) error {
	return retryOnBusy(ctx, func() error {
		return r.saveStockHistoryReport(ctx, report)
	})
}

func (r *SQLiteSalesRepository) saveStockHistoryReport(ctx context.Context, report *StockHistoryReport) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var oldID string
	_ = tx.QueryRowContext(ctx,
		`SELECT id FROM stock_history_reports WHERE report_type = ? AND start_date = ? AND end_date = ? AND stock_type = ?`,
		report.ReportType, report.StartDate, report.EndDate, report.StockType,
	).Scan(&oldID)
	if oldID != "" && oldID != report.ID {
		if _, err := tx.ExecContext(ctx, `DELETE FROM stock_history_daily WHERE report_id = ?`, oldID); err != nil {
			return fmt.Errorf("delete old daily rows: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stock_history_metrics WHERE report_id = ?`, oldID); err != nil {
			return fmt.Errorf("delete old metrics rows: %w", err)
		}
	}

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
// Retries on "database is locked" (concurrent writers from other downloaders).
func (r *SQLiteSalesRepository) SaveStockHistoryMetrics(ctx context.Context, rows []StockHistoryMetricRow) error {
	if len(rows) == 0 {
		return nil
	}
	return retryOnBusy(ctx, func() error {
		return r.saveStockHistoryMetrics(ctx, rows)
	})
}

func (r *SQLiteSalesRepository) saveStockHistoryMetrics(ctx context.Context, rows []StockHistoryMetricRow) error {
	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

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

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

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

// ============================================================================
// V2 adapter methods (stockhistory.StockHistoryWriter interface)
// Convert domain types → SQLite DB types, delegate to existing methods.
// ============================================================================

// GetReport wraps GetStockHistoryReport, converting to domain type.
func (r *SQLiteSalesRepository) GetReport(ctx context.Context, reportType, startDate, endDate, stockType string) (*stockhistory.ReportRecord, error) {
	report, err := r.GetStockHistoryReport(ctx, reportType, startDate, endDate, stockType)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, nil
	}
	return &stockhistory.ReportRecord{
		ID:           report.ID,
		ReportType:   report.ReportType,
		StartDate:    report.StartDate,
		EndDate:      report.EndDate,
		StockType:    report.StockType,
		Status:       report.Status,
		FileSize:     report.FileSize,
		RowsCount:    report.RowsCount,
		CreatedAt:    report.CreatedAt,
		DownloadedAt: shDerefStr(report.DownloadAt),
	}, nil
}

// SaveReport converts domain type to DB type and delegates.
func (r *SQLiteSalesRepository) SaveReport(ctx context.Context, record stockhistory.ReportRecord) error {
	report := &StockHistoryReport{
		ID:         record.ID,
		ReportType: record.ReportType,
		StartDate:  record.StartDate,
		EndDate:    record.EndDate,
		StockType:  record.StockType,
		Status:     record.Status,
		FileSize:   record.FileSize,
		RowsCount:  record.RowsCount,
		CreatedAt:  record.CreatedAt,
	}
	if record.DownloadedAt != "" {
		report.DownloadAt = &record.DownloadedAt
	}
	return r.SaveStockHistoryReport(ctx, report)
}

// UpdateReportStatus delegates to existing method.
func (r *SQLiteSalesRepository) UpdateReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error {
	return r.UpdateStockHistoryReportStatus(ctx, downloadID, status, rowsCount)
}

// SaveMetrics converts domain rows to DB rows and delegates.
func (r *SQLiteSalesRepository) SaveMetrics(ctx context.Context, rows []stockhistory.MetricRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	dbRows := make([]StockHistoryMetricRow, len(rows))
	for i, r := range rows {
		dbRows[i] = StockHistoryMetricRow{
			ReportID:         r.ReportID,
			VendorCode:       shStrPtr(r.VendorCode),
			Name:             shStrPtr(r.Name),
			NmID:             r.NmID,
			SubjectName:      shStrPtr(r.SubjectName),
			BrandName:        shStrPtr(r.BrandName),
			SizeName:         shStrPtr(r.SizeName),
			ChrtID:           shIntPtr(r.ChrtID),
			RegionName:       shStrPtr(r.RegionName),
			OfficeName:       shStrPtr(r.OfficeName),
			Availability:     shStrPtr(r.Availability),
			OrdersCount:      r.OrdersCount,
			OrdersSum:        r.OrdersSum,
			BuyoutCount:      r.BuyoutCount,
			BuyoutSum:        r.BuyoutSum,
			BuyoutPercent:    r.BuyoutPercent,
			AvgOrders:        r.AvgOrders,
			StockCount:       r.StockCount,
			StockSum:         r.StockSum,
			SaleRate:         r.SaleRate,
			AvgStockTurnover: r.AvgStockTurnover,
			ToClientCount:    r.ToClientCount,
			FromClientCount:  r.FromClientCount,
			Price:            r.Price,
			OfficeMissingTime: r.OfficeMissingTime,
			LostOrdersCount:  r.LostOrdersCount,
			LostOrdersSum:    r.LostOrdersSum,
			LostBuyoutsCount: r.LostBuyoutsCount,
			LostBuyoutsSum:   r.LostBuyoutsSum,
			MonthlyData:      shMonthlyDataToJSON(r.MonthlyData),
			Currency:         shStrPtr(r.Currency),
		}
	}
	if err := r.SaveStockHistoryMetrics(ctx, dbRows); err != nil {
		return 0, err
	}
	return len(dbRows), nil
}

// SaveDaily converts domain rows to DB rows and delegates.
func (r *SQLiteSalesRepository) SaveDaily(ctx context.Context, rows []stockhistory.DailyRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	dbRows := make([]StockHistoryDailyRow, len(rows))
	for i, r := range rows {
		dbRows[i] = StockHistoryDailyRow{
			ReportID:    r.ReportID,
			VendorCode:  shStrPtr(r.VendorCode),
			Name:        shStrPtr(r.Name),
			NmID:        r.NmID,
			SubjectName: shStrPtr(r.SubjectName),
			BrandName:   shStrPtr(r.BrandName),
			SizeName:    shStrPtr(r.SizeName),
			ChrtID:      shIntPtr(r.ChrtID),
			OfficeName:  shStrPtr(r.OfficeName),
			DailyData:   shDailyDataToJSON(r.DailyData),
		}
	}
	if err := r.SaveStockHistoryDaily(ctx, dbRows); err != nil {
		return 0, err
	}
	return len(dbRows), nil
}

// ============================================================================
// V2 adapter helpers
// ============================================================================

// shStrPtr returns *string for non-empty strings, nil otherwise.
func shStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// shIntPtr returns *int for non-zero int64, nil otherwise.
func shIntPtr(i int64) *int {
	if i == 0 {
		return nil
	}
	v := int(i)
	return &v
}

// shDerefStr dereferences a *string, returning "" for nil.
func shDerefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// shMonthlyDataToJSON converts map to JSON *string for DB storage.
func shMonthlyDataToJSON(data map[string]float64) *string {
	if len(data) == 0 {
		return nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	s := string(bytes)
	return &s
}

// shDailyDataToJSON converts map to JSON *string for DB storage.
func shDailyDataToJSON(data map[string]int64) *string {
	if len(data) == 0 {
		return nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	s := string(bytes)
	return &s
}
