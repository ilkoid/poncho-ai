// Package promotion provides a reusable promotion data downloader for WB Advertising API.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - Source — API abstraction (WBSource adapter wrapping *wb.Client)
//   - Writer — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Reader — cross-domain read abstraction (campaign_products from campaigns domain)
//   - Downloader — business logic depends only on interfaces
//
// Covers 14 phases:
//   1.  Campaign Bids — from AdvertDetails.NmSettings
//   2.  Normquery Stats — search cluster statistics per (advert_id, nm_id, date)
//   3.  Normquery Clusters — active/excluded search clusters
//   4.  Normquery Bids — current bid per search cluster
//   5.  Normquery Minus — minus phrases
//   6.  Bid Recommendations — recommended bid levels (5 req/min)
//   7.  Expenses — campaign write-off history
//   8.  Balance — account balance snapshot
//   9.  Payments — payment history
//   10. Calendar Promotions — WB promotion calendar
//   11. Calendar Details — promotion details, advantages, ranging
//   12. Calendar Nomenclatures — eligible products per promotion
//   13. Campaign Budgets — per-campaign budget snapshot
//   14. Min Bids — minimum bids per product per placement
package promotion

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// Source Interface — API abstraction (14 methods, 1:1 with *wb.Client)
// ============================================================================

// Source is the data source interface for promotion V2 downloads.
// Implemented by WBSource (real API) and MockSource (--mock).
//
// 14 methods — 1:1 mapping to *wb.Client promotion methods.
// WBSource delegates directly; rate limits passed per-call from Downloader.
type Source interface {
	// GetAdvertDetails fetches campaign details including NmSettings with bids.
	GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error)

	// GetNormqueryStats fetches search cluster statistics per (advert_id, nm_id).
	GetNormqueryStats(ctx context.Context, req wb.NormqueryStatsRequest, rateLimit, burst int) (*wb.NormqueryStatsResponse, error)

	// GetNormqueryList fetches active/excluded search clusters.
	GetNormqueryList(ctx context.Context, req wb.NormqueryListRequest, rateLimit, burst int) (*wb.NormqueryListResponse, error)

	// GetNormqueryBids fetches current bid per (advert_id, nm_id, cluster).
	GetNormqueryBids(ctx context.Context, req wb.NormqueryBidsRequest, rateLimit, burst int) (*wb.NormqueryBidsResponse, error)

	// GetNormqueryMinus fetches minus phrases per (advert_id, nm_id).
	GetNormqueryMinus(ctx context.Context, req wb.NormqueryMinusRequest, rateLimit, burst int) (*wb.NormqueryMinusResponse, error)

	// GetBidRecommendations fetches recommended bids per product.
	// Rate limit: 5 req/min — slowest endpoint.
	GetBidRecommendations(ctx context.Context, nmID, advertID int, rateLimit, burst int) (*wb.BidRecommendationsResponse, error)

	// GetExpenses fetches campaign write-off history.
	GetExpenses(ctx context.Context, from, to string, rateLimit, burst int) (wb.ExpensesResponse, error)

	// GetBalance fetches account balance snapshot.
	GetBalance(ctx context.Context, rateLimit, burst int) (*wb.BalanceResponse, error)

	// GetPayments fetches payment history.
	GetPayments(ctx context.Context, from, to string, rateLimit, burst int) (wb.PaymentsResponse, error)

	// GetCalendarPromotions fetches WB promotion calendar.
	GetCalendarPromotions(ctx context.Context, start, end string, allPromo bool, rateLimit, burst int) (*wb.CalendarPromotionsResponse, error)

	// GetCalendarPromotionDetails fetches promotion details, advantages, ranging.
	GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*wb.CalendarPromotionDetailsResponse, error)

	// GetCalendarPromotionNomenclatures fetches eligible products per promotion.
	// Paginated: (limit, offset).
	GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*wb.CalendarPromotionNomsResponse, error)

	// GetCampaignBudget fetches budget for one campaign.
	GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*wb.BudgetResponse, error)

	// GetMinBids fetches minimum bids per product per placement.
	GetMinBids(ctx context.Context, req wb.MinBidsRequest, rateLimit, burst int) (*wb.MinBidsResponse, error)
}

// ============================================================================
// Writer Interface — persistence abstraction (14 save methods)
// ============================================================================

