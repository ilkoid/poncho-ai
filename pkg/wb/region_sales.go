package wb

import (
	"context"
	"fmt"
	"net/url"
)

const (
	regionSaleBaseURL = "https://seller-analytics-api.wildberries.ru"
	regionSalePath    = "/api/v1/analytics/region-sale"
)

// GetRegionSales fetches regional sales data from WB Seller Analytics API.
// GET /api/v1/analytics/region-sale?dateFrom=YYYY-MM-DD&dateTo=YYYY-MM-DD
//
// Returns all data for the requested period (no pagination).
// Max period: 31 days per request.
// Rate limit: 1 req/10sec (6 req/min), burst 5 (swagger).
func (c *Client) GetRegionSales(ctx context.Context, dateFrom, dateTo string, rateLimit, burst int) ([]RegionSaleItem, error) {
	params := url.Values{}
	params.Set("dateFrom", dateFrom)
	params.Set("dateTo", dateTo)

	var resp RegionSaleResponse
	err := c.Get(ctx, "get_region_sale", regionSaleBaseURL, rateLimit, burst, regionSalePath, params, &resp)
	if err != nil {
		return nil, fmt.Errorf("region sale %s to %s: %w", dateFrom, dateTo, err)
	}

	return resp.Report, nil
}
