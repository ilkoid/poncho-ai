// Package orders provides a reusable orders downloader for WB Statistics API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - OrdersSource — API abstraction (*wb.Client via WBSource adapter)
//   - OrdersWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
package orders

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// OrdersSource is the data source interface for order downloads.
// Implemented by WBSource (real API) and MockOrdersSource (--mock).
//
// Single method: wraps wb.Client.OrdersIterator for ISP.
type OrdersSource interface {
	// OrdersIterator iterates over all order pages from WB Statistics API.
	// Calls callback for each page of orders. Returns total count processed.
	OrdersIterator(ctx context.Context, baseURL string, rateLimit, burst int, dateFrom string, callback func([]wb.OrdersItem) error) (int, error)
}

// OrdersWriter is the persistence interface for order data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 2 methods — exactly what the Downloader needs.
// No resume — orders are always fully re-downloaded (ON CONFLICT upsert is safe).
type OrdersWriter interface {
	// SaveOrders saves a batch of orders with upsert semantics.
	// Returns count of saved orders.
	SaveOrders(ctx context.Context, orders []wb.OrdersItem) (int, error)

	// DeleteOrdersOlderThan removes orders older than the given time.
	// Used for cleanup beyond the 90-day retention window.
	DeleteOrdersOlderThan(ctx context.Context, before time.Time) (int64, error)
}

// DownloadOptions configures the orders download behavior.
type DownloadOptions struct {
	// Days is how many days back to download (default: 90, WB API retention).
	Days int

	// From overrides Days with an exact start date (YYYY-MM-DD, priority over Days).
	From string

	// To overrides end date (YYYY-MM-DD, default: now).
	To string

	// Rewrite deletes orders older than Days before downloading.
	Rewrite bool

	// DryRun skips all DB writes (SaveOrders, DeleteOrdersOlderThan).
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of an orders download run.
type DownloadResult struct {
	TotalOrders int
	TotalPages  int
	Duration    time.Duration
}
