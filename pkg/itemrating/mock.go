package itemrating

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op Writer for --mock mode.
// Provides thread-safe counting for test assertions.
// Never touches any database — critical for mock safety (§1.8).
type DiscardWriter struct {
	mu        sync.Mutex
	saved     int
	sellerSaved int
}

// NewDiscardWriter creates a DiscardWriter that counts but never persists.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// Saved returns the total number of item rows "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

// SellerSaved returns how many times the seller block was "saved".
func (w *DiscardWriter) SellerSaved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sellerSaved
}

func (w *DiscardWriter) SaveItemRatings(_ context.Context, rows []Row) (int, error) {
	w.mu.Lock()
	w.saved += len(rows)
	w.mu.Unlock()
	return len(rows), nil
}

func (w *DiscardWriter) SaveSellerRating(_ context.Context, _ SellerRow) (int, error) {
	w.mu.Lock()
	w.sellerSaved++
	w.mu.Unlock()
	return 1, nil
}

func (w *DiscardWriter) CountItemRatings(_ context.Context, _, _ string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved, nil // mirrors what a real backend would report
}

// MockSource generates deterministic item rating data for --mock mode and tests.
// Supports offset-based pagination: CatalogSize items total, req.Limit per page.
// After the catalog is exhausted, returns an empty page (signals end).
type MockSource struct {
	CatalogSize int // total items (default: 2500 → partial last page at PageSize 1000)
	FailAtPage  int // 1-based page number that fails (0 = never) — error-continuation tests
}

// NewMockSource creates a mock source with the default catalog size.
func NewMockSource() *MockSource {
	return &MockSource{CatalogSize: 2500}
}

// FetchPage returns deterministic mock rows for the requested offset.
func (m *MockSource) FetchPage(_ context.Context, req wb.ItemRatingRequest) (Page, error) {
	catalog := m.CatalogSize
	if catalog == 0 {
		catalog = 2500
	}
	limit := req.Limit
	if limit <= 0 {
		limit = PageSizeMax
	}

	pageNum := req.Offset/limit + 1
	if m.FailAtPage > 0 && pageNum == m.FailAtPage {
		return Page{}, fmt.Errorf("mock failure at page %d", pageNum)
	}

	if req.Offset >= catalog {
		return Page{}, nil // end of data
	}

	count := catalog - req.Offset
	if count > limit {
		count = limit
	}

	items := make([]Row, 0, count)
	for i := 0; i < count; i++ {
		nmID := int64(162000000 + req.Offset + i)
		pct := 25.0 + float64(i%75)
		items = append(items, Row{
			PeriodStart:         req.CurrentPeriod.Start,
			PeriodEnd:           req.CurrentPeriod.End,
			NmID:                nmID,
			VendorCode:          fmt.Sprintf("mock-%d", nmID),
			Title:               fmt.Sprintf("Mock Item %d", nmID),
			SubjectID:           100,
			SubjectName:         "Mock Subject",
			BrandName:           "MockBrand",
			FeedbackRatingCurrent:  4.5,
			FeedbackRatingPercentile: &pct,
			FiveStarInc:         1 + i%3,
			Disqualified:        i % 11,
			IsShadowed:          i%997 == 0,
			RawItem:             mockRawItem(nmID),
		})
	}

	return Page{
		Items:    items,
		APICount: count,
		Seller: &SellerRow{
			PeriodStart:        req.CurrentPeriod.Start,
			PeriodEnd:         req.CurrentPeriod.End,
			SellerRatingCurrent: 4.62,
			FbIncreaseCurrent:  26,
			FbIncreaseTotal:    116,
			FiveStarTotal:      51,
			RawSeller:          []byte(`{"current":4.62,"dynamics":0.03}`),
			RawFbIncrease:      []byte(`{"current":26,"total":116}`),
		},
	}, nil
}

// mockRawItem builds a compact raw JSON for a mock row (RawItem parity with the real API).
func mockRawItem(nmID int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"nmId":         nmID,
		"feedbackRating": map[string]any{"current": 4.5},
		"isShadowed":   false,
	})
	return b
}
