package searchvis

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
)

// MockSource returns deterministic fake search visibility data.
// Implements Source for --mock mode and testing.
type MockSource struct {
	mu sync.RWMutex
}

// NewMockSource creates a mock source with deterministic fake data.
func NewMockSource() *MockSource {
	return &MockSource{}
}

// FetchPositions returns mock position data — one row per nmID with randomised metrics.
func (m *MockSource) FetchPositions(_ context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	rows := make([]SearchPositionRow, 0, len(req.NmIDs))
	for _, nmID := range req.NmIDs {
		rows = append(rows, SearchPositionRow{
			NmID:                 nmID,
			SnapshotDate:         "2026-06-04",
			AvgPosition:          float64(20 + rand.Intn(80)),
			AvgPositionDynamics:  float64(rand.Intn(40) - 20),
			MedianPosition:       float64(15 + rand.Intn(60)),
			Visibility:           float64(10 + rand.Intn(40)),
			VisibilityDynamics:   float64(rand.Intn(20) - 10),
			OpenCard:             50 + rand.Intn(200),
			OpenCardDynamics:     float64(rand.Intn(30) - 15),
			ClusterFirstHundred:  30,
			ClusterSecondHundred: 10,
			ClusterBelow:         5,
			PeriodStart:          req.Begin,
			PeriodEnd:            req.End,
		})
	}
	return rows, nil
}

// FetchSearchTexts returns mock search query data — 3 queries per nmID.
func (m *MockSource) FetchSearchTexts(_ context.Context, req TextsRequest) ([]SearchQueryRow, error) {
	mockQueries := []struct {
		text     string
		freq     int
		position float64
	}{
		{"платье женское", 150, 12.5},
		{"вечернее платье", 90, 25.3},
		{"платье черное", 75, 8.1},
	}

	rows := make([]SearchQueryRow, 0, len(req.NmIDs)*len(mockQueries))
	for _, nmID := range req.NmIDs {
		for i, q := range mockQueries {
			rows = append(rows, SearchQueryRow{
				NmID:                nmID,
				SnapshotDate:        "2026-06-04",
				SearchText:          q.text,
				Frequency:           q.freq - i*20,
				FrequencyDynamics:   float64(rand.Intn(30) - 15),
				WeekFrequency:       q.freq*7 - i*100,
				AvgPosition:         q.position + float64(i),
				AvgPositionDynamics: float64(rand.Intn(10) - 5),
				MedianPosition:      q.position - 1 + float64(i),
				MedianPosDynamics:   float64(rand.Intn(10) - 5),
				Visibility:          15.0 + float64(i*5),
				OpenCard:            100 + i*20,
				AddToCart:           30 + i*10,
				Orders:              10 + i*5,
				OpenToCart:          0.3 + float64(i)*0.05,
				CartToOrder:         0.25 + float64(i)*0.03,
				VendorCode:          fmt.Sprintf("mock%d", nmID%100),
				BrandName:           "MockBrand",
				SubjectName:         "Платья",
				PeriodStart:         req.Begin,
				PeriodEnd:           req.End,
			})
		}
	}
	return rows, nil
}

// ============================================================================
// DiscardWriter — no-op persistence for --mock mode
// ============================================================================

// DiscardWriter implements Writer with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
// Thread-safe: all counters protected by sync.Mutex.
type DiscardWriter struct {
	mu            sync.Mutex
	savedPositions int
	savedQueries   int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveSearchPositions counts rows but never writes to any database.
func (w *DiscardWriter) SaveSearchPositions(_ context.Context, rows []SearchPositionRow) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedPositions += len(rows)
	return len(rows), nil
}

// SaveSearchQueries counts rows but never writes.
func (w *DiscardWriter) SaveSearchQueries(_ context.Context, rows []SearchQueryRow) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedQueries += len(rows)
	return len(rows), nil
}

// CountSearchPositions returns total rows "saved" (counted).
func (w *DiscardWriter) CountSearchPositions(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedPositions, nil
}

// CountSearchQueries returns total rows "saved" (counted).
func (w *DiscardWriter) CountSearchQueries(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedQueries, nil
}

// SavedPositions returns count of position rows "saved" (for testing assertions).
func (w *DiscardWriter) SavedPositions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedPositions
}

// SavedQueries returns count of query rows "saved" (for testing assertions).
func (w *DiscardWriter) SavedQueries() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedQueries
}

// ============================================================================
// MockReader — synthetic nmID resolution for --mock mode
// ============================================================================

// MockReader implements Reader with synthetic data.
// Used in --mock mode — NO database interaction.
type MockReader struct {
	mu     sync.RWMutex
	nmIDs  []int
	articles map[int]string
}

// NewMockReader creates a mock reader with deterministic fake nmIDs.
func NewMockReader() *MockReader {
	nmIDs := []int{101, 102, 201, 301}
	articles := map[int]string{
		101: "1240001", // 7 chars, article[1:3]="24" → year 24
		102: "1240002",
		201: "1250001", // 7 chars, article[1:3]="25" → year 25
		301: "1260001", // 7 chars, article[1:3]="26" → year 26
	}
	return &MockReader{nmIDs: nmIDs, articles: articles}
}

// GetDistinctNmIDs returns synthetic nmID list.
func (r *MockReader) GetDistinctNmIDs(_ context.Context) ([]int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]int, len(r.nmIDs))
	copy(result, r.nmIDs)
	return result, nil
}

// GetSupplierArticlesByNmIDs returns synthetic article mapping.
func (r *MockReader) GetSupplierArticlesByNmIDs(_ context.Context, nmIDs []int) (map[int]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[int]string, len(nmIDs))
	for _, id := range nmIDs {
		if a, ok := r.articles[id]; ok {
			result[id] = a
		}
	}
	return result, nil
}

// FilterActiveNmIDs returns a subset of nmIDs (simulates active filter).
// Returns all nmIDs if activeDays <= 0.
func (r *MockReader) FilterActiveNmIDs(_ context.Context, nmIDs []int, activeDays int) ([]int, error) {
	if activeDays <= 0 {
		return nmIDs, nil
	}
	// Return first 3 out of 4 to simulate filtering
	if len(nmIDs) > 3 {
		return nmIDs[:3], nil
	}
	return nmIDs, nil
}
