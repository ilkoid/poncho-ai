package prices

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockPricesSource returns deterministic fake price data.
// Implements PricesSource for --mock mode and testing.
// Supports offset-based pagination matching the real API.
type MockPricesSource struct {
	mu     sync.RWMutex
	prices []wb.ProductPrice
}

// NewMockPricesSource creates a mock source pre-populated with count products.
func NewMockPricesSource(count int) *MockPricesSource {
	m := &MockPricesSource{}
	m.populate(count)
	return m
}

// GetPrices returns a page of mock prices using offset-based pagination.
func (m *MockPricesSource) GetPrices(ctx context.Context, limit, offset int) ([]wb.ProductPrice, int, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if offset >= len(m.prices) {
		return nil, 0, nil
	}

	end := min(offset+limit, len(m.prices))

	page := m.prices[offset:end]
	return page, len(page), nil
}

// populate fills mock with deterministic price data.
func (m *MockPricesSource) populate(count int) {
	vendors := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}
	currencies := []string{"RUB", "RUB", "RUB", "RUB", "KZT"}

	prices := make([]wb.ProductPrice, count)
	for i := range count {
		prices[i] = wb.ProductPrice{
			NmID:                100000 + i,
			VendorCode:          fmt.Sprintf("%s-%04d", vendors[i%len(vendors)], i),
			Price:               1500 + (i%500)*10,
			DiscountedPrice:     float64(1500+i%500*10) * 0.85,
			ClubDiscountedPrice: float64(1500+i%500*10) * 0.80,
			Discount:            5 + i%20,
			ClubDiscount:        10 + i%15,
			Currency:            currencies[i%len(currencies)],
			EditableSizePrice:   i%3 != 0,
		}
	}

	m.mu.Lock()
	m.prices = prices
	m.mu.Unlock()
}

// DiscardWriter implements PricesWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SavePrices counts prices but never writes to any database.
func (w *DiscardWriter) SavePrices(_ context.Context, prices []wb.ProductPrice, _ string) (int, error) {
	w.mu.Lock()
	w.saved += len(prices)
	w.mu.Unlock()
	return len(prices), nil
}

// CountPrices returns the total count of prices "saved" (counted).
func (w *DiscardWriter) CountPrices(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved, nil
}

// Saved returns the total count of prices "saved" (for test verification).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}
