package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// salesSchemaSQL defines PostgreSQL tables for WB Statistics API sales data.
	//
	// Translated from pkg/storage/sqlite/schema.go (sales + service_records tables):
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - INSERT OR IGNORE → ON CONFLICT (rrd_id) DO NOTHING
	//   - REAL → DOUBLE PRECISION
	//   - is_cancel INTEGER DEFAULT 0 → BOOLEAN DEFAULT FALSE
	//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', ...)
	//   - Sparse financial fields (stored as NULL for 0.0 values) → no NOT NULL constraint
	salesSchemaSQL = `
-- ============================================================================
-- SALES (WB Statistics API — /api/v1/supplier/reportDetailByPeriod)
-- Single-table: 1 row per sale/cancel event, identified by rrd_id
-- ============================================================================

CREATE TABLE IF NOT EXISTS sales (
    id BIGSERIAL PRIMARY KEY,

    -- WB API identifiers (unique for pagination)
    rrd_id INTEGER UNIQUE NOT NULL,
    realizationreport_id INTEGER,

    -- Product identifiers
    nm_id INTEGER NOT NULL DEFAULT 0,
    supplier_article TEXT DEFAULT '',
    barcode TEXT DEFAULT '',

    -- Product metadata
    brand_name TEXT DEFAULT '',
    subject_name TEXT DEFAULT '',
    ts_name TEXT DEFAULT '',

    -- Transaction details
    doc_type_name TEXT DEFAULT '',                -- "Продажа", "Возврат"
    quantity INTEGER DEFAULT 0,
    retail_price DOUBLE PRECISION DEFAULT 0,
    retail_amount DOUBLE PRECISION DEFAULT 0,
    sale_percent DOUBLE PRECISION DEFAULT 0,
    commission_percent DOUBLE PRECISION DEFAULT 0,

    -- Financial
    ppvz_for_pay DOUBLE PRECISION DEFAULT 0,
    delivery_rub DOUBLE PRECISION DEFAULT 0,

    -- Delivery method (KEY for FBW filtering)
    delivery_method TEXT DEFAULT '',
    gi_box_type_name TEXT DEFAULT '',

    -- Warehouse
    office_name TEXT DEFAULT '',

    -- Dates (stored as text in RFC3339 / API format)
    order_dt TEXT DEFAULT '',
    sale_dt TEXT DEFAULT '',
    rr_dt TEXT DEFAULT '',

    -- Cancellation
    is_cancel BOOLEAN DEFAULT FALSE,
    cancel_dt TEXT,                               -- nullable: CancelDateTime is *string

    -- Commission & acquiring (sparse → nullable)
    ppvz_sales_commission DOUBLE PRECISION,
    acquiring_fee DOUBLE PRECISION,
    acquiring_percent DOUBLE PRECISION,

    -- Price breakdown (sparse → nullable)
    retail_price_withdisc_rub DOUBLE PRECISION,
    ppvz_spp_prc DOUBLE PRECISION,
    ppvz_kvw_prc_base DOUBLE PRECISION,
    ppvz_kvw_prc DOUBLE PRECISION,
    sup_rating_prc_up DOUBLE PRECISION,
    is_kgvp_v2 DOUBLE PRECISION,

    -- Discounts & percentages (sparse → nullable)
    product_discount_for_report DOUBLE PRECISION,
    supplier_promo DOUBLE PRECISION,

    -- Seller promotions & loyalty (sparse → nullable)
    seller_promo_discount DOUBLE PRECISION,
    sale_price_promocode_discount_prc DOUBLE PRECISION,
    wibes_wb_discount_percent DOUBLE PRECISION,
    loyalty_discount DOUBLE PRECISION,
    cashback_amount DOUBLE PRECISION,
    cashback_discount DOUBLE PRECISION,
    cashback_commission_change DOUBLE PRECISION,

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Indexes (matching SQLite schema)
CREATE INDEX IF NOT EXISTS idx_sales_nm_id ON sales(nm_id);
CREATE INDEX IF NOT EXISTS idx_sales_sale_dt ON sales(sale_dt);
CREATE INDEX IF NOT EXISTS idx_sales_delivery_method ON sales(delivery_method);
CREATE INDEX IF NOT EXISTS idx_sales_rr_dt ON sales(rr_dt);
CREATE INDEX IF NOT EXISTS idx_sales_rrd_id ON sales(rrd_id);
CREATE INDEX IF NOT EXISTS idx_sales_barcode ON sales(barcode);
CREATE INDEX IF NOT EXISTS idx_sales_cancel_doctype ON sales(is_cancel, doc_type_name);
CREATE INDEX IF NOT EXISTS idx_sales_nm_sale_dt ON sales(nm_id, sale_dt);
`

	// serviceRecordsSchemaSQL defines PostgreSQL tables for WB service records
	// (logistics costs, penalties, deductions, storage fees).
	//
	// Separate table from sales — different columns and use case.
	// Identified by rrd_id (same ID space as sales).
	serviceRecordsSchemaSQL = `
-- ============================================================================
-- SERVICE RECORDS (logistics, penalties, deductions from WB Statistics API)
-- ============================================================================

CREATE TABLE IF NOT EXISTS service_records (
    id BIGSERIAL PRIMARY KEY,

    -- WB API identifiers
    rrd_id INTEGER UNIQUE NOT NULL,
    realizationreport_id INTEGER,

    -- Operation type (KEY field for classification!)
    -- Values: "Возмещение издержек...", "Удержание", "Логистика", etc.
    supplier_oper_name TEXT DEFAULT '',

    -- Product info (NULL for general records)
    nm_id INTEGER DEFAULT 0,
    supplier_article TEXT DEFAULT '',
    brand_name TEXT DEFAULT '',
    subject_name TEXT DEFAULT '',

    -- Partial identifiers
    barcode TEXT DEFAULT '',
    shk_id INTEGER DEFAULT 0,
    srid TEXT DEFAULT '',

    -- Delivery info
    delivery_method TEXT DEFAULT '',
    gi_box_type_name TEXT DEFAULT '',
    delivery_rub DOUBLE PRECISION DEFAULT 0,

    -- Financial penalties and deductions (sparse → nullable)
    penalty DOUBLE PRECISION,
    deduction DOUBLE PRECISION,
    storage_fee DOUBLE PRECISION,
    acceptance DOUBLE PRECISION,
    gi_id INTEGER,

    -- Financial data (always present → NOT NULL DEFAULT)
    ppvz_vw DOUBLE PRECISION DEFAULT 0,
    ppvz_vw_nds DOUBLE PRECISION DEFAULT 0,
    rebill_logistic_cost DOUBLE PRECISION DEFAULT 0,

    -- Dates
    rr_dt TEXT DEFAULT '',
    order_dt TEXT DEFAULT '',
    sale_dt TEXT DEFAULT '',

    -- Metadata
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Indexes (matching SQLite schema)
CREATE INDEX IF NOT EXISTS idx_service_rr_dt ON service_records(rr_dt);
CREATE INDEX IF NOT EXISTS idx_service_rrd_id ON service_records(rrd_id);
CREATE INDEX IF NOT EXISTS idx_service_nm_id ON service_records(nm_id);
CREATE INDEX IF NOT EXISTS idx_service_created_at ON service_records(created_at);
`
)

