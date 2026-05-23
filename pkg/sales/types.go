package sales

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SalesSource is the minimal interface required from a WB client for sales download.
// This allows mocking and makes the package testable.
type SalesSource interface {
	ReportDetailByPeriodIteratorWithTime(
		ctx context.Context,
		baseURL string,
		rateLimit int,
		burst int,
		dateFrom string,
		dateTo string,
		callback func([]wb.RealizationReportRow) error,
	) (int, error)

	ReportDetailByPeriodIterator(
		ctx context.Context,
		baseURL string,
		rateLimit int,
		burst int,
		dateFrom int,
		dateTo int,
		callback func([]wb.RealizationReportRow) error,
	) (int, error)
}

// SalesWriter defines the persistence operations needed by the downloader.
type SalesWriter interface {
	GetLastSaleDT(ctx context.Context) (time.Time, error)
	GetFirstSaleDT(ctx context.Context) (time.Time, error)
	DeleteSalesByDateRange(ctx context.Context, from, to string) (int64, error)
	DeleteServiceRecordsByDateRange(ctx context.Context, from, to string) (int64, error)
	Save(ctx context.Context, rows []wb.RealizationReportRow) error
	SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error
	Exists(ctx context.Context, rrdID int) (bool, error)
}

// DownloadOptions holds tunable parameters for a sales download job.
type DownloadOptions struct {
	RateLimit          int
	Burst              int
	SkipServiceRecords bool
	Filter             config.FunnelFilterConfig
	MaxDaysPerPeriod   int
	Rewrite            bool
	DryRun             bool // if true, skip all DB writes
	OnProgress         func(msg string) // nil = silent (ideal for Tools)
}

// DownloadResult contains high-level statistics after the download completes.
type DownloadResult struct {
	TotalRows    int
	TotalPages   int
	Duration     time.Duration
	PeriodsCount int
}
