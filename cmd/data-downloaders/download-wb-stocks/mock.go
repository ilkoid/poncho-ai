// Package main provides mock client for testing.
package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockStocksClient implements StocksClient for testing.
type MockStocksClient struct {
	mu         sync.RWMutex
	items      []wb.StockWarehouseItem
	failCount  int
	failCurrent int
}

// NewMockStocksClient creates a new mock client.
func NewMockStocksClient() *MockStocksClient {
	return &MockStocksClient{}
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockStocksClient) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.failCurrent = 0
}

func (m *MockStocksClient) maybeFail() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		return fmt.Errorf("mock failure")
	}
	return nil
}

// AddItems adds mock stock items.
func (m *MockStocksClient) AddItems(items []wb.StockWarehouseItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, items...)
}

// GetStockWarehouses returns mock stock items (paginated).
func (m *MockStocksClient) GetStockWarehouses(ctx context.Context, limit, offset, rateLimit, burst int) ([]wb.StockWarehouseItem, error) {
	if err := m.maybeFail(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if offset >= len(m.items) {
		return nil, nil
	}

	end := offset + limit
	if end > len(m.items) {
		end = len(m.items)
	}

	return m.items[offset:end], nil
}

// PopulateMockStocks fills mock client with realistic test data.
func PopulateMockStocks(m *MockStocksClient, productCount, warehouseCount int) {
	items := make([]wb.StockWarehouseItem, 0, productCount*warehouseCount)

	warehouses := []struct {
		id   int64
		name string
		region string
	}{
		{1, "Коледино", "Москва"},
		{2, "Казань", "Казань"},
		{3, "Электросталь", "Электросталь"},
		{507, "Подольск", "Подольск"},
	}

	for p := 0; p < productCount; p++ {
		for w := 0; w < warehouseCount && w < len(warehouses); w++ {
			wh := warehouses[w]
			items = append(items, wb.StockWarehouseItem{
				NmID:            int64(100000 + p),
				ChrtID:          int64(200000 + p*10 + w),
				WarehouseID:     wh.id,
				WarehouseName:   wh.name,
				RegionName:      wh.region,
				Quantity:        int64(50 + p*10 - w*5),
				InWayToClient:   int64(5 + p),
				InWayFromClient: int64(3 + p),
			})
		}
	}

	m.AddItems(items)
}
