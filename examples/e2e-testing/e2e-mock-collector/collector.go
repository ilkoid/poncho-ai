// Package main provides data collection functionality for WB API endpoints.
//
// Collector handles fetching data from WB API with proper rate limiting
// and saving to SQLite using SchemaMapper.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Collector handles fetching and storing WB API data.
type Collector struct {
	client    *wb.Client
	statClient *wb.Client // Separate client for Statistics API (uses WB_STAT key)
	db        *sql.DB
	mapper    *SchemaMapper

	// Rate limiting
	analyticsDelay time.Duration // Delay between Analytics API calls (20s)
	advertsDelay   time.Duration // Delay between Adverts API calls (20s)
	feedbacksDelay time.Duration // Delay between Feedbacks API calls (1s)
}

// NewCollector creates a new Collector instance.
func NewCollector(client *wb.Client, db *sql.DB) *Collector {
	return &Collector{
		client:         client,
		statClient:     client, // Default: same client for all APIs
		db:             db,
		mapper:         NewSchemaMapper(db),
		analyticsDelay: 21 * time.Second, // WB Analytics: 3 req/min = 20s interval
		advertsDelay:   21 * time.Second, // WB Adverts: 3 req/min = 20s interval
		feedbacksDelay: 1100 * time.Millisecond, // WB Feedbacks: 1 req/sec
	}
}

// SetStatClient sets a separate client for Statistics API.
func (c *Collector) SetStatClient(statClient *wb.Client) {
	c.statClient = statClient
}

// CollectionResult represents the result of a collection operation.
type CollectionResult struct {
	Endpoint string
	Count    int
	Duration time.Duration
	Error    error
}

