package nmreport

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockSource implements NmReportSource with deterministic data.
type MockSource struct {
	PollCount int // tracks how many times PollStatus was called
}

func (m *MockSource) CreateReport(ctx context.Context, req wb.NmReportFunnelRequest) (string, error) {
	return req.ID, nil
}

func (m *MockSource) PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	m.PollCount++
	return &wb.StockHistoryReportItem{
		ID:        downloadID,
		Status:    wb.StockHistoryStatusSuccess,
		Size:      1024,
		StartDate: time.Now().AddDate(0, 0, -3).Format("2006-01-02"),
		EndDate:   time.Now().Format("2006-01-02"),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *MockSource) DownloadFile(ctx context.Context, downloadID string) ([]byte, error) {
	csvContent := "nmID,dt,openCardCount,addToCartCount,ordersCount,ordersSumRub,buyoutsCount,buyoutsSumRub,cancelCount,cancelSumRub,addToCartConversion,cartToOrderConversion,buyoutPercent,addToWishlist,currency\n" +
		"12345,2026-05-22,100,30,10,15000,8,12000,2,3000,30.0,33.3,8.0,5,RUB\n" +
		"12345,2026-05-23,120,40,15,22500,12,18000,3,4500,33.3,37.5,10.0,7,RUB\n" +
		"67890,2026-05-22,50,10,3,4500,2,3000,1,1500,20.0,30.0,4.0,2,RUB\n"
	return makeZipWithCSV("report.csv", csvContent)
}

// MockGroupedSource returns grouped CSV data.
type MockGroupedSource struct {
	PollCount int
}

func (m *MockGroupedSource) CreateReport(ctx context.Context, req wb.NmReportFunnelRequest) (string, error) {
	return req.ID, nil
}

func (m *MockGroupedSource) PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	m.PollCount++
	return &wb.StockHistoryReportItem{
		ID:        downloadID,
		Status:    wb.StockHistoryStatusSuccess,
		Size:      512,
		StartDate: time.Now().AddDate(0, 0, -3).Format("2006-01-02"),
		EndDate:   time.Now().Format("2006-01-02"),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *MockGroupedSource) DownloadFile(ctx context.Context, downloadID string) ([]byte, error) {
	csvContent := "dt,openCardCount,addToCartCount,ordersCount,ordersSumRub,buyoutsCount,buyoutsSumRub,cancelCount,cancelSumRub,addToCartConversion,cartToOrderConversion,buyoutPercent,addToWishlist,currency\n" +
		"2026-05-22,150,40,13,19500,10,15000,3,4500,26.7,32.5,6.7,7,RUB\n" +
		"2026-05-23,200,60,20,30000,16,24000,5,7500,30.0,33.3,8.0,10,RUB\n"
	return makeZipWithCSV("report.csv", csvContent)
}

// MockWriter implements NmReportWriter for tests.
type MockWriter struct {
	DetailRows  []FunnelDetailRow
	GroupedRows []FunnelGroupedRow
	Reports     []NmReportRecord
	StatusUpdates []StatusUpdate
}

type StatusUpdate struct {
	DownloadID string
	Status     string
	RowsCount  int
}

func (m *MockWriter) GetNmReport(ctx context.Context, reportType, startDate, endDate string) (*NmReportRecord, error) {
	return nil, nil
}

func (m *MockWriter) SaveNmReport(ctx context.Context, record NmReportRecord) error {
	m.Reports = append(m.Reports, record)
	return nil
}

func (m *MockWriter) UpdateNmReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error {
	m.StatusUpdates = append(m.StatusUpdates, StatusUpdate{
		DownloadID: downloadID,
		Status:     status,
		RowsCount:  rowsCount,
	})
	return nil
}

func (m *MockWriter) SaveFunnelMetricsDetail(ctx context.Context, rows []FunnelDetailRow, refreshDays int) error {
	m.DetailRows = append(m.DetailRows, rows...)
	return nil
}

func (m *MockWriter) SaveFunnelMetricsGrouped(ctx context.Context, rows []FunnelGroupedRow) error {
	m.GroupedRows = append(m.GroupedRows, rows...)
	return nil
}

// DiscardWriter implements NmReportWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu           sync.Mutex
	savedDetail  int
	savedGrouped int
	savedReports int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// GetNmReport returns nil — no resume in mock mode.
func (w *DiscardWriter) GetNmReport(_ context.Context, _, _, _ string) (*NmReportRecord, error) {
	return nil, nil
}

// SaveNmReport counts reports but never writes to any database.
func (w *DiscardWriter) SaveNmReport(_ context.Context, record NmReportRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedReports++
	return nil
}

// UpdateNmReportStatus is a no-op in mock mode.
func (w *DiscardWriter) UpdateNmReportStatus(_ context.Context, _, _ string, _ int) error {
	return nil
}

// SaveFunnelMetricsDetail counts rows but never writes to any database.
func (w *DiscardWriter) SaveFunnelMetricsDetail(_ context.Context, rows []FunnelDetailRow, _ int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedDetail += len(rows)
	return nil
}

// SaveFunnelMetricsGrouped counts rows but never writes to any database.
func (w *DiscardWriter) SaveFunnelMetricsGrouped(_ context.Context, rows []FunnelGroupedRow) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedGrouped += len(rows)
	return nil
}

// SavedDetail returns count of detail rows "saved" (counted).
func (w *DiscardWriter) SavedDetail() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedDetail
}

// SavedGrouped returns count of grouped rows "saved" (counted).
func (w *DiscardWriter) SavedGrouped() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedGrouped
}

// makeZipWithCSV creates a ZIP archive containing a single CSV file.
func makeZipWithCSV(filename, content string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("create zip entry: %w", err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		return nil, fmt.Errorf("write zip entry: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}
