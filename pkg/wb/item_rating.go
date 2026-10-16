// Package wb provides Wildberries API client.
//
// This file contains the typed client method for the item rating endpoint:
// POST /api/analytics/v2/item-rating (Seller Analytics API, tag «Оценка товара»).
package wb

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// ToolIDItemRating is the ToolID for the item rating endpoint.
	// Must match the ToolID used in wbClient.SetRateLimit() calls.
	ToolIDItemRating = "item_rating"

	itemRatingURL  = "https://seller-analytics-api.wildberries.ru"
	itemRatingPath = "/api/analytics/v2/item-rating"
)

// ItemRatingRequest represents request to /api/analytics/v2/item-rating.
// Spec: docs/wb_api_swagger/11-analytics.yaml (ItemRatingRequest).
type ItemRatingRequest struct {
	CurrentPeriod PeriodRange  `json:"currentPeriod"`
	PastPeriod    *PeriodRange `json:"pastPeriod,omitempty"`
	NmIDs         []int        `json:"nmIds,omitempty"`      // max 50
	SubjectIDs    []int        `json:"subjectIds,omitempty"` // max 50
	BrandNames    []string     `json:"brandNames,omitempty"` // max 50
	TagIDs        []int        `json:"tagIds,omitempty"`     // max 50
	// IsNotIncludeNmsWithoutSales=false (default) returns ALL items —
	// including ones without sales in the period. Kept false: full catalog capture.
	IsNotIncludeNmsWithoutSales bool `json:"isNotIncludeNmsWithoutSales,omitempty"`
	// OnlyShadowedNms=false (default) returns all items, shadowed included.
	OnlyShadowedNms bool     `json:"onlyShadowedNms,omitempty"`
	OrderBy         OrderBy  `json:"orderBy"` // required by spec
	Limit           int      `json:"limit,omitempty"`
	Offset          int      `json:"offset"` // required by spec — NO omitempty (page 1 sends 0)
}

// ItemRatingResponse represents full API response: seller rating summary,
// account-wide feedback increase and per-item rating distribution.
type ItemRatingResponse struct {
	Data ItemRatingData `json:"data"`
}

// ItemRatingData holds the three response blocks.
type ItemRatingData struct {
	SellerRating     ItemSellerRating     `json:"sellerRating"`
	FeedbackIncrease ItemFeedbackIncrease `json:"feedbackIncrease"`
	Items            []ItemRatingItem     `json:"items"`
}

// ItemSellerRating — рейтинг продавца (TableItemFloat in spec).
// Raw keeps the exact JSON of the block as returned by WB (drift protection).
type ItemSellerRating struct {
	Current  float64 `json:"current"`  // текущий рейтинг
	Dynamics float64 `json:"dynamics"` // динамика vs прошлый период, %
	Raw      []byte  `json:"-"`
}

// ItemFeedbackIncrease — прирост оценок по всему ассортименту (FeedbacksIncreaseItem in spec).
type ItemFeedbackIncrease struct {
	Current  int `json:"current"`  // прирост оценок за период
	Total    int `json:"total"`    // всего оценок
	Dynamics int `json:"dynamics"` // динамика vs прошлый период, %
	// Per-star increase: {current, dynamics, total}
	FiveStar  StarDelta `json:"fiveStar"`
	FourStar  StarDelta `json:"fourStar"`
	ThreeStar StarDelta `json:"threeStar"`
	TwoStar   StarDelta `json:"twoStar"`
	OneStar   StarDelta `json:"oneStar"`
	Raw       []byte    `json:"-"`
}

// StarDelta — прирост оценок одного уровня звёзд за период.
// In items[] only {current, dynamics} are present; in feedbackIncrease {total} is added.
type StarDelta struct {
	Current  int `json:"current"`  // прирост за период
	Dynamics int `json:"dynamics"` // динамика vs прошлый период, %
	Total    int `json:"total"`    // всего оценок (только блок feedbackIncrease)
}

// ItemRatingItem — one product row (DistributionTableItem in spec).
type ItemRatingItem struct {
	NmID           int64  `json:"nmId"`
	Title          string `json:"title"`
	VendorCode     string `json:"vendorCode"`
	SubjectID      int64  `json:"subjectId"`
	SubjectName    string `json:"subjectName"`
	BrandName      string `json:"brandName"`
	TagName        string `json:"tagName"`
	TagID          int64  `json:"tagId"`
	PinnedFeedback bool   `json:"pinnedFeedback"`
	// Rating — «Рейтинг карточки товара»: internal WB card score, NOT the star rating
	// (spec gives no scale, example value 10). Star rating lives in FeedbackRating.
	Rating          float64              `json:"rating"`
	FeedbackRating  ItemRatingFeedback   `json:"feedbackRating"`
	FeedbackCount   ItemRatingCountDelta `json:"feedbackCount"`
	FiveStar        StarDelta            `json:"fiveStar"`
	FourStar        StarDelta            `json:"fourStar"`
	ThreeStar       StarDelta            `json:"threeStar"`
	TwoStar         StarDelta            `json:"twoStar"`
	OneStar         StarDelta            `json:"oneStar"`
	Disqualified    int                  `json:"disqualified"` // отзывы, исключённые WB из рейтинга
	IsShadowed      bool                 `json:"isShadowed"`   // товар скрыт из каталога
	Raw             []byte               `json:"-"`
}

