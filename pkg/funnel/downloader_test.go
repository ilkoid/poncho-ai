package funnel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements FunnelWriter for tests.
type mockWriter struct {
	mu sync.Mutex

	nmIDs      []int
	articles   map[int]string
	activeIDs  []int
	recentIDs  map[int]bool

	savedProducts []wb.FunnelProductMeta
	savedRows     []wb.FunnelHistoryRow
}

func newMockWriter() *mockWriter {
	return &mockWriter{
		nmIDs:     []int{101, 102, 103, 201},
		articles:  map[int]string{101: "A2401", 102: "B2502", 103: "C2603", 201: "123456"},
		activeIDs: []int{101, 102, 103, 201},
		recentIDs: map[int]bool{},
	}
}

func (m *mockWriter) GetDistinctNmIDs(_ context.Context) ([]int, error) {
	return m.nmIDs, nil
}

func (m *mockWriter) GetSupplierArticlesByNmIDs(_ context.Context, _ []int) (map[int]string, error) {
	return m.articles, nil
}

func (m *mockWriter) FilterActiveNmIDs(_ context.Context, ids []int, _ int) ([]int, error) {
	return m.activeIDs, nil
}

func (m *mockWriter) GetRecentlyLoadedNmIDs(_ context.Context, _ int) (map[int]bool, error) {
	return m.recentIDs, nil
}

func (m *mockWriter) SaveFunnelHistoryWithWindow(_ context.Context, product wb.FunnelProductMeta, rows []wb.FunnelHistoryRow, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.savedProducts = append(m.savedProducts, product)
	m.savedRows = append(m.savedRows, rows...)
	return nil
}

func (m *mockWriter) getSaved() ([]wb.FunnelProductMeta, []wb.FunnelHistoryRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedProducts, m.savedRows
}

func TestDownloader_Run_Basic(t *testing.T) {
	w := newMockWriter()
	source := &MockFunnelSource{ProductCount: 3, DaysPerProduct: 2}
	dl := NewDownloader(source, w, DownloadOptions{
		Days:      3,
		BatchSize: 20,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProductsLoaded == 0 {
		t.Error("expected products loaded > 0")
	}
	if result.MetricsLoaded == 0 {
		t.Error("expected metrics loaded > 0")
	}

	products, rows := w.getSaved()
	if len(products) == 0 {
		t.Error("expected saved products")
	}
	if len(rows) == 0 {
		t.Error("expected saved rows")
	}
}

func TestDownloader_Run_DryRun(t *testing.T) {
	w := newMockWriter()
	source := &MockFunnelSource{ProductCount: 3, DaysPerProduct: 2}
	dl := NewDownloader(source, w, DownloadOptions{
		Days:      3,
		BatchSize: 20,
		DryRun:    true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProductsLoaded == 0 {
		t.Error("dry-run should still count products")
	}
	if result.MetricsLoaded == 0 {
		t.Error("dry-run should still count metrics")
	}

	products, _ := w.getSaved()
	if len(products) > 0 {
		t.Error("dry-run should NOT save to writer")
	}
}

func TestDownloader_Run_FilterByArticle(t *testing.T) {
	w := newMockWriter()
	source := &MockFunnelSource{}
	dl := NewDownloader(source, w, DownloadOptions{
		Days:      3,
		BatchSize: 20,
		Filter: config.FunnelFilterConfig{
			ExcludeLengths: []int{6}, // "123456" has length 6
		},
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProductsLoaded == 0 {
		t.Error("expected some products to pass filter")
	}
}

func TestDownloader_Run_IncrementalSkip(t *testing.T) {
	w := newMockWriter()
	w.recentIDs = map[int]bool{101: true, 102: true, 103: true}
	source := &MockFunnelSource{}
	dl := NewDownloader(source, w, DownloadOptions{
		Days:             3,
		BatchSize:        20,
		IncrementalHours: 12,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Only nmID 201 should remain (101,102,103 are "recent")
	if result.ProductsLoaded == 0 {
		t.Error("expected nmID 201 to still be loaded")
	}
}

func TestDownloader_Run_Cancelled(t *testing.T) {
	w := newMockWriter()
	source := &MockFunnelSource{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dl := NewDownloader(source, w, DownloadOptions{Days: 3})
	_, err := dl.Run(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestDownloader_Run_EmptyProducts(t *testing.T) {
	w := newMockWriter()
	w.nmIDs = []int{}
	source := &MockFunnelSource{}
	dl := NewDownloader(source, w, DownloadOptions{Days: 3})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProductsLoaded != 0 {
		t.Error("expected 0 products with empty nmIDs")
	}
}

func TestDownloader_Run_ExplicitDates(t *testing.T) {
	w := newMockWriter()
	source := &MockFunnelSource{ProductCount: 2, DaysPerProduct: 3}
	dl := NewDownloader(source, w, DownloadOptions{
		From:      "2026-05-20",
		To:        "2026-05-22",
		BatchSize: 20,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProductsLoaded == 0 {
		t.Error("expected products with explicit dates")
	}
}

func TestShouldFilterArticle(t *testing.T) {
	tests := []struct {
		name          string
		article       string
		excludeLens   []int
		allowedYears  []int
		want          bool
	}{
		{"no filter", "A2401", nil, nil, false},
		{"exclude by length", "123456", []int{6}, nil, true},
		{"pass length", "A2401", []int{6}, nil, false},
		{"allowed year", "A2401", nil, []int{24, 25, 26}, false},
		{"disallowed year", "A2301", nil, []int{24, 25, 26}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldFilterArticle(tt.article, tt.excludeLens, tt.allowedYears)
			if got != tt.want {
				t.Errorf("shouldFilterArticle(%q) = %v, want %v", tt.article, got, tt.want)
			}
		})
	}
}

func TestChunkInts(t *testing.T) {
	result := chunkInts([]int{1, 2, 3, 4, 5}, 2)
	if len(result) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(result))
	}
	if len(result[2]) != 1 {
		t.Errorf("last chunk should have 1 element, got %d", len(result[2]))
	}
}

func TestResolveDates(t *testing.T) {
	dl := NewDownloader(nil, nil, DownloadOptions{Days: 3})
	from, to := dl.resolveDates()

	expectedTo := time.Now().Format("2006-01-02")
	if to != expectedTo {
		t.Errorf("to = %q, want %q", to, expectedTo)
	}

	dl2 := NewDownloader(nil, nil, DownloadOptions{From: "2026-01-01", To: "2026-01-07"})
	from, to = dl2.resolveDates()
	if from != "2026-01-01" || to != "2026-01-07" {
		t.Errorf("explicit dates: from=%q to=%q", from, to)
	}
}
