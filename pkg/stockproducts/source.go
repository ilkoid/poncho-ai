package stockproducts

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID re-exports the rate limiter key from pkg/wb for convenience.
const ToolID = wb.ToolIDStockProducts

// WBSource adapts *wb.Client to the StockProductsSource interface.
// Isolates rate limit parameters inside the source adapter.
// Delegates to typed client method (Rule 11: Client Method First, Wrapper Second).
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a StockProductsSource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// GetStockProducts fetches stock product metrics via WB Seller Analytics API v2.
// Rate limits are baked into WBSource at creation time.
func (s *WBSource) GetStockProducts(ctx context.Context, req wb.StockProductRequest) ([]wb.StockProductItem, error) {
	return s.client.GetStockProducts(ctx, req, s.rateLimit, s.burst)
}
