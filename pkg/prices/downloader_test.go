package prices

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// countingWriter is an in-memory PricesWriter for tests.
type countingWriter struct {
	mu     sync.Mutex
	prices []wb.ProductPrice
}

func (w *countingWriter) SavePrices(_ context.Context, prices []wb.ProductPrice, _ string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prices = append(w.prices, prices...)
	return len(prices), nil
}

func (w *countingWriter) CountPrices(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.prices), nil
}

func (w *countingWriter) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.prices)
}

func TestDownloader_Basic(t *testing.T) {
	source := NewMockPricesSource(2500) // 3 pages: 1000 + 1000 + 500
	writer := &countingWriter{}

	opts := DownloadOptions{
		PageSize:     1000,
		SnapshotDate: time.Now().Format("2006-01-02"),
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.TotalProducts != 2500 {
		t.Errorf("TotalProducts = %d, want 2500", result.TotalProducts)
	}
	if result.Pages != 3 {
		t.Errorf("Pages = %d, want 3", result.Pages)
	}
	if result.Requests != 3 {
		t.Errorf("Requests = %d, want 3", result.Requests)
	}

	// Verify writer received all prices
	if writer.len() != 2500 {
		t.Errorf("writer received %d prices, want 2500", writer.len())
	}
}

func TestDownloader_DryRun(t *testing.T) {
	source := NewMockPricesSource(1500) // 2 pages: 1000 + 500
	writer := &countingWriter{}

	opts := DownloadOptions{
		PageSize:     1000,
		SnapshotDate: "2026-01-15",
		DryRun:       true,
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.TotalProducts != 1500 {
		t.Errorf("TotalProducts = %d, want 1500", result.TotalProducts)
	}

	// Writer should NOT have been called
	if writer.len() != 0 {
		t.Errorf("dry-run: writer received %d prices, want 0", writer.len())
	}
}

func TestDownloader_ContextCancel(t *testing.T) {
	source := NewMockPricesSource(2500)
	writer := &countingWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	opts := DownloadOptions{
		PageSize:     1000,
		SnapshotDate: "2026-01-15",
	}

	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDownloader_EmptySource(t *testing.T) {
	source := NewMockPricesSource(0)
	writer := &countingWriter{}

	opts := DownloadOptions{
		PageSize:     1000,
		SnapshotDate: "2026-01-15",
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.TotalProducts != 0 {
		t.Errorf("TotalProducts = %d, want 0", result.TotalProducts)
	}
	if result.Pages != 0 {
		t.Errorf("Pages = %d, want 0", result.Pages)
	}
	if result.Requests != 1 {
		t.Errorf("Requests = %d, want 1 (initial fetch that returns empty)", result.Requests)
	}
}

func TestDownloader_DefaultPageSize(t *testing.T) {
	source := NewMockPricesSource(0)
	writer := &countingWriter{}

	opts := DownloadOptions{
		SnapshotDate: "2026-01-15",
		// PageSize intentionally 0 — should default to 1000
	}

	dl := NewDownloader(source, writer, opts)
	if dl.opts.PageSize != 1000 {
		t.Errorf("default PageSize = %d, want 1000", dl.opts.PageSize)
	}
}

func TestDiscardWriter(t *testing.T) {
	w := NewDiscardWriter()

	prices := []wb.ProductPrice{
		{NmID: 1, Price: 100},
		{NmID: 2, Price: 200},
	}

	n, err := w.SavePrices(context.Background(), prices, "2026-01-15")
	if err != nil {
		t.Fatalf("SavePrices: %v", err)
	}
	if n != 2 {
		t.Errorf("SavePrices returned %d, want 2", n)
	}
	if w.Saved() != 2 {
		t.Errorf("Saved() = %d, want 2", w.Saved())
	}

	count, err := w.CountPrices(context.Background())
	if err != nil {
		t.Fatalf("CountPrices: %v", err)
	}
	if count != 2 {
		t.Errorf("CountPrices = %d, want 2", count)
	}
}
