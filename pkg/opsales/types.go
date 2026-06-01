// Package opsales provides a reusable operational sales downloader for WB Statistics API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - OpsalesSource — API abstraction (*wb.Client via WBSource adapter)
//   - OpsalesWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
package opsales

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// OpsalesSource is the data source interface for operational sales downloads.
// Implemented by WBSource (real API) and MockOpsalesSource (--mock).
//
// Single method: wraps wb.Client.SalesIterator for ISP.
type OpsalesSource interface {
	// SalesIterator iterates over all sales pages from WB Statistics API.
	// Calls callback for each page of sales. Returns total count processed.
	SalesIterator(ctx context.Context, baseURL string, rateLimit, burst int, dateFrom string, callback func([]wb.SalesItem) error) (int, error)
}

// OpsalesWriter is the persistence interface for operational sales data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 2 methods — exactly what the Downloader needs.
// No resume — sales are always fully re-downloaded (ON CONFLICT upsert is safe).
type OpsalesWriter interface {
	// SaveSales saves a batch of sales with upsert semantics.
	// Returns count of saved sales.
	SaveSales(ctx context.Context, sales []wb.SalesItem) (int, error)

	// DeleteSalesOlderThan removes sales older than the given time.
	// Used for cleanup beyond the 90-day retention window.
	DeleteSalesOlderThan(ctx context.Context, before time.Time) (int64, error)
}

// DownloadOptions configures the operational sales download behavior.
type DownloadOptions struct {
	// Days is how many days back to download (default: 90, WB API retention).
	Days int

	// From overrides Days with an exact start date (YYYY-MM-DD, priority over Days).
	From string

	// To overrides end date (YYYY-MM-DD, default: now).
	To string

	// Rewrite deletes sales older than Days before downloading.
	Rewrite bool

	// DryRun skips all DB writes (SaveSales, DeleteSalesOlderThan).
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of an operational sales download run.
type DownloadResult struct {
	TotalSales int
	TotalPages int
	Duration   time.Duration
}
