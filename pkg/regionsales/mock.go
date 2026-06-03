package regionsales

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op RegionSalesWriter for --mock mode.
// Counts rows but never writes to any database.
//
// Mock safety: --mock mode must NOT open any database.
// See dev_v2_downloader.md §1.8 and dev_v2_postgres.md §1.8.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a DiscardWriter that counts but never persists.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// Saved returns the total number of rows "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

// SaveRegionSales counts rows without writing to any database.
func (w *DiscardWriter) SaveRegionSales(_ context.Context, _, _ string, items []wb.RegionSaleItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// MockRegionSalesSource returns deterministic region sales data for --mock mode and tests.
type MockRegionSalesSource struct {
	mu          sync.RWMutex
	items       []wb.RegionSaleItem
	failCount   int
	failCurrent int
}

// NewMockRegionSalesSource creates a new mock source.
func NewMockRegionSalesSource() *MockRegionSalesSource {
	return &MockRegionSalesSource{}
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockRegionSalesSource) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.failCurrent = 0
}

// Populate fills mock source with realistic test data.
// Generates productCount * regionCount items using a fixed set of regions.
func (m *MockRegionSalesSource) Populate(productCount, regionCount int) {
	regions := []struct {
		city, region, country, fo string
	}{
		{"Москва", "Московская область", "Россия", "Центральный"},
		{"Санкт-Петербург", "Ленинградская область", "Россия", "Северо-Западный"},
		{"Казань", "Республика Татарстан", "Россия", "Приволжский"},
		{"Новосибирск", "Новосибирская область", "Россия", "Сибирский"},
		{"Екатеринбург", "Свердловская область", "Россия", "Уральский"},
	}

	items := make([]wb.RegionSaleItem, 0, productCount*regionCount)
	for p := range productCount {
		for r := range min(regionCount, len(regions)) {
			rg := regions[r]
			items = append(items, wb.RegionSaleItem{
				CityName:                 rg.city,
				CountryName:              rg.country,
				FoName:                   rg.fo,
				NmID:                     1000000 + p,
				RegionName:               rg.region,
				Sa:                       fmt.Sprintf("SA%06d", p),
				SaleInvoiceCostPrice:     float64(1000+p*100) + float64(r)*50.5,
				SaleInvoiceCostPricePerc: float64(10+p) * 0.1,
				SaleItemInvoiceQty:       5 + p + r,
			})
		}
	}

	m.mu.Lock()
	m.items = items
	m.mu.Unlock()
}

// GetRegionSales returns mock data for any date range.
// Simulates failure if SetFailCount was called.
func (m *MockRegionSalesSource) GetRegionSales(_ context.Context, _, _ string) ([]wb.RegionSaleItem, error) {
	m.mu.Lock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		m.mu.Unlock()
		return nil, fmt.Errorf("mock failure")
	}
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to prevent mutation
	result := make([]wb.RegionSaleItem, len(m.items))
	copy(result, m.items)
	return result, nil
}
