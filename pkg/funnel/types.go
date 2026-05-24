package funnel

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// BatchResult is one product's funnel history from a single API call.
type BatchResult struct {
	Product wb.FunnelProductMeta
	Rows    []wb.FunnelHistoryRow
}

// FunnelSource is the data source interface for funnel history.
// Implemented by WBSource (real API) and MockFunnelSource (--mock).
type FunnelSource interface {
	LoadBatch(ctx context.Context, nmIDs []int, from, to string) ([]BatchResult, error)
}

// FunnelWriter is the persistence interface for funnel data.
// Declared here (consumer), implemented by pkg/storage/sqlite (adapter).
type FunnelWriter interface {
	GetDistinctNmIDs(ctx context.Context) ([]int, error)
	GetSupplierArticlesByNmIDs(ctx context.Context, nmIDs []int) (map[int]string, error)
	FilterActiveNmIDs(ctx context.Context, nmIDs []int, activeDays int) ([]int, error)
	GetRecentlyLoadedNmIDs(ctx context.Context, hours int) (map[int]bool, error)
	SaveFunnelHistoryWithWindow(ctx context.Context, product wb.FunnelProductMeta, rows []wb.FunnelHistoryRow, refreshDays int) error
}

// DownloadOptions configures the funnel download behavior.
type DownloadOptions struct {
	Days              int
	BatchSize         int
	MaxBatches        int
	RefreshWindow     int
	IncrementalHours  int
	RateLimit         int
	Burst             int
	From              string // YYYY-MM-DD override
	To                string // YYYY-MM-DD override
	Filter            config.FunnelFilterConfig
	DryRun            bool
	OnProgress        func(msg string) // nil = silent (Tool mode)
}

// DownloadResult holds the outcome of a funnel download run.
type DownloadResult struct {
	ProductsLoaded int
	MetricsLoaded  int
	BatchesTotal   int
	Duration       time.Duration
}
