// Package wb provides Stock Warehouse API methods.
package wb

import (
	"context"
	"fmt"
)

const (
	stocksBaseURL       = "https://seller-analytics-api.wildberries.ru"
	stocksWarehousePath = "/api/analytics/v1/stocks-report/wb-warehouses"
)

// GetStockWarehouses fetches warehouse-level stock data from WB Analytics API.
// POST /api/analytics/v1/stocks-report/wb-warehouses
// Request body: {"limit": N, "offset": 0}  (empty nmIds/chrtIds = ALL products)
// Rate limit: 3 req/min, burst 1 (swagger).
func (c *Client) GetStockWarehouses(ctx context.Context, limit, offset, rateLimit, burst int) ([]StockWarehouseItem, error) {
	body := map[string]interface{}{
		"limit":  limit,
		"offset": offset,
	}

	var resp StocksWarehouseAPIResponse
	err := c.Post(ctx, "get_stocks_warehouses", stocksBaseURL, rateLimit, burst, stocksWarehousePath, body, &resp)
	if err != nil {
		return nil, fmt.Errorf("stocks warehouses: %w", err)
	}

	if resp.Error {
		return nil, fmt.Errorf("API error: %s", resp.ErrorText)
	}

	return resp.Data.Items, nil
}
