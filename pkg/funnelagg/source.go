package funnelagg

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the Source interface.
// Delegates to the typed GetFunnelAggregated method (Rule 11: Client Method First).
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a Source backed by the real WB Seller Analytics API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// LoadAggregatedPage fetches one page of aggregated funnel data via the WB API.
func (s *WBSource) LoadAggregatedPage(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	resp, err := s.client.GetFunnelAggregated(ctx, req, s.rateLimit, s.burst)
	if err != nil {
		return nil, "", fmt.Errorf("get funnel aggregated: %w", err)
	}
	return resp.Data.Products, resp.Data.Currency, nil
}
