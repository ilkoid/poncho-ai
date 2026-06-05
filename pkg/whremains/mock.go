package whremains

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op WhRemainsWriter for --mock mode.
// Counts saves but never touches any database.
//
// Mock safety: --mock + rewrite must NOT delete real data.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int

	// mockCountForDate controls what CountRemainsForDate returns.
	// Default: 0 (no existing data → download runs normally).
	mockCountForDate int
}

// NewDiscardWriter creates a DiscardWriter with default mock data.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SetMockCountForDate sets the mock count for CountRemainsForDate.
// Use to test resume logic: set > 0 to simulate existing data.
func (w *DiscardWriter) SetMockCountForDate(count int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mockCountForDate = count
}

// Saved returns the total number of rows "saved" (counted, not written).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}

// SaveRemains counts rows without writing to any database.
func (w *DiscardWriter) SaveRemains(_ context.Context, _ string, rows []WhRemainsFlatRow) (int, error) {
	w.mu.Lock()
	w.saved += len(rows)
	w.mu.Unlock()
	return len(rows), nil
}

// CountRemainsForDate returns the mock count (default: 0).
func (w *DiscardWriter) CountRemainsForDate(_ context.Context, _ string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mockCountForDate, nil
}

// MockSource returns deterministic warehouse remains data for --mock mode and tests.
type MockSource struct {
	mu       sync.RWMutex
	items    []wb.WarehouseRemainsItem
	status   string // poll status to return
	fail     bool   // if true, all methods return errors
	taskID   string // last task ID from CreateReport
}

// NewMockSource creates a new mock source.
func NewMockSource() *MockSource {
	return &MockSource{
		status: wb.WrStatusDone, // report is immediately ready
	}
}

// SetStatus sets the status that PollStatus will return.
func (m *MockSource) SetStatus(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
}

// SetFail enables or disables failure mode.
func (m *MockSource) SetFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = fail
}

// PopulateItems fills mock source with realistic test data.
// Creates itemCount products, each with warehousesPerItem warehouses.
func (m *MockSource) PopulateItems(itemCount, warehousesPerItem int) {
	warehouses := []string{"Коледино", "Казань", "Электросталь", "Подольск", "Краснодар"}

	items := make([]wb.WarehouseRemainsItem, 0, itemCount)
	for i := range itemCount {
		entries := make([]wb.WarehouseRemainsEntry, 0, warehousesPerItem)
		for w := range min(warehousesPerItem, len(warehouses)) {
			entries = append(entries, wb.WarehouseRemainsEntry{
				WarehouseName: warehouses[w],
				Quantity:      int64(50 + i*10 - w*5),
			})
		}

		items = append(items, wb.WarehouseRemainsItem{
			Brand:       "TestBrand",
			SubjectName: "Test Subject",
			VendorCode:  fmt.Sprintf("VC-%04d", i+1),
			NmID:        int64(100000 + i),
			Barcode:     fmt.Sprintf("200000%07d", i+1),
			TechSize:    "42",
			Volume:      0.5 + float64(i)*0.1,
			Warehouses:  entries,
		})
	}

	m.mu.Lock()
	m.items = items
	m.mu.Unlock()
}

// CreateReport returns a mock task ID.
func (m *MockSource) CreateReport(_ context.Context, _ WHRemainsParams) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fail {
		return "", fmt.Errorf("mock failure")
	}
	m.taskID = "mock-task-001"
	return m.taskID, nil
}

// PollStatus returns the configured mock status.
func (m *MockSource) PollStatus(_ context.Context, _ string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fail {
		return "", fmt.Errorf("mock failure")
	}
	return m.status, nil
}

// DownloadReport returns mock warehouse remains items.
func (m *MockSource) DownloadReport(_ context.Context, _ string) ([]wb.WarehouseRemainsItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fail {
		return nil, fmt.Errorf("mock failure")
	}
	return m.items, nil
}
