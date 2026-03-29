// Package wb provides Stock Warehouse API methods.
package wb

import (
	"context"
	"fmt"
)

const (
	stocksBaseURL       = "https://seller-analytics-api.wildberries.ru"
	stocksWarehousePath = "/api/analytics/v1/stocks-report/wb-warehouses"

	// Stock History CSV Report endpoints
	stockHistoryReportsBaseURL = "https://seller-analytics-api.wildberries.ru"
	stockHistoryCreatePath     = "/api/v2/nm-report/downloads"
	stockHistoryListPath       = "/api/v2/nm-report/downloads"
	stockHistoryDownloadPath   = "/api/v2/nm-report/downloads/file/%s"
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

// ============================================================================
// Stock History CSV Report Methods (Async API)
// ============================================================================

// CreateStockHistoryReport creates a task to generate stock history CSV report.
// POST /api/v2/nm-report/downloads
//
// Rate limit: 3 req/min, burst 3 (swagger).
// Returns download ID for polling status.
func (c *Client) CreateStockHistoryReport(ctx context.Context, req StockHistoryReportRequest, rateLimit, burst int) (string, error) {
	var resp StockHistoryReportCreateResponse
	err := c.Post(ctx, "stock_history_create", stockHistoryReportsBaseURL, rateLimit, burst, stockHistoryCreatePath, req, &resp)
	if err != nil {
		return "", fmt.Errorf("create stock history report: %w", err)
	}

	return req.ID, nil
}

// ListStockHistoryReports fetches list of all reports with their status.
// GET /api/v2/nm-report/downloads
//
// Rate limit: 3 req/min, burst 3 (swagger).
// Used for polling report status.
func (c *Client) ListStockHistoryReports(ctx context.Context, rateLimit, burst int) ([]StockHistoryReportItem, error) {
	var resp StockHistoryReportListResponse
	err := c.Get(ctx, "stock_history_list", stockHistoryReportsBaseURL, rateLimit, burst, stockHistoryListPath, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("list stock history reports: %w", err)
	}

	return resp.Data, nil
}

// GetStockHistoryReportStatus fetches status of a specific report by ID.
// This is a convenience method that filters ListStockHistoryReports.
func (c *Client) GetStockHistoryReportStatus(ctx context.Context, downloadID string, rateLimit, burst int) (*StockHistoryReportItem, error) {
	reports, err := c.ListStockHistoryReports(ctx, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	for _, r := range reports {
		if r.ID == downloadID {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("report %s not found", downloadID)
}

// DownloadStockHistoryReport downloads the generated ZIP archive.
// GET /api/v2/nm-report/downloads/file/{downloadId}
//
// Rate limit: 3 req/min, burst 3 (swagger).
// Returns raw ZIP bytes (caller must extract CSV).
func (c *Client) DownloadStockHistoryReport(ctx context.Context, downloadID string, rateLimit, burst int) ([]byte, error) {
	path := fmt.Sprintf(stockHistoryDownloadPath, downloadID)
	return c.GetRaw(ctx, "stock_history_download", stockHistoryReportsBaseURL, rateLimit, burst, path, nil)
}
