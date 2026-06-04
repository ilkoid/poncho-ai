// Package stockhistory provides v2 domain logic for WB Stock History async CSV reports.
//
// Stock history uses an async lifecycle: create report → poll status → download ZIP →
// extract CSV → parse → save. Two report types are supported:
//   - "metrics": aggregated metrics with dynamic AvgOrdersByMonth_MM.YYYY columns
//   - "daily": daily stock levels with dynamic DD.MM.YYYY date columns
//
// This package follows the v2 dual-backend pattern: Source/Writer interfaces,
// compile-time assertions in storage adapters, and DiscardWriter for mock safety.
package stockhistory

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// StockHistorySource is the data source interface for async stock history reports.
// Implemented by WBSource (real API) and MockSource (--mock).
type StockHistorySource interface {
	CreateReport(ctx context.Context, req wb.StockHistoryReportRequest) (downloadID string, err error)
	PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error)
	DownloadFile(ctx context.Context, downloadID string) ([]byte, error)
}

// StockHistoryWriter is the persistence interface for stock history data.
// Declared here (consumer), implemented by pkg/storage/{sqlite,postgres} (adapters).
//
// 5 methods — ISP compliant. Count methods omitted: resume uses ReportRecord.RowsCount.
// Save returns (int, error) for progress tracking.
type StockHistoryWriter interface {
	// Report tracking (resume + lifecycle)
	GetReport(ctx context.Context, reportType, startDate, endDate, stockType string) (*ReportRecord, error)
	SaveReport(ctx context.Context, record ReportRecord) error
	UpdateReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error

	// Data rows — report_type selects which method is called
	SaveMetrics(ctx context.Context, rows []MetricRow) (int, error)
	SaveDaily(ctx context.Context, rows []DailyRow) (int, error)
}

// ReportRecord tracks a single async stock history report lifecycle.
type ReportRecord struct {
	ID           string // UUID download ID
	ReportType   string // "metrics" | "daily"
	StartDate    string // YYYY-MM-DD
	EndDate      string // YYYY-MM-DD
	StockType    string // "", "wb", "mp"
	Status       string // WAITING, PROCESSING, SUCCESS, FAILED
	FileSize     int64
	RowsCount    int
	CreatedAt    string
	DownloadedAt string
}

// MetricRow represents one parsed row from STOCK_HISTORY_REPORT_CSV.
// Pointers (*int, *float64) preserve CSV nullable semantics: "0" ≠ missing.
type MetricRow struct {
	ReportID string // filled by Downloader, not by parser

	// Fixed columns (29)
	VendorCode string
	Name       string
	NmID       int64
	SubjectName      string
	BrandName        string
	SizeName         string
	ChrtID           int64
	RegionName       string
	OfficeName       string
	Availability     string
	OrdersCount      *int
	OrdersSum        *int
	BuyoutCount      *int
	BuyoutSum        *int
	BuyoutPercent    *int
	AvgOrders        *float64
	StockCount       *int
	StockSum         *int
	SaleRate         *int
	AvgStockTurnover *int
	ToClientCount    *int
	FromClientCount  *int
	Price            *int
	OfficeMissingTime *int
	LostOrdersCount  *float64
	LostOrdersSum    *float64
	LostBuyoutsCount *float64
	LostBuyoutsSum   *float64
	Currency         string

	// Dynamic columns (AvgOrdersByMonth_MM.YYYY)
	MonthlyData map[string]float64 // {"02.2024": 10.5, "03.2024": 15.2}
}

// DailyRow represents one parsed row from STOCK_HISTORY_DAILY_CSV.
// Value types for fixed fields — all mandatory in daily report.
type DailyRow struct {
	ReportID string // filled by Downloader, not by parser

	// Fixed columns (8)
	VendorCode string
	Name       string
	NmID       int64
	SubjectName string
	BrandName   string
	SizeName    string
	ChrtID      int64
	OfficeName  string

	// Dynamic columns (DD.MM.YYYY — stock level at 23:59)
	DailyData map[string]int64 // {"10.02.2024": 100, "11.02.2024": 95}
}

// DownloadOptions configures the stock history download behavior.
type DownloadOptions struct {
	ReportType      string // "metrics" | "daily"
	StockType       string // "", "wb", "mp"
	From, To        string // YYYY-MM-DD
	Days            int
	DryRun          bool
	Resume          bool
	PollIntervalSec int
	PollTimeoutMin  int
	OnProgress      func(msg string) // nil = silent
}

// DownloadResult holds the outcome of a stock history download run.
type DownloadResult struct {
	DownloadID string
	RowsCount  int
	Status     string // SUCCESS, RESUMED, FAILED
	Duration   time.Duration
}
