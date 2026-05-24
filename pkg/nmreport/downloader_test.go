package nmreport

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestDownloader_DetailBasic(t *testing.T) {
	source := &MockSource{}
	writer := &MockWriter{}
	dl := NewDownloader(source, writer, DownloadOptions{
		Days:            3,
		ReportType:      "detail",
		RefreshWindow:   4,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", result.Status)
	}
	if result.RowsCount != 3 {
		t.Errorf("RowsCount = %d, want 3", result.RowsCount)
	}
	if len(writer.DetailRows) != 3 {
		t.Errorf("DetailRows = %d, want 3", len(writer.DetailRows))
	}
	if len(writer.Reports) != 1 {
		t.Errorf("Reports = %d, want 1", len(writer.Reports))
	}
}

func TestDownloader_GroupedBasic(t *testing.T) {
	source := &MockGroupedSource{}
	writer := &MockWriter{}
	dl := NewDownloader(source, writer, DownloadOptions{
		Days:            3,
		ReportType:      "grouped",
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", result.Status)
	}
	if result.RowsCount != 2 {
		t.Errorf("RowsCount = %d, want 2", result.RowsCount)
	}
	if len(writer.GroupedRows) != 2 {
		t.Errorf("GroupedRows = %d, want 2", len(writer.GroupedRows))
	}
}

func TestDownloader_DryRun(t *testing.T) {
	source := &MockSource{}
	writer := &MockWriter{}
	dl := NewDownloader(source, writer, DownloadOptions{
		Days:            3,
		ReportType:      "detail",
		DryRun:          true,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", result.Status)
	}
	if result.RowsCount != 3 {
		t.Errorf("RowsCount = %d, want 3 (parsed even in dry-run)", result.RowsCount)
	}
	if len(writer.DetailRows) != 0 {
		t.Errorf("DetailRows = %d, want 0 (dry-run should not save)", len(writer.DetailRows))
	}
	if len(writer.Reports) != 0 {
		t.Errorf("Reports = %d, want 0 (dry-run should not save)", len(writer.Reports))
	}
}

func TestDownloader_Resume(t *testing.T) {
	source := &MockSource{}
	writer := &resumeWriter{}
	dl := NewDownloader(source, writer, DownloadOptions{
		Days:            3,
		ReportType:      "detail",
		Resume:          true,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Status != "RESUMED" {
		t.Errorf("Status = %q, want RESUMED", result.Status)
	}
	if source.PollCount != 0 {
		t.Errorf("PollCount = %d, want 0 (should not poll on resume)", source.PollCount)
	}
}

func TestDownloader_ContextCancel(t *testing.T) {
	source := &slowSource{}
	writer := &MockWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dl := NewDownloader(source, writer, DownloadOptions{
		Days:            3,
		ReportType:      "detail",
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestParseDetailCSV(t *testing.T) {
	csv := "nmID,dt,openCardCount,addToCartCount,ordersCount,ordersSumRub,buyoutsCount,buyoutsSumRub,cancelCount,cancelSumRub,addToCartConversion,cartToOrderConversion,buyoutPercent,addToWishlist,currency\n" +
		"12345,2026-05-22,100,30,10,15000,8,12000,2,3000,30.0,33.3,8.0,5,RUB\n" +
		"67890,2026-05-23,50,10,3,4500,2,3000,1,1500,20.0,30.0,4.0,2,RUB\n"

	rows, err := ParseDetailCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseDetailCSV error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	r := rows[0]
	if r.NmID != 12345 {
		t.Errorf("NmID = %d, want 12345", r.NmID)
	}
	if r.MetricDate != "2026-05-22" {
		t.Errorf("MetricDate = %q, want 2026-05-22", r.MetricDate)
	}
	if r.OpenCardCount != 100 {
		t.Errorf("OpenCardCount = %d, want 100", r.OpenCardCount)
	}
	if r.CancelCount != 2 {
		t.Errorf("CancelCount = %d, want 2", r.CancelCount)
	}
	if r.CancelSumRub != 3000 {
		t.Errorf("CancelSumRub = %d, want 3000", r.CancelSumRub)
	}
	if r.BuyoutPercent != 8.0 {
		t.Errorf("BuyoutPercent = %f, want 8.0", r.BuyoutPercent)
	}
	if r.Currency != "RUB" {
		t.Errorf("Currency = %q, want RUB", r.Currency)
	}
}

func TestParseGroupedCSV(t *testing.T) {
	csv := "dt,openCardCount,addToCartCount,ordersCount,ordersSumRub,buyoutsCount,buyoutsSumRub,cancelCount,cancelSumRub,addToCartConversion,cartToOrderConversion,buyoutPercent,addToWishlist,currency\n" +
		"2026-05-22,150,40,13,19500,10,15000,3,4500,26.7,32.5,6.7,7,RUB\n"

	rows, err := ParseGroupedCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseGroupedCSV error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	r := rows[0]
	if r.MetricDate != "2026-05-22" {
		t.Errorf("MetricDate = %q, want 2026-05-22", r.MetricDate)
	}
	if r.OpenCardCount != 150 {
		t.Errorf("OpenCardCount = %d, want 150", r.OpenCardCount)
	}
	if r.CancelCount != 3 {
		t.Errorf("CancelCount = %d, want 3", r.CancelCount)
	}
}

func TestExtractCSVFromZip(t *testing.T) {
	zipBytes, err := makeZipWithCSV("data.csv", "hello;world\n1;2\n")
	if err != nil {
		t.Fatalf("makeZip error: %v", err)
	}

	reader, err := extractCSVFromZip(zipBytes)
	if err != nil {
		t.Fatalf("extractCSVFromZip error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(reader)
	content := buf.String()
	if !strings.Contains(content, "hello;world") {
		t.Errorf("content = %q, want CSV data", content)
	}
}

func TestExtractCSVFromZip_Empty(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.Close()

	_, err := extractCSVFromZip(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for empty zip")
	}
}

// resumeWriter returns a completed report for any query.
type resumeWriter struct {
	MockWriter
}

func (r *resumeWriter) GetNmReport(ctx context.Context, reportType, startDate, endDate string) (*NmReportRecord, error) {
	return &NmReportRecord{
		ID:         "test-resume-id",
		ReportType: "DETAIL_HISTORY_REPORT",
		StartDate:  startDate,
		EndDate:    endDate,
		Status:     "SUCCESS",
		RowsCount:  100,
	}, nil
}

// slowSource never completes — for context cancellation test.
type slowSource struct{}

func (s *slowSource) CreateReport(ctx context.Context, req wb.NmReportFunnelRequest) (string, error) {
	return req.ID, nil
}

func (s *slowSource) PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	return nil, ctx.Err()
}

func (s *slowSource) DownloadFile(ctx context.Context, downloadID string) ([]byte, error) {
	return nil, ctx.Err()
}
