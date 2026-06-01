package opsales

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockOpsalesSource returns deterministic fake operational sales data.
// Implements OpsalesSource for --mock mode and testing.
type MockOpsalesSource struct {
	mu    sync.RWMutex
	sales []wb.SalesItem
}

// NewMockOpsalesSource creates a mock source pre-populated with count sales.
func NewMockOpsalesSource(count int) *MockOpsalesSource {
	m := &MockOpsalesSource{}
	m.populate(count)
	return m
}

// SalesIterator iterates over mock sales, calling callback once with all sales.
// Respects context cancellation.
func (m *MockOpsalesSource) SalesIterator(
	ctx context.Context,
	_ string,
	_, _ int,
	_ string,
	callback func([]wb.SalesItem) error,
) (int, error) {
	// Check context before processing
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.sales) == 0 {
		return 0, nil
	}

	if err := callback(m.sales); err != nil {
		return 0, err
	}
	return len(m.sales), nil
}

// populate fills mock with deterministic operational sales data.
// ~80% sales (SaleID starts with "S"), ~20% returns (starts with "R").
func (m *MockOpsalesSource) populate(count int) {
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}
	subjects := []string{"Кроссовки", "Футболка", "Джинсы", "Куртка", "Шорты"}
	warehouses := []string{"Коледино", "Хабаровск", "Электросталь", "Казань", "Подольск"}
	regions := []string{"Москва", "Санкт-Петербург", "Новосибирск", "Екатеринбург", "Казань"}
	baseTime := time.Now().Add(-24 * time.Hour)

	sales := make([]wb.SalesItem, count)
	for i := 0; i < count; i++ {
		brand := brands[i%len(brands)]
		subject := subjects[i%len(subjects)]
		wh := warehouses[i%len(warehouses)]
		region := regions[i%len(regions)]

		saleTime := baseTime.Add(time.Duration(i) * time.Minute)

		// ~80% sale, ~20% return
		isReturn := i%5 == 0
		saleIDPrefix := "S"
		if isReturn {
			saleIDPrefix = "R"
		}

		totalPrice := 1500.0 + float64(i%500)*10
		discount := 5 + i%20
		priceWithDisc := totalPrice * (1 - float64(discount)/100)

		sales[i] = wb.SalesItem{
			Date:            saleTime.Format(time.RFC3339),
			LastChangeDate:  saleTime.Format(time.RFC3339),
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
			TotalPrice:      totalPrice,
			DiscountPercent: discount,
			Spp:             10.0 + float64(i%5)*2,
			PaymentSaleAmount: int(priceWithDisc * 0.9),
			ForPay:          priceWithDisc * 0.85,
			FinishedPrice:   1200.0 + float64(i%300)*5,
			PriceWithDisc:   priceWithDisc,
			SaleID:          fmt.Sprintf("%s%08d", saleIDPrefix, i),
			Sticker:         fmt.Sprintf("STK-%06d", i),
			GNumber:         fmt.Sprintf("G-%08d", i/3),
			Srid:            fmt.Sprintf("srid-%d-%d", time.Now().UnixMilli(), i),
		}
	}

	m.mu.Lock()
	m.sales = sales
	m.mu.Unlock()
}

// DiscardWriter implements OpsalesWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveSales counts sales but never writes to any database.
func (w *DiscardWriter) SaveSales(_ context.Context, items []wb.SalesItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// DeleteSalesOlderThan is a no-op for mock mode.
func (w *DiscardWriter) DeleteSalesOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// Saved returns the total count of sales "saved" (counted).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}
