package searchvis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the Source interface.
// Thin delegation: calls typed client methods, then converts WB API types
// into domain-specific row types (SearchPositionRow, SearchQueryRow).
//
// Rate limit wiring: rateLimit/burst are stored here and passed as parameters
// to client methods — matching the pattern of campaigns, stocks, and other domains.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a Source backed by the real WB Seller Analytics API.
// rateLimit and burst are passed to client methods — must be positive.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// FetchPositions downloads aggregated search position/visibility data.
// Delegates to client.GetSearchReport(), then converts the semi-structured
// response into typed SearchPositionRow per nmID.
func (s *WBSource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	pastStart, pastEnd := calculatePastPeriod(req.Begin, req.End)

	resp, err := s.client.GetSearchReport(ctx, req.NmIDs, req.Begin, req.End,
		pastStart, pastEnd, s.rateLimit, s.burst)
	if err != nil {
		return nil, err
	}

	return parsePositionResponse(resp.Data, req.NmIDs, req.Begin, req.End), nil
}

// FetchSearchTexts downloads top search queries per product.
// Delegates to client.GetSearchTexts(), then converts typed WB response items
// into domain-specific SearchQueryRow.
func (s *WBSource) FetchSearchTexts(ctx context.Context, req TextsRequest) ([]SearchQueryRow, error) {
	resp, err := s.client.GetSearchTexts(ctx, req.NmIDs, req.Begin, req.End,
		req.Limit, s.rateLimit, s.burst)
	if err != nil {
		return nil, err
	}

	snapshotDate := time.Now().Format("2006-01-02")
	rows := make([]SearchQueryRow, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
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
// Response parsing helpers (unexported)
// ============================================================================

// parsePositionResponse extracts position/visibility data from the report API response.
// The /report API returns aggregated data for all requested nmIDs.
// We create one row per nmID with the same aggregated values.
func parsePositionResponse(data map[string]any, nmIDs []int, periodStart, periodEnd string) []SearchPositionRow {
	if data == nil {
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
