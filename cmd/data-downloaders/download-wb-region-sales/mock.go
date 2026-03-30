package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockRegionSalesClient implements RegionSalesClient for testing.
type MockRegionSalesClient struct {
	mu    sync.RWMutex
	items []wb.RegionSaleItem
}

// NewMockRegionSalesClient creates a new mock client.
func NewMockRegionSalesClient() *MockRegionSalesClient {
	return &MockRegionSalesClient{}
}

// GetRegionSales returns mock region sale items (ignores date/rate params in mock).
func (m *MockRegionSalesClient) GetRegionSales(_ context.Context, _, _ string, _, _ int) ([]wb.RegionSaleItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items, nil
}

// PopulateMockRegionSales fills mock client with realistic test data.
// Generates productCount * regionCount items.
func PopulateMockRegionSales(m *MockRegionSalesClient, productCount, regionCount int) {
	regions := []struct {
		city, region, fo, country string
	}{
		{"Москва", "Москва", "Центральный федеральный округ", "Россия"},
		{"Санкт-Петербург", "Санкт-Петербург", "Северо-Западный федеральный округ", "Россия"},
		{"Казань", "Республика Татарстан", "Приволжский федеральный округ", "Россия"},
		{"Новосибирск", "Новосибирская область", "Сибирский федеральный округ", "Россия"},
		{"Екатеринбург", "Свердловская область", "Уральский федеральный округ", "Россия"},
	}

	items := make([]wb.RegionSaleItem, 0, productCount*regionCount)
	for p := range productCount {
		for r := range min(regionCount, len(regions)) {
			rg := regions[r]
			items = append(items, wb.RegionSaleItem{
				CityName:                 rg.city,
				CountryName:              rg.country,
				FoName:                   rg.fo,
				NmID:                     100000 + p,
				RegionName:               rg.region,
				Sa:                       fmt.Sprintf("SA-%d", p),
				SaleInvoiceCostPrice:     float64(100+p*10) + 0.11,
				SaleInvoiceCostPricePerc: float64(10+p) + 0.5,
				SaleItemInvoiceQty:       1 + p%5,
			})
		}
	}
	m.mu.Lock()
	m.items = items
	m.mu.Unlock()
}
