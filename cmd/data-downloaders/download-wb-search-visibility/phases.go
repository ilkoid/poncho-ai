package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

const batchSize = 100

const queryBatchSize = 50 // maxItems for /search-texts nmIds (swagger)

// SearchVisibilityClient is the interface for search visibility API operations.
type SearchVisibilityClient interface {
	Post(ctx context.Context, toolID, endpoint string, rateLimit, burst int,
		path string, body interface{}, result interface{}) error
}

// DownloadSearchPositions downloads aggregated search positions via /api/v2/search-report/report.
func DownloadSearchPositions(
	ctx context.Context,
	client SearchVisibilityClient,
	repo *sqlite.SQLiteSalesRepository,
	nmIDs []int,
	beginDate, endDate string,
	snapshotDate string,
	rateLimit, burst int,
) error {
	var allRows []sqlite.SearchPositionRow

	for i := 0; i < len(nmIDs); i += batchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := i + batchSize
		if end > len(nmIDs) {
			end = len(nmIDs)
		}
		batch := nmIDs[i:end]

		prog := formatProgress(i, len(nmIDs), batchSize, time.Now())

		pastStart, pastEnd := calculatePastPeriod(beginDate, endDate)

		reqBody := map[string]interface{}{
			"nmIds": batch,
			"currentPeriod": map[string]string{
				"start": beginDate,
				"end":   endDate,
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

		var response map[string]interface{}
		err := client.Post(ctx, "search_report", "https://seller-analytics-api.wildberries.ru",
			rateLimit, burst, "/api/v2/search-report/report", reqBody, &response)
		if err != nil {
			log.Printf("Warning: search-report batch %d-%d failed: %v", i, end, err)
			continue
		}

		rows := parsePositionResponse(response, batch, snapshotDate, beginDate, endDate)
		allRows = append(allRows, rows...)

		fmt.Printf("  Positions: %s — %d rows\n", prog, len(rows))
	}

	if len(allRows) > 0 {
		if err := repo.SaveSearchPositions(ctx, allRows); err != nil {
			return fmt.Errorf("save positions: %w", err)
		}
	}

	fmt.Printf("  Saved %d position snapshots\n", len(allRows))
	return nil
}

// DownloadSearchQueries downloads top search queries via /api/v2/search-report/product/search-texts.
func DownloadSearchQueries(
	ctx context.Context,
	client SearchVisibilityClient,
	repo *sqlite.SQLiteSalesRepository,
	nmIDs []int,
	beginDate, endDate string,
	snapshotDate string,
	limit int,
	rateLimit, burst int,
) error {
	var allRows []sqlite.SearchQueryRow
	startTime := time.Now()
	totalBatches := (len(nmIDs) + queryBatchSize - 1) / queryBatchSize

	for i := 0; i < len(nmIDs); i += queryBatchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := i + queryBatchSize
		if end > len(nmIDs) {
			end = len(nmIDs)
		}
		batch := nmIDs[i:end]

		batchNum := i/queryBatchSize + 1
		prog := formatProgress(batchNum-1, totalBatches, 1, startTime)

		reqBody := map[string]interface{}{
			"nmIds": batch,
			"currentPeriod": map[string]string{
				"start": beginDate,
				"end":   endDate,
			},
			"topOrderBy": "orders",
			"orderBy": map[string]string{
				"field": "orders",
				"mode":  "desc",
			},
			"limit": limit,
		}

		var response struct {
			Data struct {
				Items []searchTextItem `json:"items"`
			} `json:"data"`
		}

		err := client.Post(ctx, "search_texts", "https://seller-analytics-api.wildberries.ru",
			rateLimit, burst, "/api/v2/search-report/product/search-texts", reqBody, &response)
		if err != nil {
			log.Printf("Warning: search-texts batch %d-%d failed: %v", i, end, err)
			continue
		}

		for _, item := range response.Data.Items {
			allRows = append(allRows, sqlite.SearchQueryRow{
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
				PeriodStart:         beginDate,
				PeriodEnd:           endDate,
			})
		}

		fmt.Printf("  Queries: %s — %d total rows\n", prog, len(allRows))
	}

	if len(allRows) > 0 {
		if err := repo.SaveSearchQueries(ctx, allRows); err != nil {
			return fmt.Errorf("save queries: %w", err)
		}
	}

	fmt.Printf("  Saved %d query snapshots for %d products\n", len(allRows), len(nmIDs))
	return nil
}

