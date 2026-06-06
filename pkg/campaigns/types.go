// Package campaigns provides a reusable campaigns downloader for WB Promotion API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - CampaignsSource — API abstraction (*wb.Client via WBSource adapter)
//   - CampaignsWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
//
// Covers 3 phases of campaign data:
//   1. Campaign list (GET /adv/v1/promotion/count)
//   2. Campaign details (GET /api/advert/v2/adverts)
//   3. Campaign fullstats (GET /adv/v3/fullstats) — 4-level hierarchy flattened to 4 tables
package campaigns

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CampaignsSource is the data source interface for campaign downloads.
// Implemented by WBSource (real API) and MockCampaignsSource (--mock).
//
// 3 methods — one per API endpoint. *wb.Client satisfies via structural typing.
type CampaignsSource interface {
	// GetCampaignList returns all campaigns grouped by type+status.
	// Wraps GET /adv/v1/promotion/count.
	GetCampaignList(ctx context.Context) (*wb.PromotionCountResponse, error)

	// GetAdvertDetails returns campaign metadata (name, payment_type, timestamps).
	// Wraps GET /api/advert/v2/adverts. Max 50 IDs per request.
	GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error)

	// GetAllAdvertDetails returns details for ALL campaigns (no ID filter).
	// API ignores the id parameter anyway — single call fetches everything.
	GetAllAdvertDetails(ctx context.Context) ([]wb.AdvertDetail, error)

	// GetCampaignFullstats returns full statistics with 4-level hierarchy.
	// Wraps GET /adv/v3/fullstats. Max 50 IDs, max 31 days per request.
	GetCampaignFullstats(ctx context.Context, ids []int, begin, end string, rl, burst int) ([]wb.CampaignFullstatsResponse, error)
}

// CampaignsWriter is the persistence interface for campaign data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 5 methods — exactly what Downloader.Run() calls.
// SaveFullstats aggregates 4 stat table writes (daily, app, nm, booster).
type CampaignsWriter interface {
	// SaveCampaigns saves campaign metadata from promotion/count.
	SaveCampaigns(ctx context.Context, groups []wb.PromotionAdvertGroup) error

	// SaveCampaignDetails updates campaign metadata from adverts endpoint.
	// Uses UPDATE — rows exist after SaveCampaigns().
	SaveCampaignDetails(ctx context.Context, details []wb.AdvertDetail) error

	// SaveFullstats saves all 4 stat tables from flattened fullstats data.
	// Aggregate method: internally writes to daily, app, nm, booster tables.
	SaveFullstats(ctx context.Context, flat wb.FlattenAllResult) error

	// GetLastCampaignStatsDateAll returns the most recent stats_date per campaign.
	// Used for resume mode: skip already-loaded date ranges.
	GetLastCampaignStatsDateAll(ctx context.Context) (map[int]time.Time, error)

	// PopulateCampaignProducts rebuilds the campaign_products materialized view.
	// Called after stats download as a post-processing step.
	PopulateCampaignProducts(ctx context.Context) error
}

// DownloadOptions configures the campaign download behavior.
type DownloadOptions struct {
	// Date range (resolved by CLI before passing to Downloader)
	Begin string // YYYY-MM-DD
	End   string // YYYY-MM-DD

	// Statuses filters campaigns for stats download (empty = all).
	Statuses []int

	// Skip flags for partial runs
	SkipCampaigns bool // Skip phase 1 (reuse IDs from DB — NOT supported in v2)
	SkipDetails   bool // Skip phase 2
	SkipStats     bool // Skip phase 3

	// Resume continues stats download from last loaded date per campaign.
	Resume bool

	// DryRun skips all DB writes (Save*, Populate).
	DryRun bool

	// Fullstats rate limit (req/min) and burst — passed to source.GetCampaignFullstats.
	FullstatsRate  int
	FullstatsBurst int

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a campaign download run.
type DownloadResult struct {
	CampaignsTotal    int // Total campaigns from API
	CampaignsForStats int // Campaigns matching status filter
	DetailsLoaded     int // Campaign details updated

	// Stats row counts across all batches + date windows
	DailyRows   int
	AppRows     int
	NmRows      int
	BoosterRows int

	DateWindows int // Number of 31-day windows processed
	Errors      int // Batches/windows that failed but were skipped
	Duration    time.Duration
}
