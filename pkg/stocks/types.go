// Package stocks provides the v2 domain logic for downloading WB warehouse stock snapshots.
//
// Architecture: Source/Writer interfaces + Downloader. Business logic lives here;
// CLI driver in cmd/ does only flags → config → DI → Run.
//
// WB API: POST /api/analytics/v1/stocks-report/wb-warehouses
// Rate limit: 3 req/min (seller-analytics-api.wildberries.ru)
package stocks

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// StocksSource is the data source interface for warehouse stock snapshots.
// *wb.Client satisfies this directly through structural typing.
type StocksSource interface {
	GetStockWarehouses(ctx context.Context, limit, offset, rateLimit, burst int) ([]wb.StockWarehouseItem, error)
}

// StocksWriter is the persistence interface for stock snapshot data.
// Focused (ISP) — only methods called in Downloader.Run().
type StocksWriter interface {
	// SaveStocks saves a batch of stock items for a given snapshot date.
	// Returns count of inserted rows.
	SaveStocks(ctx context.Context, snapshotDate string, items []wb.StockWarehouseItem) (int, error)
	// CountStocks returns total number of stock rows in the database.
	CountStocks(ctx context.Context) (int, error)
	// CountStocksForDate returns number of stock rows for a specific snapshot date.
	CountStocksForDate(ctx context.Context, date string) (int, error)
	// GetDistinctSnapshotDates returns all dates that have stock snapshots, for gap detection.
	GetDistinctSnapshotDates(ctx context.Context) ([]string, error)
}

// DownloadOptions configures the stock snapshot download behavior.
type DownloadOptions struct {
	SnapshotDate string          // YYYY-MM-DD (default: today)
	DryRun       bool            // If true, skip Writer.Save
	FirstDate    string          // Start date for gap detection (optional)
	RateLimit    int             // Desired rate (req/min)
	Burst        int             // Desired burst
	OnProgress   func(msg string) // nil = silent (Tool mode)
}

// DownloadResult holds the outcome of a stock snapshot download run.
type DownloadResult struct {
	TotalRows int
	Pages     int
	Duration  time.Duration
}
