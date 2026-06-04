package searchvis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SellerAnalyticsURL is the base URL for WB Seller Analytics API.
const SellerAnalyticsURL = "https://seller-analytics-api.wildberries.ru"

// API paths for search-report endpoints.
const (
	ReportPath     = "/api/v2/search-report/report"
	SearchTextsPath = "/api/v2/search-report/product/search-texts"
)

// WBSource adapts *wb.Client to the Source interface.
// Unlike simpler domains (campaigns, stocks), WBSource does non-trivial response parsing:
//   - /report returns nested map[string]any → typed SearchPositionRow
//   - /search-texts returns typed JSON → SearchQueryRow
//
// The /report API returns ONE set of aggregated values for the entire batch of nmIDs.
// WBSource replicates those values across each nmID (same behavior as v1).
type WBSource struct {
	client *wb.Client
}

// NewWBSource creates a Source backed by the real WB Seller Analytics API.
func NewWBSource(client *wb.Client) *WBSource {
	return &WBSource{client: client}
}

// FetchPositions downloads aggregated search position/visibility data.
// Calls POST /api/v2/search-report/report and parses the nested response.
// Returns one SearchPositionRow per nmID (API returns single aggregated set).
func (s *WBSource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	pastStart, pastEnd := calculatePastPeriod(req.Begin, req.End)

	reqBody := map[string]any{
		"nmIds": req.NmIDs,
		"currentPeriod": map[string]string{
			"start": req.Begin,
			"end":   req.End,
		},
		"pastPeriod": map[string]string{
			"start": pastStart,
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

	var response map[string]any
	err := s.client.Post(ctx, ToolIDReport, SellerAnalyticsURL,
		0, 0, ReportPath, reqBody, &response)
	if err != nil {
		return nil, err
	}

	return parsePositionResponse(response, req.NmIDs, req.Begin, req.End), nil
}

// FetchSearchTexts downloads top search queries per product.
// Calls POST /api/v2/search-report/product/search-texts.
func (s *WBSource) FetchSearchTexts(ctx context.Context, req TextsRequest) ([]SearchQueryRow, error) {
	reqBody := map[string]any{
		"nmIds": req.NmIDs,
		"currentPeriod": map[string]string{
			"start": req.Begin,
			"end":   req.End,
		},
		"topOrderBy": "orders",
		"orderBy": map[string]string{
			"field": "orders",
			"mode":  "desc",
		},
		"limit": req.Limit,
	}

	var response struct {
		Data struct {
			Items []searchTextItem `json:"items"`
		} `json:"data"`
	}

	err := s.client.Post(ctx, ToolIDSearchTexts, SellerAnalyticsURL,
		0, 0, SearchTextsPath, reqBody, &response)
	if err != nil {
		return nil, err
	}

	snapshotDate := time.Now().Format("2006-01-02")
	rows := make([]SearchQueryRow, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		rows = append(rows, SearchQueryRow{
			NmID:                item.NmID,
			SnapshotDate:        snapshotDate,
			SearchText:          item.Text,
			Frequency:           getInt(item.Frequency, "current"),
			FrequencyDynamics:   getFloat(item.Frequency, "dynamics"),
			WeekFrequency:       item.WeekFrequency,
			AvgPosition:         getFloat(item.AvgPosition, "current"),
			AvgPositionDynamics: getFloat(item.AvgPosition, "dynamics"),
			MedianPosition:      getFloat(item.MedianPosition, "current"),
			MedianPosDynamics:   getFloat(item.MedianPosition, "dynamics"),
			Visibility:          getFloat(item.Visibility, "current"),
			OpenCard:            getInt(item.OpenCard, "current"),
			AddToCart:           getInt(item.AddToCart, "current"),
			Orders:              getInt(item.Orders, "current"),
			OpenToCart:          getFloat(item.OpenToCart, "current"),
			CartToOrder:         getFloat(item.CartToOrder, "current"),
			VendorCode:          item.VendorCode,
			BrandName:           item.BrandName,
			SubjectName:         item.SubjectName,
			PeriodStart:         req.Begin,
			PeriodEnd:           req.End,
		})
	}

	return rows, nil
}

// ============================================================================
// Response parsing types and helpers (unexported)
// ============================================================================

// searchTextItem matches the JSON structure of /search-texts response items.
type searchTextItem struct {
	Text           string                 `json:"text"`
	NmID           int                    `json:"nmId"`
	VendorCode     string                 `json:"vendorCode"`
	BrandName      string                 `json:"brandName"`
	SubjectName    string                 `json:"subjectName"`
	WeekFrequency  int                    `json:"weekFrequency"`
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

// parsePositionResponse extracts position/visibility data from the report API response.
// The /report API returns aggregated data for all requested nmIDs.
// We create one row per nmID with the same aggregated values.
func parsePositionResponse(resp map[string]any, nmIDs []int, periodStart, periodEnd string) []SearchPositionRow {
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return nil
	}

	posInfo, _ := data["positionInfo"].(map[string]any)
	visInfo, _ := data["visibilityInfo"].(map[string]any)

	avgPos := getFloatFromNested(posInfo, "average", "current")
	avgPosDyn := getFloatFromNested(posInfo, "average", "dynamics")
	medianPos := getFloatFromNested(posInfo, "median", "current")

	visibility := getFloatFromNested(visInfo, "visibility", "current")
	visDyn := getFloatFromNested(visInfo, "visibility", "dynamics")
	openCard := getIntFromNested(visInfo, "openCard", "current")
	openCardDyn := getFloatFromNested(visInfo, "openCard", "dynamics")

	clusters, _ := posInfo["clusters"].(map[string]any)
	clusterFirst := getIntFromNested(clusters, "firstHundred", "current")
	clusterSecond := getIntFromNested(clusters, "secondHundred", "current")
	clusterBelow := getIntFromNested(clusters, "below", "current")

	snapshotDate := time.Now().Format("2006-01-02")
	rows := make([]SearchPositionRow, 0, len(nmIDs))
	for _, nmID := range nmIDs {
		rows = append(rows, SearchPositionRow{
			NmID:                 nmID,
			SnapshotDate:         snapshotDate,
			AvgPosition:          avgPos,
			AvgPositionDynamics:  avgPosDyn,
			MedianPosition:       medianPos,
			Visibility:           visibility,
			VisibilityDynamics:   visDyn,
			OpenCard:             openCard,
			OpenCardDynamics:     openCardDyn,
			ClusterFirstHundred:  clusterFirst,
			ClusterSecondHundred: clusterSecond,
			ClusterBelow:         clusterBelow,
			PeriodStart:          periodStart,
			PeriodEnd:            periodEnd,
		})
	}

	return rows
}

// calculatePastPeriod returns the equivalent previous period for dynamics comparison.
// If current is Jan 15 → Jan 21 (7 days), past is Jan 8 → Jan 14.
func calculatePastPeriod(begin, end string) (string, string) {
	b, err := time.Parse("2006-01-02", begin)
	if err != nil {
		return begin, end
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return begin, end
	}
	days := int(e.Sub(b).Hours() / 24)
	pastEnd := b.AddDate(0, 0, -1)
	pastBegin := pastEnd.AddDate(0, 0, -days)
	return pastBegin.Format("2006-01-02"), pastEnd.Format("2006-01-02")
}

// getFloat extracts a float64 from a nested map by key.
func getFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

// getInt extracts an int from a nested map by key.
func getInt(m map[string]any, key string) int {
	return int(getFloat(m, key))
}

// getFloatFromNested traverses nested maps to extract a float64 value.
func getFloatFromNested(m map[string]any, keys ...string) float64 {
	for i := 0; i < len(keys)-1; i++ {
		if m == nil {
			return 0
		}
		v, ok := m[keys[i]]
		if !ok {
			return 0
		}
		m, ok = v.(map[string]any)
		if !ok {
			return 0
		}
	}
	return getFloat(m, keys[len(keys)-1])
}

// getIntFromNested traverses nested maps to extract an int value.
func getIntFromNested(m map[string]any, keys ...string) int {
	return int(getFloatFromNested(m, keys...))
}
