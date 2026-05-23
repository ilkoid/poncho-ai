package sales

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements SalesWriter for testing.
type mockWriter struct {
	mu       sync.Mutex
	saved    []wb.RealizationReportRow
	svcSaved []wb.RealizationReportRow
	exists   map[int]bool
	lastDT   time.Time
	firstDT  time.Time

	deletedSales  int64
	deletedSvc    int64
}

func newMockWriter() *mockWriter {
	return &mockWriter{exists: make(map[int]bool)}
}

func (m *mockWriter) GetLastSaleDT(_ context.Context) (time.Time, error)  { return m.lastDT, nil }
func (m *mockWriter) GetFirstSaleDT(_ context.Context) (time.Time, error) { return m.firstDT, nil }
func (m *mockWriter) DeleteSalesByDateRange(_ context.Context, _, _ string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := int64(len(m.saved))
	m.deletedSales += n
	m.saved = nil
	return n, nil
}
func (m *mockWriter) DeleteServiceRecordsByDateRange(_ context.Context, _, _ string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := int64(len(m.svcSaved))
	m.deletedSvc += n
	m.svcSaved = nil
	return n, nil
}
func (m *mockWriter) Save(_ context.Context, rows []wb.RealizationReportRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, rows...)
	return nil
}
func (m *mockWriter) SaveServiceRecords(_ context.Context, rows []wb.RealizationReportRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.svcSaved = append(m.svcSaved, rows...)
	return nil
}
func (m *mockWriter) Exists(_ context.Context, rrdID int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exists[rrdID], nil
}

func TestDownloader_Run_Basic(t *testing.T) {
	ctx := context.Background()
	writer := newMockWriter()
	source := &MockSalesSource{RowCount: 3}

	opts := DownloadOptions{
		OnProgress: func(msg string) { t.Log(msg) },
	}
	dl := NewDownloader(source, writer, opts)

	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ranges := []wb.DateRange{{From: begin, To: end}}

	result, err := dl.Run(ctx, ranges, false, false)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalRows != 3 {
		t.Errorf("expected 3 rows, got %d", result.TotalRows)
	}
	if result.PeriodsCount != 1 {
		t.Errorf("expected 1 period, got %d", result.PeriodsCount)
	}
	if len(writer.saved) != 3 {
		t.Errorf("expected 3 saved rows, got %d", len(writer.saved))
	}
}

func TestDownloader_Run_DryRun(t *testing.T) {
	ctx := context.Background()
	writer := newMockWriter()
	source := &MockSalesSource{RowCount: 5}

	opts := DownloadOptions{
		DryRun:     true,
		OnProgress: func(msg string) { t.Log(msg) },
	}
	dl := NewDownloader(source, writer, opts)

	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ranges := []wb.DateRange{{From: begin, To: end}}

	result, err := dl.Run(ctx, ranges, false, false)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalRows != 5 {
		t.Errorf("expected 5 rows counted, got %d", result.TotalRows)
	}
	if len(writer.saved) != 0 {
		t.Errorf("dry-run should not save, but got %d rows", len(writer.saved))
	}
}

func TestDownloader_Run_Rewrite(t *testing.T) {
	ctx := context.Background()
	writer := newMockWriter()
	// Pre-populate some data to be deleted
	writer.saved = make([]wb.RealizationReportRow, 10)
	source := &MockSalesSource{RowCount: 4}

	opts := DownloadOptions{
		OnProgress: func(msg string) { t.Log(msg) },
	}
	dl := NewDownloader(source, writer, opts)

	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ranges := []wb.DateRange{{From: begin, To: end}}

	result, err := dl.Run(ctx, ranges, false, true)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalRows != 4 {
		t.Errorf("expected 4 rows, got %d", result.TotalRows)
	}
	if writer.deletedSales != 10 {
		t.Errorf("expected 10 deleted, got %d", writer.deletedSales)
	}
}

func TestDownloader_Run_Resume_SkipsExisting(t *testing.T) {
	ctx := context.Background()
	writer := newMockWriter()
	// Mark rrd_id 1 and 2 as existing
	writer.exists[1] = true
	writer.exists[2] = true
	source := &MockSalesSource{RowCount: 4}

	opts := DownloadOptions{
		OnProgress: func(msg string) { t.Log(msg) },
	}
	dl := NewDownloader(source, writer, opts)

	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ranges := []wb.DateRange{{From: begin, To: end}}

	result, err := dl.Run(ctx, ranges, true, false)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// 4 rows total, 2 exist → 2 new
	if result.TotalRows != 2 {
		t.Errorf("expected 2 new rows (4 total - 2 existing), got %d", result.TotalRows)
	}
}

func TestDownloader_Run_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	writer := newMockWriter()
	source := &MockSalesSource{RowCount: 5}

	opts := DownloadOptions{}
	dl := NewDownloader(source, writer, opts)

	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ranges := []wb.DateRange{{From: begin, To: end}}

	_, err := dl.Run(ctx, ranges, false, false)
	if err == nil {
		t.Error("expected error on cancelled context")
	}
}