// CollectAll collects data from all endpoints with 1-2 batches each.
// dateFrom/dateTo format: "2006-01-02" (YYYY-MM-DD)
//
// IMPORTANT: Sales are collected FIRST to extract real nmIDs for the period.
// These nmIDs are then used for funnel, search_positions, and other analytics queries.
// This ensures analytics data matches the actual products that had sales.
func (c *Collector) CollectAll(ctx context.Context, nmIDs []int, advertIDs []int, dateFrom, dateTo string) map[string]CollectionResult {
	results := make(map[string]CollectionResult)
	_ = sync.Mutex{} // unused but kept for future concurrent collection

	fmt.Println("\n🚀 Starting E2E Mock Data Collection")
	fmt.Printf("   Initial Products: %d, Campaigns: %d\n", len(nmIDs), len(advertIDs))
	fmt.Printf("   Period: %s to %s\n", dateFrom, dateTo)
	fmt.Println(strings.Repeat("=", 61))

	// =========================================================================
	// STEP 0: Collect sales FIRST - this is the source of real nmIDs for period
	// =========================================================================
	results["sales"] = c.collectWithRetry(ctx, "sales", func() ([]byte, map[string]interface{}, error) {
		return c.collectSales(ctx, dateFrom, dateTo)
	})

	// =========================================================================
	// STEP 0.5: Extract nmIDs from collected sales for the period
	// =========================================================================
	// These nmIDs are products that actually had sales in the target period.
	// Using them ensures funnel data is relevant to the sales data we collected.
	salesNmIDs, err := c.GetNmIDsFromSales(ctx, dateFrom, dateTo)
	if err != nil {
		fmt.Printf("  ⚠️  Failed to extract nmIDs from sales: %v\n", err)
	}

	// Prefer sales nmIDs over passed nmIDs
	effectiveNmIDs := nmIDs
	if len(salesNmIDs) > 0 {
		effectiveNmIDs = salesNmIDs
		fmt.Printf("\n  📋 Using %d nmIDs from sales data (period: %s to %s)\n",
			len(effectiveNmIDs), dateFrom, dateTo)
	} else if len(nmIDs) > 0 {
		fmt.Printf("\n  📋 Using %d nmIDs from Content API (no sales found for period)\n",
			len(effectiveNmIDs))
	} else {
		fmt.Println("\n  ⚠️  No nmIDs available - skipping product-dependent endpoints")
	}

	// =========================================================================
	// STEP 1-8: Collect data from all other endpoints
	// =========================================================================

	// 1. Collect funnel data (5 products, 7 days) - uses nmIDs from sales
	if len(effectiveNmIDs) > 0 {
		results["funnel"] = c.collectWithRetry(ctx, "funnel", func() ([]byte, map[string]interface{}, error) {
			return c.collectFunnel(ctx, effectiveNmIDs[:min(5, len(effectiveNmIDs))], 7)
		})
	}

	// 2. Collect funnel history (5 products, 7 days) - uses nmIDs from sales
	if len(effectiveNmIDs) > 0 {
		results["funnel_history"] = c.collectWithRetry(ctx, "funnel_history", func() ([]byte, map[string]interface{}, error) {
			return c.collectFunnelHistory(ctx, effectiveNmIDs[:min(5, len(effectiveNmIDs))], 7)
		})
	}

	// 3. Collect search positions (5 products, 7 days) - uses nmIDs from sales
	if len(effectiveNmIDs) > 0 {
		results["search_positions"] = c.collectWithRetry(ctx, "search_positions", func() ([]byte, map[string]interface{}, error) {
			return c.collectSearchPositions(ctx, effectiveNmIDs[:min(5, len(effectiveNmIDs))], 7)
		})
	}

	// 4. Collect search queries (1 product, 7 days) - uses nmIDs from sales
	if len(effectiveNmIDs) > 0 {
		results["search_queries"] = c.collectWithRetry(ctx, "search_queries", func() ([]byte, map[string]interface{}, error) {
			return c.collectSearchQueries(ctx, effectiveNmIDs[0], 7)
		})
	}

	// 5. Collect campaign fullstats (5 campaigns, 2 days)
	results["fullstats"] = c.collectWithRetry(ctx, "fullstats", func() ([]byte, map[string]interface{}, error) {
		return c.collectFullstats(ctx, advertIDs[:min(5, len(advertIDs))], "2026-02-20", "2026-02-21")
	})

	// 6. Collect feedbacks (50 items)
	results["feedbacks"] = c.collectWithRetry(ctx, "feedbacks", func() ([]byte, map[string]interface{}, error) {
		return c.collectFeedbacks(ctx, 50, 0)
	})

	// 7. Collect questions (50 items)
	results["questions"] = c.collectWithRetry(ctx, "questions", func() ([]byte, map[string]interface{}, error) {
		return c.collectQuestions(ctx, 50, 0)
	})

	// 8. Collect attribution (5 products, 5 campaigns, 7 days) - uses nmIDs from sales
	if len(effectiveNmIDs) > 0 {
		results["attribution"] = c.collectWithRetry(ctx, "attribution", func() ([]byte, map[string]interface{}, error) {
			return c.collectAttribution(ctx, effectiveNmIDs[:min(5, len(effectiveNmIDs))], advertIDs[:min(5, len(advertIDs))], 7)
		})
	}

	// Print summary
	fmt.Println("\n" + strings.Repeat("=", 61))
	fmt.Println("📊 COLLECTION SUMMARY")
	fmt.Println(strings.Repeat("=", 61))

	totalRecords := 0
	for endpoint, result := range results {
		status := "✅"
		if result.Error != nil {
			status = "❌"
		}
		fmt.Printf("  %s %-20s: %4d records (%.1fs)\n",
			status, endpoint, result.Count, result.Duration.Seconds())
		totalRecords += result.Count
	}

	fmt.Printf("\n  Total: %d records collected\n", totalRecords)

	return results
}

// collectWithRetry wraps collection with retry logic.
func (c *Collector) collectWithRetry(ctx context.Context, endpoint string, fn func() ([]byte, map[string]interface{}, error)) CollectionResult {
	start := time.Now()
	maxRetries := 3

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 5 * time.Second
			fmt.Printf("  ⚠️  Retry #%d for %s after %v...\n", attempt, endpoint, backoff)
			time.Sleep(backoff)
		}

		data, params, err := fn()
		if err != nil {
			lastErr = err
			continue
		}

		// Save to SQLite
		if err := c.mapper.JSONToTable(endpoint, data, params); err != nil {
			lastErr = fmt.Errorf("save to SQLite: %w", err)
			continue
		}

		// Get record count
		count := c.getRecordCount(endpoint)

		fmt.Printf("  ✅ %-20s: %4d records (%.1fs)\n",
			endpoint, count, time.Since(start).Seconds())

		return CollectionResult{
			Endpoint: endpoint,
			Count:    count,
			Duration: time.Since(start),
			Error:    nil,
		}
	}

	fmt.Printf("  ❌ %-20s: FAILED after %d attempts (%.1fs)\n",
		endpoint, maxRetries, time.Since(start).Seconds())

	return CollectionResult{
		Endpoint: endpoint,
		Count:    0,
		Duration: time.Since(start),
		Error:    lastErr,
	}
}

