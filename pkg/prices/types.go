// Package prices provides a reusable prices downloader for WB Discounts-Prices API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - PricesSource — API abstraction (*wb.Client via WBSource adapter)
//   - PricesWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
package prices

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// PricesSource is the data source interface for price downloads.
// Implemented by WBSource (real API) and MockPricesSource (--mock).
//
// Single method: offset-based pagination mirrors the WB Discounts-Prices API
// where each page returns products at a given limit/offset.
type PricesSource interface {
	// GetPrices fetches one page of product prices.
	// Returns (prices, count, error). Count is len(prices) on success.
	GetPrices(ctx context.Context, limit, offset int) ([]wb.ProductPrice, int, error)
}

// PricesWriter is the persistence interface for price data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 2 methods — exactly what the Downloader needs.
// No resume — prices are a full snapshot (ON CONFLICT upsert is safe).
type PricesWriter interface {
	// SavePrices saves a batch of prices with upsert semantics.
	// snapshotDate is applied to all rows (YYYY-MM-DD, set by caller).
	// Returns count of saved rows.
	SavePrices(ctx context.Context, prices []wb.ProductPrice, snapshotDate string) (int, error)

	// CountPrices returns total price record count (for verification).
	CountPrices(ctx context.Context) (int, error)
}

// DownloadOptions configures the prices download behavior.
type DownloadOptions struct {
	// PageSize is products per page (default: 1000, API max).
	PageSize int

	// SnapshotDate is the date for this snapshot (YYYY-MM-DD).
	// Typically today. Set by caller, not the API.
	SnapshotDate string

	// DryRun skips all DB writes (SavePrices).
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a prices download run.
type DownloadResult struct {
	TotalProducts int
	Pages         int
	Requests      int
	Duration      time.Duration
}