// Expression and partial indexes — executed individually because pgx's
// multi-statement Exec can't parse complex expressions in a single call.
var (
	// idxServiceOperTypeSQL maps ~50 supplier_oper_name values to 6 categories.
	// Note: double-parentheses required for expression indexes in PG.
	idxServiceOperTypeSQL = `CREATE INDEX IF NOT EXISTS idx_service_oper_type
    ON service_records((
        CASE
            WHEN supplier_oper_name LIKE 'Возмещение издержек%' THEN 'logistics'
            WHEN supplier_oper_name LIKE 'Возмещение за выдача%' THEN 'pvz'
            WHEN supplier_oper_name = 'Логистика' THEN 'logistics_direct'
            WHEN supplier_oper_name = 'Удержание' THEN 'deduction'
            WHEN supplier_oper_name = 'Штраф' THEN 'penalty'
            ELSE 'other'
        END
    ))`

	idxServicePenaltySQL   = `CREATE INDEX IF NOT EXISTS idx_service_penalty ON service_records(nm_id) WHERE penalty IS NOT NULL`
	idxServiceDeductionSQL = `CREATE INDEX IF NOT EXISTS idx_service_deduction ON service_records(nm_id) WHERE deduction IS NOT NULL`
)

// initSalesSchema creates sales table in the PostgreSQL database.
func initSalesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, salesSchemaSQL)
	if err != nil {
		return fmt.Errorf("sales schema: %w", err)
	}
	return nil
}

// initServiceRecordsSchema creates service_records table and indexes in the PostgreSQL database.
func initServiceRecordsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	// Table + simple indexes (multi-statement OK for simple DDL)
	if _, err := pool.Exec(ctx, serviceRecordsSchemaSQL); err != nil {
		return fmt.Errorf("service_records schema: %w", err)
	}

	// Expression/partial indexes — must run as individual Exec calls
	indexes := []struct {
		sql string
		msg string
	}{
		{idxServiceOperTypeSQL, "oper_type expression index"},
		{idxServicePenaltySQL, "penalty partial index"},
		{idxServiceDeductionSQL, "deduction partial index"},
	}
	for _, idx := range indexes {
		if _, err := pool.Exec(ctx, idx.sql); err != nil {
			return fmt.Errorf("service_records %s: %w", idx.msg, err)
		}
	}
	return nil
}