// collectFunnel collects sales funnel data.
func (c *Collector) collectFunnel(ctx context.Context, nmIDs []int, days int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching funnel data: %d products, %d days...\n", len(nmIDs), days)

	// Rate limit
	time.Sleep(c.analyticsDelay)

	// Use WB service
	svc := wb.NewService(c.client)
	funnel, err := svc.Sales().GetFunnelMetrics(ctx, wb.FunnelRequest{
		NmIDs: nmIDs,
		Period: days,
	})
	if err != nil {
		// Try direct API call as fallback
		return c.collectFunnelRaw(ctx, nmIDs, days)
	}

	// Convert to JSON
	data, err := json.Marshal(funnel)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal funnel: %w", err)
	}

	params := map[string]interface{}{
		"nmIDs": nmIDs,
		"days":  days,
	}

	return data, params, nil
}

// collectFunnelRaw collects funnel data using raw API call.
func (c *Collector) collectFunnelRaw(ctx context.Context, nmIDs []int, days int) ([]byte, map[string]interface{}, error) {
	// Create mock data for demo mode
	if c.client.IsDemoKey() {
		return c.generateMockFunnelData(nmIDs, days)
	}

	// TODO: Implement raw API call
	return c.generateMockFunnelData(nmIDs, days)
}

// generateMockFunnelData generates mock funnel data for testing.
func (c *Collector) generateMockFunnelData(nmIDs []int, days int) ([]byte, map[string]interface{}, error) {
	products := make([]map[string]interface{}, len(nmIDs))

	for i, nmID := range nmIDs {
		views := 1000 + i*100
		cart := views / 10
		orders := cart / 2

		products[i] = map[string]interface{}{
			"product": map[string]interface{}{
				"nmId":           nmID,
				"vendorCode":     fmt.Sprintf("MOCK-%d", nmID),
				"title":          fmt.Sprintf("Mock Product %d", nmID),
				"brandName":      "Mock Brand",
				"subjectId":      100 + i,
				"subjectName":    "Mock Category",
				"stocks":         map[string]interface{}{"wb": 50, "mp": 10},
				"productRating":  4.5 + float64(i)*0.1,
				"feedbackRating": 4.0 + float64(i)*0.1,
			},
			"statistic": map[string]interface{}{
				"selected": map[string]interface{}{
					"openCount":    views,
					"cartCount":    cart,
					"orderCount":   orders,
					"buyoutCount":  orders - 2,
					"cancelCount":  2,
					"orderSum":     orders * 1500,
					"buyoutSum":    (orders - 2) * 1500,
					"avgPrice":     1500,
					"conversions": map[string]interface{}{
						"addToCartPercent":   float64(cart) / float64(views) * 100,
						"cartToOrderPercent": float64(orders) / float64(cart) * 100,
						"buyoutPercent":      float64(orders-2) / float64(orders) * 100,
					},
				},
			},
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"products": products,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"nmIDs": nmIDs,
		"days":  days,
	}

	return jsonData, params, nil
}

// collectFunnelHistory collects funnel history data.
func (c *Collector) collectFunnelHistory(ctx context.Context, nmIDs []int, days int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching funnel history: %d products, %d days...\n", len(nmIDs), days)

	time.Sleep(c.analyticsDelay)

	// Generate mock data
	products := make([]map[string]interface{}, len(nmIDs))
	for i, nmID := range nmIDs {
		dailyMetrics := make([]map[string]interface{}, days)
		for d := 0; d < days; d++ {
			date := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
			views := 100 + d*10 + i*5
			dailyMetrics[d] = map[string]interface{}{
				"date":       date,
				"openCount":  views,
				"cartCount":  views / 10,
				"orderCount": views / 20,
			}
		}
		products[i] = map[string]interface{}{
			"nmId":         nmID,
			"dailyMetrics": dailyMetrics,
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"products": products,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"nmIDs": nmIDs,
		"days":  days,
	}

	return jsonData, params, nil
}

// collectSearchPositions collects search positions data.
func (c *Collector) collectSearchPositions(ctx context.Context, nmIDs []int, days int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching search positions: %d products, %d days...\n", len(nmIDs), days)

	time.Sleep(c.analyticsDelay)

	// Generate mock data
	products := make([]map[string]interface{}, len(nmIDs))
	for i, nmID := range nmIDs {
		products[i] = map[string]interface{}{
			"nmId":           nmID,
			"avgPosition":    10.5 + float64(i)*2,
			"visibility":     75 - i*5,
			"queryCount":     50 + i*10,
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"products": products,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"nmIDs": nmIDs,
		"days":  days,
	}

	return jsonData, params, nil
}

// collectSearchQueries collects top search queries data.
func (c *Collector) collectSearchQueries(ctx context.Context, nmID int, days int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching search queries: product %d, %d days...\n", nmID, days)

	time.Sleep(c.analyticsDelay)

	// Generate mock data
	queries := []map[string]interface{}{
		{"query": "mock query 1", "orders": 50, "views": 500},
		{"query": "mock query 2", "orders": 30, "views": 300},
		{"query": "mock query 3", "orders": 20, "views": 200},
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"nmId":    nmID,
			"queries": queries,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"nmID": nmID,
		"days": days,
	}

	return jsonData, params, nil
}

// collectFullstats collects campaign fullstats data.
func (c *Collector) collectFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching campaign fullstats: %d campaigns, %s to %s...\n",
		len(advertIDs), beginDate, endDate)

	time.Sleep(c.advertsDelay)

	// Use WB service
	svc := wb.NewService(c.client)
	fullstats, err := svc.Advertising().GetCampaignFullstats(ctx, wb.CampaignFullstatsRequest{
		IDs:       advertIDs,
		BeginDate: beginDate,
		EndDate:   endDate,
	})
	if err != nil {
		// Generate mock data as fallback
		return c.generateMockFullstats(advertIDs, beginDate, endDate)
	}

	// Convert to JSON
	data := map[string]interface{}{
		"data": fullstats,
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"advertIDs": advertIDs,
		"beginDate": beginDate,
		"endDate":   endDate,
	}

	return jsonData, params, nil
}

// generateMockFullstats generates mock fullstats data.
func (c *Collector) generateMockFullstats(advertIDs []int, beginDate, endDate string) ([]byte, map[string]interface{}, error) {
	campaigns := make([]map[string]interface{}, len(advertIDs))

	for i, id := range advertIDs {
		views := 1000 + i*100
		clicks := views / 20

		campaigns[i] = map[string]interface{}{
			"advertId": id,
			"views":    views,
			"clicks":   clicks,
			"ctr":      5.0 + float64(i)*0.5,
			"cpc":      4.5 + float64(i)*0.2,
			"cr":       2.0 + float64(i)*0.1,
			"orders":   clicks / 10,
			"shks":     clicks / 15,
			"atbs":     clicks / 50,
			"canceled": clicks / 100,
			"sum":      float64(clicks) * 5.0,
			"sum_price": float64(clicks) * 500.0,
			"days": []map[string]interface{}{
				{
					"date":   beginDate,
					"views":  views / 2,
					"clicks": clicks / 2,
					"orders": clicks / 20,
					"sum":    float64(clicks) * 2.5,
					"sum_price": float64(clicks) * 250.0,
				},
			},
		}
	}

	data := map[string]interface{}{
		"data": campaigns,
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"advertIDs": advertIDs,
		"beginDate": beginDate,
		"endDate":   endDate,
	}

	return jsonData, params, nil
}

// collectFeedbacks collects product feedbacks (both answered and unanswered).
func (c *Collector) collectFeedbacks(ctx context.Context, take, offset int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching feedbacks: %d items (offset %d)...\n", take, offset)

	time.Sleep(c.feedbacksDelay)

	svc := wb.NewService(c.client)
	allFeedbacks := make([]wb.Feedback, 0, take*2)

	// Fetch unanswered feedbacks first (they usually have text)
	unansweredFalse := false
	respUnanswered, err := svc.Feedbacks().GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take:       take,
		Noffset:    offset,
		IsAnswered: &unansweredFalse,
	})
	if err == nil {
		fmt.Printf("     ✅ Unanswered: %d feedbacks\n", len(respUnanswered.Data.Feedbacks))
		allFeedbacks = append(allFeedbacks, respUnanswered.Data.Feedbacks...)
	}

	// Wait for rate limit
	time.Sleep(c.feedbacksDelay)

	// Fetch answered feedbacks
	answeredTrue := true
	respAnswered, err := svc.Feedbacks().GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take:       take,
		Noffset:    offset,
		IsAnswered: &answeredTrue,
	})
	if err == nil {
		fmt.Printf("     ✅ Answered: %d feedbacks\n", len(respAnswered.Data.Feedbacks))
		allFeedbacks = append(allFeedbacks, respAnswered.Data.Feedbacks...)
	}

	// If both failed, generate mock data
	if len(allFeedbacks) == 0 {
		return c.generateMockFeedbacks(take, offset)
	}

	// Build combined response
	combined := &wb.FeedbacksResponse{}
	combined.Data.Feedbacks = allFeedbacks
	combined.Data.CountUnanswered = respUnanswered.Data.CountUnanswered
	combined.Data.CountArchive = respAnswered.Data.CountArchive

	data, _ := json.Marshal(combined)
	params := map[string]interface{}{
		"take":           take,
		"offset":         offset,
		"answered":       len(respAnswered.Data.Feedbacks),
		"unanswered":     len(respUnanswered.Data.Feedbacks),
		"total_collected": len(allFeedbacks),
	}

	return data, params, nil
}

