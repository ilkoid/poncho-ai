package penalties

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to PenaltiesSource interface.
// Stores rate limit params from config, passes them to each client call.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a PenaltiesSource backed by the real WB Seller Analytics API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// MeasurementPenaltiesIterator delegates to wb.Client.MeasurementPenaltiesIterator.
func (s *WBSource) MeasurementPenaltiesIterator(
	ctx context.Context,
	dateFrom, dateTo string,
	rateLimit, burst int,
	callback func([]wb.MeasurementPenaltyItem, int) error,
) (int, error) {
	return s.client.MeasurementPenaltiesIterator(ctx, dateFrom, dateTo, rateLimit, burst, callback)
}
