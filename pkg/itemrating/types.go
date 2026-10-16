// Package itemrating provides a v2 dual-backend downloader for the WB item
// rating report («Оценка товара», POST /api/analytics/v2/item-rating).
//
// The report returns three blocks — account seller rating, account-wide
// feedback increase and a per-item rating distribution. All three are
// persisted as-is (RAW layer): every spec field gets a column and each
// row/block also keeps its exact JSON in RawItem/RawSeller/RawFbIncrease,
// so fields added by WB between spec refreshes are not lost.
//
// Key design (modelled on pkg/funnelagg): autonomous offset pagination,
// no Reader interface; rows live in this package (searchvis convention).
package itemrating

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// PageSizeMax is the max page size allowed by the API (limit param).
const PageSizeMax = 1000

// DefaultOrderByField/Mode keep pagination deterministic (full catalog sweep).
const (
	DefaultOrderByField = "feedbackCount"
	DefaultOrderByMode  = "desc"
)

// Row is one item of the rating distribution for one period window.
// Grain: UNIQUE(nm_id, period_start, period_end).
type Row struct {
	PeriodStart string // YYYY-MM-DD
	PeriodEnd   string // YYYY-MM-DD

	// Identification
	NmID        int64
	VendorCode  string
	Title       string
	SubjectID   int64
	SubjectName string
	BrandName   string
	TagID       int64
	TagName     string

	// Flags & scores
	PinnedFeedback bool
	CardRating     float64 // WB field `rating` — «Рейтинг карточки товара» (не звёзды)

	// Star rating: current + dynamics vs past period + percentile in subject
	FeedbackRatingCurrent    float64
	FeedbackRatingDynamics   float64
	FeedbackRatingPercentile *float64 // nullable

	// Feedback counts: period increment + dynamics
	FeedbackCountInc      int
	FeedbackCountDynamics int

	// Per-star period increments (+ dynamics) — 5 levels
	FiveStarInc        int
	FiveStarDynamics   int
	FourStarInc        int
	FourStarDynamics   int
	ThreeStarInc       int
	ThreeStarDynamics  int
	TwoStarInc         int
	TwoStarDynamics    int
	OneStarInc         int
	OneStarDynamics    int

	// WB-side quality signals
	Disqualified int  // отзывы, исключённые WB из рейтинга
	IsShadowed   bool // товар скрыт из каталога

	// Exact JSON of the item as returned by WB (drift protection).
	RawItem []byte
}

// SellerRow is the account-level block for one period window.
// Grain: UNIQUE(period_start, period_end).
type SellerRow struct {
	PeriodStart string
	PeriodEnd   string

	SellerRatingCurrent  float64
	SellerRatingDynamics float64

	FbIncreaseCurrent  int
	FbIncreaseTotal    int
	FbIncreaseDynamics int

	FiveStarInc      int
	FiveStarDynamics int
	FiveStarTotal    int
	FourStarInc      int
	FourStarDynamics int
	FourStarTotal    int
	ThreeStarInc     int
	ThreeStarDynamics int
	ThreeStarTotal   int
	TwoStarInc       int
	TwoStarDynamics  int
	TwoStarTotal     int
	OneStarInc       int
	OneStarDynamics  int
	OneStarTotal     int

	// Exact JSON of both blocks as returned by WB.
	RawSeller     []byte
	RawFbIncrease []byte
}

// Page is the result of one API page fetch.
type Page struct {
	Items    []Row
	Seller   *SellerRow // account block (present on every page; saved from the last one)
	APICount int        // len(Items) as returned by the API
}

// Source is the data source interface.
// Implemented by WBSource (real API) and MockSource (--mock mode / tests).
type Source interface {
	// FetchPage fetches one page of the item rating report.
	FetchPage(ctx context.Context, req wb.ItemRatingRequest) (Page, error)
}

// Writer is the persistence interface.
// Declared here (consumer), implemented by pkg/storage/sqlite and pkg/storage/postgres.
//
// ISP: only 3 methods — exactly what Downloader.Run() calls.
type Writer interface {
	// SaveItemRatings upserts a batch of item rows. Returns count of rows saved.
	SaveItemRatings(ctx context.Context, rows []Row) (int, error)

	// SaveSellerRating upserts the account-level block. Returns count of rows saved (0 or 1).
	SaveSellerRating(ctx context.Context, row SellerRow) (int, error)

	// CountItemRatings returns the row count stored for a period window.
	// Used for the post-run completeness check.
	CountItemRatings(ctx context.Context, periodStart, periodEnd string) (int, error)
}

// DownloadOptions configures the download behavior.
type DownloadOptions struct {
	// Period (required): current window, e.g. yesterday (days=1)
	PeriodStart string // YYYY-MM-DD
	PeriodEnd   string // YYYY-MM-DD

	// Past period (optional — equal-length window right before PeriodStart;
	// enables the API-side dynamics fields)
	PastStart string
	PastEnd   string

	// Ordering (deterministic full-catalog sweep)
	OrderByField string // default feedbackCount
	OrderByMode  string // default desc

	// Pagination
	PageSize int // default/max 1000

	// Rate limiting (req/min + burst), passed through to the wb client by the CLI
	RateLimit int
	Burst     int

	// DryRun skips DB writes but still fetches data.
	DryRun bool

	// OnProgress is called with a human-readable progress message.
	// nil = silent (Tool mode).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a run.
type DownloadResult struct {
	ItemsLoaded int           // rows fetched (and saved unless DryRun)
	PagesLoaded int
	Errors      int
	// DBCount is the stored row count for the period after the run
	// (0 when DryRun). Mismatch vs ItemsLoaded is expected on re-runs
	// and hourly data drift between pages (upserts collapse duplicates).
	DBCount  int
	Duration time.Duration
}
