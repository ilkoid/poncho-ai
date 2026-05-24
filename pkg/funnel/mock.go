package funnel

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

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