// Response parsing types

type searchTextItem struct {
	Text           string                 `json:"text"`
	NmID           int                    `json:"nmId"`
	VendorCode     string                 `json:"vendorCode"`
	BrandName      string                 `json:"brandName"`
	SubjectName    string                 `json:"subjectName"`
	WeekFrequency  int                    `json:"weekFrequency"`
	Frequency      map[string]interface{} `json:"frequency"`
	AvgPosition    map[string]interface{} `json:"avgPosition"`
	MedianPosition map[string]interface{} `json:"medianPosition"`
	OpenCard       map[string]interface{} `json:"openCard"`
	AddToCart      map[string]interface{} `json:"addToCart"`
	Orders         map[string]interface{} `json:"orders"`
	OpenToCart     map[string]interface{} `json:"openToCart"`
	CartToOrder    map[string]interface{} `json:"cartToOrder"`
	Visibility     map[string]interface{} `json:"visibility"`
}

// parsePositionResponse extracts position/visibility data from the report API response.
func parsePositionResponse(resp map[string]interface{}, nmIDs []int, snapshotDate, periodStart, periodEnd string) []sqlite.SearchPositionRow {
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	posInfo, _ := data["positionInfo"].(map[string]interface{})
	visInfo, _ := data["visibilityInfo"].(map[string]interface{})

	avgPos := getFloatFromNested(posInfo, "average", "current")
	avgPosDyn := getFloatFromNested(posInfo, "average", "dynamics")
	medianPos := getFloatFromNested(posInfo, "median", "current")

	visibility := getFloatFromNested(visInfo, "visibility", "current")
	visDyn := getFloatFromNested(visInfo, "visibility", "dynamics")
	openCard := getIntFromNested(visInfo, "openCard", "current")
	openCardDyn := getFloatFromNested(visInfo, "openCard", "dynamics")

	clusters, _ := posInfo["clusters"].(map[string]interface{})
	clusterFirst := getIntFromNested(clusters, "firstHundred", "current")
	clusterSecond := getIntFromNested(clusters, "secondHundred", "current")
	clusterBelow := getIntFromNested(clusters, "below", "current")

	// The report API returns aggregated data for all requested nmIDs.
	// We create one row per nmID with the same aggregated values.
	rows := make([]sqlite.SearchPositionRow, 0, len(nmIDs))
	for _, nmID := range nmIDs {
		rows = append(rows, sqlite.SearchPositionRow{
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

// Helpers

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

func formatProgress(current, total, batchSize int, startTime time.Time) string {
	pct := float64(current) / float64(total) * 100
	elapsed := time.Since(startTime)
	if current > 0 {
		remaining := elapsed / time.Duration(current) * time.Duration(total-current)
		return fmt.Sprintf("%d/%d (%.0f%%, ETA %s)", current, total, pct, remaining.Truncate(time.Second))
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", current, total, pct)
}

func getFloat(m map[string]interface{}, key string) float64 {
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

func getInt(m map[string]interface{}, key string) int {
	return int(getFloat(m, key))
}

func getFloatFromNested(m map[string]interface{}, keys ...string) float64 {
	for i := 0; i < len(keys)-1; i++ {
		if m == nil {
			return 0
		}
		v, ok := m[keys[i]]
		if !ok {
			return 0
		}
		m, ok = v.(map[string]interface{})
		if !ok {
			return 0
		}
	}
	return getFloat(m, keys[len(keys)-1])
}

func getIntFromNested(m map[string]interface{}, keys ...string) int {
	return int(getFloatFromNested(m, keys...))
}
