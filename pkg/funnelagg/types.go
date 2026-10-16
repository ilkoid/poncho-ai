// Package funnelagg provides a v2 dual-backend downloader for WB aggregated funnel metrics.
//
// Aggregated funnel data comes from POST /api/analytics/v3/sales-funnel/products
// (Seller Analytics API v3). Unlike regular funnel (per-day history per nmID batch),
// the aggregated endpoint returns period-level metrics for ALL products via offset pagination.
//
// Key difference from pkg/funnel/: no Reader interface needed — autonomous pagination.
package funnelagg

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID is the wb.Client ToolID for rate limiting setup.
const ToolID = "get_wb_funnel_aggregated"

// Source is the data source interface for aggregated funnel data.
// Implemented by WBSource (real API) and MockSource (--mock mode).
type Source interface {
	// LoadAggregatedPage fetches one page of aggregated funnel data.
	// Returns (products, currency, error).
	LoadAggregatedPage(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error)
}

// Writer is the persistence interface for aggregated funnel data.
// Declared here (consumer), implemented by pkg/storage/sqlite and pkg/storage/postgres (adapters).
//
// ISP-focused: only 4 methods — exactly what Downloader.Run() calls.
type Writer interface {
	// SaveFunnelAggregatedBatch saves a batch of aggregated products.
	// Returns count of products actually saved.
	SaveFunnelAggregatedBatch(ctx context.Context, products []wb.FunnelAggregatedProduct, periodStart, periodEnd, currency string) (int, error)

	// GetFunnelAggregatedCount returns count of existing records for a period.
	// Used for offset resume: skip already-downloaded pages.
	GetFunnelAggregatedCount(ctx context.Context, periodStart, periodEnd string) (int, error)

	// GetHistoryProductCount returns the MAX product count among the last 3
	// completed runs of the SAME window length, strictly older than currentEnd.
	// История самой воронки — лучший оценщик тотала [N/M % ETA]: состав отчёта
	// шире orders/sales (переходы в карточку, корзина), а ночь-к-ночи стабилен.
	// MAX, а не последний прогон: незавершённый прогон (offset-resume после
	// падения) не должен занижать оценку. 0 = истории такой длины окна нет.
	GetHistoryProductCount(ctx context.Context, windowDays int, currentEnd string) (int, error)

	// GetDistinctNmIDCount returns count of distinct nmIDs (from sales table).
	// Fallback оценки прогресса, когда истории воронки нет (best-effort,
	// may return 0 if no sales data) — систематически ЗАНИЖАЕТ тотал.
	GetDistinctNmIDCount(ctx context.Context) (int, error)
}

// DownloadOptions configures the aggregated funnel download behavior.
type DownloadOptions struct {
	// Period (required)
	SelectedStart string // YYYY-MM-DD
	SelectedEnd   string // YYYY-MM-DD

	// Past period (optional — enables comparison metrics)
	PastStart string // YYYY-MM-DD
	PastEnd   string // YYYY-MM-DD

	// Filters (optional — empty = all products)
	NmIDs         []int
	BrandNames    []string
	SubjectIDs    []int
	TagIDs        []int
	SkipDeletedNm bool

	// Ordering (optional)
	OrderByField string // openCard, orders, buyouts, etc.
	OrderByMode  string // asc, desc

	// Pagination
	PageSize int // Products per API request (default: 100, max: 1000)

	// Rate limiting
	RateLimit int // req/min (default: 3)
	Burst     int // burst (default: 3)

	// Page retry for global limiter recovery
	//
	// Два режима по PageRetryBudget:
	//   - Budget > 0 — «бой за страницу по времени»: циклы попыток идут подряд
	//     (пауза pageRetryCyclePause между ними, каждая попытка внутри цикла —
	//     hint-гонка клиента с кулдауном «подсказка+ε»); бюджет исчерпан —
	//     честная ошибка «page budget exhausted», offset-resume продолжит
	//     со сохранённой страницы при следующем запуске.
	//   - Budget == 0 — прежняя лестница: MaxPageRetries попыток с линейным
	//     сном PageRetryBaseSleep×n (backcompat).
	MaxPageRetries     int           // retries per page in ladder mode (default: 3)
	PageRetryBaseSleep time.Duration // ladder mode: base sleep 2m×n (default: 2min)
	PageRetryBudget    time.Duration // budget mode: wall-clock per page (0 = ladder)

	// DryRun skips DB writes but still fetches data.
	DryRun bool

	// OnProgress вызывается с прогрессом: cur/total — страница и оценка числа
	// страниц (total=0 — оценка неизвестна, например пустая БД), msg —
	// человекочитаемое сообщение. Инфо-строки (период, resume) идут с cur=0.
	// nil = silent (Tool mode).
	OnProgress func(cur, total int, msg string)
}

// DownloadResult holds the outcome of an aggregated funnel download run.
type DownloadResult struct {
	ProductsLoaded int
	PagesLoaded    int
	Errors         int
	Duration       time.Duration
}
