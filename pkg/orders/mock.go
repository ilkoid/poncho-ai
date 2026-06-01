package orders

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockOrdersSource returns deterministic fake order data.
// Implements OrdersSource for --mock mode and testing.
type MockOrdersSource struct {
	mu     sync.RWMutex
	orders []wb.OrdersItem
}

// NewMockOrdersSource creates a mock source pre-populated with count orders.
func NewMockOrdersSource(count int) *MockOrdersSource {
	m := &MockOrdersSource{}
	m.populate(count)
	return m
}

// OrdersIterator iterates over mock orders, calling callback once with all orders.
// Respects context cancellation.
func (m *MockOrdersSource) OrdersIterator(
	ctx context.Context,
	_ string,
	_, _ int,
	_ string,
	callback func([]wb.OrdersItem) error,
) (int, error) {
	// Check context before processing
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.orders) == 0 {
		return 0, nil
	}

	if err := callback(m.orders); err != nil {
		return 0, err
	}
	return len(m.orders), nil
}

// populate fills mock with deterministic order data.
func (m *MockOrdersSource) populate(count int) {
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}
	subjects := []string{"Кроссовки", "Футболка", "Джинсы", "Куртка", "Шорты"}
	warehouses := []string{"Коледино", "Хабаровск", "Электросталь", "Казань", "Подольск"}
	regions := []string{"Москва", "Санкт-Петербург", "Новосибирск", "Екатеринбург", "Казань"}
	baseTime := time.Now().Add(-24 * time.Hour)

	orders := make([]wb.OrdersItem, count)
	for i := 0; i < count; i++ {
		brand := brands[i%len(brands)]
		subject := subjects[i%len(subjects)]
		wh := warehouses[i%len(warehouses)]
		region := regions[i%len(regions)]

		orderTime := baseTime.Add(time.Duration(i) * time.Minute)
		orders[i] = wb.OrdersItem{
			Date:            orderTime.Format(time.RFC3339),
			LastChangeDate:  orderTime.Format(time.RFC3339),
			WarehouseName:   wh,
			WarehouseType:   "FBW",
			CountryName:     "Российская Федерация",
			OblastOkrugName: region + " область",
			RegionName:      region,
			SupplierArticle: fmt.Sprintf("%d25%04d", i%3+1, i), // e.g. "12500001" → year 25
			NmID:            100000 + i,
			Barcode:         fmt.Sprintf("2000%010d", i),
			Category:        "Одежда",
			Subject:         subject,
			Brand:           brand,
			TechSize:        []string{"42", "44", "46", "48", "50"}[i%5],
			IncomeID:        50000 + i%100,
			IsSupply:        i%10 == 0,
			IsRealization:   true,
			TotalPrice:      1500.0 + float64(i%500)*10,
			DiscountPercent: 5 + i%20,
			Spp:             10.0 + float64(i%5)*2,
			FinishedPrice:   1200.0 + float64(i%300)*5,
			PriceWithDisc:   1300.0 + float64(i%400)*3,
			IsCancel:        i%25 == 0,
			CancelDate: func() string {
				if i%25 == 0 {
					return orderTime.Add(1 * time.Hour).Format(time.RFC3339)
				}
				return "0001-01-01T00:00:00"
			}(),
			Sticker: fmt.Sprintf("STK-%06d", i),
			GNumber: fmt.Sprintf("G-%08d", i/3),
			Srid:    fmt.Sprintf("srid-%d-%d", time.Now().UnixMilli(), i),
		}
	}

	m.mu.Lock()
	m.orders = orders
	m.mu.Unlock()
}

// DiscardWriter implements OrdersWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveOrders counts orders but never writes to any database.
func (w *DiscardWriter) SaveOrders(_ context.Context, items []wb.OrdersItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// DeleteOrdersOlderThan is a no-op for mock mode.
func (w *DiscardWriter) DeleteOrdersOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// Saved returns the total count of orders "saved" (counted).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}
