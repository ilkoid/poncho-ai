// Package stockproducts provides the v2 domain logic for downloading WB stock product metrics.
//
// Architecture: Source/Writer interfaces + Downloader. Business logic lives here;
// CLI driver in cmd/ does only flags → config → DI → Run.
//
// WB API: POST /api/v2/stocks-report/products/products
// Rate limit: 3 req/min (seller-analytics-api.wildberries.ru)
package stockproducts

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// StockProductsSource is the data source interface for stock product metrics.
type StockProductsSource interface {
	GetStockProducts(ctx context.Context, req wb.StockProductRequest) ([]wb.StockProductItem, error)
}

// StockProductsWriter is the persistence interface for stock product data.
// ISP-focused — only methods called in Downloader.Run().
type StockProductsWriter interface {
	// SaveStockProducts saves a batch of product metrics for a given snapshot date.
	// Returns count of upserted rows.
	SaveStockProducts(ctx context.Context, snapshotDate string, items []wb.StockProductItem) (int, error)
	// CountStockProducts returns total number of rows in stock_products table.
	CountStockProducts(ctx context.Context) (int, error)
	// CountStockProductsForDate returns number of rows for a specific snapshot date.
	CountStockProductsForDate(ctx context.Context, date string) (int, error)
}

// DownloadOptions configures the stock product metrics download behavior.
type DownloadOptions struct {
	SnapshotDate string        // YYYY-MM-DD (default: yesterday — today's data is incomplete)
	PeriodStart  string        // YYYY-MM-DD (default: SnapshotDate)
	PeriodEnd    string        // YYYY-MM-DD (default: SnapshotDate)
	PageSize     int           // max 1000 (default: 1000)
	DryRun       bool          // If true, skip Writer.Save
	RateLimit    int           // Desired rate (req/min, default: 3)
	Burst        int           // Desired burst (default: 3)
	OnProgress   func(msg string) // nil = silent (Tool mode)
}

// DownloadResult holds the outcome of a stock product metrics download run.
type DownloadResult struct {
	TotalRows int
	Pages     int
	Duration  time.Duration
}
