package wb

import (
	"context"
	"fmt"
)

// ToolID constants for SetRateLimit — search-report endpoints.
// Both endpoints share a global 3 req/min rate limit on Seller Analytics API.
// CLI must call wbClient.SetRateLimit() for ToolIDSearchReport,
// then ShareRateLimit for ToolIDSearchTexts.
const (
	ToolIDSearchReport = "search_report" // POST /api/v2/search-report/report
	ToolIDSearchTexts  = "search_texts"  // POST /api/v2/search-report/product/search-texts
)

const (
	// sellerAnalyticsBaseURL is the base URL for WB Seller Analytics API.
	sellerAnalyticsBaseURL = "https://seller-analytics-api.wildberries.ru"

	searchReportPath     = "/api/v2/search-report/report"
	searchTextsPath      = "/api/v2/search-report/product/search-texts"
)

// SearchReportResponse is the response from POST /api/v2/search-report/report.
// The /report API returns aggregated position/visibility data for all requested nmIDs.
// Fields inside Data are semi-structured nested maps (positionInfo, visibilityInfo)
// with 3+ levels of nesting and dynamics — typed structs are impractical.
type SearchReportResponse struct {
	Data map[string]any `json:"data"`
}

// SearchTextsResponse is the response from POST /api/v2/search-report/product/search-texts.
type SearchTextsResponse struct {
	Data SearchTextsData `json:"data"`
}

// SearchTextsData wraps the list of search text items.
type SearchTextsData struct {
	Items []SearchTextItem `json:"items"`
}

// SearchTextItem represents one search query entry for a product.
type SearchTextItem struct {
	Text           string         `json:"text"`
	NmID           int            `json:"nmId"`
	VendorCode     string         `json:"vendorCode"`
	BrandName      string         `json:"brandName"`
	SubjectName    string         `json:"subjectName"`
	WeekFrequency  int            `json:"weekFrequency"`
	Frequency      map[string]any `json:"frequency"`
	AvgPosition    map[string]any `json:"avgPosition"`
	MedianPosition map[string]any `json:"medianPosition"`
	OpenCard       map[string]any `json:"openCard"`
	AddToCart      map[string]any `json:"addToCart"`
	Orders         map[string]any `json:"orders"`
	OpenToCart     map[string]any `json:"openToCart"`
	CartToOrder    map[string]any `json:"cartToOrder"`
	Visibility     map[string]any `json:"visibility"`
}

// GetSearchReport fetches aggregated search position/visibility data.
// POST /api/v2/search-report/report
//
// The API returns ONE aggregated result for the entire batch of nmIDs.
// The response.Data contains nested positionInfo and visibilityInfo maps
// that the caller parses into domain-specific types.
//
// Rate limit: 3 req/min shared across all search-report endpoints.
func (c *Client) GetSearchReport(ctx context.Context, nmIDs []int, begin, end, pastBegin, pastEnd string, rateLimit, burst int) (*SearchReportResponse, error) {
	if c.IsDemoKey() {
		return c.getMockSearchReport(nmIDs), nil
	}

	reqBody := map[string]any{
		"nmIds": nmIDs,
		"currentPeriod": map[string]string{
			"start": begin,
			"end":   end,
		},
		"pastPeriod": map[string]string{
			"start": pastBegin,
			"end":   pastEnd,
		},
		"orderBy": map[string]string{
			"field": "orders",
			"mode":  "desc",
		},
		"positionCluster":        "all",
		"includeSubstitutedSKUs": true,
		"includeSearchTexts":     false,
		"limit":                  100,
		"offset":                 0,
	}

	var resp SearchReportResponse
	err := c.Post(ctx, ToolIDSearchReport, sellerAnalyticsBaseURL,
		rateLimit, burst, searchReportPath, reqBody, &resp)
	if err != nil {
		return nil, fmt.Errorf("search report: %w", err)
	}

	return &resp, nil
}

