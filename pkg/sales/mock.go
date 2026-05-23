package sales

import (
	"context"
	"fmt"

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
