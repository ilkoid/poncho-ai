package supplies

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockSource returns deterministic fake supply data.
// Implements Source for --mock mode and testing.
type MockSource struct {
	mu         sync.RWMutex
	warehouses []wb.Warehouse
	tariffs    []wb.TransitTariff
	supplies   []wb.Supply
	details    map[int64]*wb.SupplyDetails
	goods      map[int64][]wb.GoodInSupply
	packages   map[int64][]wb.Box
}

// NewMockSource creates a mock source with deterministic fake data.
// Generates supplyCount supplies, each with 3 goods and 1 package.
func NewMockSource(supplyCount int) *MockSource {
	m := &MockSource{
		details:  make(map[int64]*wb.SupplyDetails),
		goods:    make(map[int64][]wb.GoodInSupply),
		packages: make(map[int64][]wb.Box),
	}
	m.populate(supplyCount)
	return m
}

// GetWarehouses returns mock warehouse list.
func (m *MockSource) GetWarehouses(ctx context.Context) ([]wb.Warehouse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.warehouses, nil
}

// GetTransitTariffs returns mock tariff list.
func (m *MockSource) GetTransitTariffs(ctx context.Context) ([]wb.TransitTariff, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tariffs, nil
}

// GetSupplies returns paginated mock supplies.
func (m *MockSource) GetSupplies(ctx context.Context, filter wb.SuppliesFilterRequest, limit, offset int) ([]wb.Supply, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if offset >= len(m.supplies) {
		return nil, nil
	}
	end := min(offset+limit, len(m.supplies))
	return m.supplies[offset:end], nil
}

// GetSupplyDetails returns mock details for a supply.
func (m *MockSource) GetSupplyDetails(ctx context.Context, supplyID int64) (*wb.SupplyDetails, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.details[supplyID], nil
}

// GetSupplyGoods returns mock goods for a supply (paginated).
func (m *MockSource) GetSupplyGoods(ctx context.Context, supplyID int64, limit, offset int) ([]wb.GoodInSupply, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	goods, ok := m.goods[supplyID]
	if !ok {
		return nil, nil
	}
	if offset >= len(goods) {
		return nil, nil
	}
	end := min(offset+limit, len(goods))
	return goods[offset:end], nil
}

// GetSupplyPackages returns mock packages for a supply.
func (m *MockSource) GetSupplyPackages(ctx context.Context, supplyID int64) ([]wb.Box, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.packages[supplyID], nil
}

// SupplyCount returns the number of mock supplies.
func (m *MockSource) SupplyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.supplies)
}

