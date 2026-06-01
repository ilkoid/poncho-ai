package orders

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const ToolID = "wb_orders"

// WBSource adapts *wb.Client to OrdersSource interface.
// Same pattern as cards.WBSource — isolates rate limiting details from the downloader.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates an OrdersSource backed by the real WB Statistics API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// OrdersIterator iterates over all order pages from WB Statistics API.
func (s *WBSource) OrdersIterator(
	ctx context.Context,
	baseURL string,
	rateLimit, burst int,
	dateFrom string,
	callback func([]wb.OrdersItem) error,
) (int, error) {
	return s.client.OrdersIterator(ctx, baseURL, rateLimit, burst, dateFrom, callback)
}
