// Package sqlite provides supply storage methods on SQLiteSalesRepository.
//
// Methods for saving and querying FBW supply data from supplies-api.wildberries.ru:
//   - Warehouses (reference data, full rewrite)
//   - Transit tariffs (reference data, full rewrite)
//   - Supplies (INSERT OR REPLACE by composite PK)
//   - Supply goods (DELETE + INSERT per supply)
//   - Supply packages (DELETE + INSERT per supply)
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// Warehouses (reference data — full rewrite)
// ============================================================================

// SaveWarehouses replaces all warehouse data with the provided list.
// Uses DELETE + INSERT (full rewrite on each download).
func (r *SQLiteSalesRepository) SaveWarehouses(ctx context.Context, warehouses []wb.Warehouse) (int, error) {
	if len(warehouses) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Full rewrite: delete all existing
	if _, err := tx.ExecContext(ctx, "DELETE FROM wb_warehouses"); err != nil {
		return 0, fmt.Errorf("delete warehouses: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wb_warehouses (id, name, address, work_time, is_active, is_transit_active, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, w := range warehouses {
		_, err := stmt.ExecContext(ctx, w.ID, w.Name, w.Address, w.WorkTime, w.IsActive, w.IsTransitActive, now)
		if err != nil {
			return 0, fmt.Errorf("insert warehouse id=%d: %w", w.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(warehouses), nil
}

// ============================================================================
// Transit Tariffs (reference data — full rewrite)
// ============================================================================

// SaveTransitTariffs replaces all transit tariff data with the provided list.
// Uses DELETE + INSERT (full rewrite on each download).
func (r *SQLiteSalesRepository) SaveTransitTariffs(ctx context.Context, tariffs []wb.TransitTariff) (int, error) {
	if len(tariffs) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM wb_transit_tariffs"); err != nil {
		return 0, fmt.Errorf("delete tariffs: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wb_transit_tariffs (transit_warehouse_name, destination_warehouse_name, active_from, pallet_tariff, box_tariff, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, t := range tariffs {
		var boxTariffJSON string
		if t.BoxTariff != nil {
			b, _ := json.Marshal(t.BoxTariff)
			boxTariffJSON = string(b)
		}

		_, err := stmt.ExecContext(ctx, t.TransitWarehouseName, t.DestinationWarehouseName,
			t.ActiveFrom, t.PalletTariff, boxTariffJSON, now)
		if err != nil {
			return 0, fmt.Errorf("insert tariff: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(tariffs), nil
}

// ============================================================================
// Supplies (INSERT OR REPLACE by composite PK)
// ============================================================================

const insertSupplySQL = `
INSERT OR REPLACE INTO supplies (
	supply_id, preorder_id, status_id, box_type_id, phone,
	create_date, supply_date, fact_date, updated_date,
	warehouse_id, warehouse_name, actual_warehouse_id, actual_warehouse_name,
	transit_warehouse_id, transit_warehouse_name,
	acceptance_cost, paid_acceptance_coefficient, reject_reason,
	supplier_assign_name, storage_coef, delivery_coef,
	quantity, accepted_quantity, ready_for_sale_quantity,
	unloading_quantity, depersonalized_quantity,
	is_box_on_pallet, virtual_type_id, downloaded_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// SaveSupplies saves a batch of supplies using INSERT OR REPLACE.
// supplyID=null is stored as 0 for unplanned supplies.
func (r *SQLiteSalesRepository) SaveSupplies(ctx context.Context, supplies []SupplyRow) (int, error) {
	if len(supplies) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSupplySQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range supplies {
		_, err := stmt.ExecContext(ctx,
			s.SupplyID, s.PreorderID, s.StatusID, s.BoxTypeID, s.Phone,
			s.CreateDate, s.SupplyDate, s.FactDate, s.UpdatedDate,
			s.WarehouseID, s.WarehouseName, s.ActualWarehouseID, s.ActualWarehouseName,
			s.TransitWarehouseID, s.TransitWarehouseName,
			s.AcceptanceCost, s.PaidAcceptanceCoefficient, s.RejectReason,
			s.SupplierAssignName, s.StorageCoef, s.DeliveryCoef,
			s.Quantity, s.AcceptedQuantity, s.ReadyForSaleQuantity,
			s.UnloadingQuantity, s.DepersonalizedQuantity,
			s.IsBoxOnPallet, s.VirtualTypeID, s.DownloadedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("insert supply_id=%d preorder_id=%d: %w", s.SupplyID, s.PreorderID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(supplies), nil
}

// CountSupplies returns total number of supplies in the database.
func (r *SQLiteSalesRepository) CountSupplies(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM supplies").Scan(&count)
	return count, err
}

// GetSupplyIDs returns all (supply_id, preorder_id) pairs for downloading goods/packages.
func (r *SQLiteSalesRepository) GetSupplyIDs(ctx context.Context) ([]SupplyIDPair, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT supply_id, preorder_id FROM supplies")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs []SupplyIDPair
	for rows.Next() {
		var p SupplyIDPair
		if err := rows.Scan(&p.SupplyID, &p.PreorderID); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

// ============================================================================
// Supply Goods (DELETE + INSERT per supply)
// ============================================================================

const insertSupplyGoodsSQL = `
INSERT INTO supply_goods (
	supply_id, preorder_id, barcode, vendor_code, nm_id,
	tech_size, color, need_kiz, tnved, supplier_box_amount,
	quantity, accepted_quantity, ready_for_sale_quantity, unloading_quantity
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// SaveSupplyGoods replaces all goods for a supply and inserts new ones.
// Uses DELETE + INSERT (full rewrite per supply).
func (r *SQLiteSalesRepository) SaveSupplyGoods(ctx context.Context, supplyID, preorderID int64, goods []wb.GoodInSupply) (int, error) {
	if len(goods) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing goods for this supply
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM supply_goods WHERE supply_id = ? AND preorder_id = ?",
		supplyID, preorderID); err != nil {
		return 0, fmt.Errorf("delete goods: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, insertSupplyGoodsSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, g := range goods {
		_, err := stmt.ExecContext(ctx,
			supplyID, preorderID, g.Barcode, g.VendorCode, g.NmID,
			g.TechSize, g.Color, g.NeedKiz, g.Tnved, g.SupplierBoxAmount,
			g.Quantity, g.AcceptedQuantity, g.ReadyForSaleQuantity, g.UnloadingQuantity,
		)
		if err != nil {
			return 0, fmt.Errorf("insert good barcode=%s: %w", g.Barcode, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(goods), nil
}

// CountSupplyGoods returns total number of supply goods in the database.
func (r *SQLiteSalesRepository) CountSupplyGoods(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM supply_goods").Scan(&count)
	return count, err
}

// ============================================================================
// Supply Packages (DELETE + INSERT per supply)
// ============================================================================

const insertSupplyPackagesSQL = `
INSERT INTO supply_packages (
	supply_id, preorder_id, package_code, quantity, barcodes
) VALUES (?, ?, ?, ?, ?)`

// SaveSupplyPackages replaces all packages for a supply and inserts new ones.
// Uses DELETE + INSERT (full rewrite per supply).
func (r *SQLiteSalesRepository) SaveSupplyPackages(ctx context.Context, supplyID, preorderID int64, boxes []wb.Box) (int, error) {
	if len(boxes) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing packages for this supply
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM supply_packages WHERE supply_id = ? AND preorder_id = ?",
		supplyID, preorderID); err != nil {
		return 0, fmt.Errorf("delete packages: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, insertSupplyPackagesSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, b := range boxes {
		var barcodesJSON string
		if b.Barcodes != nil {
			data, _ := json.Marshal(b.Barcodes)
			barcodesJSON = string(data)
		}

		_, err := stmt.ExecContext(ctx, supplyID, preorderID, b.PackageCode, b.Quantity, barcodesJSON)
		if err != nil {
			return 0, fmt.Errorf("insert package code=%s: %w", b.PackageCode, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(boxes), nil
}

// ============================================================================
// Helper types (used by downloader)
// ============================================================================

// SupplyRow is a flattened supply for DB storage.
// Converts nullable API fields to non-nullable DB values.
type SupplyRow struct {
	SupplyID                   int64
	PreorderID                 int64
	StatusID                   int
	BoxTypeID                  int
	Phone                      string
	CreateDate                 string
	SupplyDate                 sql.NullString
	FactDate                   sql.NullString
	UpdatedDate                sql.NullString
	WarehouseID                sql.NullInt64
	WarehouseName              sql.NullString
	ActualWarehouseID          sql.NullInt64
	ActualWarehouseName        sql.NullString
	TransitWarehouseID         sql.NullInt64
	TransitWarehouseName       sql.NullString
	AcceptanceCost             sql.NullFloat64
	PaidAcceptanceCoefficient  sql.NullFloat64
	RejectReason               sql.NullString
	SupplierAssignName         sql.NullString
	StorageCoef                sql.NullString
	DeliveryCoef               sql.NullString
	Quantity                   sql.NullInt64
	AcceptedQuantity           sql.NullInt64
	ReadyForSaleQuantity       sql.NullInt64
	UnloadingQuantity          sql.NullInt64
	DepersonalizedQuantity     sql.NullInt64
	IsBoxOnPallet              sql.NullBool
	VirtualTypeID              sql.NullInt64
	DownloadedAt               string
}

// SupplyIDPair is used for querying supply IDs for goods/package download.
type SupplyIDPair struct {
	SupplyID   int64
	PreorderID int64
}

// SupplyFromAPIDetail converts API SupplyDetails to a SupplyRow for DB storage.
// This is used when supply details are fetched individually.
func SupplyFromAPIDetail(d *wb.SupplyDetails, downloadedAt string) SupplyRow {
	row := SupplyRow{
		PreorderID:         0, // Details don't have preorderID, set separately
		StatusID:           d.StatusID,
		BoxTypeID:          d.BoxTypeID,
		Phone:              d.Phone,
		CreateDate:         d.CreateDate,
		DownloadedAt:       downloadedAt,
	}
	// Nullable fields from *string → sql.NullString
	row.SupplyDate = sqlNullStringPtr(d.SupplyDate)
	row.FactDate = sqlNullStringPtr(d.FactDate)
	row.UpdatedDate = sqlNullStringPtr(d.UpdatedDate)
	row.WarehouseID = sqlNullInt64(intFromIntPtr(d.ActualWarehouseID))
	row.WarehouseName = sqlNullString(d.WarehouseName)
	row.ActualWarehouseID = sqlNullInt64(intFromIntPtr(d.ActualWarehouseID))
	row.ActualWarehouseName = sqlNullStringPtr(d.ActualWarehouseName)
	row.TransitWarehouseID = sqlNullInt64(intFromIntPtr(d.TransitWarehouseID))
	row.TransitWarehouseName = sqlNullStringPtr(d.TransitWarehouseName)
	row.AcceptanceCost = sqlNullFloat64Ptr(d.AcceptanceCost)
	row.PaidAcceptanceCoefficient = sqlNullFloat64Ptr(d.PaidAcceptanceCoefficient)
	row.RejectReason = sqlNullStringPtr(d.RejectReason)
	row.SupplierAssignName = sqlNullString(d.SupplierAssignName)
	row.StorageCoef = sqlNullStringPtr(d.StorageCoef)
	row.DeliveryCoef = sqlNullStringPtr(d.DeliveryCoef)
	row.VirtualTypeID = sqlNullInt64Ptr(d.VirtualTypeID)
	row.Quantity = sqlNullInt64(d.Quantity)
	row.AcceptedQuantity = sqlNullInt64(d.AcceptedQuantity)
	row.ReadyForSaleQuantity = sqlNullInt64(d.ReadyForSaleQuantity)
	row.UnloadingQuantity = sqlNullInt64(d.UnloadingQuantity)
	row.DepersonalizedQuantity = sqlNullInt64Ptr(d.DepersonalizedQuantity)
	if d.IsBoxOnPallet != nil {
		row.IsBoxOnPallet = sql.NullBool{Bool: *d.IsBoxOnPallet, Valid: true}
	}
	return row
}

// SupplyFromAPI converts API Supply (list item) to a SupplyRow for DB storage.
func SupplyFromAPI(s *wb.Supply, downloadedAt string) SupplyRow {
	supplyID := int64(0)
	if s.SupplyID != nil {
		supplyID = *s.SupplyID
	}

	row := SupplyRow{
		SupplyID:     supplyID,
		PreorderID:   s.PreorderID,
		StatusID:     s.StatusID,
		BoxTypeID:    s.BoxTypeID,
		Phone:        s.Phone,
		CreateDate:   s.CreateDate,
		DownloadedAt: downloadedAt,
	}
	row.SupplyDate = sqlNullString(stringFromPtrP(s.SupplyDate))
	row.FactDate = sqlNullString(stringFromPtrP(s.FactDate))
	row.UpdatedDate = sqlNullString(stringFromPtrP(s.UpdatedDate))

	if s.IsBoxOnPallet != nil {
		row.IsBoxOnPallet = sql.NullBool{Bool: *s.IsBoxOnPallet, Valid: true}
	}
	return row
}

// ============================================================================
// Null helpers
// ============================================================================

func sqlNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func sqlNullStringPtr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func sqlNullInt64(i int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(i), Valid: i != 0}
}

func sqlNullInt64Ptr(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func sqlNullFloat64(f float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: f, Valid: f != 0}
}

func sqlNullFloat64Ptr(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func intFromIntPtr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// stringFromPtrP converts *string (from API) to plain string for sql.NullString.
func stringFromPtrP(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
