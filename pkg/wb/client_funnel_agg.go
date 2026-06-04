// Package wb provides Wildberries API client.
//
// This file contains the typed client method for the aggregated funnel endpoint:
// POST /api/analytics/v3/sales-funnel/products (Seller Analytics API v3).
package wb

import (
	"context"
	"fmt"
)

const (
	// ToolIDFunnelAggregated is the ToolID for the aggregated funnel endpoint.
	// Must match the ToolID used in wbClient.SetRateLimit() calls.
	ToolIDFunnelAggregated = "get_wb_funnel_aggregated"

	funnelAggregatedURL   = "https://seller-analytics-api.wildberries.ru"
	funnelAggregatedPath  = "/api/analytics/v3/sales-funnel/products"
)

// GetFunnelAggregated fetches aggregated funnel metrics for products.
// Wraps POST /api/analytics/v3/sales-funnel/products with typed request/response.
//
// Rate: 3 req/min (shared with other Seller Analytics endpoints).
// Pagination: offset-based, max 1000 per page.
func (c *Client) GetFunnelAggregated(
	ctx context.Context,
	req FunnelAggregatedRequest,
	rateLimit, burst int,
) (*FunnelAggregatedResponse, error) {
	var resp FunnelAggregatedResponse
	err := c.Post(ctx, ToolIDFunnelAggregated, funnelAggregatedURL, rateLimit, burst,
		funnelAggregatedPath, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", funnelAggregatedPath, err)
	}
	return &resp, nil
}
