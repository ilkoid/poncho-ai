package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/stockhistory"
)

// Compile-time assertion: PgStockHistoryRepo implements stockhistory.StockHistoryWriter.
var _ stockhistory.StockHistoryWriter = (*PgStockHistoryRepo)(nil)

// PgStockHistoryRepo implements stockhistory.StockHistoryWriter for PostgreSQL.
type PgStockHistoryRepo struct {
	pool *pgxpool.Pool
}

// NewPgStockHistoryRepo creates a new PostgreSQL stock history repository.
func NewPgStockHistoryRepo(pool *pgxpool.Pool) *PgStockHistoryRepo {
	return &PgStockHistoryRepo{pool: pool}
}

// InitSchema creates stock history tables if they don't exist.
func (r *PgStockHistoryRepo) InitSchema(ctx context.Context) error {
	return initStockHistorySchema(ctx, r.pool)
}

// ============================================================================
// Writer methods
// ============================================================================

const pgStockHistChunkSize = 500

// GetReport looks up an existing report record by type, dates, and stock type.
func (r *PgStockHistoryRepo) GetReport(ctx context.Context, reportType, startDate, endDate, stockType string) (*stockhistory.ReportRecord, error) {
	var rec stockhistory.ReportRecord
	var downloadedAt *string // nullable

	err := r.pool.QueryRow(ctx,
		"SELECT id, report_type, start_date, end_date, stock_type, status, file_size, "+
			"created_at, downloaded_at, rows_count "+
			"FROM stock_history_reports "+
			"WHERE report_type = $1 AND start_date = $2 AND end_date = $3 AND stock_type = $4 "+
			"ORDER BY created_at DESC LIMIT 1",
		reportType, startDate, endDate, stockType,
	).Scan(&rec.ID, &rec.ReportType, &rec.StartDate, &rec.EndDate, &rec.StockType,
		&rec.Status, &rec.FileSize, &rec.CreatedAt, &downloadedAt, &rec.RowsCount)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get stock history report: %w", err)
	}

	if downloadedAt != nil {
		rec.DownloadedAt = *downloadedAt
	}
	return &rec, nil
}

// SaveReport inserts or updates a report tracking record.
// Transactional: deletes old child rows (metrics + daily) if the report ID changed
// for the same (report_type, start_date, end_date, stock_type) key.
func (r *PgStockHistoryRepo) SaveReport(ctx context.Context, record stockhistory.ReportRecord) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Delete old child rows if a different report ID exists for the same key
	var oldID string
	_ = tx.QueryRow(ctx,
		"SELECT id FROM stock_history_reports WHERE report_type=$1 AND start_date=$2 AND end_date=$3 AND stock_type=$4",
		record.ReportType, record.StartDate, record.EndDate, record.StockType,
	).Scan(&oldID)
	if oldID != "" && oldID != record.ID {
		if _, err := tx.Exec(ctx, "DELETE FROM stock_history_daily WHERE report_id = $1", oldID); err != nil {
			return fmt.Errorf("delete old daily rows: %w", err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM stock_history_metrics WHERE report_id = $1", oldID); err != nil {
			return fmt.Errorf("delete old metrics rows: %w", err)
		}
	}

	_, err = tx.Exec(ctx, insertSHReportSQL,
		record.ID, record.ReportType, record.StartDate, record.EndDate, record.StockType,
		record.Status, record.FileSize, record.CreatedAt, record.DownloadedAt, record.RowsCount,
	)
	if err != nil {
		return fmt.Errorf("save stock history report: %w", err)
	}

	return tx.Commit(ctx)
}

// UpdateReportStatus updates the status, row count, and download timestamp.
func (r *PgStockHistoryRepo) UpdateReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error {
	downloadedAt := ""
	if status == "SUCCESS" {
		downloadedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE stock_history_reports SET status=$1, rows_count=$2, downloaded_at=$3 WHERE id=$4",
		status, rowsCount, downloadedAt, downloadID,
	)
	if err != nil {
		return fmt.Errorf("update stock history report status: %w", err)
	}
	return nil
}

