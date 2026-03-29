// Package main provides download logic for WB Stock History CSV data.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DownloadResult represents the result of a download operation.
type DownloadResult struct {
	ReportID  string
	Status    string
	RowsCount int
	Duration  time.Duration
}

// DownloadStockHistory orchestrates the stock history download process.
//
// Flow:
// 1. Check if report exists (resume mode)
// 2. Create report request
// 3. Poll status until SUCCESS/FAILED/TIMEOUT
// 4. Download ZIP archive
// 5. Extract and parse CSV
// 6. Save to database
func DownloadStockHistory(
	ctx context.Context,
	wbClient *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	cfg config.StockHistoryConfig,
	beginDate, endDate string,
) (*DownloadResult, error) {

	start := time.Now()

	// 1. Check resume mode
	if cfg.Resume {
		existing, err := repo.GetStockHistoryReport(ctx, cfg.ReportType, beginDate, endDate, cfg.StockType)
		if err == nil && existing != nil {
			log.Printf("📋 Found existing report: %s (status: %s)", existing.ID, existing.Status)
			if existing.Status == "SUCCESS" {
				// Count rows
				var count int
				if cfg.ReportType == "metrics" {
					count, _ = repo.CountStockHistoryMetrics(ctx, existing.ID)
				} else {
					count, _ = repo.CountStockHistoryDaily(ctx, existing.ID)
				}
				return &DownloadResult{
					ReportID:  existing.ID,
					Status:    "RESUMED",
					RowsCount: count,
					Duration:  time.Since(start),
				}, nil
			}
		}
	}

	// 2. Create report request
	reportID, err := createStockHistoryReport(ctx, wbClient, cfg, beginDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}

	log.Printf("📄 Report created: %s", reportID)

	// 3. Poll status
	reportItem, err := pollReportStatus(ctx, wbClient, reportID, cfg)
	if err != nil {
		return nil, fmt.Errorf("poll status: %w", err)
	}

	log.Printf("📊 Report status: %s (size: %d bytes)", reportItem.Status, reportItem.Size)

	// 4. Download ZIP
	zipData, err := downloadReportZip(ctx, wbClient, reportID, cfg)
	if err != nil {
		return nil, fmt.Errorf("download zip: %w", err)
	}

	log.Printf("📦 ZIP downloaded: %d bytes", len(zipData))

	// 5. Save report metadata FIRST (required for foreign key constraint)
	report := &sqlite.StockHistoryReport{
		ID:         reportID,
		ReportType: cfg.ReportType,
		StartDate:  beginDate,
		EndDate:    endDate,
		StockType:  cfg.StockType,
		Status:     "SUCCESS",
		FileSize:   reportItem.Size,
		CreatedAt:  reportItem.CreatedAt,
		RowsCount:  0, // Will be updated after parsing
	}
	if err := repo.SaveStockHistoryReport(ctx, report); err != nil {
		return nil, fmt.Errorf("save report metadata: %w", err)
	}

	// 6. Extract and parse CSV
	csvReader, err := extractCSVFromZip(zipData)
	if err != nil {
		return nil, fmt.Errorf("extract csv: %w", err)
	}

	// 7. Parse and save data rows to database
	var rowsCount int
	if cfg.ReportType == "metrics" {
		metricsRows, err := ParseMetricsCSV(csvReader)
		if err != nil {
			return nil, fmt.Errorf("parse metrics csv: %w", err)
		}

		// Convert to repo rows
		repoRows := make([]sqlite.StockHistoryMetricRow, len(metricsRows))
		for i, r := range metricsRows {
			repoRows[i] = sqlite.StockHistoryMetricRow{
				ReportID:          reportID,
				VendorCode:        stringPtr(r.VendorCode),
				Name:              stringPtr(r.Name),
				NmID:              r.NmID,
				SubjectName:       stringPtr(r.SubjectName),
				BrandName:         stringPtr(r.BrandName),
				SizeName:          stringPtr(r.SizeName),
				ChrtID:            intPtr(r.ChrtID),
				RegionName:        stringPtr(r.RegionName),
				OfficeName:        stringPtr(r.OfficeName),
				Availability:      stringPtr(r.Availability),
				OrdersCount:       r.OrdersCount,
				OrdersSum:         r.OrdersSum,
				BuyoutCount:       r.BuyoutCount,
				BuyoutSum:         r.BuyoutSum,
				BuyoutPercent:     r.BuyoutPercent,
				AvgOrders:         r.AvgOrders,
				StockCount:        r.StockCount,
				StockSum:          r.StockSum,
				SaleRate:          r.SaleRate,
				AvgStockTurnover:  r.AvgStockTurnover,
				ToClientCount:     r.ToClientCount,
				FromClientCount:   r.FromClientCount,
				Price:             r.Price,
				OfficeMissingTime: r.OfficeMissingTime,
				LostOrdersCount:   r.LostOrdersCount,
				LostOrdersSum:     r.LostOrdersSum,
				LostBuyoutsCount:  r.LostBuyoutsCount,
				LostBuyoutsSum:    r.LostBuyoutsSum,
				MonthlyData:       MonthlyDataToJSON(r.MonthlyData),
				Currency:          stringPtr(r.Currency),
			}
		}

		if err := repo.SaveStockHistoryMetrics(ctx, repoRows); err != nil {
			return nil, fmt.Errorf("save metrics: %w", err)
		}
		rowsCount = len(metricsRows)

	} else { // daily
		dailyRows, err := ParseDailyCSV(csvReader)
		if err != nil {
			return nil, fmt.Errorf("parse daily csv: %w", err)
		}

		// Convert to repo rows
		repoRows := make([]sqlite.StockHistoryDailyRow, len(dailyRows))
		for i, r := range dailyRows {
			repoRows[i] = sqlite.StockHistoryDailyRow{
				ReportID:    reportID,
				VendorCode:  stringPtr(r.VendorCode),
				Name:        stringPtr(r.Name),
				NmID:        r.NmID,
				SubjectName: stringPtr(r.SubjectName),
				BrandName:   stringPtr(r.BrandName),
				SizeName:    stringPtr(r.SizeName),
				ChrtID:      intPtr(r.ChrtID),
				OfficeName:  stringPtr(r.OfficeName),
				DailyData:   DailyDataToJSON(r.DailyData),
			}
		}

		if err := repo.SaveStockHistoryDaily(ctx, repoRows); err != nil {
			return nil, fmt.Errorf("save daily: %w", err)
		}
		rowsCount = len(dailyRows)
	}

	// 8. Update report with actual row count
	if err := repo.UpdateStockHistoryReportStatus(ctx, reportID, "SUCCESS", rowsCount); err != nil {
		return nil, fmt.Errorf("update report status: %w", err)
	}

	return &DownloadResult{
		ReportID:  reportID,
		Status:    "SUCCESS",
		RowsCount: rowsCount,
		Duration:  time.Since(start),
	}, nil
}

