package stockhistory

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockMetricsSource implements StockHistorySource with deterministic metrics data.
type MockMetricsSource struct {
	PollCount int
}

func (m *MockMetricsSource) CreateReport(_ context.Context, req wb.StockHistoryReportRequest) (string, error) {
	return req.ID, nil
}

func (m *MockMetricsSource) PollStatus(_ context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	m.PollCount++
	return &wb.StockHistoryReportItem{
		ID:        downloadID,
		Status:    wb.StockHistoryStatusSuccess,
		Size:      2048,
		StartDate: time.Now().AddDate(0, 0, -3).Format("2006-01-02"),
		EndDate:   time.Now().Format("2006-01-02"),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *MockMetricsSource) DownloadFile(_ context.Context, _ string) ([]byte, error) {
	csvContent := "VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,RegionName,OfficeName,Availability," +
		"OrdersCount,OrdersSum,BuyoutCount,BuyoutSum,BuyoutPercent,AvgOrders,StockCount,StockSum," +
		"SaleRate,AvgStockTurnover,ToClientCount,FromClientCount,Price,OfficeMissingTime," +
		"LostOrdersCount,LostOrdersSum,LostBuyoutsCount,LostBuyoutsSum,Currency," +
		"AvgOrdersByMonth_03.2026,AvgOrdersByMonth_04.2026\n" +
		"ART001,Sneakers,12345,Кроссовки,Nike,42,100,Москва,Коледино,actual," +
		"10,15000,8,12000,80,3.5,50,75000," +
		"24,18,2,1,1500,0," +
		"2.5,3750.0,1.0,1500.0,RUB," +
		"3.2,4.1\n" +
		"ART002,T-Shirt,67890,Футболка,Adidas,M,200,Москва,Электросталь,deficient," +
		"5,7500,4,6000,80,1.8,10,15000," +
		"12,8,1,0,999,48," +
		"0.5,750.0,0.2,300.0,RUB," +
		"1.5,2.0\n"
	return makeZipWithCSV("metrics.csv", csvContent)
}

// MockDailySource implements StockHistorySource with deterministic daily data.
type MockDailySource struct {
	PollCount int
}

func (m *MockDailySource) CreateReport(_ context.Context, req wb.StockHistoryReportRequest) (string, error) {
	return req.ID, nil
}

func (m *MockDailySource) PollStatus(_ context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
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

func (m *MockDailySource) DownloadFile(_ context.Context, _ string) ([]byte, error) {
	csvContent := "VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,OfficeName," +
		"01.06.2026,02.06.2026,03.06.2026\n" +
		"ART001,Sneakers,12345,Кроссовки,Nike,42,100,Коледино," +
		"50,48,45\n" +
		"ART002,T-Shirt,67890,Футболка,Adidas,M,200,Электросталь," +
		"10,10,8\n"
	return makeZipWithCSV("daily.csv", csvContent)
}

// MockPollTimeoutSource never returns SUCCESS — for timeout testing.
type MockPollTimeoutSource struct {
	PollCount int
}

func (m *MockPollTimeoutSource) CreateReport(_ context.Context, req wb.StockHistoryReportRequest) (string, error) {
	return req.ID, nil
}

func (m *MockPollTimeoutSource) PollStatus(_ context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	m.PollCount++
	return &wb.StockHistoryReportItem{
		ID:     downloadID,
		Status: wb.StockHistoryStatusWaiting,
	}, nil
}

func (m *MockPollTimeoutSource) DownloadFile(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("should not be called")
}

// MockWriter implements StockHistoryWriter for tests.
type MockWriter struct {
	mu             sync.Mutex
	MetricsRows    []MetricRow
	DailyRows      []DailyRow
	Reports        []ReportRecord
	StatusUpdates  []StatusUpdate
	ExistingReport *ReportRecord // returned by GetReport if set
}

// StatusUpdate records a call to UpdateReportStatus.
type StatusUpdate struct {
	DownloadID string
	Status     string
	RowsCount  int
}

func (m *MockWriter) GetReport(_ context.Context, _, _, _, _ string) (*ReportRecord, error) {
	return m.ExistingReport, nil
}

func (m *MockWriter) SaveReport(_ context.Context, record ReportRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Reports = append(m.Reports, record)
	return nil
}

func (m *MockWriter) UpdateReportStatus(_ context.Context, downloadID, status string, rowsCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StatusUpdates = append(m.StatusUpdates, StatusUpdate{
		DownloadID: downloadID,
		Status:     status,
		RowsCount:  rowsCount,
	})
	return nil
}

func (m *MockWriter) SaveMetrics(_ context.Context, rows []MetricRow) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MetricsRows = append(m.MetricsRows, rows...)
	return len(rows), nil
}

func (m *MockWriter) SaveDaily(_ context.Context, rows []DailyRow) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DailyRows = append(m.DailyRows, rows...)
	return len(rows), nil
}

// DiscardWriter implements StockHistoryWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu            sync.Mutex
	savedMetrics  int
	savedDaily    int
	savedReports  int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// GetReport returns nil — no resume in mock mode.
func (w *DiscardWriter) GetReport(_ context.Context, _, _, _, _ string) (*ReportRecord, error) {
	return nil, nil
}

// SaveReport counts reports but never writes to any database.
func (w *DiscardWriter) SaveReport(_ context.Context, _ ReportRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedReports++
	return nil
}

// UpdateReportStatus is a no-op in mock mode.
func (w *DiscardWriter) UpdateReportStatus(_ context.Context, _, _ string, _ int) error {
	return nil
}

// SaveMetrics counts rows but never writes to any database.
func (w *DiscardWriter) SaveMetrics(_ context.Context, rows []MetricRow) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedMetrics += len(rows)
	return len(rows), nil
}

// SaveDaily counts rows but never writes to any database.
func (w *DiscardWriter) SaveDaily(_ context.Context, rows []DailyRow) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedDaily += len(rows)
	return len(rows), nil
}

// SavedMetrics returns count of metrics rows "saved" (counted).
func (w *DiscardWriter) SavedMetrics() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedMetrics
}

// SavedDaily returns count of daily rows "saved" (counted).
func (w *DiscardWriter) SavedDaily() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedDaily
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
