// Package wb provides service layer implementations for Wildberries API.
//
// This file contains the SalesService implementation with business logic
// for sales funnel analytics, history, and search positions.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// Ensure salesService implements SalesService.
var _ SalesService = (*salesService)(nil)

// salesService implements SalesService using the WB Client.
type salesService struct {
	client *Client
}

// GetFunnelMetrics retrieves funnel metrics for products.
//
// Uses Analytics API v3: POST /api/analytics/v3/sales-funnel/products
// Returns aggregated data including views, cart additions, orders, buyouts,
// cancellations, wishlist, WB Club metrics, stocks, and ratings.
//
// Parameters:
//   - req.NmIDs: List of product IDs (max 100)
//   - req.Period: Period in days (1-7 free, up to 365 with subscription)
func (s *salesService) GetFunnelMetrics(ctx context.Context, req FunnelRequest) (*FunnelMetrics, error) {
	// Validation
	if len(req.NmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(req.NmIDs) > 100 {
		return nil, fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if req.Period < 1 || req.Period > 365 {
		return nil, fmt.Errorf("period must be between 1 and 365")
	}

	// Mock mode for demo key
	if s.client.IsDemoKey() {
		return s.getMockFunnelMetrics(req.NmIDs, req.Period)
	}

	// Calculate period dates
	now := time.Now()
	begin := now.AddDate(0, 0, -req.Period)

	// Build request body
	reqBody := map[string]interface{}{
		"nmIds": req.NmIDs,
		"selectedPeriod": map[string]string{
			"start": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
	}

	// API response structure
	var response struct {
		Data struct {
			Products []struct {
				Product struct {
					NmID           int     `json:"nmId"`
					Title          string  `json:"title"`
					VendorCode     string  `json:"vendorCode"`
					BrandName      string  `json:"brandName"`
					SubjectID      int     `json:"subjectId"`
					SubjectName    string  `json:"subjectName"`
					ProductRating  float64 `json:"productRating"`
					FeedbackRating float64 `json:"feedbackRating"`
					Stocks         struct {
						WB         int `json:"wb"`
						MP         int `json:"mp"`
						BalanceSum int `json:"balanceSum"`
					} `json:"stocks"`
				} `json:"product"`
				Statistic struct {
					Selected struct {
						Period struct {
							Start string `json:"start"`
							End   string `json:"end"`
						} `json:"period"`
						OpenCount        int64   `json:"openCount"`
						CartCount        int64   `json:"cartCount"`
						OrderCount       int64   `json:"orderCount"`
						OrderSum         int64   `json:"orderSum"`
						BuyoutCount      int64   `json:"buyoutCount"`
						BuyoutSum        int64   `json:"buyoutSum"`
						CancelCount      int64   `json:"cancelCount"`
						CancelSum        int64   `json:"cancelSum"`
						AvgPrice         int     `json:"avgPrice"`
						AddToWishlist    int64   `json:"addToWishlist"`
						WBClub           struct {
							OrderCount    int64   `json:"orderCount"`
							BuyoutCount   int64   `json:"buyoutCount"`
							BuyoutPercent float64 `json:"buyoutPercent"`
						} `json:"wbClub"`
						Conversions struct {
							AddToCartPercent   float64 `json:"addToCartPercent"`
							CartToOrderPercent float64 `json:"cartToOrderPercent"`
							BuyoutPercent      float64 `json:"buyoutPercent"`
						} `json:"conversions"`
					} `json:"selected"`
				} `json:"statistic"`
			} `json:"products"`
			Currency string `json:"currency"`
		} `json:"data"`
	}

	// Call API
	err := s.client.Post(ctx, "get_wb_product_funnel2",
		"https://seller-analytics-api.wildberries.ru", 3, 3,
		"/api/analytics/v3/sales-funnel/products", reqBody, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get funnel metrics: %w", err)
	}

	// Transform response
	metrics := &FunnelMetrics{
		Products:  make(map[int]*ProductFunnelData),
		Period:    req.Period,
		Timestamp: time.Now(),
	}

	for _, p := range response.Data.Products {
		metrics.Products[p.Product.NmID] = &ProductFunnelData{
			NmID:           p.Product.NmID,
			OpenCount:      p.Statistic.Selected.OpenCount,
			CartCount:      p.Statistic.Selected.CartCount,
			OrderCount:     p.Statistic.Selected.OrderCount,
			BuyoutCount:    p.Statistic.Selected.BuyoutCount,
			CancelCount:    p.Statistic.Selected.CancelCount,
			OrderSum:       p.Statistic.Selected.OrderSum,
			BuyoutSum:      p.Statistic.Selected.BuyoutSum,
			ConversionRate: p.Statistic.Selected.Conversions.BuyoutPercent,
			WBClubPercent:  p.Statistic.Selected.WBClub.BuyoutPercent,
		}
	}

	return metrics, nil
}

// getMockFunnelMetrics returns mock data for demo mode.
func (s *salesService) getMockFunnelMetrics(nmIDs []int, period int) (*FunnelMetrics, error) {
	metrics := &FunnelMetrics{
		Products:  make(map[int]*ProductFunnelData),
		Period:    period,
		Timestamp: time.Now(),
	}

	for _, nmID := range nmIDs {
		openCount := int64(1000 + (nmID % 500))
		cartCount := openCount / 10
		orderCount := cartCount / 3
		buyoutCount := int64(float64(orderCount) * 0.85)

		metrics.Products[nmID] = &ProductFunnelData{
			NmID:           nmID,
			OpenCount:      openCount,
			CartCount:      cartCount,
			OrderCount:     orderCount,
			BuyoutCount:    buyoutCount,
			CancelCount:    orderCount - buyoutCount,
			OrderSum:       orderCount * 1500,
			BuyoutSum:      buyoutCount * 1500,
			ConversionRate: 85.0,
			WBClubPercent:  45.0,
		}
	}

	return metrics, nil
}

// GetFunnelHistory retrieves historical funnel metrics.
//
// Uses Analytics API v3: POST /api/analytics/v3/sales-funnel/products/history
// Returns daily metrics for the specified period (1-7 days free, up to 365 with subscription).
func (s *salesService) GetFunnelHistory(ctx context.Context, req FunnelHistoryRequest) (*FunnelHistory, error) {
	// Validation
	if len(req.NmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(req.NmIDs) > 100 {
		return nil, fmt.Errorf("nmIDs cannot exceed 100 items")
	}

	// Mock mode for demo key
	if s.client.IsDemoKey() {
		return s.getMockFunnelHistory(req.NmIDs, req.DateFrom, req.DateTo)
	}

	// Build request body
	reqBody := map[string]interface{}{
		"nmIds": req.NmIDs,
		"selectedPeriod": map[string]string{
			"start": req.DateFrom.Format("2006-01-02"),
			"end":   req.DateTo.Format("2006-01-02"),
		},
	}

	// API response structure
	var response struct {
		Data struct {
			Products []struct {
				Product struct {
					NmID       int    `json:"nmId"`
					Title      string `json:"title"`
					VendorCode string `json:"vendorCode"`
				} `json:"product"`
				Statistic struct {
					History []struct {
						Date       string `json:"date"`
						OpenCount  int64  `json:"openCount"`
						CartCount  int64  `json:"cartCount"`
						OrderCount int64  `json:"orderCount"`
						BuyoutCount int64 `json:"buyoutCount"`
					} `json:"history"`
				} `json:"statistic"`
			} `json:"products"`
		} `json:"data"`
	}

	// Call API
	err := s.client.Post(ctx, "get_wb_product_funnel_history2",
		"https://seller-analytics-api.wildberries.ru", 3, 3,
		"/api/analytics/v3/sales-funnel/products/history", reqBody, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get funnel history: %w", err)
	}

	// Transform response
	history := &FunnelHistory{
		DailyMetrics: make(map[time.Time]map[int]*ProductFunnelData),
		StartDate:    req.DateFrom,
		EndDate:      req.DateTo,
	}

	for _, p := range response.Data.Products {
		for _, h := range p.Statistic.History {
			date, err := time.Parse("2006-01-02", h.Date)
			if err != nil {
				continue
			}

			if history.DailyMetrics[date] == nil {
				history.DailyMetrics[date] = make(map[int]*ProductFunnelData)
			}

			history.DailyMetrics[date][p.Product.NmID] = &ProductFunnelData{
				NmID:        p.Product.NmID,
				OpenCount:   h.OpenCount,
				CartCount:   h.CartCount,
				OrderCount:  h.OrderCount,
				BuyoutCount: h.BuyoutCount,
			}
		}
	}

	return history, nil
}

// getMockFunnelHistory returns mock history data for demo mode.
func (s *salesService) getMockFunnelHistory(nmIDs []int, dateFrom, dateTo time.Time) (*FunnelHistory, error) {
	history := &FunnelHistory{
		DailyMetrics: make(map[time.Time]map[int]*ProductFunnelData),
		StartDate:    dateFrom,
		EndDate:      dateTo,
	}

	// Generate mock data for each day
	for d := dateFrom; !d.After(dateTo); d = d.AddDate(0, 0, 1) {
		history.DailyMetrics[d] = make(map[int]*ProductFunnelData)

		for _, nmID := range nmIDs {
			openCount := int64(100 + (nmID % 50))
			orderCount := openCount / 10

			history.DailyMetrics[d][nmID] = &ProductFunnelData{
				NmID:        nmID,
				OpenCount:   openCount,
				CartCount:   openCount / 5,
				OrderCount:  orderCount,
				BuyoutCount: int64(float64(orderCount) * 0.85),
			}
		}
	}

	return history, nil
}

// GetSalesReport downloads sales data for a period.
// Handles pagination and resume logic internally.
func (s *salesService) GetSalesReport(ctx context.Context, req SalesReportRequest) (*SalesReport, error) {
	// TODO: Implement with ReportDetailByPeriodIterator
	return &SalesReport{}, nil
}

// GetSearchPositions retrieves search positions for products.
//
// Uses Analytics API v3: POST /api/analytics/v3/search/positions
func (s *salesService) GetSearchPositions(ctx context.Context, nmIDs []int, period int) (string, error) {
	// Validation
	if len(nmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if period < 1 || period > 365 {
		return "", fmt.Errorf("period must be between 1 and 365")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockSearchPositions(nmIDs, period)
	}

	// Build request
	now := time.Now()
	begin := now.AddDate(0, 0, -period)

	reqBody := map[string]interface{}{
		"nmIds": nmIDs,
		"period": map[string]string{
			"start": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
	}

	var response json.RawMessage
	err := s.client.Post(ctx, "get_wb_search_positions2",
		"https://seller-analytics-api.wildberries.ru", 3, 3,
		"/api/analytics/v3/search/positions", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get search positions: %w", err)
	}

	return string(response), nil
}

// getMockSearchPositions returns mock search positions for demo mode.
func (s *salesService) getMockSearchPositions(nmIDs []int, period int) (string, error) {
	results := make([]map[string]interface{}, 0, len(nmIDs))

	for _, nmID := range nmIDs {
		results = append(results, map[string]interface{}{
			"nmId":            nmID,
			"avgPosition":     15 + (nmID % 20),
			"visibility":      75.0 + float64(nmID%10),
			"top100Queries":   25 + (nmID % 10),
			"totalQueries":    100 + (nmID % 50),
			"period":          period,
			"mock":            true,
		})
	}

	data, _ := json.Marshal(results)
	return string(data), nil
}

// GetTopSearchQueries retrieves top search queries for a product.
func (s *salesService) GetTopSearchQueries(ctx context.Context, nmID int, period int) (string, error) {
	// Validation
	if nmID <= 0 {
		return "", fmt.Errorf("nmID must be positive")
	}
	if period < 1 || period > 365 {
		return "", fmt.Errorf("period must be between 1 and 365")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockTopSearchQueries(nmID, period)
	}

	// Build request
	now := time.Now()
	begin := now.AddDate(0, 0, -period)

	params := url.Values{}
	params.Set("nmId", fmt.Sprintf("%d", nmID))
	params.Set("start", begin.Format("2006-01-02"))
	params.Set("end", now.Format("2006-01-02"))

	var response json.RawMessage
	err := s.client.Get(ctx, "get_wb_top_search_queries2",
		"https://seller-analytics-api.wildberries.ru", 3, 3,
		"/api/analytics/v3/search/queries", params, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get top search queries: %w", err)
	}

	return string(response), nil
}

// getMockTopSearchQueries returns mock search queries for demo mode.
func (s *salesService) getMockTopSearchQueries(nmID int, period int) (string, error) {
	queries := []map[string]interface{}{
		{"query": "платье женское", "orders": 50 + nmID%20, "views": 500 + nmID%100},
		{"query": "вечернее платье", "orders": 30 + nmID%15, "views": 350 + nmID%80},
		{"query": "платье черное", "orders": 25 + nmID%10, "views": 280 + nmID%60},
		{"query": "платье длинное", "orders": 20 + nmID%8, "views": 220 + nmID%50},
		{"query": "платье летнее", "orders": 15 + nmID%5, "views": 180 + nmID%40},
	}

	data, _ := json.Marshal(map[string]interface{}{
		"nmId":    nmID,
		"queries": queries,
		"period":  period,
		"mock":    true,
	})

	return string(data), nil
}
