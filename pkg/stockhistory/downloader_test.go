package stockhistory

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Downloader integration tests (mock source + mock writer)
// ============================================================================

func TestDownloader_MetricsBasic(t *testing.T) {
	src := &MockMetricsSource{}
	writer := &MockWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		ReportType:      "metrics",
		Days:            3,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "SUCCESS" {
		t.Errorf("expected SUCCESS, got %s", result.Status)
	}
	if result.RowsCount != 2 {
		t.Errorf("expected 2 rows, got %d", result.RowsCount)
	}
	if result.DownloadID == "" {
		t.Error("expected non-empty download ID")
	}

	// Verify writer received data
	if len(writer.MetricsRows) != 2 {
		t.Errorf("expected 2 metrics rows in writer, got %d", len(writer.MetricsRows))
	}
	if len(writer.Reports) != 1 {
		t.Errorf("expected 1 report record, got %d", len(writer.Reports))
	}
	if len(writer.StatusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(writer.StatusUpdates))
	}
	if writer.StatusUpdates[0].Status != "SUCCESS" {
		t.Errorf("expected SUCCESS status update, got %s", writer.StatusUpdates[0].Status)
	}

	// Verify ReportID propagation
	for _, row := range writer.MetricsRows {
		if row.ReportID != result.DownloadID {
			t.Errorf("row ReportID=%s, expected %s", row.ReportID, result.DownloadID)
		}
	}
}

func TestDownloader_DailyBasic(t *testing.T) {
	src := &MockDailySource{}
	writer := &MockWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		ReportType:      "daily",
		Days:            3,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "SUCCESS" {
		t.Errorf("expected SUCCESS, got %s", result.Status)
	}
	if result.RowsCount != 2 {
		t.Errorf("expected 2 rows, got %d", result.RowsCount)
	}

	if len(writer.DailyRows) != 2 {
		t.Errorf("expected 2 daily rows in writer, got %d", len(writer.DailyRows))
	}

	// Verify dynamic data parsed
	if len(writer.DailyRows[0].DailyData) != 3 {
		t.Errorf("expected 3 daily data entries, got %d", len(writer.DailyRows[0].DailyData))
	}
}

func TestDownloader_DryRun(t *testing.T) {
	src := &MockMetricsSource{}
	writer := &MockWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		ReportType:      "metrics",
		Days:            3,
		DryRun:          true,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "SUCCESS" {
		t.Errorf("expected SUCCESS, got %s", result.Status)
	}

	// DryRun should NOT save any data
	if len(writer.MetricsRows) != 0 {
		t.Errorf("dry-run should not save metrics rows, got %d", len(writer.MetricsRows))
	}
	if len(writer.Reports) != 0 {
		t.Errorf("dry-run should not save report records, got %d", len(writer.Reports))
	}
	if len(writer.StatusUpdates) != 0 {
		t.Errorf("dry-run should not update status, got %d", len(writer.StatusUpdates))
	}
}

