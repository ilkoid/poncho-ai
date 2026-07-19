package sales

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockSalesSource returns deterministic fake sales data.
// Implements SalesSource for --mock mode and testing.
type MockSalesSource struct {
	RowCount int // rows per batch (default: 5)
}

func (m *MockSalesSource) ReportDetailByPeriodIteratorWithTime(
	ctx context.Context,
	baseURL string,
	rateLimit, burst int,
	dateFrom, dateTo string,
	callback func([]wb.RealizationReportRow) error,
) (int, error) {
	return m.iterate(ctx, callback)
}

func (m *MockSalesSource) ReportDetailByPeriodIterator(
	ctx context.Context,
	baseURL string,
	rateLimit, burst int,
	dateFrom, dateTo int,
	callback func([]wb.RealizationReportRow) error,
) (int, error) {
	return m.iterate(ctx, callback)
}

func (m *MockSalesSource) iterate(ctx context.Context, callback func([]wb.RealizationReportRow) error) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	rows := m.generateRows()
	if err := callback(rows); err != nil {
		return 0, err
	}
	return 1, nil
}

func (m *MockSalesSource) generateRows() []wb.RealizationReportRow {
	n := m.RowCount
	if n == 0 {
		n = 5
	}
	now := "2026-01-15T12:00:00Z"
	rows := make([]wb.RealizationReportRow, n)
	for i := range rows {
		rows[i] = wb.RealizationReportRow{
			RrdID:           i + 1,
			NmID:            100 + i,
			DocTypeName:     "Продажа",
			SupplierArticle: fmt.Sprintf("ART%03d", i+1),
			RetailPrice:     float64(1000 * (i + 1)),
			SaleDT:          now,
			OrderDT:         now,
		}
	}
	return rows
}

// DiscardWriter implements SalesWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// GetLastSaleDT returns a zero time — no data in mock mode.
func (w *DiscardWriter) GetLastSaleDT(_ context.Context) (time.Time, error) {
	return time.Time{}, nil
}

// GetFirstSaleDT returns a zero time — no data in mock mode.
func (w *DiscardWriter) GetFirstSaleDT(_ context.Context) (time.Time, error) {
	return time.Time{}, nil
}

// DeleteSalesByDateRange is a no-op for mock mode.
func (w *DiscardWriter) DeleteSalesByDateRange(_ context.Context, _, _ string) (int64, error) {
	return 0, nil
}

// DeleteServiceRecordsByDateRange is a no-op for mock mode.
func (w *DiscardWriter) DeleteServiceRecordsByDateRange(_ context.Context, _, _ string) (int64, error) {
	return 0, nil
}

// Save counts rows but never writes to any database.
func (w *DiscardWriter) Save(_ context.Context, rows []wb.RealizationReportRow) error {
	w.mu.Lock()
	w.saved += len(rows)
	w.mu.Unlock()
	return nil
}

// SavePlain — для mock режима эквивалентно Save (считаем строки).
func (w *DiscardWriter) SavePlain(ctx context.Context, rows []wb.RealizationReportRow) error {
	return w.Save(ctx, rows)
}

// SaveServiceRecords is a no-op for mock mode.
func (w *DiscardWriter) SaveServiceRecords(_ context.Context, _ []wb.RealizationReportRow) error {
	return nil
}

// SaveServiceRecordsPlain — no-op, как и SaveServiceRecords.
func (w *DiscardWriter) SaveServiceRecordsPlain(_ context.Context, _ []wb.RealizationReportRow) error {
	return nil
}

// Exists always returns false — no data in mock mode.
func (w *DiscardWriter) Exists(_ context.Context, _ int) (bool, error) {
	return false, nil
}
