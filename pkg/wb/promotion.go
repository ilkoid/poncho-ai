// Package wb provides Promotion API methods.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GetPromotionCount returns list of campaigns from /adv/v1/promotion/count.
// Despite the name, this endpoint returns full campaign list grouped by type+status.
func (c *Client) GetPromotionCount(ctx context.Context) (*PromotionCountResponse, error) {
	// Use demo mode if configured
	if c.IsDemoKey() {
		return c.getMockPromotionCount(), nil
	}

	// Build URL
	endpoint := "https://advert-api.wildberries.ru"
	path := "/adv/v1/promotion/count"

	var resp PromotionCountResponse
	err := c.Get(ctx, "get_promotion_count", endpoint, 100, 5, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("promotion count: %w", err)
	}

	return &resp, nil
}

// GetCampaignFullstats returns full campaign statistics from /adv/v3/fullstats.
// Returns the complete 4-level hierarchy: Campaign → Day → App → Nm.
// Max 50 campaign IDs per request, 3 req/min rate limit.
func (c *Client) GetCampaignFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]CampaignFullstatsResponse, error) {
	// Use demo mode if configured
	if c.IsDemoKey() {
		return c.getMockCampaignFullstats(advertIDs, beginDate, endDate), nil
	}

	if len(advertIDs) == 0 {
		return nil, nil
	}

	// Build URL with query parameters
	// GET /adv/v3/fullstats?ids=123,456&beginDate=2025-01-01&endDate=2025-01-07
	endpoint := "https://advert-api.wildberries.ru"

	idStrs := make([]string, len(advertIDs))
	for i, id := range advertIDs {
		idStrs[i] = fmt.Sprintf("%d", id)
	}

	params := url.Values{}
	params.Set("ids", strings.Join(idStrs, ","))
	params.Set("beginDate", beginDate)
	params.Set("endDate", endDate)

	path := "/adv/v3/fullstats?" + params.Encode()

	// Parse into canonical type with full hierarchy
	var response []CampaignFullstatsResponse
	err := c.Get(ctx, "get_campaign_fullstats", endpoint, 20, 1, path, nil, &response)
	if err != nil {
		return nil, fmt.Errorf("campaign fullstats: %w", err)
	}

	return response, nil
}

// Mock implementations for demo mode

func (c *Client) getMockPromotionCount() *PromotionCountResponse {
	return &PromotionCountResponse{
		Adverts: []PromotionAdvertGroup{
			{
				Type:   9,
				Status: 9,
				Count:  2,
				AdvertList: []PromotionAdvert{
					{AdvertID: 12345, ChangeTime: "2025-01-01T00:00:00Z"},
					{AdvertID: 67890, ChangeTime: "2025-01-15T00:00:00Z"},
				},
			},
		},
		All: 2,
	}
}

func (c *Client) getMockCampaignFullstats(advertIDs []int, beginDate, endDate string) []CampaignFullstatsResponse {
	results := make([]CampaignFullstatsResponse, 0, len(advertIDs))
	for i, id := range advertIDs {
		results = append(results, CampaignFullstatsResponse{
			AdvertID: id,
			Views:    1000 + i*100,
			Clicks:   50 + i*5,
			CTR:      5.0 + float64(i),
			CPC:      4.5 + float64(i%3),
			CR:       2.0 + float64(i%5),
			Orders:   5 + i,
			Shks:     4 + i,
			Atbs:     0,
			Canceled: 0,
			Sum:      250.0 + float64(i*10),
			SumPrice: 5000.0 + float64(i*100),
			Days: []CampaignFullstatsDay{
				{
					Date:     beginDate,
					Views:    500 + i*50,
					Clicks:   25 + i*3,
					CTR:      5.0 + float64(i)*0.1,
					CPC:      4.5,
					CR:       2.0,
					Orders:   3 + i,
					Shks:     2 + i,
					Sum:      125.0 + float64(i*5),
					SumPrice: 2500.0 + float64(i*50),
					Apps: []CampaignFullstatsApp{
						{AppType: 1, Views: 300 + i*30, Clicks: 15 + i*2, Orders: 2 + i, Sum: 75.0 + float64(i)*3, SumPrice: 1500.0 + float64(i)*30},
						{AppType: 32, Views: 150 + i*15, Clicks: 8 + i, Orders: 1 + i, Sum: 40.0 + float64(i)*2, SumPrice: 800.0 + float64(i)*20},
						{AppType: 64, Views: 50 + i*5, Clicks: 2 + i, Orders: 0, Sum: 10.0 + float64(i), SumPrice: 200.0 + float64(i)*10},
					},
				},
			},
		})
	}
	return results
}

// Get is a wrapper around the internal Get method for external use.
// This provides a simple way to call the WB API from tools.
func (c *Client) GetAPI(ctx context.Context, toolID, endpoint string, rateLimit, burst int, path string, params map[string]string, result any) error {
	// Convert map[string]string to url.Values
	values := make(url.Values)
	for k, v := range params {
		values.Set(k, v)
	}
	return c.Get(ctx, toolID, endpoint, rateLimit, burst, path, values, result)
}

// PostAPI is a wrapper around the internal Post method for external use.
func (c *Client) PostAPI(ctx context.Context, toolID, endpoint string, rateLimit, burst int, path string, body any, result any) error {
	return c.Post(ctx, toolID, endpoint, rateLimit, burst, path, body, result)
}

// ParseJSON is a helper for parsing JSON responses.
func ParseJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// IsHTTPError checks if error is an HTTP error with specific status.
func IsHTTPError(err error, status int) bool {
	if httpErr, ok := err.(*HTTPError); ok {
		return httpErr.StatusCode == status
	}
	// Also check for status in error message
	return strings.Contains(err.Error(), fmt.Sprintf("status %d", status))
}

// WrapHTTPError wraps HTTP response to error if status >= 400.
func WrapHTTPError(resp *http.Response, body []byte) error {
	if resp.StatusCode < 400 {
		return nil
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