// SaveMetrics saves metrics rows in chunks of 500 using multi-row INSERT.
// Converts domain types to DB types (pointer -> nullable, map -> JSON).
func (r *PgStockHistoryRepo) SaveMetrics(ctx context.Context, rows []stockhistory.MetricRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	saved := 0
	for i := 0; i < len(rows); i += pgStockHistChunkSize {
		end := min(i+pgStockHistChunkSize, len(rows))
		chunk := rows[i:end]

		n, err := r.saveMetricsChunk(ctx, chunk)
		if err != nil {
			return saved, fmt.Errorf("save metrics chunk at offset %d: %w", i, err)
		}
		saved += n
	}

	return saved, nil
}

// saveMetricsChunk saves up to 500 metrics rows using a single multi-row INSERT.
func (r *PgStockHistoryRepo) saveMetricsChunk(ctx context.Context, chunk []stockhistory.MetricRow) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSHMetricsCols)
	for _, row := range chunk {
		monthlyJSON, _ := json.Marshal(row.MonthlyData)
		monthlyStr := string(monthlyJSON)
		if len(row.MonthlyData) == 0 {
			monthlyStr = ""
		}

		args = append(args,
			row.ReportID, pgStr(row.VendorCode), pgStr(row.Name), row.NmID,
			pgStr(row.SubjectName), pgStr(row.BrandName), pgStr(row.SizeName), row.ChrtID,
			pgStr(row.RegionName), pgStr(row.OfficeName), pgStr(row.Availability),
			row.OrdersCount, row.OrdersSum, row.BuyoutCount, row.BuyoutSum, row.BuyoutPercent,
			row.AvgOrders, row.StockCount, row.StockSum, row.SaleRate, row.AvgStockTurnover,
			row.ToClientCount, row.FromClientCount, row.Price, row.OfficeMissingTime,
			row.LostOrdersCount, row.LostOrdersSum, row.LostBuyoutsCount, row.LostBuyoutsSum,
			monthlyStr, pgStr(row.Currency),
		)
	}

	query := insertSHMetricsFullChunkSQL
	if len(chunk) < pgStockHistChunkSize {
		query = BuildMultiRowInsert(insertSHMetricsPrefixSQL, insertSHMetricsOnConflictSQL, len(chunk), insertSHMetricsCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save metrics batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// SaveDaily saves daily rows in chunks of 500 using multi-row INSERT.
// Converts domain types to DB types (map -> JSON).
func (r *PgStockHistoryRepo) SaveDaily(ctx context.Context, rows []stockhistory.DailyRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	saved := 0
	for i := 0; i < len(rows); i += pgStockHistChunkSize {
		end := min(i+pgStockHistChunkSize, len(rows))
		chunk := rows[i:end]

		n, err := r.saveDailyChunk(ctx, chunk)
		if err != nil {
			return saved, fmt.Errorf("save daily chunk at offset %d: %w", i, err)
		}
		saved += n
	}

	return saved, nil
}

// saveDailyChunk saves up to 500 daily rows using a single multi-row INSERT.
func (r *PgStockHistoryRepo) saveDailyChunk(ctx context.Context, chunk []stockhistory.DailyRow) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSHDailyCols)
	for _, row := range chunk {
		dailyJSON, _ := json.Marshal(row.DailyData)
		dailyStr := string(dailyJSON)
		if len(row.DailyData) == 0 {
			dailyStr = ""
		}

		args = append(args,
			row.ReportID, pgStr(row.VendorCode), pgStr(row.Name), row.NmID,
			pgStr(row.SubjectName), pgStr(row.BrandName), pgStr(row.SizeName), row.ChrtID,
			pgStr(row.OfficeName),
			dailyStr,
		)
	}

	query := insertSHDailyFullChunkSQL
	if len(chunk) < pgStockHistChunkSize {
		query = BuildMultiRowInsert(insertSHDailyPrefixSQL, insertSHDailyOnConflictSQL, len(chunk), insertSHDailyCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save daily batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// ============================================================================
// SQL statements
// ============================================================================

// Single-row upsert for stock_history_reports (used by SaveReport only).
var insertSHReportSQL = `
INSERT INTO stock_history_reports (
    id, report_type, start_date, end_date, stock_type,
    status, file_size, created_at, downloaded_at, rows_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (report_type, start_date, end_date, stock_type) DO UPDATE SET
    id = EXCLUDED.id,
    status = EXCLUDED.status,
    file_size = EXCLUDED.file_size,
    created_at = EXCLUDED.created_at,
    downloaded_at = EXCLUDED.downloaded_at,
    rows_count = EXCLUDED.rows_count`

// Multi-row INSERT SQL fragments for stock_history_metrics — 31 columns.
// UNIQUE: (report_id, nm_id, chrt_id).
const (
	insertSHMetricsCols = 31

	insertSHMetricsPrefixSQL = `INSERT INTO stock_history_metrics (
    report_id, vendor_code, name, nm_id, subject_name, brand_name, size_name, chrt_id,
    region_name, office_name, availability,
    orders_count, orders_sum, buyout_count, buyout_sum, buyout_percent,
    avg_orders, stock_count, stock_sum, sale_rate, avg_stock_turnover,
    to_client_count, from_client_count, price, office_missing_time,
    lost_orders_count, lost_orders_sum, lost_buyouts_count, lost_buyouts_sum,
    monthly_data, currency
) VALUES `

	insertSHMetricsOnConflictSQL = `
ON CONFLICT (report_id, nm_id, chrt_id) DO UPDATE SET
    vendor_code = EXCLUDED.vendor_code,
    name = EXCLUDED.name,
    subject_name = EXCLUDED.subject_name,
    brand_name = EXCLUDED.brand_name,
    size_name = EXCLUDED.size_name,
    region_name = EXCLUDED.region_name,
    office_name = EXCLUDED.office_name,
    availability = EXCLUDED.availability,
    orders_count = EXCLUDED.orders_count,
    orders_sum = EXCLUDED.orders_sum,
    buyout_count = EXCLUDED.buyout_count,
    buyout_sum = EXCLUDED.buyout_sum,
    buyout_percent = EXCLUDED.buyout_percent,
    avg_orders = EXCLUDED.avg_orders,
    stock_count = EXCLUDED.stock_count,
    stock_sum = EXCLUDED.stock_sum,
    sale_rate = EXCLUDED.sale_rate,
    avg_stock_turnover = EXCLUDED.avg_stock_turnover,
    to_client_count = EXCLUDED.to_client_count,
    from_client_count = EXCLUDED.from_client_count,
    price = EXCLUDED.price,
    office_missing_time = EXCLUDED.office_missing_time,
    lost_orders_count = EXCLUDED.lost_orders_count,
    lost_orders_sum = EXCLUDED.lost_orders_sum,
    lost_buyouts_count = EXCLUDED.lost_buyouts_count,
    lost_buyouts_sum = EXCLUDED.lost_buyouts_sum,
    monthly_data = EXCLUDED.monthly_data,
    currency = EXCLUDED.currency`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertSHMetricsFullChunkSQL = BuildMultiRowInsert(insertSHMetricsPrefixSQL, insertSHMetricsOnConflictSQL, pgStockHistChunkSize, insertSHMetricsCols)

// Multi-row INSERT SQL fragments for stock_history_daily — 10 columns.
// UNIQUE: (report_id, nm_id, chrt_id).
const (
	insertSHDailyCols = 10

	insertSHDailyPrefixSQL = `INSERT INTO stock_history_daily (
    report_id, vendor_code, name, nm_id, subject_name, brand_name, size_name, chrt_id,
    office_name, daily_data
) VALUES `

	insertSHDailyOnConflictSQL = `
ON CONFLICT (report_id, nm_id, chrt_id) DO UPDATE SET
    vendor_code = EXCLUDED.vendor_code,
    name = EXCLUDED.name,
    subject_name = EXCLUDED.subject_name,
    brand_name = EXCLUDED.brand_name,
    size_name = EXCLUDED.size_name,
    office_name = EXCLUDED.office_name,
    daily_data = EXCLUDED.daily_data`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertSHDailyFullChunkSQL = BuildMultiRowInsert(insertSHDailyPrefixSQL, insertSHDailyOnConflictSQL, pgStockHistChunkSize, insertSHDailyCols)

// pgStr converts a string to *string for PG nullable columns.
// Returns nil for empty strings.
func pgStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
