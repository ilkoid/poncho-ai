package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/supplies"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgSuppliesRepo implements supplies.Writer.
var _ supplies.Writer = (*PgSuppliesRepo)(nil)

// PgSuppliesRepo implements supplies.Writer for PostgreSQL.
// Focused repository (ISP) — only supply persistence methods.
type PgSuppliesRepo struct {
	pool *pgxpool.Pool
}

// NewPgSuppliesRepo creates a new PostgreSQL supplies repository.
func NewPgSuppliesRepo(pool *pgxpool.Pool) *PgSuppliesRepo {
	return &PgSuppliesRepo{pool: pool}
}

// InitSchema creates supply tables if they don't exist.
func (r *PgSuppliesRepo) InitSchema(ctx context.Context) error {
	return initSuppliesSchema(ctx, r.pool)
}

const pgSuppliesChunkSize = 500

// ============================================================================
// SaveWarehouses — full rewrite (DELETE + INSERT)
// ============================================================================

// SaveWarehouses replaces all warehouse data with the provided list.
func (r *PgSuppliesRepo) SaveWarehouses(ctx context.Context, warehouses []wb.Warehouse) (int, error) {
	if len(warehouses) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM wb_warehouses"); err != nil {
		return 0, fmt.Errorf("delete warehouses: %w", err)
	}

	for _, w := range warehouses {
		_, err := tx.Exec(ctx, pgInsertWarehouseSQL,
			w.ID, w.Name, w.Address, w.WorkTime, w.IsActive, w.IsTransitActive,
		)
		if err != nil {
			return 0, fmt.Errorf("insert warehouse id=%d: %w", w.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(warehouses), nil
}

// ============================================================================
// SaveTransitTariffs — full rewrite (DELETE + INSERT)
// ============================================================================

// SaveTransitTariffs replaces all transit tariff data with the provided list.
func (r *PgSuppliesRepo) SaveTransitTariffs(ctx context.Context, tariffs []wb.TransitTariff) (int, error) {
	if len(tariffs) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM wb_transit_tariffs"); err != nil {
		return 0, fmt.Errorf("delete tariffs: %w", err)
	}

	for _, t := range tariffs {
		var boxTariffJSON string
		if t.BoxTariff != nil {
			b, _ := json.Marshal(t.BoxTariff)
			boxTariffJSON = string(b)
		}
		_, err := tx.Exec(ctx, pgInsertTransitTariffSQL,
			t.TransitWarehouseName, t.DestinationWarehouseName,
			t.ActiveFrom, t.PalletTariff, boxTariffJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("insert tariff: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(tariffs), nil
}

// ============================================================================
// SaveSupplies — ON CONFLICT upsert with COALESCE
// ============================================================================

// SaveSupplies saves a batch of supplies using ON CONFLICT upsert.
// Uses COALESCE for nullable fields to preserve values from the first insert
// when the second insert (from details) provides NULL.
func (r *PgSuppliesRepo) SaveSupplies(ctx context.Context, supplyRows []supplies.SupplyRow) (int, error) {
	if len(supplyRows) == 0 {
		return 0, nil
	}

	totalSaved := 0
	for i := 0; i < len(supplyRows); i += pgSuppliesChunkSize {
		end := min(i+pgSuppliesChunkSize, len(supplyRows))
		chunk := supplyRows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return totalSaved, fmt.Errorf("begin transaction: %w", err)
		}

		for _, s := range chunk {
			_, err := tx.Exec(ctx, pgUpsertSupplySQL,
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
				tx.Rollback(ctx)
				return totalSaved, fmt.Errorf("upsert supply_id=%d preorder_id=%d: %w", s.SupplyID, s.PreorderID, err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return totalSaved, fmt.Errorf("commit: %w", err)
		}
		totalSaved += len(chunk)
	}

	return totalSaved, nil
}

// ============================================================================
// SaveSupplyGoods — DELETE + INSERT per supply
// ============================================================================

// SaveSupplyGoods replaces all goods for a supply (DELETE + INSERT).
func (r *PgSuppliesRepo) SaveSupplyGoods(ctx context.Context, supplyID, preorderID int64, goods []wb.GoodInSupply) (int, error) {
	if len(goods) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		"DELETE FROM supply_goods WHERE supply_id = $1 AND preorder_id = $2",
		supplyID, preorderID); err != nil {
		return 0, fmt.Errorf("delete goods: %w", err)
	}

	for _, g := range goods {
		_, err := tx.Exec(ctx, pgInsertSupplyGoodsSQL,
			supplyID, preorderID, g.Barcode, g.VendorCode, g.NmID,
			g.TechSize, g.Color, g.NeedKiz, g.Tnved, g.SupplierBoxAmount,
			g.Quantity, g.AcceptedQuantity, g.ReadyForSaleQuantity, g.UnloadingQuantity,
		)
		if err != nil {
			return 0, fmt.Errorf("insert good barcode=%s: %w", g.Barcode, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(goods), nil
}

// ============================================================================
// SaveSupplyPackages — DELETE + INSERT per supply
// ============================================================================

// SaveSupplyPackages replaces all packages for a supply (DELETE + INSERT).
func (r *PgSuppliesRepo) SaveSupplyPackages(ctx context.Context, supplyID, preorderID int64, boxes []wb.Box) (int, error) {
	if len(boxes) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		"DELETE FROM supply_packages WHERE supply_id = $1 AND preorder_id = $2",
		supplyID, preorderID); err != nil {
		return 0, fmt.Errorf("delete packages: %w", err)
	}

	for _, b := range boxes {
		var barcodesJSON string
		if b.Barcodes != nil {
			data, _ := json.Marshal(b.Barcodes)
			barcodesJSON = string(data)
		}
		_, err := tx.Exec(ctx, pgInsertSupplyPackagesSQL,
			supplyID, preorderID, b.PackageCode, b.Quantity, barcodesJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("insert package code=%s: %w", b.PackageCode, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(boxes), nil
}

// ============================================================================
// Count methods
// ============================================================================

// CountSupplies returns total number of supplies in the database.
func (r *PgSuppliesRepo) CountSupplies(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM supplies").Scan(&count)
	return count, err
}

// CountSupplyGoods returns total number of supply goods in the database.
func (r *PgSuppliesRepo) CountSupplyGoods(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM supply_goods").Scan(&count)
	return count, err
}

// ============================================================================
// SQL statements
// ============================================================================

var (
	pgInsertWarehouseSQL = `
	INSERT INTO wb_warehouses (id, name, address, work_time, is_active, is_transit_active, downloaded_at)
	VALUES ($1, $2, $3, $4, $5, $6, TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))`

	pgInsertTransitTariffSQL = `
	INSERT INTO wb_transit_tariffs (transit_warehouse_name, destination_warehouse_name, active_from, pallet_tariff, box_tariff, downloaded_at)
	VALUES ($1, $2, $3, $4, $5, TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))`

	// pgUpsertSupplySQL uses COALESCE for nullable detail fields.
	// Phase 1 (from list): basic fields set, detail fields NULL.
	// Phase 2 (from details): detail fields set, COALESCE preserves non-null values from phase 1.
	pgUpsertSupplySQL = `
	INSERT INTO supplies (
		supply_id, preorder_id, status_id, box_type_id, phone,
		create_date, supply_date, fact_date, updated_date,
		warehouse_id, warehouse_name, actual_warehouse_id, actual_warehouse_name,
		transit_warehouse_id, transit_warehouse_name,
		acceptance_cost, paid_acceptance_coefficient, reject_reason,
		supplier_assign_name, storage_coef, delivery_coef,
		quantity, accepted_quantity, ready_for_sale_quantity,
		unloading_quantity, depersonalized_quantity,
		is_box_on_pallet, virtual_type_id, downloaded_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
	ON CONFLICT (supply_id, preorder_id) DO UPDATE SET
		status_id = EXCLUDED.status_id,
		box_type_id = EXCLUDED.box_type_id,
		phone = EXCLUDED.phone,
		create_date = EXCLUDED.create_date,
		supply_date = COALESCE(EXCLUDED.supply_date, supplies.supply_date),
		fact_date = COALESCE(EXCLUDED.fact_date, supplies.fact_date),
		updated_date = COALESCE(EXCLUDED.updated_date, supplies.updated_date),
		warehouse_id = COALESCE(EXCLUDED.warehouse_id, supplies.warehouse_id),
		warehouse_name = COALESCE(EXCLUDED.warehouse_name, supplies.warehouse_name),
		actual_warehouse_id = COALESCE(EXCLUDED.actual_warehouse_id, supplies.actual_warehouse_id),
		actual_warehouse_name = COALESCE(EXCLUDED.actual_warehouse_name, supplies.actual_warehouse_name),
		transit_warehouse_id = COALESCE(EXCLUDED.transit_warehouse_id, supplies.transit_warehouse_id),
		transit_warehouse_name = COALESCE(EXCLUDED.transit_warehouse_name, supplies.transit_warehouse_name),
		acceptance_cost = COALESCE(EXCLUDED.acceptance_cost, supplies.acceptance_cost),
		paid_acceptance_coefficient = COALESCE(EXCLUDED.paid_acceptance_coefficient, supplies.paid_acceptance_coefficient),
		reject_reason = COALESCE(EXCLUDED.reject_reason, supplies.reject_reason),
		supplier_assign_name = COALESCE(EXCLUDED.supplier_assign_name, supplies.supplier_assign_name),
		storage_coef = COALESCE(EXCLUDED.storage_coef, supplies.storage_coef),
		delivery_coef = COALESCE(EXCLUDED.delivery_coef, supplies.delivery_coef),
		quantity = COALESCE(EXCLUDED.quantity, supplies.quantity),
		accepted_quantity = COALESCE(EXCLUDED.accepted_quantity, supplies.accepted_quantity),
		ready_for_sale_quantity = COALESCE(EXCLUDED.ready_for_sale_quantity, supplies.ready_for_sale_quantity),
		unloading_quantity = COALESCE(EXCLUDED.unloading_quantity, supplies.unloading_quantity),
		depersonalized_quantity = COALESCE(EXCLUDED.depersonalized_quantity, supplies.depersonalized_quantity),
		is_box_on_pallet = COALESCE(EXCLUDED.is_box_on_pallet, supplies.is_box_on_pallet),
		virtual_type_id = COALESCE(EXCLUDED.virtual_type_id, supplies.virtual_type_id),
		downloaded_at = EXCLUDED.downloaded_at`

	pgInsertSupplyGoodsSQL = `
	INSERT INTO supply_goods (
		supply_id, preorder_id, barcode, vendor_code, nm_id,
		tech_size, color, need_kiz, tnved, supplier_box_amount,
		quantity, accepted_quantity, ready_for_sale_quantity, unloading_quantity
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`

	pgInsertSupplyPackagesSQL = `
	INSERT INTO supply_packages (
		supply_id, preorder_id, package_code, quantity, barcodes
	) VALUES ($1, $2, $3, $4, $5)`
)