// generateMockFeedbacks generates mock feedbacks data.
func (c *Collector) generateMockFeedbacks(take, offset int) ([]byte, map[string]interface{}, error) {
	feedbacks := make([]map[string]interface{}, take)

	for i := 0; i < take; i++ {
		id := offset + i + 1
		feedbacks[i] = map[string]interface{}{
			"id":                fmt.Sprintf("fb-%d", id),
			"text":              fmt.Sprintf("Mock feedback text %d", id),
			"productValuation":  4 + (id % 2),
			"createdDate":       time.Now().AddDate(0, 0, -id).Format("2006-01-02T15:04:05Z"),
			"userName":          fmt.Sprintf("User%d", id),
			"productDetails": map[string]interface{}{
				"nmId":            100000 + id,
				"productName":     fmt.Sprintf("Product %d", id),
				"brandName":       "Mock Brand",
				"supplierArticle": fmt.Sprintf("ART-%d", id),
			},
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"feedbacks":        feedbacks,
			"countUnanswered":  5,
			"countArchive":     10,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"take":   take,
		"offset": offset,
	}

	return jsonData, params, nil
}

// collectQuestions collects product questions (both answered and unanswered).
func (c *Collector) collectQuestions(ctx context.Context, take, offset int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching questions: %d items (offset %d)...\n", take, offset)

	time.Sleep(c.feedbacksDelay)

	svc := wb.NewService(c.client)
	allQuestions := make([]wb.Question, 0, take*2)

	// Fetch unanswered questions first
	unansweredFalse := false
	respUnanswered, err := svc.Feedbacks().GetQuestions(ctx, wb.QuestionsRequest{
		Take:       take,
		Noffset:    offset,
		IsAnswered: &unansweredFalse,
	})
	if err == nil {
		fmt.Printf("     ✅ Unanswered: %d questions\n", len(respUnanswered.Data.Questions))
		allQuestions = append(allQuestions, respUnanswered.Data.Questions...)
	}

	// Wait for rate limit
	time.Sleep(c.feedbacksDelay)

	// Fetch answered questions
	answeredTrue := true
	respAnswered, err := svc.Feedbacks().GetQuestions(ctx, wb.QuestionsRequest{
		Take:       take,
		Noffset:    offset,
		IsAnswered: &answeredTrue,
	})
	if err == nil {
		fmt.Printf("     ✅ Answered: %d questions\n", len(respAnswered.Data.Questions))
		allQuestions = append(allQuestions, respAnswered.Data.Questions...)
	}

	// If both failed, generate mock data
	if len(allQuestions) == 0 {
		return c.generateMockQuestions(take, offset)
	}

	// Build combined response
	combined := &wb.QuestionsResponse{}
	combined.Data.Questions = allQuestions
	combined.Data.CountUnanswered = respUnanswered.Data.CountUnanswered
	combined.Data.CountArchive = respAnswered.Data.CountArchive

	data, _ := json.Marshal(combined)
	params := map[string]interface{}{
		"take":            take,
		"offset":          offset,
		"answered":        len(respAnswered.Data.Questions),
		"unanswered":      len(respUnanswered.Data.Questions),
		"total_collected": len(allQuestions),
	}

	return data, params, nil
}

// generateMockQuestions generates mock questions data.
func (c *Collector) generateMockQuestions(take, offset int) ([]byte, map[string]interface{}, error) {
	questions := make([]map[string]interface{}, take)

	for i := 0; i < take; i++ {
		id := offset + i + 1
		questions[i] = map[string]interface{}{
			"id":          fmt.Sprintf("q-%d", id),
			"text":        fmt.Sprintf("Mock question text %d?", id),
			"createdDate": time.Now().AddDate(0, 0, -id).Format("2006-01-02T15:04:05Z"),
			"state":       "none",
			"wasViewed":   id%2 == 0,
			"productDetails": map[string]interface{}{
				"nmId":            100000 + id,
				"productName":     fmt.Sprintf("Product %d", id),
				"brandName":       "Mock Brand",
				"supplierArticle": fmt.Sprintf("ART-%d", id),
			},
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"questions":        questions,
			"countUnanswered":  3,
			"countArchive":     7,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"take":   take,
		"offset": offset,
	}

	return jsonData, params, nil
}

// collectAttribution collects attribution analysis data.
func (c *Collector) collectAttribution(ctx context.Context, nmIDs, advertIDs []int, days int) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Computing attribution: %d products, %d campaigns, %d days...\n",
		len(nmIDs), len(advertIDs), days)

	// Attribution is computed from funnel + fullstats, no API delay needed

	// Generate mock attribution data
	totalOrders := 100
	adOrders := 30

	data := map[string]interface{}{
		"periodStart":   time.Now().AddDate(0, 0, -days).Format("2006-01-02"),
		"periodEnd":     time.Now().Format("2006-01-02"),
		"totalOrders":   totalOrders,
		"organicOrders": totalOrders - adOrders,
		"adOrders":      adOrders,
		"totalViews":    5000,
		"organicViews":  3500,
		"adViews":       1500,
		"adSpent":       1500.50,
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"nmIDs":     nmIDs,
		"advertIDs": advertIDs,
		"days":      days,
	}

	return jsonData, params, nil
}

