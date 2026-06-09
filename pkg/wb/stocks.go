// Package wb provides Stock Warehouse API methods.
package wb

import (
	"context"
	"fmt"
)

const (
	stocksBaseURL       = "https://seller-analytics-api.wildberries.ru"
	stocksWarehousePath = "/api/analytics/v1/stocks-report/wb-warehouses"

	// Stock Products endpoint (Seller Analytics API v2)
	ToolIDStockProducts   = "stock_products"
	stockProductsPath     = "/api/v2/stocks-report/products/products"

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

// GetStockProducts fetches product-level stock metrics from WB Seller Analytics API v2.
// POST /api/v2/stocks-report/products/products
// Rate limit: 3 req/min, burst 3 (shared with other stocks-report/search-report endpoints).
func (c *Client) GetStockProducts(ctx context.Context, req StockProductRequest, rateLimit, burst int) ([]StockProductItem, error) {
	if c.IsDemoKey() {
		return c.getMockStockProducts(req), nil
	}
	var resp StockProductResponse
	err := c.Post(ctx, ToolIDStockProducts, sellerAnalyticsBaseURL,
		rateLimit, burst, stockProductsPath, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("stock products: %w", err)
	}
	if resp.Error {
		return nil, fmt.Errorf("stock products API error: %s", resp.ErrorText)
	}
	return resp.Data.Items, nil
}

// getMockStockProducts returns deterministic mock data for --mock mode.
func (c *Client) getMockStockProducts(req StockProductRequest) []StockProductItem {
	count := min(req.Limit, 5)
	items := make([]StockProductItem, count)
	for i := range count {
		items[i] = StockProductItem{
			NmID:        int64(100000 + i + req.Offset),
			IsDeleted:   false,
			SubjectName: "Кроссовки",
			Name:        fmt.Sprintf("Mock Product %d", i+req.Offset),
			VendorCode:  fmt.Sprintf("12%d", i+req.Offset),
			BrandName:   "MockBrand",
			MainPhoto:   "https://mock.photo/1.jpg",
			HasSizes:    true,
			Metrics: StockProductMetrics{
				OrdersCount:     int64(100 + i*10),
				OrdersSum:       int64(150000 + i*1000),
				BuyoutCount:     int64(85 + i*5),
				BuyoutSum:       int64(120000 + i*500),
				BuyoutPercent:   85,
				StockCount:      int64(50 + i*3),
				StockSum:        int64(75000 + i*200),
				ToClientCount:   int64(10 + i),
				FromClientCount: int64(5 + i),
				AvgOrders:       float64(3 + i),
				SaleRate:        DurationMetrics{Days: 14, Hours: 5},
				AvgStockTurnover: DurationMetrics{Days: 30, Hours: 0},
				OfficeMissingTime: DurationMetrics{Days: -3, Hours: 0}, // -3 = not calculated
				LostOrdersCount:  float64(12 + i),
				LostOrdersSum:    float64(18000 + i*100),
				LostBuyoutsCount: float64(8 + i),
				LostBuyoutsSum:   float64(12000 + i*50),
				AvgOrdersByMonth: []FloatGraphByPeriodItem{
					{Start: "2026-05-01", End: "2026-05-31", Value: 2.5 + float64(i)},
				},
				CurrentPrice: PriceRange{MinPrice: int64(1500 + i*100), MaxPrice: int64(2500 + i*100)},
				Availability: "actual",
			},
		}
	}
	return items
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
