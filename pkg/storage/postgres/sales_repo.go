package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/sales"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgSalesRepo implements sales.SalesWriter.
var _ sales.SalesWriter = (*PgSalesRepo)(nil)

// PgSalesRepo implements sales.SalesWriter for PostgreSQL.
// Focused repository (ISP) — only the 7 persistence methods needed by the downloader.
type PgSalesRepo struct {
	pool *pgxpool.Pool
}

// NewPgSalesRepo creates a new PostgreSQL sales repository.
func NewPgSalesRepo(pool *pgxpool.Pool) *PgSalesRepo {
	return &PgSalesRepo{pool: pool}
}

// InitSchema creates sales and service_records tables if they don't exist.
func (r *PgSalesRepo) InitSchema(ctx context.Context) error {
	if err := initSalesSchema(ctx, r.pool); err != nil {
		return err
	}
	return initServiceRecordsSchema(ctx, r.pool)
}

// GetLastSaleDT returns timestamp of the last sale by rr_dt.
func (r *PgSalesRepo) GetLastSaleDT(ctx context.Context) (time.Time, error) {
	var lastDT *string
	err := r.pool.QueryRow(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)
	if err != nil {
		return time.Time{}, fmt.Errorf("get last rr_dt: %w", err)
	}
	if lastDT == nil {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, *lastDT)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse rr_dt '%s': %w", *lastDT, err)
	}
	return t, nil
}

// GetFirstSaleDT returns timestamp of the earliest sale by rr_dt.
func (r *PgSalesRepo) GetFirstSaleDT(ctx context.Context) (time.Time, error) {
	var firstDT *string
	err := r.pool.QueryRow(ctx, "SELECT MIN(rr_dt) FROM sales").Scan(&firstDT)
	if err != nil {
		return time.Time{}, fmt.Errorf("get first rr_dt: %w", err)
	}
	if firstDT == nil {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, *firstDT)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse rr_dt '%s': %w", *firstDT, err)
	}
	return t, nil
}

