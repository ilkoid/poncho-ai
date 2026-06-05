// Package penalties implements the Measurement Penalties downloader domain.
//
// WB Seller Analytics API: GET /api/analytics/v1/measurement-penalties
// Штрафы за неверные габариты и вес упаковки.
//
// Light domain: no resume, no cross-domain dependencies, single-pass date-range download.
package penalties

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID for rate limiting — separate from other seller-analytics endpoints.
const ToolID = "wb_measurement_penalties"

// PenaltiesSource abstracts penalty data iteration (WB API or mock).
// *wb.Client satisfies this via structural typing (MeasurementPenaltiesIterator).
type PenaltiesSource interface {
	MeasurementPenaltiesIterator(
		ctx context.Context,
		dateFrom, dateTo string,
		rateLimit, burst int,
		callback func([]wb.MeasurementPenaltyItem, int) error,
	) (int, error)
}

// PenaltiesWriter persists penalty data (SQLite or PostgreSQL).
// ISP-focused: only methods called in Downloader.Run().
type PenaltiesWriter interface {
	// SavePenalties upserts a batch of penalties. Returns count saved.
	SavePenalties(ctx context.Context, items []wb.MeasurementPenaltyItem) (int, error)
	// DeletePenaltiesOlderThan removes penalties with dt_bonus before the given time.
	DeletePenaltiesOlderThan(ctx context.Context, before time.Time) (int64, error)
}

// DownloadOptions controls downloader behavior.
type DownloadOptions struct {
	Days       int    // дней от вчерашнего (default: 90)
	From       string // точная начальная дата (приоритет над Days)
	To         string // точная конечная дата (приоритет над Days)
	Rewrite    bool   // удалить старые данные за период перед загрузкой
	DryRun     bool   // skip DB writes, show what would be saved
	OnProgress func(msg string) // nil = silent (Tool mode)
}

// DownloadResult holds download statistics.
type DownloadResult struct {
	TotalPenalties int
	TotalPages     int
	Duration       time.Duration
}
