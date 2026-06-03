package stocks

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op StocksWriter for --mock mode.
// Provides realistic mock data for read methods so the pipeline can run end-to-end,
// but never touches any database.
//
// Mock safety: --mock + rewrite must NOT delete real data.
// See dev_v2_downloader.md §1.8 and dev_v2_postgres.md §1.8.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int

	// Mock data for read methods (drives gap detection)
	mockDates []string
}

// NewDiscardWriter creates a DiscardWriter with deterministic mock data.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{
		mockDates: []string{"2026-06-01", "2026-06-02", "2026-06-03"},
	}
}

// Saved returns the total number of rows "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

func (w *DiscardWriter) SaveStocks(_ context.Context, _ string, items []wb.StockWarehouseItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

func (w *DiscardWriter) CountStocks(_ context.Context) (int, error) {
	return w.saved, nil
}

func (w *DiscardWriter) CountStocksForDate(_ context.Context, _ string) (int, error) {
	return w.saved, nil
}

func (w *DiscardWriter) GetDistinctSnapshotDates(_ context.Context) ([]string, error) {
	return w.mockDates, nil
}

// MockStocksSource returns deterministic stock data for --mock mode and tests.
type MockStocksSource struct {
	mu         sync.RWMutex
	items      []wb.StockWarehouseItem
	failCount  int
	failCurrent int
}

// NewMockStocksSource creates a new mock source.
func NewMockStocksSource() *MockStocksSource {
	return &MockStocksSource{}
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockStocksSource) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.failCurrent = 0
}

// PopulateStocks fills mock source with realistic test data.
func (m *MockStocksSource) PopulateStocks(productCount, warehouseCount int) {
	warehouses := []struct {
		id     int64
		name   string
		region string
	}{
		{1, "Коледино", "Москва"},
		{2, "Казань", "Казань"},
		{3, "Электросталь", "Электросталь"},
		{507, "Подольск", "Подольск"},
	}

	items := make([]wb.StockWarehouseItem, 0, productCount*warehouseCount)
	for p := range productCount {
		for w := range min(warehouseCount, len(warehouses)) {
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

	m.mu.Lock()
	m.items = items
	m.mu.Unlock()
}

// GetStockWarehouses returns mock stock items (paginated).
func (m *MockStocksSource) GetStockWarehouses(_ context.Context, limit, offset, _, _ int) ([]wb.StockWarehouseItem, error) {
	m.mu.Lock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		m.mu.Unlock()
		return nil, fmt.Errorf("mock failure")
	}
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if offset >= len(m.items) {
		return nil, nil
	}

	end := min(offset+limit, len(m.items))

	return m.items[offset:end], nil
}
