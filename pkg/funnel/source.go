package funnel

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	sellerAnalyticsURL = "https://seller-analytics-api.wildberries.ru"
	funnelHistoryPath  = "/api/analytics/v3/sales-funnel/products/history"
	ToolID             = "get_wb_product_funnel_history"
	MaxBatchSize       = 20 // WB API enforces max 20 nmIds despite OpenAPI spec saying 1000
)

// apiProduct mirrors the camelCase JSON from WB Analytics API v3.
type apiProduct struct {
	NmID           int     `json:"nmId"`
	VendorCode     string  `json:"vendorCode"`
	Title          string  `json:"title"`
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
}

// apiHistoryRow mirrors one day of funnel metrics from the API.
type apiHistoryRow struct {
	Date                  string  `json:"date"`
	OpenCount             int     `json:"openCount"`
	CartCount             int     `json:"cartCount"`
	OrderCount            int     `json:"orderCount"`
	OrderSum              int     `json:"orderSum"`
	BuyoutCount           int     `json:"buyoutCount"`
	BuyoutSum             int     `json:"buyoutSum"`
	BuyoutPercent         float64 `json:"buyoutPercent"`
	AddToCartConversion   float64 `json:"addToCartConversion"`
	CartToOrderConversion float64 `json:"cartToOrderConversion"`
	AddToWishlistCount    int     `json:"addToWishlistCount"`
}

// apiResponse mirrors the full API response: array of product+history.
type apiResponse []struct {
	Product   apiProduct     `json:"product"`
	History   []apiHistoryRow `json:"history"`
	Currency  string         `json:"currency"`
}

// WBSource adapts *wb.Client to the FunnelSource interface.
// Isolates JSON parsing (camelCase → domain types) inside pkg/funnel/.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a FunnelSource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// LoadBatch fetches funnel history for a batch of nmIDs.
func (s *WBSource) LoadBatch(ctx context.Context, nmIDs []int, from, to string) ([]BatchResult, error) {
	reqBody := map[string]interface{}{
		"nmIds": nmIDs,
		"selectedPeriod": map[string]string{
			"start": from,
			"end":   to,
		},
	}

	var resp apiResponse
	err := s.client.Post(ctx, ToolID, sellerAnalyticsURL, s.rateLimit, s.burst, funnelHistoryPath, reqBody, &resp)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", funnelHistoryPath, err)
	}

	results := make([]BatchResult, 0, len(resp))
	for _, item := range resp {
		meta := wb.FunnelProductMeta{
			NmID:           item.Product.NmID,
			VendorCode:     item.Product.VendorCode,
			Title:          item.Product.Title,
			BrandName:      item.Product.BrandName,
			SubjectID:      item.Product.SubjectID,
			SubjectName:    item.Product.SubjectName,
			ProductRating:  item.Product.ProductRating,
			FeedbackRating: item.Product.FeedbackRating,
			StockWB:        item.Product.Stocks.WB,
			StockMP:        item.Product.Stocks.MP,
			StockBalance:   item.Product.Stocks.BalanceSum,
		}

		rows := make([]wb.FunnelHistoryRow, 0, len(item.History))
		for _, h := range item.History {
			rows = append(rows, wb.FunnelHistoryRow{
				NmID:                  item.Product.NmID,
				MetricDate:            h.Date,
				OpenCount:             h.OpenCount,
				CartCount:             h.CartCount,
				OrderCount:            h.OrderCount,
				BuyoutCount:           h.BuyoutCount,
				AddToWishlist:         h.AddToWishlistCount,
				OrderSum:              h.OrderSum,
				BuyoutSum:             h.BuyoutSum,
				ConversionAddToCart:   h.AddToCartConversion,
				ConversionCartToOrder: h.CartToOrderConversion,
				ConversionBuyout:      h.BuyoutPercent,
			})
		}

		results = append(results, BatchResult{Product: meta, Rows: rows})
	}

	return results, nil
}
