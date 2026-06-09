package stockproducts

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op StockProductsWriter for --mock mode.
// Never touches any database — only counts rows.
//
// Mock safety: --mock + rewrite must NOT delete real data.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a DiscardWriter for --mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// Saved returns the total number of rows "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

func (w *DiscardWriter) SaveStockProducts(_ context.Context, _ string, items []wb.StockProductItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

func (w *DiscardWriter) CountStockProducts(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved, nil
}

func (w *DiscardWriter) CountStockProductsForDate(_ context.Context, _ string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved, nil
}

// MockStockProductsSource returns deterministic stock product data for tests.
type MockStockProductsSource struct {
	mu          sync.RWMutex
	items       []wb.StockProductItem
	failCount   int
	failCurrent int
}

// NewMockStockProductsSource creates a new mock source with 50 default products.
func NewMockStockProductsSource() *MockStockProductsSource {
	m := &MockStockProductsSource{}
	m.PopulateProducts(50)
	return m
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockStockProductsSource) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.failCurrent = 0
}

// PopulateProducts fills mock source with deterministic test data.
func (m *MockStockProductsSource) PopulateProducts(count int) {
	items := make([]wb.StockProductItem, count)
	for i := range count {
		items[i] = wb.StockProductItem{
			NmID:        int64(100000 + i),
			IsDeleted:   false,
			SubjectName: "Кроссовки",
			Name:        fmt.Sprintf("Mock Product %d", i),
			VendorCode:  fmt.Sprintf("12%d", i),
			BrandName:   "MockBrand",
			MainPhoto:   "https://mock.photo/1.jpg",
			HasSizes:    true,
			Metrics: wb.StockProductMetrics{
				OrdersCount:     int64(100 + i*10),
				OrdersSum:       int64(150000 + i*1000),
				BuyoutCount:     int64(85 + i*5),
				BuyoutSum:       int64(120000 + i*500),
				BuyoutPercent:   85,
				StockCount:      int64(50 + i*3),
				StockSum:        int64(75000 + i*200),
				ToClientCount:   int64(10 + i),
				FromClientCount: int64(5 + i),
				AvgOrders:       float64(3 + i),
				SaleRate:        wb.DurationMetrics{Days: 14, Hours: 5},
				AvgStockTurnover: wb.DurationMetrics{Days: 30, Hours: 0},
				OfficeMissingTime: wb.DurationMetrics{Days: -3, Hours: 0},
				LostOrdersCount:  float64(12 + i),
				LostOrdersSum:    float64(18000 + i*100),
				LostBuyoutsCount: float64(8 + i),
				LostBuyoutsSum:   float64(12000 + i*50),
				AvgOrdersByMonth: []wb.FloatGraphByPeriodItem{
					{Start: "2026-05-01", End: "2026-05-31", Value: 2.5 + float64(i)},
				},
				CurrentPrice: wb.PriceRange{MinPrice: int64(1500 + i*100), MaxPrice: int64(2500 + i*100)},
				Availability: "actual",
			},
		}
	}
	m.mu.Lock()
	m.items = items
	m.mu.Unlock()
}

// GetStockProducts returns mock stock product items (paginated by Limit/Offset).
func (m *MockStockProductsSource) GetStockProducts(_ context.Context, req wb.StockProductRequest) ([]wb.StockProductItem, error) {
	m.mu.Lock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		m.mu.Unlock()
		return nil, fmt.Errorf("mock failure")
	}
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if req.Offset >= len(m.items) {
		return nil, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = MaxPageSize
	}
	end := min(req.Offset+limit, len(m.items))
	return m.items[req.Offset:end], nil
}
