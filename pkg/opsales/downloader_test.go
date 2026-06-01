package opsales

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements OpsalesWriter in-memory for testing.
type mockWriter struct {
	mu      sync.Mutex
	sales   []wb.SalesItem
	deleted int64
}

func newMockWriter() *mockWriter {
	return &mockWriter{}
}

func (w *mockWriter) SaveSales(_ context.Context, sales []wb.SalesItem) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sales = append(w.sales, sales...)
	return len(sales), nil
}

func (w *mockWriter) DeleteSalesOlderThan(_ context.Context, _ time.Time) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deleted = 42 // simulate deletion count
	return w.deleted, nil
}

func (w *mockWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sales)
}

func TestDownloader_Basic(t *testing.T) {
	source := NewMockOpsalesSource(250)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{Days: 90}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalSales != 250 {
		t.Errorf("TotalSales = %d, want 250", result.TotalSales)
	}
	if result.TotalPages != 1 {
		t.Errorf("TotalPages = %d, want 1", result.TotalPages)
	}

	if writer.count() != 250 {
		t.Errorf("saved count = %d, want 250", writer.count())
	}
}

func TestDownloader_DryRun(t *testing.T) {
	source := NewMockOpsalesSource(100)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{
		Days:   90,
		DryRun: true,
	}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalSales != 100 {
		t.Errorf("TotalSales = %d, want 100", result.TotalSales)
	}

	// Writer should have NO sales (dry-run skips SaveSales)
	if writer.count() != 0 {
		t.Errorf("saved count = %d, want 0 (dry-run)", writer.count())
	}
}

func TestDownloader_Rewrite(t *testing.T) {
	source := NewMockOpsalesSource(50)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{
		Days:    90,
		Rewrite: true,
	}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalSales != 50 {
		t.Errorf("TotalSales = %d, want 50", result.TotalSales)
	}
	if writer.count() != 50 {
		t.Errorf("saved count = %d, want 50", writer.count())
	}
	if writer.deleted != 42 {
		t.Errorf("deleted = %d, want 42 (rewrite mode should call DeleteSalesOlderThan)", writer.deleted)
	}
}

func TestDownloader_ContextCancel(t *testing.T) {
	source := NewMockOpsalesSource(250)
	writer := newMockWriter()

	// Pre-cancelled context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	dl := NewDownloader(source, writer, DownloadOptions{Days: 90}, config.FunnelFilterConfig{})
	_, err := dl.Run(ctx)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDownloader_Filter(t *testing.T) {
	source := NewMockOpsalesSource(100)
	writer := newMockWriter()

	// Filter: only articles with year 25 (SupplierArticle format: "X25NNNNN")
	filter := config.FunnelFilterConfig{AllowedYears: []int{25}}
	dl := NewDownloader(source, writer, DownloadOptions{Days: 90}, filter)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// Mock generates articles with year 25 (format: "%d25%04d" where first digit is i%3+1)
	// So most articles should pass the filter
	if result.TotalSales == 0 {
		t.Error("TotalSales = 0, expected some sales to pass year filter")
	}
	if result.TotalSales > 100 {
		t.Errorf("TotalSales = %d, should be <= 100 (filter should remove some)", result.TotalSales)
	}
}
