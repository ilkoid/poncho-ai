package prices

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const ToolID = "get_prices"

// WBSource adapts *wb.Client to PricesSource interface.
// Same pattern as cards.WBSource — isolates rate limiting details from the downloader.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a PricesSource backed by the real WB Discounts-Prices API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// GetPrices fetches one page of product prices from WB Discounts-Prices API.
func (s *WBSource) GetPrices(ctx context.Context, limit, offset int) ([]wb.ProductPrice, int, error) {
	return s.client.GetPrices(ctx, limit, offset, s.rateLimit, s.burst)
}
