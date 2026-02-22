// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Save saves batch of sales rows to storage.
// Uses INSERT OR IGNORE to skip duplicates by rrd_id.
// Returns error if database operation fails.
func (r *SQLiteSalesRepository) Save(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Using INSERT OR IGNORE for idempotency (resume support)
	// If rrd_id already exists, row is skipped without error
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO sales (
			rrd_id, realizationreport_id, nm_id, supplier_article, barcode,
			brand_name, subject_name, ts_name, doc_type_name, quantity,
			retail_price, retail_amount, sale_percent, commission_percent,
			ppvz_for_pay, delivery_rub, delivery_method, gi_box_type_name,
			office_name, order_dt, sale_dt, rr_dt, is_cancel, cancel_dt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		// Convert bool to integer for SQLite
		isCancel := 0
		if row.IsCancel {
			isCancel = 1
		}

		var cancelDT sql.NullString
		if row.CancelDateTime != nil {
			cancelDT = sql.NullString{String: *row.CancelDateTime, Valid: true}
		}

		_, err := stmt.ExecContext(ctx,
			row.RrdID,
			0,                   // realizationreport_id - not in our type yet
			row.NmID,
			row.SupplierArticle,
			row.Barcode,         // barcode from API
			row.BrandName,
			row.SubjectName,
			row.TechSize,        // ts_name = размер
			row.DocTypeName,
			row.Quantity,
			row.RetailPrice,
			row.RetailAmount,
			row.SalePercent,
			row.CommissionPercent,
			row.PPVzForPay,
			row.DeliveryRub,
			row.DeliveryMethod,
			row.GiBoxTypeName,
			row.OfficeName,
			row.OrderDT,
			row.SaleDT,
			row.RRDT,
			isCancel,
			cancelDT,
		)
		if err != nil {
			return fmt.Errorf("insert row rrd_id=%d: %w", row.RrdID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SaveServiceRecords saves batch of service records (logistics, deductions) to storage.
// Uses INSERT OR IGNORE to skip duplicates by rrd_id.
// Returns error if database operation fails.
func (r *SQLiteSalesRepository) SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Using INSERT OR IGNORE for idempotency (resume support)
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO service_records (
			rrd_id, realizationreport_id, supplier_oper_name,
			nm_id, supplier_article, brand_name, subject_name,
			barcode, shk_id, srid,
			delivery_method, gi_box_type_name, delivery_rub,
			ppvz_vw, ppvz_vw_nds, rebill_logistic_cost,
			rr_dt, order_dt, sale_dt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.RrdID,
			0, // realizationreport_id - not in our type yet
			row.SupplierOperName,
			row.NmID,
			row.SupplierArticle,
			row.BrandName,
			row.SubjectName,
			row.Barcode,
			row.ShkID,
			row.Srid,
			row.DeliveryMethod,
			row.GiBoxTypeName,
			row.DeliveryRub,
			row.PPVzVw,
			row.PPVzVwNds,
			row.RebillLogisticCost,
			row.RRDT,
			row.OrderDT,
			row.SaleDT,
		)
		if err != nil {
			return fmt.Errorf("insert service row rrd_id=%d: %w", row.RrdID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// Exists checks if row with given rrdID already exists.
// Used for resume functionality after interruption.
func (r *SQLiteSalesRepository) Exists(ctx context.Context, rrdID int) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM sales WHERE rrd_id = ?)",
		rrdID,
	).Scan(&exists)

	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check exists rrd_id=%d: %w", rrdID, err)
	}

	return exists, nil
}

// GetFBWOnly returns only FBW sales (filtered by gi_box_type_name).
// FBW = Fulfillment by Wildberries (товар на складах WB).
// Returns empty slice if no FBW sales found.
//
// FBW filter logic (based on real API data analysis):
// - gi_box_type_name IN ('Микс', 'Без коробов', 'Моно') = FBW
// - Empty gi_box_type_name = likely FBS (seller warehouse)
func (r *SQLiteSalesRepository) GetFBWOnly(ctx context.Context) ([]wb.RealizationReportRow, error) {
	// FBW filter: товары с непустым gi_box_type_name (склады WB)
	// Используем только существующие столбцы из таблицы
	query := `
		SELECT rrd_id, nm_id, supplier_article, brand_name, subject_name,
		       quantity, is_cancel, cancel_dt,
		       doc_type_name, order_dt, sale_dt
		FROM sales
		WHERE gi_box_type_name IS NOT NULL AND LENGTH(gi_box_type_name) > 0
		ORDER BY sale_dt DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query fbw sales: %w", err)
	}
	defer rows.Close()

	var result []wb.RealizationReportRow
	for rows.Next() {
		var row wb.RealizationReportRow
		var cancelDT sql.NullString
		var orderDT, saleDT sql.NullString

		err := rows.Scan(
			&row.RrdID,
			&row.NmID,
			&row.SupplierArticle,
			&row.BrandName,
			&row.SubjectName,
			&row.Quantity,
			&row.IsCancel,
			&cancelDT,
			&row.DocTypeName,
			&orderDT,
			&saleDT,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		if cancelDT.Valid {
			row.CancelDateTime = &cancelDT.String
		}
		if orderDT.Valid {
			row.OrderDT = orderDT.String
		}
		if saleDT.Valid {
			row.SaleDT = saleDT.String
		}

		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return result, nil
}

// Count returns total number of sales in database.
func (r *SQLiteSalesRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sales").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count sales: %w", err)
	}
	return count, nil
}

// MaxRrdID returns the maximum rrd_id in database.
// Used for pagination resume point detection.
func (r *SQLiteSalesRepository) MaxRrdID(ctx context.Context) (int, error) {
	var maxID sql.NullInt64
	err := r.db.QueryRowContext(ctx, "SELECT MAX(rrd_id) FROM sales").Scan(&maxID)
	if err != nil {
		return 0, fmt.Errorf("max rrd_id: %w", err)
	}
	if !maxID.Valid {
		return 0, nil // Empty table
	}
	return int(maxID.Int64), nil
}

// GetDeliveryMethods returns distinct gi_box_type_name values.
// Useful for analyzing actual FBW vs FBS values from API.
// Note: delivery_method field does not exist in WB API response.
// We use gi_box_type_name as proxy for delivery method:
// - "Микс", "Без коробов", "Моно" = FBW (склады WB)
// - Empty = likely FBS (склад продавца)
func (r *SQLiteSalesRepository) GetDeliveryMethods(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT DISTINCT gi_box_type_name FROM sales ORDER BY gi_box_type_name")
	if err != nil {
		return nil, fmt.Errorf("query gi_box_type_name: %w", err)
	}
	defer rows.Close()

	var methods []string
	for rows.Next() {
		var method string
		if err := rows.Scan(&method); err != nil {
			return nil, fmt.Errorf("scan method: %w", err)
		}
		// Show empty string as "(пусто)"
		if method == "" {
			method = "(пусто - возможно FBS)"
		}
		methods = append(methods, method)
	}

	return methods, rows.Err()
}

// GetLastSaleDT returns timestamp of the last sale in database.
// For smart resume: start loading from this moment + 1 second.
// Returns zero time if database is empty.
//
// IMPORTANT: Uses MAX(rr_dt) not MAX(sale_dt) because WB API filters by report date (rr_dt).
// This ensures resume logic correctly continues from where API actually stopped.
func (r *SQLiteSalesRepository) GetLastSaleDT(ctx context.Context) (time.Time, error) {
	var lastDT sql.NullString
	err := r.db.QueryRowContext(ctx,
		"SELECT MAX(rr_dt) FROM sales",
	).Scan(&lastDT)
	if err != nil {
		return time.Time{}, fmt.Errorf("get last rr_dt: %w", err)
	}
	if !lastDT.Valid {
		return time.Time{}, nil // Empty database
	}
	// rr_dt is stored in RFC3339 format (e.g., "2026-02-17T23:59:00+03:00")
	return time.Parse(time.RFC3339, lastDT.String)
}

// ServiceRecordStats holds statistics about service records.
type ServiceRecordStats struct {
	Total        int
	ByOperation  map[string]int // Count by supplier_oper_name
}

// GetServiceRecordStats returns statistics about service records.
func (r *SQLiteSalesRepository) GetServiceRecordStats(ctx context.Context) (*ServiceRecordStats, error) {
	stats := &ServiceRecordStats{
		ByOperation: make(map[string]int),
	}

	// Get total count
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM service_records",
	).Scan(&stats.Total)
	if err != nil {
		return nil, fmt.Errorf("count service_records: %w", err)
	}

	// Get counts by operation type
	rows, err := r.db.QueryContext(ctx,
		"SELECT supplier_oper_name, COUNT(*) FROM service_records GROUP BY supplier_oper_name",
	)
	if err != nil {
		return nil, fmt.Errorf("group by supplier_oper_name: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var operName sql.NullString
		var count int
		if err := rows.Scan(&operName, &count); err != nil {
			return nil, fmt.Errorf("scan operation stats: %w", err)
		}
		name := operName.String
		if !operName.Valid || name == "" {
			name = "(пусто)"
		}
		stats.ByOperation[name] = count
	}

	return stats, rows.Err()
}

// CountServiceRecords returns total number of service records.
func (r *SQLiteSalesRepository) CountServiceRecords(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM service_records",
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count service_records: %w", err)
	}
	return count, nil
}
