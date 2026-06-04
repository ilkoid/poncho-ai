package funnelagg

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op Writer for --mock mode.
// Provides thread-safe counting for test assertions.
// Never touches any database — critical for safety (--mock + rewrite must NOT delete real data).
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a DiscardWriter that counts but never persists.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// Saved returns the total number of products "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

func (w *DiscardWriter) SaveFunnelAggregatedBatch(_ context.Context, products []wb.FunnelAggregatedProduct, _, _, _ string) (int, error) {
	w.mu.Lock()
	w.saved += len(products)
	w.mu.Unlock()
	return len(products), nil
}

func (w *DiscardWriter) GetFunnelAggregatedCount(_ context.Context, _, _ string) (int, error) {
	return 0, nil // no existing data in discard mode
}

func (w *DiscardWriter) GetDistinctNmIDCount(_ context.Context) (int, error) {
	return 0, nil // no sales data in discard mode
}

// MockSource generates deterministic aggregated funnel data for --mock mode and tests.
// Supports offset-based pagination: returns products in pages of PageSize,
// up to TotalPages total. After that, returns empty (signals end).
type MockSource struct {
	ProductCount int // products per page (default: 5)
	TotalPages   int // total pages before empty response (default: 3)
}

// LoadAggregatedPage returns deterministic mock products for the requested offset.
func (m *MockSource) LoadAggregatedPage(_ context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	productCount := m.ProductCount
	if productCount == 0 {
		productCount = 5
	}
	totalPages := m.TotalPages
	if totalPages == 0 {
		totalPages = 3
	}

	// Calculate which page this is
	page := req.Offset / req.Limit
	if page >= totalPages {
		return nil, "RUB", nil // end of data
	}

	// Generate mock products
	products := make([]wb.FunnelAggregatedProduct, 0, productCount)
	for i := 0; i < productCount; i++ {
		nmID := 100000000 + page*productCount + i
		products = append(products, wb.FunnelAggregatedProduct{
			Product: wb.FunnelProductExtended{
				FunnelProductMeta: wb.FunnelProductMeta{
					NmID:          nmID,
					VendorCode:    fmt.Sprintf("ART-%d", nmID),
					Title:         fmt.Sprintf("Mock Product %d", nmID),
					BrandName:     "MockBrand",
					SubjectID:     100 + i,
					SubjectName:   fmt.Sprintf("Category %d", 100+i),
					ProductRating: 4.5,
					FeedbackRating: 4.2,
					StockWB:       50,
					StockMP:       30,
					StockBalance:  80,
				},
				Stocks: wb.ProductStocks{WB: 50, MP: 30, BalanceSum: 80},
				Tags:   []wb.ProductTag{{ID: 1, Name: "mock-tag"}},
			},
			Statistic: wb.FunnelAggregatedStatistic{
				Selected: wb.FunnelPeriodStats{
					Period:               wb.PeriodRange{Start: req.SelectedPeriod.Start, End: req.SelectedPeriod.End},
					OpenCount:            1000 + nmID%500,
					CartCount:            100,
					OrderCount:           50,
					OrderSum:             25000,
					BuyoutCount:          20,
					BuyoutSum:            8000,
					CancelCount:          2,
					CancelSum:            500,
					AvgPrice:             400,
					AvgOrdersCountPerDay: 7.14,
					ShareOrderPercent:    5.0,
					AddToWishlist:        30,
					LocalizationPercent:  85.0,
					TimeToReady:          wb.FunnelTimeToReady{Days: 2, Hours: 5, Mins: 30},
					WBClub:               wb.FunnelWBClubStats{OrderCount: 5, OrderSum: 2000, BuyoutCount: 3, BuyoutSum: 1200, AvgPrice: 400, BuyoutPercent: 60.0, AvgOrderCountPerDay: 0.7},
					Conversions:          wb.FunnelConversionStats{AddToCartPercent: 10.0, CartToOrderPercent: 50.0, BuyoutPercent: 40.0},
				},
			},
		})
	}

	return products, "RUB", nil
}