// ItemRatingFeedback — рейтинг товара по отзывам с перцентилем в предмете.
type ItemRatingFeedback struct {
	Current    float64  `json:"current"`
	Dynamics   float64  `json:"dynamics"`
	Percentile *float64 `json:"percentile"` // nullable: % товаров предмета с рейтингом ниже
}

// ItemRatingCountDelta — прирост количества отзывов.
type ItemRatingCountDelta struct {
	Current  int `json:"current"`  // прирост за период
	Dynamics int `json:"dynamics"` // динамика vs прошлый период, %
}

// UnmarshalJSON captures the exact block bytes before typed decoding,
// so fields added by WB between spec refreshes are not lost (stored in Raw).
func (b *ItemSellerRating) UnmarshalJSON(data []byte) error {
	type alias ItemSellerRating
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*b = ItemSellerRating(v)
	b.Raw = append([]byte(nil), data...)
	return nil
}

func (b *ItemFeedbackIncrease) UnmarshalJSON(data []byte) error {
	type alias ItemFeedbackIncrease
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*b = ItemFeedbackIncrease(v)
	b.Raw = append([]byte(nil), data...)
	return nil
}

func (i *ItemRatingItem) UnmarshalJSON(data []byte) error {
	type alias ItemRatingItem
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*i = ItemRatingItem(v)
	i.Raw = append([]byte(nil), data...)
	return nil
}

// GetItemRating fetches one page of the item rating report.
// Wraps POST /api/analytics/v2/item-rating with typed request/response.
//
// Rate: 3 req/min, interval 20 sec, burst 3 (per account; spec 11-analytics.yaml).
// Pagination: offset-based, max 1000 per page. Data refreshed hourly by WB.
// History: periods no earlier than 364 days back from yesterday.
func (c *Client) GetItemRating(
	ctx context.Context,
	req ItemRatingRequest,
	rateLimit, burst int,
) (*ItemRatingResponse, error) {
	if c.IsDemoKey() {
		return c.getMockItemRating(req), nil
	}
	var resp ItemRatingResponse
	err := c.Post(ctx, ToolIDItemRating, itemRatingURL, rateLimit, burst, itemRatingPath, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", itemRatingPath, err)
	}
	return &resp, nil
}

// getMockItemRating generates a deterministic mock response honoring pagination.
// Mock catalog size is 2500 items → 3 pages at PageSize 1000 (incl. a partial last page).
func (c *Client) getMockItemRating(req ItemRatingRequest) *ItemRatingResponse {
	const mockCatalog = 2500
	offset := req.Offset
	if offset >= mockCatalog {
		return &ItemRatingResponse{Data: ItemRatingData{Items: []ItemRatingItem{}}}
	}

	count := req.Limit
	if count <= 0 || offset+count > mockCatalog {
		count = mockCatalog - offset
	}

	items := make([]ItemRatingItem, 0, count)
	for i := 0; i < count; i++ {
		nmID := int64(162000000 + offset + i)
		pct := 50.0 + float64(i%50)
		items = append(items, ItemRatingItem{
			NmID:          nmID,
			Title:         fmt.Sprintf("Mock Item %d", nmID),
			VendorCode:    fmt.Sprintf("mock-%d", nmID),
			SubjectID:     100 + int64(i%7),
			SubjectName:   fmt.Sprintf("Subject %d", 100+i%7),
			BrandName:     "MockBrand",
			TagName:       "mock-tag",
			TagID:         3,
			Rating:        85.5,
			FeedbackRating: ItemRatingFeedback{
				Current:    4.5,
				Dynamics:   0.2,
				Percentile: &pct,
			},
			FeedbackCount: ItemRatingCountDelta{Current: 2 + i%5, Dynamics: 1},
			FiveStar:      StarDelta{Current: 1 + i%3, Dynamics: 1},
			FourStar:      StarDelta{Current: i % 2, Dynamics: 0},
			Disqualified:  i % 11,
			IsShadowed:    i%997 == 0, // rare shadowed item for coverage
		})
	}

	resp := &ItemRatingResponse{Data: ItemRatingData{
		SellerRating:     ItemSellerRating{Current: 4.62, Dynamics: 0.03},
		FeedbackIncrease: ItemFeedbackIncrease{Current: 26, Total: 116, Dynamics: 23, FiveStar: StarDelta{Current: 19, Dynamics: 17, Total: 51}},
		Items:            items,
	}}
	// Fill Raw blocks for parity with the real API path.
	resp.Data.SellerRating.Raw = []byte(`{"current":4.62,"dynamics":0.03}`)
	resp.Data.FeedbackIncrease.Raw = []byte(`{"current":26,"total":116,"dynamics":23}`)
	return resp
}