// Writer is the persistence interface for promotion V2 data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// 14 methods — exactly what Downloader.Run() calls.
// Wide but cohesive: all methods belong to single WB Promotion API domain.
type Writer interface {
	// SaveCampaignBids saves bid data extracted from AdvertDetails.
	SaveCampaignBids(ctx context.Context, rows []wb.CampaignBidRow) error

	// SaveNormqueryStatsBatch saves search cluster statistics (batch).
	SaveNormqueryStatsBatch(ctx context.Context, groups []wb.NormqueryStatsGroup, date string) error

	// SaveNormqueryBids saves current bid per search cluster.
	SaveNormqueryBids(ctx context.Context, items []wb.NormqueryBidItem) error

	// SaveNormqueryMinus saves minus phrases.
	SaveNormqueryMinus(ctx context.Context, items []wb.NormqueryMinusItem) error

	// SaveNormqueryClusters saves active/excluded search clusters.
	SaveNormqueryClusters(ctx context.Context, items []wb.NormqueryListItem) error

	// SaveBidRecommendations saves recommended bid levels per product.
	SaveBidRecommendations(ctx context.Context, recs []wb.BidRecommendationsResponse, snapshotDate string) error

	// SaveExpenses saves campaign write-off history.
	SaveExpenses(ctx context.Context, rows []wb.ExpenseRow) error

	// SaveBalance saves account balance snapshot.
	SaveBalance(ctx context.Context, balance wb.BalanceResponse, snapshotDate string) error

	// SavePayments saves payment history.
	SavePayments(ctx context.Context, rows []wb.PaymentRow) error

	// SaveCalendarPromotions saves WB promotion calendar entries.
	SaveCalendarPromotions(ctx context.Context, promos []wb.CalendarPromotion) error

	// SaveCalendarPromotionDetails saves promotion details, advantages, ranging.
	SaveCalendarPromotionDetails(ctx context.Context, details []wb.CalendarPromotionDetail) error

	// SaveCalendarPromotionNomenclatures saves eligible products per promotion.
	SaveCalendarPromotionNomenclatures(ctx context.Context, promotionID int, noms []wb.CalendarPromotionNom, snapshotDate string) error

	// SaveCampaignBudget saves per-campaign budget snapshot.
	SaveCampaignBudget(ctx context.Context, advertID int, budget wb.BudgetResponse, snapshotDate string) error

	// SaveMinBids saves minimum bids per product per placement.
	SaveMinBids(ctx context.Context, advertID int, items []wb.MinBidItem, snapshotDate string) error
}

// ============================================================================
// Reader Interface — cross-domain reads (campaign_products)
// ============================================================================

// Reader provides cross-domain read access for promotion V2 downloads.
//
// Promotion V2 needs (advert_id, nm_id) pairs from campaigns domain tables,
// and calendar promotion IDs saved by earlier phases within the same run.
// Reader reads from the SAME backend as Writer — dual-backend consistency.
// One object can implement both Writer and Reader.
type Reader interface {
	// GetCampaignProductIDs returns (advert_id, nm_id) pairs matching status filter.
	GetCampaignProductIDs(ctx context.Context, statuses []int, changedSince string) ([]wb.NormqueryItem, error)

	// GetNormqueryLastRun returns the most recent stats_date from normquery_stats.
	// Used for incremental mode: only fetch campaigns changed since last run.
	GetNormqueryLastRun(ctx context.Context) (string, error)

	// GetCalendarPromotionIDs returns all promotion IDs from calendar.
	// Used by calendar details phase to fetch details for each promotion.
	GetCalendarPromotionIDs(ctx context.Context) ([]int, error)

	// GetCalendarPromotionIDsByType returns IDs excluding a specific type.
	// Used by calendar nomenclatures phase (skips "auto" type — API returns 422).
	GetCalendarPromotionIDsByType(ctx context.Context, excludeType string) ([]int, error)
}

// ============================================================================
// Rate Limits — per-phase configuration
// ============================================================================

// RateLimits holds per-phase rate limit configuration.
// Mapped from config.PromotionV2RateLimits in CLI.
type RateLimits struct {
	Normquery      int // normquery list/bids/minus + advert details
	NormqueryBurst int
	NormqueryStats      int // normquery stats (separate, 10/min)
	NormqueryStatsBurst int
	BidRec      int // bid recommendations (5/min)
	BidRecBurst int
	Finance      int // expenses, balance, payments, budget
	FinanceBurst int
	Calendar      int // calendar promotions, details, noms
	CalendarBurst int
	MinBids      int // minimum bids
	MinBidsBurst int
}

// ============================================================================
// Download Options & Result
// ============================================================================

// DownloadOptions configures the promotion V2 download behavior.
type DownloadOptions struct {
	// Input data (loaded by CLI via Reader before calling Run).
	ProductIDs []wb.NormqueryItem

	// Date range for normquery stats, expenses, payments.
	BeginDate string // YYYY-MM-DD
	EndDate   string // YYYY-MM-DD

	// Calendar date range (separate from main period).
	CalendarBegin string // YYYY-MM-DD
	CalendarEnd   string // YYYY-MM-DD

	// Rate limits per phase.
	RateLimits RateLimits

	// Skip flags for partial runs.
	SkipBids            bool
	SkipNormquery       bool
	SkipRecommendations bool
	SkipFinance         bool
	SkipCalendar        bool
	SkipBudgets         bool
	SkipMinBids         bool

	// DryRun skips all DB writes.
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a promotion V2 download run.
type DownloadResult struct {
	TotalSteps     int           // phases attempted
	CompletedSteps int           // phases completed (including with non-fatal errors)
	Errors         int           // phases with logged errors
	Duration       time.Duration // total run time
}
