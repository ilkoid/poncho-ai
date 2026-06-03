package stocks

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the StocksSource interface.
// Isolates rate limit parameters inside the source adapter.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a StocksSource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// GetStockWarehouses fetches warehouse stock data via WB Analytics API.
// Delegates to wb.Client which handles rate limiting, retries, and 429 backoff.
func (s *WBSource) GetStockWarehouses(ctx context.Context, limit, offset, _, _ int) ([]wb.StockWarehouseItem, error) {
	// Rate limit and burst are stored in WBSource — ignore the parameters
	// passed by the downloader (they come from Downloader.opts which are
	// already baked into this WBSource at creation time).
	return s.client.GetStockWarehouses(ctx, limit, offset, s.rateLimit, s.burst)
}