// createStockHistoryReport creates a new stock history report request.
func createStockHistoryReport(
	ctx context.Context,
	client *wb.Client,
	cfg config.StockHistoryConfig,
	beginDate, endDate string,
) (string, error) {

	// Generate UUID for the request
	requestID := uuid.New().String()

	// Determine report type
	var reportType string
	if cfg.ReportType == "metrics" {
		reportType = "STOCK_HISTORY_REPORT_CSV"
	} else {
		reportType = "STOCK_HISTORY_DAILY_CSV"
	}

	// Build request
	params := wb.StockHistoryReportParams{
		CurrentPeriod: wb.StockHistoryPeriod{
			Start: beginDate,
			End:   endDate,
		},
		StockType:     cfg.StockType,
		SkipDeletedNm: true,
	}

	// Metrics type requires additional fields
	if cfg.ReportType == "metrics" {
		// Availability filters: all availability types
		params.AvailabilityFilters = []string{"deficient", "actual", "balanced"}
		// Order by: average orders descending
		params.OrderBy = &wb.StockHistoryOrderBy{
			Field: "avgOrders",
			Mode:  "desc",
		}
	}

	req := wb.StockHistoryReportRequest{
		ID:           requestID,
		ReportType:   reportType,
		UserReportName: fmt.Sprintf("Stock History %s %s-%s", cfg.ReportType, beginDate, endDate),
		Params:       params,
	}

	// Get rate limits from config
	rateLimit := cfg.RateLimits.Create
	burst := cfg.RateLimits.CreateBurst

	// Create report
	downloadID, err := client.CreateStockHistoryReport(ctx, req, rateLimit, burst)
	if err != nil {
		return "", err
	}

	return downloadID, nil
}

// pollReportStatus polls the report status until SUCCESS, FAILED, or timeout.
func pollReportStatus(
	ctx context.Context,
	client *wb.Client,
	reportID string,
	cfg config.StockHistoryConfig,
) (*wb.StockHistoryReportItem, error) {

	pollInterval := time.Duration(cfg.PollIntervalSec) * time.Second
	timeout := time.Duration(cfg.PollTimeoutMin) * time.Minute
	deadline := time.Now().Add(timeout)

	rateLimit := cfg.RateLimits.StatusCheck
	burst := cfg.RateLimits.StatusCheckBurst

	for time.Now().Before(deadline) {
		// Check context
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Get status
		report, err := client.GetStockHistoryReportStatus(ctx, reportID, rateLimit, burst)
		if err != nil {
			log.Printf("⚠️  Status check error: %v (retrying...)", err)
			time.Sleep(pollInterval)
			continue
		}

		log.Printf("⏳ Polling... status: %s", report.Status)

		switch report.Status {
		case "SUCCESS":
			return report, nil
		case "FAILED":
			return report, fmt.Errorf("report generation failed")
		case "WAITING", "PROCESSING", "RETRY":
			// Continue polling
		default:
			return report, fmt.Errorf("unknown status: %s", report.Status)
		}

		time.Sleep(pollInterval)
	}

	return nil, fmt.Errorf("poll timeout after %v", timeout)
}

// downloadReportZip downloads the ZIP archive for a completed report.
func downloadReportZip(
	ctx context.Context,
	client *wb.Client,
	reportID string,
	cfg config.StockHistoryConfig,
) ([]byte, error) {

	rateLimit := cfg.RateLimits.Download
	burst := cfg.RateLimits.DownloadBurst

	return client.DownloadStockHistoryReport(ctx, reportID, rateLimit, burst)
}

// extractCSVFromZip extracts the CSV file from the ZIP archive.
// Returns an io.Reader for the CSV content.
func extractCSVFromZip(zipData []byte) (io.Reader, error) {
	// Open ZIP archive
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	// Find CSV file (usually only one file in the archive)
	var csvFile *zip.File
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, ".csv") {
			csvFile = f
			break
		}
	}

	if csvFile == nil {
		return nil, fmt.Errorf("no CSV file found in ZIP archive")
	}

	// Open file for reading
	rc, err := csvFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open csv file: %w", err)
	}
	defer rc.Close()

	// Read all content
	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read csv content: %w", err)
	}

	return bytes.NewReader(content), nil
}

// Helper functions for pointer conversion
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int64) *int {
	if i == 0 {
		return nil
	}
	val := int(i)
	return &val
}
