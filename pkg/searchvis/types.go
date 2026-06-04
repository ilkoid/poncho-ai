// Package searchvis provides a reusable search visibility downloader for WB Seller Analytics API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - Source — API abstraction (WBSource adapter wrapping *wb.Client)
//   - Writer — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Reader — cross-domain read abstraction (nmIDs from sales/cards tables)
//   - Downloader — business logic depends only on interfaces
//
// Covers 2 phases:
//   1. Search Positions — POST /api/v2/search-report/report (aggregated positions, visibility %)
//   2. Search Queries  — POST /api/v2/search-report/product/search-texts (top queries per product)
//
// Both endpoints share a global 3 req/min rate limit.
package searchvis

import (
	"context"
	"time"
)

// ToolID constants for SetRateLimit — must match the ToolIDs used in wb.Client calls.
// CLI must call wbClient.SetRateLimit() for ToolIDReport, then ShareRateLimit for ToolIDSearchTexts.
const (
	ToolIDReport     = "search_report"  // POST /api/v2/search-report/report
	ToolIDSearchTexts = "search_texts"  // POST /api/v2/search-report/product/search-texts
)

// Batch size constants from WB API limits.
const (
	PositionsBatchSize = 100 // max nmIDs per /report request
	QueryBatchSize     = 50  // max nmIDs per /search-texts request
)

// ============================================================================
// Row Types — moved from pkg/storage/sqlite/ to this package for ISP.
// ============================================================================

// SearchPositionRow represents one row in search_positions_daily.
// UNIQUE constraint: (nm_id, snapshot_date, period_start).
type SearchPositionRow struct {
	NmID                 int
	SnapshotDate         string
	AvgPosition          float64
	AvgPositionDynamics  float64
	MedianPosition       float64
	Visibility           float64
	VisibilityDynamics   float64
	OpenCard             int
	OpenCardDynamics     float64
	ClusterFirstHundred  int
	ClusterSecondHundred int
	ClusterBelow         int
	PeriodStart          string
	PeriodEnd            string
}

// SearchQueryRow represents one row in search_queries_daily.
// UNIQUE constraint: (nm_id, search_text, snapshot_date).
type SearchQueryRow struct {
	NmID                int
	SnapshotDate        string
	SearchText          string
	Frequency           int
	FrequencyDynamics   float64
	WeekFrequency       int
	AvgPosition         float64
	AvgPositionDynamics float64
	MedianPosition      float64
	MedianPosDynamics   float64
	Visibility          float64
	OpenCard            int
	AddToCart           int
	Orders              int
	OpenToCart          float64
	CartToOrder         float64
	VendorCode          string
	BrandName           string
	SubjectName         string
	PeriodStart         string
	PeriodEnd           string
}

// ============================================================================
// Source Interface — API abstraction
// ============================================================================

// PositionsRequest holds parameters for FetchPositions.
// Uses struct because there are 5+ parameters after ctx (PageRequest pattern).
type PositionsRequest struct {
	NmIDs []int
	Begin string // YYYY-MM-DD
	End   string // YYYY-MM-DD
}

// TextsRequest holds parameters for FetchSearchTexts.
type TextsRequest struct {
	NmIDs []int
	Begin string // YYYY-MM-DD
	End   string // YYYY-MM-DD
	Limit int    // max queries per product (default: 30)
}

// Source is the data source interface for search visibility downloads.
// Implemented by WBSource (real API) and MockSource (--mock).
//
// 2 methods — one per API endpoint.
// WBSource wraps *wb.Client because response parsing is non-trivial
// (nested JSON → typed rows).
type Source interface {
	// FetchPositions downloads aggregated search position/visibility data.
	// Wraps POST /api/v2/search-report/report.
	// Returns one row per nmID (API returns aggregated values — WBSource replicates).
	FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error)

	// FetchSearchTexts downloads top search queries per product.
	// Wraps POST /api/v2/search-report/product/search-texts.
	FetchSearchTexts(ctx context.Context, req TextsRequest) ([]SearchQueryRow, error)
}

// ============================================================================
// Writer Interface — persistence abstraction
// ============================================================================

// Writer is the persistence interface for search visibility data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 4 methods — exactly what Downloader.Run() calls.
type Writer interface {
	// SaveSearchPositions saves batch of position snapshots.
	// Returns (rows saved, error) — count for progress tracking.
	SaveSearchPositions(ctx context.Context, rows []SearchPositionRow) (int, error)

	// SaveSearchQueries saves batch of search query snapshots.
	// Returns (rows saved, error) — count for progress tracking.
	SaveSearchQueries(ctx context.Context, rows []SearchQueryRow) (int, error)

	// CountSearchPositions returns total rows in search_positions_daily.
	CountSearchPositions(ctx context.Context) (int, error)

	// CountSearchQueries returns total rows in search_queries_daily.
	CountSearchQueries(ctx context.Context) (int, error)
}

// ============================================================================
// Reader Interface — cross-domain reads (nmID resolution)
// ============================================================================

// Reader provides cross-domain read access for nmID resolution.
// Search-visibility is unique among v2 downloaders: it needs nmIDs from DB
// as INPUT for API calls (from sales/cards/orders tables).
//
// Reader reads from the SAME backend as Writer — dual-backend consistency.
// One object can implement both Writer and Reader.
type Reader interface {
	// GetDistinctNmIDs returns all distinct nm_id from sales table.
	GetDistinctNmIDs(ctx context.Context) ([]int, error)

	// GetSupplierArticlesByNmIDs returns nm_id → supplier_article map.
	// Used for filtering by article length and year digits.
	GetSupplierArticlesByNmIDs(ctx context.Context, nmIDs []int) (map[int]string, error)

	// FilterActiveNmIDs filters to only nmIDs with recent sales activity.
	FilterActiveNmIDs(ctx context.Context, nmIDs []int, activeDays int) ([]int, error)
}

// ============================================================================
// Download Options & Result
// ============================================================================

// DownloadOptions configures the search visibility download behavior.
type DownloadOptions struct {
	// NmIDs — product list to download (resolved by CLI via Reader).
	NmIDs []int

	// Date range (resolved by CLI before passing to Downloader)
	BeginDate    string // YYYY-MM-DD
	EndDate      string // YYYY-MM-DD
	SnapshotDate string // YYYY-MM-DD (today — labels the snapshot)

	// Query limit — max search queries per product (default: 30)
	QueryLimit int

	// Skip flags for partial runs
	SkipPositions bool // Skip phase 1 (positions)
	SkipQueries   bool // Skip phase 2 (queries)

	// DryRun skips all DB writes.
	DryRun bool

	// Rate limit (req/min) and burst — shared between both endpoints.
	RateLimit int
	Burst     int

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a search visibility download run.
type DownloadResult struct {
	PositionRows int           // Total position rows saved
	QueryRows    int           // Total query rows saved
	Errors       int           // Batches that failed but were skipped
	Duration     time.Duration // Total run time
}