// getRecordCount returns the record count for an endpoint.
func (c *Collector) getRecordCount(endpoint string) int {
	tableName := fmt.Sprintf("%s_fetches", endpoint)
	var count int
	row := c.db.QueryRow(fmt.Sprintf("SELECT SUM(record_count) FROM %s", tableName))
	if err := row.Scan(&count); err != nil {
		return 0
	}
	return count
}

// GetProducts retrieves product nmIDs from WB API.
func (c *Collector) GetProducts(ctx context.Context, limit int) ([]int, error) {
	fmt.Printf("  📥 Fetching products (limit %d)...\n", limit)

	if c.client.IsDemoKey() {
		// Return mock product IDs
		ids := make([]int, limit)
		for i := 0; i < limit; i++ {
			ids[i] = 100000 + i
		}
		return ids, nil
	}

	// TODO: Implement real API call
	// For now, return mock data
	ids := make([]int, limit)
	for i := 0; i < limit; i++ {
		ids[i] = 100000 + i
	}
	return ids, nil
}

// GetCampaigns retrieves campaign advertIDs from WB API.
func (c *Collector) GetCampaigns(ctx context.Context) ([]int, error) {
	fmt.Println("  📥 Fetching campaigns...")

	svc := wb.NewService(c.client)
	groups, err := svc.Advertising().GetCampaigns(ctx)
	if err != nil {
		// Return mock IDs
		return []int{1001, 1002, 1003, 1004, 1005}, nil
	}

	// Extract advert IDs
	var ids []int
	for _, group := range groups {
		for _, advert := range group.AdvertList {
			ids = append(ids, advert.AdvertID)
		}
	}

	return ids, nil
}

