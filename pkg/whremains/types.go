// Package whremains provides the v2 domain logic for downloading WB warehouse remains reports.
//
// Architecture: Source/Writer interfaces + Downloader. Business logic lives here;
// CLI driver in cmd/ does only flags → config → DI → Run.
//
// WB API: async 3-step flow (all GET):
//   - GET /api/v1/warehouse_remains                      → create report task
//   - GET /api/v1/warehouse_remains/tasks/{id}/status    → poll status
//   - GET /api/v1/warehouse_remains/tasks/{id}/download  → download data (bare JSON array)
//
// Rate limits: create 1/min, status 12/min, download 1/min
// Base URL: https://seller-analytics-api.wildberries.ru
package whremains

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WhRemainsSource is the data source interface for warehouse remains async reports.
// Three methods matching the 3-step async API flow.
type WhRemainsSource interface {
	// CreateReport initiates report generation. Returns task ID.
	CreateReport(ctx context.Context, params WHRemainsParams) (taskID string, err error)
	// PollStatus checks current task status. Returns one of: new, processing, done, purged, canceled.
	PollStatus(ctx context.Context, taskID string) (status string, err error)
	// DownloadReport downloads the completed report data. Returns bare JSON array items.
	DownloadReport(ctx context.Context, taskID string) ([]wb.WarehouseRemainsItem, error)
}

// WhRemainsWriter is the persistence interface for warehouse remains data.
// Focused (ISP) — only methods called in Downloader.Run().
type WhRemainsWriter interface {
	// SaveRemains saves flattened warehouse remains rows for a given snapshot date.
	// Returns count of upserted rows.
	SaveRemains(ctx context.Context, snapshotDate string, rows []WhRemainsFlatRow) (int, error)
	// CountRemainsForDate returns number of rows for a specific snapshot date.
	// Used for resume check: if > 0, skip download.
	CountRemainsForDate(ctx context.Context, date string) (int, error)
}

// WHRemainsParams controls report generation grouping options.
type WHRemainsParams struct {
	GroupByNm   bool // group by nmID (adds volume field)
	GroupBySize bool // group by size (techSize)
}

// WhRemainsFlatRow is a single flattened row: one product × one warehouse.
// The API returns nested items with warehouses[] — this is the denormalized form for storage.
type WhRemainsFlatRow struct {
	Brand         string
	SubjectName   string
	VendorCode    string
	NmID          int64
	Barcode       string
	TechSize      string
	Volume        float64
	WarehouseName string
	Quantity      int64
}

// DownloadOptions configures the warehouse remains download behavior.
type DownloadOptions struct {
	SnapshotDate   string        // YYYY-MM-DD (default: today)
	DryRun         bool          // If true, skip Writer.Save
	Params         WHRemainsParams // Grouping options
	PollIntervalSec int          // Poll interval in seconds (default: 30)
	PollTimeoutMin  int          // Poll timeout in minutes (default: 30)
	OnProgress     func(msg string) // nil = silent (Tool mode)
}

// DownloadResult holds the outcome of a warehouse remains download run.
type DownloadResult struct {
	TaskID    string
	Status    string // SUCCESS, RESUMED
	TotalRows int
	Duration  time.Duration
}
