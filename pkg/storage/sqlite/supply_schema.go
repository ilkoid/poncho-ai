// Package sqlite provides supply-related table schemas.
//
// Tables for FBW supply data from supplies-api.wildberries.ru:
//   - wb_warehouses: справочник складов (перезапись)
//   - wb_transit_tariffs: транзитные тарифы (перезапись)
//   - supplies: поставки (INSERT OR REPLACE)
//   - supply_goods: товары поставки (INSERT OR REPLACE)
//   - supply_packages: упаковка поставки (INSERT OR REPLACE)
package sqlite

// SupplySchemaSQL defines all supply-related tables.
const SupplySchemaSQL = `
-- ============================================================================
-- WB WAREHOUSES (reference data — full rewrite on each download)
-- Source: GET /api/v1/warehouses (supplies-api.wildberries.ru)
-- Grain: one row per warehouse ID
-- ============================================================================
CREATE TABLE IF NOT EXISTS wb_warehouses (
    id INTEGER PRIMARY KEY,
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
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    transit_warehouse_name TEXT NOT NULL,
    destination_warehouse_name TEXT NOT NULL,
    active_from TEXT,
    pallet_tariff INTEGER,
    box_tariff TEXT,
    downloaded_at TEXT NOT NULL
);

-- ============================================================================
-- SUPPLIES (main supply data — INSERT OR REPLACE)
-- Source: POST /api/v1/supplies (supplies-api.wildberries.ru)
-- Grain: one row per (supply_id, preorder_id)
-- Note: supplyID is null for unplanned supplies (status=1), stored as 0
-- ============================================================================
CREATE TABLE IF NOT EXISTS supplies (
    supply_id INTEGER NOT NULL,
    preorder_id INTEGER NOT NULL,
    status_id INTEGER NOT NULL,
    box_type_id INTEGER NOT NULL,
    phone TEXT,
    create_date TEXT,
    supply_date TEXT,
    fact_date TEXT,
    updated_date TEXT,
    warehouse_id INTEGER,
    warehouse_name TEXT,
    actual_warehouse_id INTEGER,
    actual_warehouse_name TEXT,
    transit_warehouse_id INTEGER,
    transit_warehouse_name TEXT,
    acceptance_cost REAL,
    paid_acceptance_coefficient REAL,
    reject_reason TEXT,
    supplier_assign_name TEXT,
    storage_coef TEXT,
    delivery_coef TEXT,
    quantity INTEGER,
    accepted_quantity INTEGER,
    ready_for_sale_quantity INTEGER,
    unloading_quantity INTEGER,
    depersonalized_quantity INTEGER,
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
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    supply_id INTEGER NOT NULL,
    preorder_id INTEGER NOT NULL,
    barcode TEXT NOT NULL,
    vendor_code TEXT,
    nm_id INTEGER,
    tech_size TEXT,
    color TEXT,
    need_kiz BOOLEAN,
    tnved TEXT,
    supplier_box_amount INTEGER,
    quantity INTEGER,
    accepted_quantity INTEGER,
    ready_for_sale_quantity INTEGER,
    unloading_quantity INTEGER,
    FOREIGN KEY (supply_id, preorder_id) REFERENCES supplies(supply_id, preorder_id),
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
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    supply_id INTEGER NOT NULL,
    preorder_id INTEGER NOT NULL,
    package_code TEXT NOT NULL,
    quantity INTEGER,
    barcodes TEXT,
    FOREIGN KEY (supply_id, preorder_id) REFERENCES supplies(supply_id, preorder_id),
    UNIQUE(supply_id, preorder_id, package_code)
);

CREATE INDEX IF NOT EXISTS idx_supply_packages_supply
    ON supply_packages(supply_id, preorder_id);
`

// GetSupplySchemaSQL returns the supply-related tables schema.
func GetSupplySchemaSQL() string {
	return SupplySchemaSQL
}
