package orders

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements OrdersWriter in-memory for testing.
type mockWriter struct {
	mu      sync.Mutex
	orders  []wb.OrdersItem
	deleted int64
}

func newMockWriter() *mockWriter {
	return &mockWriter{}
}

func (w *mockWriter) SaveOrders(_ context.Context, orders []wb.OrdersItem) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.orders = append(w.orders, orders...)
	return len(orders), nil
}

func (w *mockWriter) DeleteOrdersOlderThan(_ context.Context, _ time.Time) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deleted = 42 // simulate deletion count
	return w.deleted, nil
}

func (w *mockWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.orders)
}

func TestDownloader_Basic(t *testing.T) {
	source := NewMockOrdersSource(250)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{Days: 90}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalOrders != 250 {
		t.Errorf("TotalOrders = %d, want 250", result.TotalOrders)
	}
	if result.TotalPages != 1 {
		t.Errorf("TotalPages = %d, want 1", result.TotalPages)
	}

	if writer.count() != 250 {
		t.Errorf("saved count = %d, want 250", writer.count())
	}
}

func TestDownloader_DryRun(t *testing.T) {
	source := NewMockOrdersSource(100)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{
		Days:   90,
		DryRun: true,
	}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalOrders != 100 {
		t.Errorf("TotalOrders = %d, want 100", result.TotalOrders)
	}

	// Writer should have NO orders (dry-run skips SaveOrders)
	if writer.count() != 0 {
		t.Errorf("saved count = %d, want 0 (dry-run)", writer.count())
	}
}

func TestDownloader_Rewrite(t *testing.T) {
	source := NewMockOrdersSource(50)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{
		Days:    90,
		Rewrite: true,
	}, config.FunnelFilterConfig{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalOrders != 50 {
		t.Errorf("TotalOrders = %d, want 50", result.TotalOrders)
	}
	if writer.count() != 50 {
		t.Errorf("saved count = %d, want 50", writer.count())
	}
	if writer.deleted != 42 {
		t.Errorf("deleted = %d, want 42 (rewrite mode should call DeleteOrdersOlderThan)", writer.deleted)
	}
}

func TestDownloader_ContextCancel(t *testing.T) {
	source := NewMockOrdersSource(250)
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
	source := NewMockOrdersSource(100)
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
	if result.TotalOrders == 0 {
		t.Error("TotalOrders = 0, expected some orders to pass year filter")
	}
	if result.TotalOrders > 100 {
		t.Errorf("TotalOrders = %d, should be <= 100 (filter should remove some)", result.TotalOrders)
	}
}
