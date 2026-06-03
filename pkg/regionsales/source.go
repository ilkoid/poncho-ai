package regionsales

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID matches the rate limiter key used in wb.Client.GetRegionSales().
const ToolID = "get_region_sale"

// WBSource adapts *wb.Client to the RegionSalesSource interface.
// Isolates rate limit parameters inside the source adapter.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a RegionSalesSource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// GetRegionSales fetches regional sales data via WB Seller Analytics API.
// Delegates to wb.Client which handles rate limiting, retries, and 429 backoff.
func (s *WBSource) GetRegionSales(ctx context.Context, dateFrom, dateTo string) ([]wb.RegionSaleItem, error) {
	return s.client.GetRegionSales(ctx, dateFrom, dateTo, s.rateLimit, s.burst)
}
