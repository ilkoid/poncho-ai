// Package supplies provides a reusable supplies downloader for WB Supplies API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - Source — API abstraction (*wb.Client via WBSource adapter)
//   - Writer — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
//
// Covers 3 phases of supply data:
//   1. Reference data (warehouses, transit tariffs)
//   2. Supply list (paginated via POST /api/v1/supplies)
//   3. Per-supply details (details + goods + packages)
package supplies

import (
	"context"
	"database/sql"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Source is the data source interface for supply downloads.
// Implemented by WBSource (real API) and MockSource (--mock).
//
// 6 methods — one per API endpoint. Rate limits are hidden in WBSource.
type Source interface {
	// GetWarehouses returns all WB warehouses.
	// Wraps GET /api/v1/warehouses.
	GetWarehouses(ctx context.Context) ([]wb.Warehouse, error)

	// GetTransitTariffs returns all transit tariffs.
	// Wraps GET /api/v1/transit-tariffs.
	GetTransitTariffs(ctx context.Context) ([]wb.TransitTariff, error)

	// GetSupplies returns paginated supply list.
	// Wraps POST /api/v1/supplies. Max 1000 per page.
	GetSupplies(ctx context.Context, filter wb.SuppliesFilterRequest, limit, offset int) ([]wb.Supply, error)

	// GetSupplyDetails returns detailed info for a single supply.
	// Wraps GET /api/v1/supplies/{ID}.
	GetSupplyDetails(ctx context.Context, supplyID int64) (*wb.SupplyDetails, error)

	// GetSupplyGoods returns goods for a single supply (paginated).
	// Wraps GET /api/v1/supplies/{ID}/goods.
	GetSupplyGoods(ctx context.Context, supplyID int64, limit, offset int) ([]wb.GoodInSupply, error)

	// GetSupplyPackages returns packages for a single supply.
	// Wraps GET /api/v1/supplies/{ID}/package.
	GetSupplyPackages(ctx context.Context, supplyID int64) ([]wb.Box, error)
}

// Writer is the persistence interface for supply data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 7 methods — exactly what Downloader.Run() calls.
type Writer interface {
	// SaveWarehouses replaces all warehouse data (full rewrite).
	SaveWarehouses(ctx context.Context, warehouses []wb.Warehouse) (int, error)

	// SaveTransitTariffs replaces all transit tariff data (full rewrite).
	SaveTransitTariffs(ctx context.Context, tariffs []wb.TransitTariff) (int, error)

	// SaveSupplies saves a batch of supplies using upsert.
	// Called twice: once from supply list, once from details (enriches warehouse fields).
	SaveSupplies(ctx context.Context, supplies []SupplyRow) (int, error)

	// SaveSupplyGoods replaces all goods for a supply (DELETE + INSERT).
	SaveSupplyGoods(ctx context.Context, supplyID, preorderID int64, goods []wb.GoodInSupply) (int, error)

	// SaveSupplyPackages replaces all packages for a supply (DELETE + INSERT).
	SaveSupplyPackages(ctx context.Context, supplyID, preorderID int64, boxes []wb.Box) (int, error)

	// CountSupplies returns total number of supplies in the database.
	CountSupplies(ctx context.Context) (int, error)

	// CountSupplyGoods returns total number of supply goods in the database.
	CountSupplyGoods(ctx context.Context) (int, error)
}

// DownloadOptions configures the supply download behavior.
type DownloadOptions struct {
	// Date range (resolved by CLI before passing to Downloader)
	Begin string // YYYY-MM-DD
	End   string // YYYY-MM-DD

	// DateFilterType determines which date field to filter on.
	// Options: "factDate", "createDate", "supplyDate", "updatedDate" (default).
	DateFilterType string

	// SkipReference skips warehouse and tariff download (phase 1).
	SkipReference bool

	// DryRun skips all DB writes.
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a supply download run.
type DownloadResult struct {
	Warehouses int
	Tariffs    int
	Supplies   int
	Goods      int
	Packages   int
	APICalls   int
	Errors     int
	Duration   time.Duration
}

// ============================================================================
// Domain types (moved from pkg/storage/sqlite/supply_repo.go)
// ============================================================================

// SupplyRow is a flattened supply for DB storage.
// Converts nullable API fields to sql.Null* types for both SQLite and PG.
type SupplyRow struct {
	SupplyID                  int64
	PreorderID                int64
	StatusID                  int
	BoxTypeID                 int
	Phone                     string
	CreateDate                string
	SupplyDate                sql.NullString
	FactDate                  sql.NullString
	UpdatedDate               sql.NullString
	WarehouseID               sql.NullInt64
	WarehouseName             sql.NullString
	ActualWarehouseID         sql.NullInt64
	ActualWarehouseName       sql.NullString
	TransitWarehouseID        sql.NullInt64
	TransitWarehouseName      sql.NullString
	AcceptanceCost            sql.NullFloat64
	PaidAcceptanceCoefficient sql.NullFloat64
	RejectReason              sql.NullString
	SupplierAssignName        sql.NullString
	StorageCoef               sql.NullString
	DeliveryCoef              sql.NullString
	Quantity                  sql.NullInt64
	AcceptedQuantity          sql.NullInt64
	ReadyForSaleQuantity      sql.NullInt64
	UnloadingQuantity         sql.NullInt64
	DepersonalizedQuantity    sql.NullInt64
	IsBoxOnPallet             sql.NullBool
	VirtualTypeID             sql.NullInt64
	DownloadedAt              string
}

// SupplyIDPair is used for querying supply IDs for goods/package download.
type SupplyIDPair struct {
	SupplyID   int64
	PreorderID int64
}

// ============================================================================
// API → DB conversion functions
// ============================================================================

// SupplyFromAPIDetail converts API SupplyDetails to a SupplyRow for DB storage.
// Used when supply details are fetched individually.
// PreorderID is set to 0 — caller must set it separately.
func SupplyFromAPIDetail(d *wb.SupplyDetails, downloadedAt string) SupplyRow {
	row := SupplyRow{
		PreorderID:   0, // Details don't have preorderID, set separately
		StatusID:     d.StatusID,
		BoxTypeID:    d.BoxTypeID,
		Phone:        d.Phone,
		CreateDate:   d.CreateDate,
		DownloadedAt: downloadedAt,
	}
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
// Sets basic fields only — warehouse/detail fields are zero/NULL.
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
// Null helpers (shared by conversion functions + adapters)
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

func stringFromPtrP(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
