package fbsorders

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the Source interface.
// Базовые URL и дефолтные лимиты инкапсулированы здесь; фактические лимиты
// задаёт CLI через client.SetRateLimit (wb.FBSOrdersToolID / wb.FBSStatusToolID /
// wb.OrderFeedToolID) — getOrCreateLimiter возвращает уже зарегистрированный лимитер.
type WBSource struct {
	client *wb.Client
}

// NewWBSource creates a Source backed by the real WB Marketplace/Analytics API.
func NewWBSource(client *wb.Client) *WBSource {
	return &WBSource{client: client}
}

// Дефолтные лимиты (fallback, если SetRateLimit не вызван):
// сборочные задания/статусы — консервативно 120 req/min против swagger 300;
// лента заказов — 1 req/min (swagger), burst 1.
const (
	fbsDefaultRate  = 120
	fbsDefaultBurst = 20

	feedDefaultRate  = 1
	feedDefaultBurst = 1
)

// FBSOrdersIterator итерирует по окнам ≤ 30 дней и страницам /api/v3/orders.
func (s *WBSource) FBSOrdersIterator(
	ctx context.Context,
	from, to time.Time,
	callback func([]wb.FBSOrder) error,
) (int, error) {
	return s.client.FBSOrdersIterator(ctx, fbsDefaultRate, fbsDefaultBurst, from, to, callback)
}

// GetFBSOrdersStatus получает текущие статусы батчем ID (≤ 1000).
func (s *WBSource) GetFBSOrdersStatus(ctx context.Context, ids []int64) ([]wb.FBSOrderStatus, error) {
	return s.client.GetFBSOrdersStatus(ctx, fbsDefaultRate, fbsDefaultBurst, ids)
}

// OrderFeedIterator итерирует по ленте заказов с пагинацией snapshotTime.
func (s *WBSource) OrderFeedIterator(
	ctx context.Context,
	from time.Time,
	callback func([]wb.OrderFeedItem) error,
) (int, error) {
	return s.client.OrderFeedIterator(ctx, feedDefaultRate, feedDefaultBurst, from, callback)
}
