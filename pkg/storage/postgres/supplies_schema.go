package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// suppliesSchemaSQL defines PostgreSQL tables for WB Supplies API data.
	//
	// 5 tables: wb_warehouses, wb_transit_tariffs, supplies, supply_goods, supply_packages.
	//
	// Translated from pkg/storage/sqlite/supply_schema.go:
	//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
	//   - REAL → DOUBLE PRECISION
	//   - INSERT OR REPLACE → ON CONFLICT ... DO UPDATE SET ... = EXCLUDED
	//   - BOOLEAN stays BOOLEAN (native PG type)
	suppliesSchemaSQL = `
-- ============================================================================
-- WB WAREHOUSES (reference data — full rewrite on each download)
-- Source: GET /api/v1/warehouses (supplies-api.wildberries.ru)
-- Grain: one row per warehouse ID
-- ============================================================================
CREATE TABLE IF NOT EXISTS wb_warehouses (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT,
    work_time TEXT,
    is_active BOOLEAN,
    is_transit_active BOOLEAN,
    downloaded_at TEXT NOT NULL
);

-- ============================================================================
-- WB TRANSIT TARIFFS (reference data — full rewrite on each download)
-- Source: GET /api/v1/transit-tariffs (supplies-api.wildberries.ru)
-- Grain: one row per transit+destination pair
-- ============================================================================
CREATE TABLE IF NOT EXISTS wb_transit_tariffs (
    id BIGSERIAL PRIMARY KEY,
    transit_warehouse_name TEXT NOT NULL,
    destination_warehouse_name TEXT NOT NULL,
    active_from TEXT,
    pallet_tariff BIGINT,
    box_tariff TEXT,
    downloaded_at TEXT NOT NULL
);

-- ============================================================================
-- SUPPLIES (main supply data — ON CONFLICT upsert)
-- Source: POST /api/v1/supplies (supplies-api.wildberries.ru)
-- Grain: one row per (supply_id, preorder_id)
-- Note: supplyID is null for unplanned supplies (status=1), stored as 0
-- ============================================================================
CREATE TABLE IF NOT EXISTS supplies (
    supply_id BIGINT NOT NULL,
    preorder_id BIGINT NOT NULL,
    status_id INTEGER NOT NULL,
    box_type_id INTEGER NOT NULL,
    phone TEXT,
    create_date TEXT,
    supply_date TEXT,
    fact_date TEXT,
    updated_date TEXT,
    warehouse_id BIGINT,
    warehouse_name TEXT,
    actual_warehouse_id BIGINT,
    actual_warehouse_name TEXT,
    transit_warehouse_id BIGINT,
    transit_warehouse_name TEXT,
    acceptance_cost DOUBLE PRECISION,
    paid_acceptance_coefficient DOUBLE PRECISION,
    reject_reason TEXT,
    supplier_assign_name TEXT,
    storage_coef TEXT,
    delivery_coef TEXT,
    quantity BIGINT,
    accepted_quantity BIGINT,
    ready_for_sale_quantity BIGINT,
    unloading_quantity BIGINT,
    depersonalized_quantity BIGINT,
    is_box_on_pallet BOOLEAN,
    virtual_type_id INTEGER,
    downloaded_at TEXT NOT NULL,
    PRIMARY KEY (supply_id, preorder_id)
);

CREATE INDEX IF NOT EXISTS idx_supplies_status
    ON supplies(status_id);

CREATE INDEX IF NOT EXISTS idx_supplies_create_date
    ON supplies(create_date);

CREATE INDEX IF NOT EXISTS idx_supplies_warehouse
    ON supplies(warehouse_id);

-- ============================================================================
-- SUPPLY GOODS (products in supply — DELETE + INSERT per supply)
-- Source: GET /api/v1/supplies/{ID}/goods (supplies-api.wildberries.ru)
-- Grain: one row per (supply_id, preorder_id, barcode)
-- ============================================================================
CREATE TABLE IF NOT EXISTS supply_goods (
    id BIGSERIAL PRIMARY KEY,
    supply_id BIGINT NOT NULL,
    preorder_id BIGINT NOT NULL,
    barcode TEXT NOT NULL,
    vendor_code TEXT,
    nm_id BIGINT,
    tech_size TEXT,
    color TEXT,
    need_kiz BOOLEAN,
    tnved TEXT,
    supplier_box_amount BIGINT,
    quantity BIGINT,
    accepted_quantity BIGINT,
    ready_for_sale_quantity BIGINT,
    unloading_quantity BIGINT,
    UNIQUE(supply_id, preorder_id, barcode)
);

CREATE INDEX IF NOT EXISTS idx_supply_goods_supply
    ON supply_goods(supply_id, preorder_id);

CREATE INDEX IF NOT EXISTS idx_supply_goods_barcode
    ON supply_goods(barcode);

CREATE INDEX IF NOT EXISTS idx_supply_goods_nm_id
    ON supply_goods(nm_id);

-- ============================================================================
-- SUPPLY PACKAGES (boxes in supply — DELETE + INSERT per supply)
-- Source: GET /api/v1/supplies/{ID}/package (supplies-api.wildberries.ru)
-- Grain: one row per (supply_id, preorder_id, package_code)
-- ============================================================================
CREATE TABLE IF NOT EXISTS supply_packages (
    id BIGSERIAL PRIMARY KEY,
    supply_id BIGINT NOT NULL,
    preorder_id BIGINT NOT NULL,
    package_code TEXT NOT NULL,
    quantity BIGINT,
    barcodes TEXT,
    UNIQUE(supply_id, preorder_id, package_code)
);

CREATE INDEX IF NOT EXISTS idx_supply_packages_supply
    ON supply_packages(supply_id, preorder_id);
`
)

// suppliesMigrations widens INTEGER columns to BIGINT for ID and quantity fields.
// Safe: INTEGER→BIGINT is a widening conversion — no data loss.
const suppliesMigrations = `
ALTER TABLE wb_warehouses ALTER COLUMN id TYPE BIGINT;
ALTER TABLE wb_transit_tariffs ALTER COLUMN pallet_tariff TYPE BIGINT;
ALTER TABLE supplies ALTER COLUMN quantity TYPE BIGINT;
ALTER TABLE supplies ALTER COLUMN accepted_quantity TYPE BIGINT;
ALTER TABLE supplies ALTER COLUMN ready_for_sale_quantity TYPE BIGINT;
ALTER TABLE supplies ALTER COLUMN unloading_quantity TYPE BIGINT;
ALTER TABLE supplies ALTER COLUMN depersonalized_quantity TYPE BIGINT;
ALTER TABLE supply_goods ALTER COLUMN supplier_box_amount TYPE BIGINT;
ALTER TABLE supply_goods ALTER COLUMN quantity TYPE BIGINT;
ALTER TABLE supply_goods ALTER COLUMN accepted_quantity TYPE BIGINT;
ALTER TABLE supply_goods ALTER COLUMN ready_for_sale_quantity TYPE BIGINT;
ALTER TABLE supply_goods ALTER COLUMN unloading_quantity TYPE BIGINT;
ALTER TABLE supply_packages ALTER COLUMN quantity TYPE BIGINT;
`

// initSuppliesSchema creates supply tables in the PostgreSQL database.
func initSuppliesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, suppliesSchemaSQL)
	if err != nil {
		return fmt.Errorf("supplies schema: %w", err)
	}
	if _, err := pool.Exec(ctx, suppliesMigrations); err != nil {
		return fmt.Errorf("supplies migrations (int4→bigint): %w", err)
	}
	return nil
}
