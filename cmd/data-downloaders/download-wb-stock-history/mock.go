// Package main provides mock client for download-wb-stock-history testing.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MockStockHistoryClient is a mock implementation of StockHistoryClient for testing.
type MockStockHistoryClient struct {
	Reports       map[string]*MockStockReport
	CreateCalled  bool
	ListCalled    bool
	DownloadCalled bool
	CreateDelay   time.Duration
}

type MockStockReport struct {
	ID         string
	Status     string
	Name       string
	Size       int64
	StartDate  string
	EndDate    string
	CreatedAt  string
	CSVContent string
}

// NewMockStockHistoryClient creates a new mock client.
func NewMockStockHistoryClient() *MockStockHistoryClient {
	return &MockStockHistoryClient{
		Reports: make(map[string]*MockStockReport),
	}
}

// CreateStockHistoryReport creates a mock report task.
func (m *MockStockHistoryClient) CreateStockHistoryReport(ctx context.Context, req interface{}, rateLimit, burst int) (string, error) {
	m.CreateCalled = true

	if m.CreateDelay > 0 {
		time.Sleep(m.CreateDelay)
	}

	// Generate mock report with simple dates
	startDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")

	// Generate UUID for mock report
	reportID := uuid.New().String()

	// Simulate processing (success after 2 seconds)
	report := &MockStockReport{
		ID:        reportID,
		Status:    "SUCCESS",
		Name:      "Mock Stock History Report",
		Size:      1024,
		StartDate: startDate,
		EndDate:   endDate,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
		CSVContent: generateMockDailyCSV(startDate, endDate),
	}

	m.Reports[reportID] = report

	return reportID, nil
}

// ListStockHistoryReports returns all mock reports.
func (m *MockStockHistoryClient) ListStockHistoryReports(ctx context.Context, rateLimit, burst int) ([]interface{}, error) {
	m.ListCalled = true

	var result []interface{}
	for _, r := range m.Reports {
		result = append(result, map[string]interface{}{
			"id":         r.ID,
			"status":     r.Status,
			"name":       r.Name,
			"size":       r.Size,
			"startDate":  r.StartDate,
			"endDate":    r.EndDate,
			"createdAt":  r.CreatedAt,
		})
	}

	return result, nil
}

// DownloadStockHistoryReport returns mock CSV content as ZIP bytes.
func (m *MockStockHistoryClient) DownloadStockHistoryReport(ctx context.Context, downloadID string, rateLimit, burst int) ([]byte, error) {
	m.DownloadCalled = true

	report, ok := m.Reports[downloadID]
	if !ok {
		return nil, fmt.Errorf("report not found: %s", downloadID)
	}

	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("report not ready: %s", report.Status)
	}

	// Return mock CSV content (not a real ZIP for simplicity)
	return []byte(report.CSVContent), nil
}

// generateMockDailyCSV creates mock daily CSV content.
func generateMockDailyCSV(start, end string) string {
	return fmt.Sprintf(`VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,OfficeName,%s
601012,Костюм,202989,Костюмы,Spider-Man,134,963864,Остальные,0
517005,Полукомбинезон,206200,Полукомбинезоны,Disney,86,977755,Остальные,5
517005,Полукомбинезон,206200,Полукомбинезоны,Disney,92,977756,Тула,3
`, start)
}

// generateMockMetricsCSV creates mock metrics CSV content.
func generateMockMetricsCSV(start, end string) string {
	return fmt.Sprintf(`VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,RegionName,OfficeName,Availability,OrdersCount,OrdersSum,BuyoutCount,BuyoutSum,BuyoutPercent,AvgOrders,StockCount,StockSum,SaleRate,AvgStockTurnover,ToClientCount,FromClientCount,Price,OfficeMissingTime,LostOrdersCount,LostOrdersSum,LostBuyoutsCount,LostBuyoutsSum,AvgOrdersByMonth_03.2026,Currency
601012,Костюм,202989,Костюмы,Spider-Man,134,963864,Центр,Остальные,actual,10,15000,8,12000,80,1.0,50,75000,120,240,5,2,1500,12,2.5,1.5,0.2,0.3,0.1,0.05,RUB
`)
}

// PopulateMockStockHistory adds mock reports for testing.
func PopulateMockStockHistory(client *MockStockHistoryClient, count int) {
	for i := 0; i < count; i++ {
		reportID := uuid.New().String()

		startDate := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		endDate := startDate

		client.Reports[reportID] = &MockStockReport{
			ID:        reportID,
			Status:    "SUCCESS",
			Name:      fmt.Sprintf("Mock Report %d", i),
			Size:      1024,
			StartDate: startDate,
			EndDate:   endDate,
			CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
			CSVContent: generateMockDailyCSV(startDate, endDate),
		}
	}
}
