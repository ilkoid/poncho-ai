// Package regionsales provides a reusable region sales downloader for WB Seller Analytics API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - RegionSalesSource — API abstraction (*wb.Client via WBSource adapter)
//   - RegionSalesWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
package regionsales

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// RegionSalesSource is the data source interface for region sales downloads.
// Implemented by WBSource (real API) and MockRegionSalesSource (--mock).
//
// Single method: wraps wb.Client.GetRegionSales for ISP.
// Rate limits are baked into the WBSource adapter — not passed through.
type RegionSalesSource interface {
	// GetRegionSales fetches all regional sales data for a given period.
	// Max period: 31 days. No pagination — single request returns all data.
	GetRegionSales(ctx context.Context, dateFrom, dateTo string) ([]wb.RegionSaleItem, error)
}

// RegionSalesWriter is the persistence interface for region sales data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 1 method — exactly what the Downloader needs.
// No resume — region sales are fully re-downloaded (ON CONFLICT upsert is safe).
type RegionSalesWriter interface {
	// SaveRegionSales saves a batch of region sale items for a given period.
	// Returns count of saved rows.
	SaveRegionSales(ctx context.Context, dateFrom, dateTo string, items []wb.RegionSaleItem) (int, error)
}

// DownloadOptions configures the region sales download behavior.
type DownloadOptions struct {
	// Days is how many days back to download (default: 7).
	Days int

	// Begin overrides Days with an exact start date (YYYY-MM-DD, priority over Days).
	Begin string

	// End overrides end date (YYYY-MM-DD, default: yesterday).
	End string

	// Date sets a single date download (sets Begin=End, highest priority).
	Date string

	// DryRun skips all DB writes (SaveRegionSales).
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a region sales download run.
type DownloadResult struct {
	TotalRows int
	Requests  int
	Duration  time.Duration
}
