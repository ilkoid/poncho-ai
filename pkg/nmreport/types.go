package nmreport

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// NmReportSource is the data source interface for async CSV funnel reports.
// Implemented by WBSource (real API) and MockSource (--mock).
type NmReportSource interface {
	CreateReport(ctx context.Context, req wb.NmReportFunnelRequest) (downloadID string, err error)
	PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error)
	DownloadFile(ctx context.Context, downloadID string) ([]byte, error)
}

// NmReportWriter is the persistence interface for nm-report funnel data.
// Declared here (consumer), implemented by pkg/storage/sqlite (adapter).
type NmReportWriter interface {
	// Report tracking (--resume)
	GetNmReport(ctx context.Context, reportType, startDate, endDate string) (*NmReportRecord, error)
	SaveNmReport(ctx context.Context, record NmReportRecord) error
	UpdateNmReportStatus(ctx context.Context, downloadID, status string, rowsCount int) error

	// DETAIL: writes into funnel_metrics_daily (with cancel columns)
	SaveFunnelMetricsDetail(ctx context.Context, rows []FunnelDetailRow, refreshDays int) error

	// GROUPED: writes into funnel_metrics_grouped_daily
	SaveFunnelMetricsGrouped(ctx context.Context, rows []FunnelGroupedRow) error
}

// FunnelDetailRow is one row from DETAIL_HISTORY_REPORT CSV (per nmID per day).
type FunnelDetailRow struct {
	NmID                  int
	MetricDate            string
	OpenCardCount         int
	AddToCartCount        int
	OrdersCount           int
	OrdersSumRub          int
	BuyoutsCount          int
	BuyoutsSumRub         int
	CancelCount           int
	CancelSumRub          int
	AddToCartConversion   float64
	CartToOrderConversion float64
	BuyoutPercent         float64
	AddToWishlist         int
	Currency              string
}

// FunnelGroupedRow is one row from GROUPED_HISTORY_REPORT CSV (per day, aggregated).
type FunnelGroupedRow struct {
	MetricDate            string
	OpenCardCount         int
	AddToCartCount        int
	OrdersCount           int
	OrdersSumRub          int
	BuyoutsCount          int
	BuyoutsSumRub         int
	CancelCount           int
	CancelSumRub          int
	AddToCartConversion   float64
	CartToOrderConversion float64
	BuyoutPercent         float64
	AddToWishlist         int
	Currency              string
}

// NmReportRecord tracks a single async report lifecycle.
type NmReportRecord struct {
	ID          string // UUID download ID
	ReportType  string
	StartDate   string
	EndDate     string
	Status      string
	FileSize    int64
	RowsCount   int
	CreatedAt   string
	CompletedAt string
}

// DownloadOptions configures the nm-report funnel download behavior.
type DownloadOptions struct {
	ReportType      string // "detail" | "grouped"
	From, To        string // YYYY-MM-DD
	Days            int
	RefreshWindow   int    // for detail mode: REPLACE vs IGNORE
	DryRun          bool
	Resume          bool
	PollIntervalSec int
	PollTimeoutMin  int
	OnProgress      func(msg string) // nil = silent
}

// DownloadResult holds the outcome of a nm-report download run.
type DownloadResult struct {
	DownloadID string
	RowsCount  int
	Status     string // SUCCESS, RESUMED, FAILED
	Duration   time.Duration
}
