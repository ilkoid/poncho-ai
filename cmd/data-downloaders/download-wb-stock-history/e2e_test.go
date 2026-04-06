// Package main provides E2E tests for download-wb-stock-history.
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// TestE2E_MockFlow tests the complete flow with mock data.
func TestE2E_MockFlow(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create repository
	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create mock client
	mockClient := NewMockStockHistoryClient()
	PopulateMockStockHistory(mockClient, 3)

	// Create a mock report and get its ID
	ctx := context.Background()
	reportID, err := mockClient.CreateStockHistoryReport(ctx, nil, 3, 3)
	if err != nil {
		t.Fatalf("Failed to create mock report: %v", err)
	}

	// Download the ZIP (mock returns CSV content directly)
	zipData, err := mockClient.DownloadStockHistoryReport(ctx, reportID, 3, 3)
	if err != nil {
		t.Fatalf("Failed to download mock report: %v", err)
	}

	if len(zipData) == 0 {
		t.Fatal("Empty ZIP data")
	}

	t.Logf("✓ Mock report downloaded: %d bytes", len(zipData))
}

// TestParser_ParseDailyCSV tests the daily CSV parser.
func TestParser_ParseDailyCSV(t *testing.T) {
	csvData := `VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,OfficeName,28.03.2026,29.03.2026
601012,Костюм,202989,Костюмы,Spider-Man,134,963864,Остальные,100,95
517005,Полукомбинезон,206200,Полукомбинезоны,Disney,86,977755,Тула,50,45
`

	rows, err := ParseDailyCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("Failed to parse daily CSV: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("Expected 2 rows, got %d", len(rows))
	}

	// Check first row
	if rows[0].VendorCode != "601012" {
		t.Errorf("Expected VendorCode 601012, got %s", rows[0].VendorCode)
	}
	if rows[0].NmID != 202989 {
		t.Errorf("Expected NmID 202989, got %d", rows[0].NmID)
	}
	if rows[0].DailyData["28.03.2026"] != 100 {
		t.Errorf("Expected 100 for 28.03.2026, got %d", rows[0].DailyData["28.03.2026"])
	}
	if rows[0].DailyData["29.03.2026"] != 95 {
		t.Errorf("Expected 95 for 29.03.2026, got %d", rows[0].DailyData["29.03.2026"])
	}

	t.Logf("✓ Daily CSV parser works correctly")
}

// TestParser_ParseMetricsCSV tests the metrics CSV parser.
func TestParser_ParseMetricsCSV(t *testing.T) {
	csvData := `VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,RegionName,OfficeName,Availability,OrdersCount,OrdersSum,BuyoutCount,BuyoutSum,BuyoutPercent,AvgOrders,StockCount,StockSum,SaleRate,AvgStockTurnover,ToClientCount,FromClientCount,Price,OfficeMissingTime,LostOrdersCount,LostOrdersSum,LostBuyoutsCount,LostBuyoutsSum,AvgOrdersByMonth_03.2026,Currency
601012,Костюм,202989,Костюмы,Spider-Man,134,963864,Центр,Остальные,actual,10,15000,8,12000,80,1.0,50,75000,120,240,5,2,1500,12,2.5,1.5,0.2,0.3,15.5,RUB
`

	rows, err := ParseMetricsCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("Failed to parse metrics CSV: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	// Check row data
	if rows[0].VendorCode != "601012" {
		t.Errorf("Expected VendorCode 601012, got %s", rows[0].VendorCode)
	}
	if rows[0].NmID != 202989 {
		t.Errorf("Expected NmID 202989, got %d", rows[0].NmID)
	}
	if *rows[0].OrdersCount != 10 {
		t.Errorf("Expected OrdersCount 10, got %d", *rows[0].OrdersCount)
	}
	if *rows[0].OrdersSum != 15000 {
		t.Errorf("Expected OrdersSum 15000, got %d", *rows[0].OrdersSum)
	}
	if rows[0].MonthlyData["03.2026"] != 15.5 {
		t.Errorf("Expected 15.5 for 03.2026, got %f", rows[0].MonthlyData["03.2026"])
	}
	if rows[0].Currency != "RUB" {
		t.Errorf("Expected Currency RUB, got %s", rows[0].Currency)
	}

	t.Logf("✓ Metrics CSV parser works correctly")
}