func TestDownloader_Resume(t *testing.T) {
	src := &MockMetricsSource{}
	writer := &MockWriter{
		ExistingReport: &ReportRecord{
			ID:        "existing-report-id",
			ReportType: "STOCK_HISTORY_REPORT_CSV",
			Status:    "SUCCESS",
			RowsCount: 42,
		},
	}

	dl := NewDownloader(src, writer, DownloadOptions{
		ReportType:      "metrics",
		Days:            3,
		Resume:          true,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "RESUMED" {
		t.Errorf("expected RESUMED, got %s", result.Status)
	}
	if result.RowsCount != 42 {
		t.Errorf("expected 42 rows from existing report, got %d", result.RowsCount)
	}
	if result.DownloadID != "existing-report-id" {
		t.Errorf("expected existing-report-id, got %s", result.DownloadID)
	}

	// Resume should NOT create new report
	if len(writer.Reports) != 0 {
		t.Errorf("resume should not create new reports, got %d", len(writer.Reports))
	}
	if src.PollCount != 0 {
		t.Errorf("resume should not poll API, got %d polls", src.PollCount)
	}
}

func TestDownloader_PollTimeout(t *testing.T) {
	src := &MockPollTimeoutSource{}
	writer := &MockWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		ReportType:      "metrics",
		Days:            3,
		PollIntervalSec: 1,
		PollTimeoutMin:  1, // 1 minute = short timeout for test
	})
	// Override poll timeout to seconds for fast test
	dl.opts.PollTimeoutMin = 0
	// We'll use context timeout instead
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Error should mention poll timeout or context
	if !strings.Contains(err.Error(), "poll timeout") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

// ============================================================================
// Parser unit tests
// ============================================================================

func TestParseMetricsCSV(t *testing.T) {
	csv := "VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,RegionName,OfficeName,Availability," +
		"OrdersCount,OrdersSum,BuyoutCount,BuyoutSum,BuyoutPercent,AvgOrders,StockCount,StockSum," +
		"SaleRate,AvgStockTurnover,ToClientCount,FromClientCount,Price,OfficeMissingTime," +
		"LostOrdersCount,LostOrdersSum,LostBuyoutsCount,LostBuyoutsSum,Currency," +
		"AvgOrdersByMonth_03.2026,AvgOrdersByMonth_04.2026\n" +
		"ART001,Test Product,12345,Кроссовки,Nike,42,100,Москва,Коледино,actual," +
		"10,15000,8,12000,80,3.5,50,75000," +
		"24,18,2,1,1500,0," +
		"2.5,3750.0,1.0,1500.0,RUB," +
		"3.2,4.1\n"

	rows, err := ParseMetricsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	r := rows[0]
	if r.VendorCode != "ART001" {
		t.Errorf("VendorCode: expected ART001, got %s", r.VendorCode)
	}
	if r.NmID != 12345 {
		t.Errorf("NmID: expected 12345, got %d", r.NmID)
	}
	if r.OrdersCount == nil || *r.OrdersCount != 10 {
		t.Errorf("OrdersCount: expected *10, got %v", r.OrdersCount)
	}
	if r.AvgOrders == nil || *r.AvgOrders != 3.5 {
		t.Errorf("AvgOrders: expected *3.5, got %v", r.AvgOrders)
	}

	// Dynamic monthly data
	if len(r.MonthlyData) != 2 {
		t.Fatalf("expected 2 monthly entries, got %d", len(r.MonthlyData))
	}
	if r.MonthlyData["03.2026"] != 3.2 {
		t.Errorf("MonthlyData[03.2026]: expected 3.2, got %f", r.MonthlyData["03.2026"])
	}
	if r.MonthlyData["04.2026"] != 4.1 {
		t.Errorf("MonthlyData[04.2026]: expected 4.1, got %f", r.MonthlyData["04.2026"])
	}
}

func TestParseDailyCSV(t *testing.T) {
	csv := "VendorCode,Name,NmID,SubjectName,BrandName,SizeName,ChrtID,OfficeName," +
		"01.06.2026,02.06.2026,03.06.2026\n" +
		"ART001,Test Product,12345,Кроссовки,Nike,42,100,Коледино," +
		"50,48,45\n"

	rows, err := ParseDailyCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	r := rows[0]
	if r.VendorCode != "ART001" {
		t.Errorf("VendorCode: expected ART001, got %s", r.VendorCode)
	}
	if r.NmID != 12345 {
		t.Errorf("NmID: expected 12345, got %d", r.NmID)
	}

	// Dynamic daily data
	if len(r.DailyData) != 3 {
		t.Fatalf("expected 3 daily entries, got %d", len(r.DailyData))
	}
	if r.DailyData["01.06.2026"] != 50 {
		t.Errorf("DailyData[01.06.2026]: expected 50, got %d", r.DailyData["01.06.2026"])
	}
	if r.DailyData["02.06.2026"] != 48 {
		t.Errorf("DailyData[02.06.2026]: expected 48, got %d", r.DailyData["02.06.2026"])
	}
	if r.DailyData["03.06.2026"] != 45 {
		t.Errorf("DailyData[03.06.2026]: expected 45, got %d", r.DailyData["03.06.2026"])
	}
}
