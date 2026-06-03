package funnel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op FunnelWriter for --mock mode.
// Provides realistic mock nmIDs so the prepare pipeline can run end-to-end,
// but never touches any database.
//
// This is critical for safety: --mock + rewrite: true must NOT delete real data.
// See dev_v2_downloader.md Anti-Pattern: "Mock mode writing to real DB".
type DiscardWriter struct {
	mu    sync.Mutex
	saved int

	// Mock data for read methods (drives prepareNmIDs pipeline)
	mockNmIDs   []int
	mockArticles map[int]string
}

// NewDiscardWriter creates a DiscardWriter with deterministic mock data.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{
		mockNmIDs:   []int{101, 102, 103, 201},
		mockArticles: map[int]string{
			101: "A2401",
			102: "B2502",
			103: "C2603",
			201: "D2604",
		},
	}
}

// Saved returns the total number of products "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

func (w *DiscardWriter) GetDistinctNmIDs(_ context.Context) ([]int, error) {
	return w.mockNmIDs, nil
}

func (w *DiscardWriter) GetSupplierArticlesByNmIDs(_ context.Context, _ []int) (map[int]string, error) {
	return w.mockArticles, nil
}

func (w *DiscardWriter) FilterActiveNmIDs(_ context.Context, ids []int, _ int) ([]int, error) {
	return ids, nil // all pass
}

func (w *DiscardWriter) GetRecentlyLoadedNmIDs(_ context.Context, _ int) (map[int]bool, error) {
	return map[int]bool{}, nil // nothing recently loaded
}

func (w *DiscardWriter) SaveFunnelHistoryWithWindow(_ context.Context, _ wb.FunnelProductMeta, rows []wb.FunnelHistoryRow, _ int) error {
	w.mu.Lock()
	w.saved++
	w.mu.Unlock()
	_ = rows
	return nil
}

// MockFunnelSource returns deterministic funnel data for --mock mode and tests.
type MockFunnelSource struct {
	ProductCount  int // number of products per batch (default: 3)
	DaysPerProduct int // days of history per product (default: 7)
}

// LoadBatch generates deterministic funnel history for the requested nmIDs.
func (m *MockFunnelSource) LoadBatch(_ context.Context, nmIDs []int, from, to string) ([]BatchResult, error) {
	productCount := m.ProductCount
	if productCount == 0 {
		productCount = 3
	}
	daysPerProduct := m.DaysPerProduct
	if daysPerProduct == 0 {
		daysPerProduct = 7
	}

	// Use actual nmIDs if provided, otherwise generate productCount synthetic ones.
	ids := nmIDs
	if len(ids) == 0 {
		ids = make([]int, productCount)
		for i := range ids {
			ids[i] = 100000000 + i
		}
	}

	// Parse date range and cap days.
	days := daysPerProduct
	if from != "" && to != "" {
		tFrom, err := time.Parse("2006-01-02", from)
		if err == nil {
			tTo, err := time.Parse("2006-01-02", to)
			if err == nil {
				d := int(tTo.Sub(tFrom).Hours()/24) + 1
				if d > 0 && d < days {
					days = d
				}
			}
		}
	}

	results := make([]BatchResult, 0, len(ids))
	for i, nmID := range ids {
		meta := wb.FunnelProductMeta{
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
		}

		rows := make([]wb.FunnelHistoryRow, 0, days)
		for day := 0; day < days; day++ {
			baseOpen := 1000 + nmID%500 - day*10
			if baseOpen < 0 {
				baseOpen = 0
			}
			rows = append(rows, wb.FunnelHistoryRow{
				NmID:                  nmID,
				MetricDate:            fmt.Sprintf("2026-01-%02d", day+1),
				OpenCount:             baseOpen,
				CartCount:             baseOpen / 10,
				OrderCount:            baseOpen / 20,
				BuyoutCount:           baseOpen / 50,
				AddToWishlist:         baseOpen / 30,
				OrderSum:              baseOpen / 20 * 500,
				BuyoutSum:             baseOpen / 50 * 400,
				ConversionAddToCart:   10.0,
				ConversionCartToOrder: 50.0,
				ConversionBuyout:      20.0,
			})
		}

		results = append(results, BatchResult{Product: meta, Rows: rows})
	}

	return results, nil
}