// GetSearchTexts fetches top search queries per product.
// POST /api/v2/search-report/product/search-texts
//
// Returns typed response with per-product search query data including
// frequency, positions, visibility, and conversion metrics.
//
// Rate limit: 3 req/min shared across all search-report endpoints.
func (c *Client) GetSearchTexts(ctx context.Context, nmIDs []int, begin, end string, queryLimit, rateLimit, burst int) (*SearchTextsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockSearchTexts(nmIDs), nil
	}

	reqBody := map[string]any{
		"nmIds": nmIDs,
		"currentPeriod": map[string]string{
			"start": begin,
			"end":   end,
		},
		"topOrderBy": "orders",
		"orderBy": map[string]string{
			"field": "orders",
			"mode":  "desc",
		},
		"limit": queryLimit,
	}

	var resp SearchTextsResponse
	err := c.Post(ctx, ToolIDSearchTexts, sellerAnalyticsBaseURL,
		rateLimit, burst, searchTextsPath, reqBody, &resp)
	if err != nil {
		return nil, fmt.Errorf("search texts: %w", err)
	}

	return &resp, nil
}

// getMockSearchReport returns a deterministic mock for --mock mode.
func (c *Client) getMockSearchReport(nmIDs []int) *SearchReportResponse {
	data := map[string]any{
		"positionInfo": map[string]any{
			"average": map[string]any{"current": 15.5, "dynamics": -2.3},
			"median":  map[string]any{"current": 12.0},
			"clusters": map[string]any{
				"firstHundred":  map[string]any{"current": float64(len(nmIDs) * 3 / 4)},
				"secondHundred": map[string]any{"current": float64(len(nmIDs) / 4)},
				"below":         map[string]any{"current": 0.0},
			},
		},
		"visibilityInfo": map[string]any{
			"visibility": map[string]any{"current": 45.2, "dynamics": 3.1},
			"openCard":   map[string]any{"current": float64(len(nmIDs) * 120), "dynamics": 5.0},
		},
	}
	return &SearchReportResponse{Data: data}
}

// getMockSearchTexts returns a deterministic mock for --mock mode.
func (c *Client) getMockSearchTexts(nmIDs []int) *SearchTextsResponse {
	items := make([]SearchTextItem, 0, len(nmIDs)*2)
	for _, nmID := range nmIDs {
		items = append(items,
			SearchTextItem{
				Text:          "кроссовки мужские",
				NmID:          nmID,
				VendorCode:    fmt.Sprintf("vendor-%d", nmID),
				BrandName:     "MockBrand",
				SubjectName:   "Кроссовки",
				WeekFrequency: 1500,
				Frequency:     map[string]any{"current": 250.0, "dynamics": 10.0},
				AvgPosition:   map[string]any{"current": 5.0, "dynamics": -1.0},
				MedianPosition: map[string]any{"current": 4.0, "dynamics": -1.0},
				Visibility:    map[string]any{"current": 80.0},
				OpenCard:      map[string]any{"current": 500.0},
				AddToCart:     map[string]any{"current": 120.0},
				Orders:        map[string]any{"current": 45.0},
				OpenToCart:    map[string]any{"current": 24.0},
				CartToOrder:   map[string]any{"current": 37.5},
			},
			SearchTextItem{
				Text:          "кроссовки летние",
				NmID:          nmID,
				VendorCode:    fmt.Sprintf("vendor-%d", nmID),
				BrandName:     "MockBrand",
				SubjectName:   "Кроссовки",
				WeekFrequency: 800,
				Frequency:     map[string]any{"current": 130.0, "dynamics": -5.0},
				AvgPosition:   map[string]any{"current": 12.0, "dynamics": 2.0},
				MedianPosition: map[string]any{"current": 10.0, "dynamics": 1.0},
				Visibility:    map[string]any{"current": 55.0},
				OpenCard:      map[string]any{"current": 200.0},
				AddToCart:     map[string]any{"current": 50.0},
				Orders:        map[string]any{"current": 15.0},
				OpenToCart:    map[string]any{"current": 25.0},
				CartToOrder:   map[string]any{"current": 30.0},
			},
		)
	}
	return &SearchTextsResponse{Data: SearchTextsData{Items: items}}
}
