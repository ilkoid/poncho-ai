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
//
// rr_dt — TEXT в двух форматах: finance-API отдаёт дату без времени ('2026-08-28'),
// статистик-эра — RFC3339 ('2026-08-28T23:59:59+03:00'). Чисто строковое сравнение
// с RFC3339-границей пропускает дата-онли строки первого дня окна ('2026-08-28' <
// '2026-08-28T00:00:00+03:00'), из-за чего rewrite-INSERT падал на sales_rrd_id_key
// (инцидент 04.09.2026). substr(...,1,10) нормализует обе стороны к ISO-дате.
func (r *PgSalesRepo) DeleteSalesByDateRange(ctx context.Context, from, to string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		"DELETE FROM sales WHERE substr(rr_dt,1,10) >= substr($1,1,10) AND substr(rr_dt,1,10) <= substr($2,1,10)",
		from, to,
	)
	if err != nil {
		return 0, fmt.Errorf("delete sales by date range: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteServiceRecordsByDateRange deletes all service records within a date range.
// Сравнение — как в DeleteSalesByDateRange: substr(...,1,10) из-за mixed
// TEXT-форматов rr_dt (дата-онли от finance-API vs RFC3339).
func (r *PgSalesRepo) DeleteServiceRecordsByDateRange(ctx context.Context, from, to string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		"DELETE FROM service_records WHERE substr(rr_dt,1,10) >= substr($1,1,10) AND substr(rr_dt,1,10) <= substr($2,1,10)",
		from, to,
	)
	if err != nil {
		return 0, fmt.Errorf("delete service records by date range: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Save saves batch of sales rows to storage.
// Upsert по rrd_id: ON CONFLICT DO UPDATE обновляет все столбцы — finance-API
// может вернуть уже существующую строку (WB дозаполняет комиссии/возвраты с
// лагом публикации), и DELETE окна не гарантирует её отсутствие.
func (r *PgSalesRepo) Save(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}
	return r.saveSalesBatch(ctx, rows)
}

// SavePlain — алиас Save (исторически «plain INSERT без ON CONFLICT» для
// rewrite-режима: считалось, что DELETE уже очистил диапазон и конфликты
// невозможны). Инвариант оказался ложным (инцидент 04.09.2026: дата-онли
// rr_dt первого дня окна переживал DELETE), поэтому оба пути — upsert.
func (r *PgSalesRepo) SavePlain(ctx context.Context, rows []wb.RealizationReportRow) error {
	return r.Save(ctx, rows)
}

// saveSalesBatch дедуплицирует rows по rrd_id и пишет чанками через saveSalesChunk.
func (r *PgSalesRepo) saveSalesBatch(ctx context.Context, rows []wb.RealizationReportRow) error {
	rows = dedupByRrdID(rows)
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

// dedupByRrdID убирает дубликаты rrd_id, оставляя последнее вхождение (поздние
// корректировки важнее) и сохраняя порядок. Обязателен перед multi-row INSERT
// с ON CONFLICT DO UPDATE: один и тот же rrd_id дважды в одном стейтменте
// даёт «cannot affect row a second time».
func dedupByRrdID(rows []wb.RealizationReportRow) []wb.RealizationReportRow {
	if len(rows) < 2 {
		return rows
	}
	lastPos := make(map[int]int, len(rows))
	for i, row := range rows {
		lastPos[row.RrdID] = i
	}
	out := rows[:0]
	for i, row := range rows {
		if lastPos[row.RrdID] == i {
			out = append(out, row)
		}
	}
	return out
}

// SaveServiceRecords saves batch of service records to storage.
// Upsert по rrd_id — как Save (см. комментарий там).
func (r *PgSalesRepo) SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error {
	if len(rows) == 0 {
		return nil
	}
	return r.saveServiceRecordsBatch(ctx, rows)
}

// SaveServiceRecordsPlain — алиас SaveServiceRecords (см. SavePlain).
func (r *PgSalesRepo) SaveServiceRecordsPlain(ctx context.Context, rows []wb.RealizationReportRow) error {
	return r.SaveServiceRecords(ctx, rows)
}

// saveServiceRecordsBatch дедуплицирует rows по rrd_id и пишет чанками.
func (r *PgSalesRepo) saveServiceRecordsBatch(ctx context.Context, rows []wb.RealizationReportRow) error {
	rows = dedupByRrdID(rows)
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
// 47 columns per row, upsert по rrd_id (см. Save).
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
			row.IsLegalEntity,
			nullFloat64(row.SalePriceAffiliatedDiscountPrc),
			nullFloat64(row.SalePriceWholesaleDiscountPrc),
		)
	}

	var query string
	if len(chunk) == salesChunkSize {
		query = insertSaleRowFullChunkSQL
	} else {
		query = BuildMultiRowInsert(insertSaleRowPrefixSQL, insertSaleRowOnConflictSQL, len(chunk), insertSaleRowCols)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save sales batch (size %d): %w", len(chunk), err)
	}
	return tx.Commit(ctx)
}

// saveServiceRecordsChunk saves up to 500 service records using a single multi-row INSERT.
// 24 columns per row, upsert по rrd_id (см. SaveServiceRecords).
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

	var query string
	if len(chunk) == salesChunkSize {
		query = insertServiceRecFullChunkSQL
	} else {
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
	    b2b_customer_tin, order_uid, is_legal_entity,
	    sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
	) VALUES `

	// DO UPDATE (не DO NOTHING): rewrite-семантика = «свежая правда» из API —
	// WB дозаполняет комиссии/возвраты, и повторно возвращённая строка должна
	// перезаписать устаревшую, а не молча остаться старой.
	insertSaleRowOnConflictSQL = `
	ON CONFLICT (rrd_id) DO UPDATE SET
	    realizationreport_id = EXCLUDED.realizationreport_id,
	    nm_id = EXCLUDED.nm_id,
	    supplier_article = EXCLUDED.supplier_article,
	    barcode = EXCLUDED.barcode,
	    brand_name = EXCLUDED.brand_name,
	    subject_name = EXCLUDED.subject_name,
	    ts_name = EXCLUDED.ts_name,
	    doc_type_name = EXCLUDED.doc_type_name,
	    quantity = EXCLUDED.quantity,
	    retail_price = EXCLUDED.retail_price,
	    retail_amount = EXCLUDED.retail_amount,
	    sale_percent = EXCLUDED.sale_percent,
	    commission_percent = EXCLUDED.commission_percent,
	    ppvz_for_pay = EXCLUDED.ppvz_for_pay,
	    delivery_rub = EXCLUDED.delivery_rub,
	    delivery_method = EXCLUDED.delivery_method,
	    gi_box_type_name = EXCLUDED.gi_box_type_name,
	    office_name = EXCLUDED.office_name,
	    order_dt = EXCLUDED.order_dt,
	    sale_dt = EXCLUDED.sale_dt,
	    rr_dt = EXCLUDED.rr_dt,
	    is_cancel = EXCLUDED.is_cancel,
	    cancel_dt = EXCLUDED.cancel_dt,
	    ppvz_sales_commission = EXCLUDED.ppvz_sales_commission,
	    acquiring_fee = EXCLUDED.acquiring_fee,
	    acquiring_percent = EXCLUDED.acquiring_percent,
	    retail_price_withdisc_rub = EXCLUDED.retail_price_withdisc_rub,
	    ppvz_spp_prc = EXCLUDED.ppvz_spp_prc,
	    ppvz_kvw_prc_base = EXCLUDED.ppvz_kvw_prc_base,
	    ppvz_kvw_prc = EXCLUDED.ppvz_kvw_prc,
	    sup_rating_prc_up = EXCLUDED.sup_rating_prc_up,
	    is_kgvp_v2 = EXCLUDED.is_kgvp_v2,
	    product_discount_for_report = EXCLUDED.product_discount_for_report,
	    supplier_promo = EXCLUDED.supplier_promo,
	    seller_promo_discount = EXCLUDED.seller_promo_discount,
	    sale_price_promocode_discount_prc = EXCLUDED.sale_price_promocode_discount_prc,
	    wibes_wb_discount_percent = EXCLUDED.wibes_wb_discount_percent,
	    loyalty_discount = EXCLUDED.loyalty_discount,
	    cashback_amount = EXCLUDED.cashback_amount,
	    cashback_discount = EXCLUDED.cashback_discount,
	    cashback_commission_change = EXCLUDED.cashback_commission_change,
	    b2b_customer_tin = EXCLUDED.b2b_customer_tin,
	    order_uid = EXCLUDED.order_uid,
	    is_legal_entity = EXCLUDED.is_legal_entity,
	    sale_price_affiliated_discount_prc = EXCLUDED.sale_price_affiliated_discount_prc,
	    sale_price_wholesale_discount_prc = EXCLUDED.sale_price_wholesale_discount_prc`
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

	// DO UPDATE по той же причине, что и в insertSaleRowOnConflictSQL.
	insertServiceRecOnConflictSQL = `
	ON CONFLICT (rrd_id) DO UPDATE SET
	    realizationreport_id = EXCLUDED.realizationreport_id,
	    supplier_oper_name = EXCLUDED.supplier_oper_name,
	    nm_id = EXCLUDED.nm_id,
	    supplier_article = EXCLUDED.supplier_article,
	    brand_name = EXCLUDED.brand_name,
	    subject_name = EXCLUDED.subject_name,
	    barcode = EXCLUDED.barcode,
	    shk_id = EXCLUDED.shk_id,
	    srid = EXCLUDED.srid,
	    delivery_method = EXCLUDED.delivery_method,
	    gi_box_type_name = EXCLUDED.gi_box_type_name,
	    delivery_rub = EXCLUDED.delivery_rub,
	    penalty = EXCLUDED.penalty,
	    deduction = EXCLUDED.deduction,
	    storage_fee = EXCLUDED.storage_fee,
	    acceptance = EXCLUDED.acceptance,
	    gi_id = EXCLUDED.gi_id,
	    ppvz_vw = EXCLUDED.ppvz_vw,
	    ppvz_vw_nds = EXCLUDED.ppvz_vw_nds,
	    rebill_logistic_cost = EXCLUDED.rebill_logistic_cost,
	    rr_dt = EXCLUDED.rr_dt,
	    order_dt = EXCLUDED.order_dt,
	    sale_dt = EXCLUDED.sale_dt`
)

var insertServiceRecFullChunkSQL = BuildMultiRowInsert(insertServiceRecPrefixSQL, insertServiceRecOnConflictSQL, salesChunkSize, insertServiceRecCols)

// Ensure pgx.Tx satisfies our needs (used in chunk methods).
var _ pgx.Tx = (pgx.Tx)(nil)