// DeleteSalesByDateRange deletes all sales records within a date range.
func (r *PgSalesRepo) DeleteSalesByDateRange(ctx context.Context, from, to string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		"DELETE FROM sales WHERE rr_dt >= $1 AND rr_dt <= $2",
		from, to,
	)
	if err != nil {
		return 0, fmt.Errorf("delete sales by date range: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteServiceRecordsByDateRange deletes all service records within a date range.
func (r *PgSalesRepo) DeleteServiceRecordsByDateRange(ctx context.Context, from, to string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		"DELETE FROM service_records WHERE rr_dt >= $1 AND rr_dt <= $2",
		from, to,
	)
	if err != nil {
		return 0, fmt.Errorf("delete service records by date range: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Save saves batch of sales rows to storage.
// Uses ON CONFLICT (rrd_id) DO NOTHING for idempotency (resume support).
func (r *PgSalesRepo) Save(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}

	for i := 0; i < len(rows); i += salesChunkSize {
		end := i + salesChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		if err := r.saveSalesChunk(ctx, chunk); err != nil {
			return fmt.Errorf("save sales chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// SaveServiceRecords saves batch of service records to storage.
// Uses ON CONFLICT (rrd_id) DO NOTHING for idempotency.
func (r *PgSalesRepo) SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}

	for i := 0; i < len(rows); i += salesChunkSize {
		end := i + salesChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		if err := r.saveServiceRecordsChunk(ctx, chunk); err != nil {
			return fmt.Errorf("save service records chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

// Exists checks if a sale with the given rrd_id already exists.
func (r *PgSalesRepo) Exists(ctx context.Context, rrdID int) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM sales WHERE rrd_id = $1)",
		rrdID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check exists rrd_id=%d: %w", rrdID, err)
	}
	return exists, nil
}

const salesChunkSize = 500

// saveSalesChunk saves up to 500 sales rows using a single multi-row INSERT.
// 47 columns per row, ON CONFLICT (rrd_id) DO NOTHING.
func (r *PgSalesRepo) saveSalesChunk(ctx context.Context, chunk []wb.RealizationReportRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSaleRowCols)
	for _, row := range chunk {
		args = append(args,
			row.RrdID,
			row.RealizationReportID,
			row.NmID,
			row.SupplierArticle,
			row.Barcode,
			row.BrandName,
			row.SubjectName,
			row.TechSize,
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
			row.IsCancel,
			row.CancelDateTime,
			nullFloat64(row.PPVzSalesCommission),
			nullFloat64(row.AcquiringFee),
			nullFloat64(row.AcquiringPercent),
			nullFloat64(row.RetailPriceWithDiscRub),
			nullFloat64(row.PPVzSppPrc),
			nullFloat64(row.PPVzKvwPrcBase),
			nullFloat64(row.PPVzKvwPrc),
			nullFloat64(row.SupRatingPrcUp),
			nullFloat64(row.IsKgvpV2),
			nullFloat64(row.ProductDiscountForReport),
			nullFloat64(row.SupplierPromo),
			nullFloat64(row.SellerPromoDiscount),
			nullFloat64(row.SalePricePromocodeDiscPrc),
			nullFloat64(row.WibesWbDiscountPercent),
			nullFloat64(row.LoyaltyDiscount),
			nullFloat64(row.CashbackAmount),
			nullFloat64(row.CashbackDiscount),
			nullFloat64(row.CashbackCommissionChange),
			row.B2BCustomerTin,
			row.OrderUID,
			row.IsB2b,
			nullFloat64(row.SalePriceAffiliatedDiscountPrc),
			nullFloat64(row.SalePriceWholesaleDiscountPrc),
		)
	}

	query := insertSaleRowFullChunkSQL
	if len(chunk) < salesChunkSize {
		query = BuildMultiRowInsert(insertSaleRowPrefixSQL, insertSaleRowOnConflictSQL, len(chunk), insertSaleRowCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save sales batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// saveServiceRecordsChunk saves up to 500 service records using a single multi-row INSERT.
// 24 columns per row, ON CONFLICT (rrd_id) DO NOTHING.
func (r *PgSalesRepo) saveServiceRecordsChunk(ctx context.Context, chunk []wb.RealizationReportRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertServiceRecCols)
	for _, row := range chunk {
		args = append(args,
			row.RrdID,
			row.RealizationReportID,
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
			nullFloat64(row.Penalty),
			nullFloat64(row.Deduction),
			nullFloat64(row.StorageFee),
			nullFloat64(row.Acceptance),
			nullInt64(row.GiID),
			row.PPVzVw,
			row.PPVzVwNds,
			row.RebillLogisticCost,
			row.RRDT,
			row.OrderDT,
			row.SaleDT,
		)
	}

	query := insertServiceRecFullChunkSQL
	if len(chunk) < salesChunkSize {
		query = BuildMultiRowInsert(insertServiceRecPrefixSQL, insertServiceRecOnConflictSQL, len(chunk), insertServiceRecCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save service_records batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// nullFloat64 returns a pointer to v, or nil if v is 0.
// Sparse financial fields store NULL instead of 0.0 for storage efficiency.
func nullFloat64(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// nullInt64 returns a pointer to v as int64, or nil if v is 0.
func nullInt64(v int) *int64 {
	if v == 0 {
		return nil
	}
	n := int64(v)
	return &n
}

// Multi-row INSERT SQL fragments for sales table.
const (
	// insertSaleRowCols MUST match the column count in insertSaleRowPrefixSQL and the
	// arg count appended per row in saveSalesChunk. BuildMultiRowInsert generates
	// $1..$N placeholders from this number; a mismatch is a runtime SQL error.
	insertSaleRowCols = 47

	insertSaleRowPrefixSQL = `INSERT INTO sales (
	    rrd_id, realizationreport_id, nm_id, supplier_article, barcode,
	    brand_name, subject_name, ts_name, doc_type_name, quantity,
	    retail_price, retail_amount, sale_percent, commission_percent,
	    ppvz_for_pay, delivery_rub, delivery_method, gi_box_type_name,
	    office_name, order_dt, sale_dt, rr_dt,
	    is_cancel, cancel_dt,
	    ppvz_sales_commission, acquiring_fee, acquiring_percent,
	    retail_price_withdisc_rub, ppvz_spp_prc, ppvz_kvw_prc_base, ppvz_kvw_prc,
	    sup_rating_prc_up, is_kgvp_v2,
	    product_discount_for_report, supplier_promo,
	    seller_promo_discount, sale_price_promocode_discount_prc,
	    wibes_wb_discount_percent, loyalty_discount,
	    cashback_amount, cashback_discount, cashback_commission_change,
	    b2b_customer_tin, order_uid, is_b2b,
	    sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
	) VALUES `

	insertSaleRowOnConflictSQL = `
	ON CONFLICT (rrd_id) DO NOTHING`
)

var insertSaleRowFullChunkSQL = BuildMultiRowInsert(insertSaleRowPrefixSQL, insertSaleRowOnConflictSQL, salesChunkSize, insertSaleRowCols)

// Multi-row INSERT SQL fragments for service_records table.
const (
	insertServiceRecCols = 24

	insertServiceRecPrefixSQL = `INSERT INTO service_records (
	    rrd_id, realizationreport_id, supplier_oper_name,
	    nm_id, supplier_article, brand_name, subject_name,
	    barcode, shk_id, srid,
	    delivery_method, gi_box_type_name, delivery_rub,
	    penalty, deduction, storage_fee, acceptance, gi_id,
	    ppvz_vw, ppvz_vw_nds, rebill_logistic_cost,
	    rr_dt, order_dt, sale_dt
	) VALUES `

	insertServiceRecOnConflictSQL = `
	ON CONFLICT (rrd_id) DO NOTHING`
)

var insertServiceRecFullChunkSQL = BuildMultiRowInsert(insertServiceRecPrefixSQL, insertServiceRecOnConflictSQL, salesChunkSize, insertServiceRecCols)

// Ensure pgx.Tx satisfies our needs (used in chunk methods).
var _ pgx.Tx = (pgx.Tx)(nil)