// populate fills mock with deterministic supply data.
func (m *MockSource) populate(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Warehouses
	m.warehouses = []wb.Warehouse{
		{ID: 1, Name: "Коледино", Address: "Подольск, Коледино", WorkTime: "10:00-20:00", IsActive: true, IsTransitActive: true},
		{ID: 2, Name: "Казань", Address: "Казань, Дагестанская", WorkTime: "09:00-18:00", IsActive: true, IsTransitActive: false},
		{ID: 3, Name: "Электросталь", Address: "Электросталь, Южный", WorkTime: "08:00-20:00", IsActive: true, IsTransitActive: true},
	}

	// Tariffs
	m.tariffs = []wb.TransitTariff{
		{
			TransitWarehouseName:     "Коледино",
			DestinationWarehouseName: "Казань",
			ActiveFrom:               "2025-01-01",
			PalletTariff:             500,
			BoxTariff: []wb.VolumeTariff{
				{From: 0, To: 10, Value: 50.0},
				{From: 10, To: 100, Value: 30.0},
			},
		},
	}

	// Supplies
	m.supplies = make([]wb.Supply, count)
	createDate := "2026-01-15"

	for i := range count {
		supplyID := int64(10001 + i)
		preorderID := int64(20001 + i)
		statusID := 4 // Shipped

		m.supplies[i] = wb.Supply{
			Phone:        fmt.Sprintf("+7999%07d", i),
			SupplyID:     &supplyID,
			PreorderID:   preorderID,
			CreateDate:   createDate,
			SupplyDate:   strPtr("2026-01-16"),
			FactDate:     strPtr("2026-01-17"),
			UpdatedDate:  strPtr("2026-01-18"),
			StatusID:     statusID,
			BoxTypeID:    1,
			IsBoxOnPallet: boolPtr(false),
		}

		// Details
		actualWHID := 1
		m.details[supplyID] = &wb.SupplyDetails{
			Phone:                m.supplies[i].Phone,
			StatusID:             statusID,
			BoxTypeID:            1,
			CreateDate:           createDate,
			SupplyDate:           strPtr("2026-01-16"),
			FactDate:             strPtr("2026-01-17"),
			UpdatedDate:          strPtr("2026-01-18"),
			WarehouseID:          1,
			WarehouseName:        "Коледино",
			ActualWarehouseID:    &actualWHID,
			ActualWarehouseName:  strPtr("Коледино"),
			SupplierAssignName:   "Mock Supplier",
			Quantity:             3 + i%5,
			AcceptedQuantity:     3 + i%5,
			ReadyForSaleQuantity: 3 + i%5,
			UnloadingQuantity:    3 + i%5,
			IsBoxOnPallet:        boolPtr(false),
		}

		// Goods (3 per supply)
		m.goods[supplyID] = []wb.GoodInSupply{
			{Barcode: fmt.Sprintf("BC%07d01", i), VendorCode: fmt.Sprintf("VC%04d", i*3+1), NmID: int64(300001 + i*3), TechSize: "42", Quantity: 1},
			{Barcode: fmt.Sprintf("BC%07d02", i), VendorCode: fmt.Sprintf("VC%04d", i*3+2), NmID: int64(300002 + i*3), TechSize: "44", Quantity: 1},
			{Barcode: fmt.Sprintf("BC%07d03", i), VendorCode: fmt.Sprintf("VC%04d", i*3+3), NmID: int64(300003 + i*3), TechSize: "46", Quantity: 1},
		}

		// Packages (1 per supply with 3 barcodes)
		m.packages[supplyID] = []wb.Box{
			{
				PackageCode: fmt.Sprintf("PKG%07d", i),
				Quantity:    1,
				Barcodes: []wb.GoodInBox{
					{Barcode: fmt.Sprintf("BC%07d01", i), Quantity: 1},
					{Barcode: fmt.Sprintf("BC%07d02", i), Quantity: 1},
					{Barcode: fmt.Sprintf("BC%07d03", i), Quantity: 1},
				},
			},
		}
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// ============================================================================
// DiscardWriter
// ============================================================================

// DiscardWriter implements Writer with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu              sync.Mutex
	savedWarehouses int
	savedTariffs    int
	savedSupplies   int
	savedGoods      int
	savedPackages   int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveWarehouses counts warehouses but never writes to any database.
func (w *DiscardWriter) SaveWarehouses(_ context.Context, warehouses []wb.Warehouse) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedWarehouses += len(warehouses)
	return len(warehouses), nil
}

// SaveTransitTariffs counts tariffs but never writes.
func (w *DiscardWriter) SaveTransitTariffs(_ context.Context, tariffs []wb.TransitTariff) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedTariffs += len(tariffs)
	return len(tariffs), nil
}

// SaveSupplies counts supplies but never writes.
func (w *DiscardWriter) SaveSupplies(_ context.Context, supplies []SupplyRow) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedSupplies += len(supplies)
	return len(supplies), nil
}

// SaveSupplyGoods counts goods but never writes.
func (w *DiscardWriter) SaveSupplyGoods(_ context.Context, _, _ int64, goods []wb.GoodInSupply) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedGoods += len(goods)
	return len(goods), nil
}

// SaveSupplyPackages counts packages but never writes.
func (w *DiscardWriter) SaveSupplyPackages(_ context.Context, _, _ int64, boxes []wb.Box) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedPackages += len(boxes)
	return len(boxes), nil
}

// CountSupplies returns 0 (no DB in mock mode).
func (w *DiscardWriter) CountSupplies(_ context.Context) (int, error) { return 0, nil }

// CountSupplyGoods returns 0 (no DB in mock mode).
func (w *DiscardWriter) CountSupplyGoods(_ context.Context) (int, error) { return 0, nil }

// SavedWarehouses returns count of warehouses "saved" (counted).
func (w *DiscardWriter) SavedWarehouses() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedWarehouses
}

// SavedTariffs returns count of tariffs "saved" (counted).
func (w *DiscardWriter) SavedTariffs() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedTariffs
}

// SavedSupplies returns count of supplies "saved" (counted).
func (w *DiscardWriter) SavedSupplies() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedSupplies
}

// SavedGoods returns count of goods "saved" (counted).
func (w *DiscardWriter) SavedGoods() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedGoods
}

// SavedPackages returns count of packages "saved" (counted).
func (w *DiscardWriter) SavedPackages() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedPackages
}