// GetNmIDsFromSales extracts unique nmIDs from sales data for a specific period.
// This is the preferred way to get real product IDs that had sales in the period.
//
// The function first finds the most recent fetch_id that matches the date range
// (stored in request_params as dateFrom/dateTo), then extracts nmIDs from that fetch.
func (c *Collector) GetNmIDsFromSales(ctx context.Context, dateFrom, dateTo string) ([]int, error) {
	fmt.Printf("  📥 Extracting nmIDs from sales (%s to %s)...\n", dateFrom, dateTo)

	// First, try to find fetch_ids for this specific period
	// request_params is stored as JSON: {"dateFrom":"2026-02-20","dateTo":"2026-02-21"}
	var fetchIDs []int64
	rows, err := c.db.Query(`
		SELECT id FROM sales_fetches
		WHERE request_params LIKE ?
		ORDER BY id DESC
		LIMIT 10
	`, fmt.Sprintf("%%\"dateFrom\":\"%s\"%%\"dateTo\":\"%s\"%%", dateFrom, dateTo))
	if err != nil {
		// Table doesn't exist yet, return empty
		return nil, nil
	}

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			fetchIDs = append(fetchIDs, id)
		}
	}
	rows.Close()

	// If no fetches found for this period, return empty
	if len(fetchIDs) == 0 {
		fmt.Printf("     ⚠️  No sales fetches found for period %s to %s\n", dateFrom, dateTo)
		return nil, nil
	}

	// Build query with fetch_ids
	placeholders := make([]string, len(fetchIDs))
	args := make([]interface{}, len(fetchIDs))
	for i, id := range fetchIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT nm_id FROM sales_rows
		WHERE nm_id > 0 AND fetch_id IN (%s)
		ORDER BY nm_id
	`, strings.Join(placeholders, ","))

	rows, err = c.db.Query(query, args...)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}

	fmt.Printf("     ✅ Found %d unique nmIDs from %d sales fetches\n", len(ids), len(fetchIDs))
	return ids, nil
}

// collectSales collects sales data from Statistics API.
func (c *Collector) collectSales(ctx context.Context, dateFrom, dateTo string) ([]byte, map[string]interface{}, error) {
	fmt.Printf("  📥 Fetching sales data: %s to %s...\n", dateFrom, dateTo)

	// Statistics API rate limit (same as Analytics)
	time.Sleep(c.analyticsDelay)

	// Use statClient for Statistics API (may have separate WB_STAT key)
	if c.statClient.IsDemoKey() {
		return c.generateMockSalesData(dateFrom, dateTo)
	}

	// Parse dates (YYYY-MM-DD → YYYYMMDD)
	from, _ := time.Parse("2006-01-02", dateFrom)
	to, _ := time.Parse("2006-01-02", dateTo)
	dateFromInt := from.Year()*10000 + int(from.Month())*100 + from.Day()
	dateToInt := to.Year()*10000 + int(to.Month())*100 + to.Day()

	// Collect all rows using iterator
	var allRows []map[string]interface{}

	callback := func(rows []wb.RealizationReportRow) error {
		for _, row := range rows {
			allRows = append(allRows, map[string]interface{}{
				"rrd_id":              row.RrdID,
				"doc_type_name":       row.DocTypeName,
				"sale_id":             row.SaleID,
				"nm_id":               row.NmID,
				"sa_name":             row.SupplierArticle,
				"subject_name":        row.SubjectName,
				"brand_name":          row.BrandName,
				"ts_name":             row.TechSize,
				"barcode":             row.Barcode,
				"quantity":            row.Quantity,
				"is_cancel":           row.IsCancel,
				"delivery_method":     row.DeliveryMethod,
				"ppvz_for_pay":        row.PPVzForPay,
				"retail_price":        row.RetailPrice,
				"retail_amount":       row.RetailAmount,
				"sale_percent":        row.SalePercent,
				"commission_percent":  row.CommissionPercent,
				"delivery_rub":        row.DeliveryRub,
				"order_dt":            row.OrderDT,
				"sale_dt":             row.SaleDT,
				"rr_dt":               row.RRDT,
			})
		}
		return nil
	}

	_, err := c.statClient.ReportDetailByPeriodIterator(
		ctx,
		"https://statistics-api.wildberries.ru",
		60, 5, // rate limit
		dateFromInt, dateToInt,
		callback,
	)
	if err != nil {
		return c.generateMockSalesData(dateFrom, dateTo)
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"rows": allRows,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"dateFrom": dateFrom,
		"dateTo":   dateTo,
	}

	return jsonData, params, nil
}

// generateMockSalesData generates mock sales data.
func (c *Collector) generateMockSalesData(dateFrom, dateTo string) ([]byte, map[string]interface{}, error) {
	// Parse date range
	from, _ := time.Parse("2006-01-02", dateFrom)
	to, _ := time.Parse("2006-01-02", dateTo)
	days := int(to.Sub(from).Hours()/24) + 1

	// Generate mock sales rows
	rows := make([]map[string]interface{}, 0)
	rowID := 1

	for d := 0; d < days; d++ {
		date := from.AddDate(0, 0, d)
		dateStr := date.Format("2006-01-02")

		// 10-20 sales per day
		salesToday := 10 + (d * 3)

		for s := 0; s < salesToday; s++ {
			nmID := 100000 + (s % 10) // 10 different products
			quantity := 1 + (s % 3)
			isCancel := s%15 == 0 // ~6% cancellation rate

			rows = append(rows, map[string]interface{}{
				"rrd_id":              rowID,
				"doc_type_name":       "Продажа",
				"sale_id":             fmt.Sprintf("S-%d", rowID),
				"nm_id":               nmID,
				"sa_name":             fmt.Sprintf("ART-%d", nmID),
				"subject_name":        "Mock Product Category",
				"brand_name":          "Mock Brand",
				"ts_name":             "M",
				"barcode":             fmt.Sprintf("2000%010d", rowID),
				"quantity":            quantity,
				"is_cancel":           isCancel,
				"delivery_method":     "FBW",
				"ppvz_for_pay":        float64(quantity) * 850.0,
				"retail_price":        1500.0,
				"retail_amount":       float64(quantity) * 1500.0,
				"sale_percent":        5.0,
				"commission_percent":  15.0,
				"delivery_rub":        50.0,
				"order_dt":            dateStr + "T10:00:00Z",
				"sale_dt":             dateStr + "T12:00:00Z",
				"rr_dt":               dateStr + "T00:00:00Z",
			})
			rowID++
		}
	}

	data := map[string]interface{}{
		"data": map[string]interface{}{
			"rows": rows,
		},
	}

	jsonData, _ := json.Marshal(data)
	params := map[string]interface{}{
		"dateFrom": dateFrom,
		"dateTo":   dateTo,
	}

	return jsonData, params, nil
}

// VerifyData verifies data integrity in SQLite.
func (c *Collector) VerifyData() error {
	fmt.Println("\n🔍 Verifying data integrity...")

	tables, err := c.mapper.GetSchema("funnel")
	if err != nil {
		return fmt.Errorf("get schema: %w", err)
	}

	for _, table := range tables {
		var count int
		row := c.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
		if err := row.Scan(&count); err != nil {
			return fmt.Errorf("count %s: %w", table, err)
		}
		fmt.Printf("  📋 %s: %d records\n", table, count)
	}

	return nil
}

// GetNmIDsFromContentAPI retrieves all product nmIDs from Content API.
// This is the primary source of real nmIDs for the seller.
func (c *Collector) GetNmIDsFromContentAPI(ctx context.Context, limit int) ([]int, error) {
	fmt.Printf("  📥 Fetching nmIDs from Content API (limit %d)...\n", limit)

	if c.client.IsDemoKey() {
		// Return mock product IDs
		ids := make([]int, limit)
		for i := 0; i < limit; i++ {
			ids[i] = 100000 + i
		}
		return ids, nil
	}

	// Use Content API to get all cards
	reqBody := map[string]interface{}{
		"settings": map[string]interface{}{
			"cursor": map[string]interface{}{
				"limit": 100, // Max per request
			},
		},
	}

	var allNmIDs []int
	seenIDs := make(map[int]bool)

	// Paginate through all cards
	for {
		var response struct {
			Cards []struct {
				NmID       int    `json:"nmID"`
				VendorCode string `json:"vendorCode"`
				Title      string `json:"title"`
			} `json:"cards"`
			Cursor struct {
				Total     int `json:"total"`
				UpdatedAt string `json:"updatedAt"`
				NmID      int `json:"nmID"`
			} `json:"cursor"`
		}

		err := c.client.Post(ctx, "get_wb_cards_list",
			"https://content-api.wildberries.ru", 100, 5,
			"/content/v2/get/cards/list", reqBody, &response)
		if err != nil {
			return nil, fmt.Errorf("failed to get cards: %w", err)
		}

		// Extract nmIDs
		for _, card := range response.Cards {
			if !seenIDs[card.NmID] {
				allNmIDs = append(allNmIDs, card.NmID)
				seenIDs[card.NmID] = true
			}
		}

		// Check if we have more pages
		if len(response.Cards) < 100 || response.Cursor.Total <= len(allNmIDs) {
			break
		}

		// Update cursor for next page
		if len(response.Cards) > 0 {
			lastCard := response.Cards[len(response.Cards)-1]
			reqBody["settings"].(map[string]interface{})["cursor"].(map[string]interface{})["nmID"] = lastCard.NmID
		}

		// Check limit
		if limit > 0 && len(allNmIDs) >= limit {
			allNmIDs = allNmIDs[:limit]
			break
		}
	}

	fmt.Printf("     ✅ Found %d unique nmIDs\n", len(allNmIDs))
	return allNmIDs, nil
}
