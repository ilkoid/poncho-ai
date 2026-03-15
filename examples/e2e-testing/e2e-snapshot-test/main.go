// Package main provides E2E Snapshot Client Test
//
// Автономная утилита для тестирования SnapshotDBClient.
// Верифицирует корректность работы mock-клиента на реальных данных из SQLite.
//
// Usage:
//
//	cd examples/e2e-snapshot-test
//	go run main.go                                    # Использует ../e2e-snapshot.db
//	go run main.go -db ../e2e-snapshot.db            # Указать базу данных
//	go run main.go -test funnel                       # Запустить конкретный тест
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	// Parse command-line flags
	dbPath := flag.String("db", "../e2e-snapshot.db", "Path to snapshot database")
	testName := flag.String("test", "all", "Test to run (all, funnel, campaigns, stats, feedbacks, questions)")
	flag.Parse()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║           E2E SnapshotDBClient Test                        ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Create snapshot client
	client, err := wb.NewSnapshotDBClient(*dbPath)
	if err != nil {
		log.Fatalf("❌ Failed to create snapshot client: %v", err)
	}
	defer client.Close()

	fmt.Printf("💾 Database: %s\n", *dbPath)
	fmt.Printf("🧪 Test: %s\n\n", *testName)

	// Get stats
	stats, err := client.GetStats()
	if err != nil {
		log.Fatalf("❌ Failed to get stats: %v", err)
	}

	fmt.Println("📊 Snapshot Statistics:")
	fmt.Printf("   Sales:          %d rows\n", stats["sales"])
	fmt.Printf("   Funnel metrics: %d rows\n", stats["funnel_metrics"])
	fmt.Printf("   Products:       %d\n", stats["products"])
	fmt.Printf("   Campaigns:      %d\n", stats["campaigns"])
	fmt.Printf("   Campaign stats: %d rows\n", stats["campaign_stats"])
	fmt.Printf("   Unique nmIDs:   %d\n", stats["unique_nm_ids"])
	fmt.Printf("   Unique adverts: %d\n", stats["unique_advert_ids"])
	fmt.Printf("   Feedbacks:      %d\n", stats["feedbacks"])
	fmt.Printf("   Questions:      %d\n", stats["questions"])
	fmt.Println()

	// Run tests
	ctx := context.Background()
	allPassed := true

	if *testName == "all" || *testName == "funnel" {
		if !testFunnelMetrics(ctx, client) {
			allPassed = false
		}
	}

	if *testName == "all" || *testName == "campaigns" {
		if !testCampaigns(ctx, client) {
			allPassed = false
		}
	}

	if *testName == "all" || *testName == "stats" {
		if !testCampaignStats(ctx, client) {
			allPassed = false
		}
	}

	if *testName == "all" || *testName == "feedbacks" {
		if !testFeedbacks(ctx, client) {
			allPassed = false
		}
	}

	if *testName == "all" || *testName == "questions" {
		if !testQuestions(ctx, client) {
			allPassed = false
		}
	}

	// Print final result
	fmt.Println("\n" + "╔════════════════════════════════════════════════════════════╗")
	if allPassed {
		fmt.Println("║  ✅ ALL TESTS PASSED                                       ║")
	} else {
		fmt.Println("║  ❌ SOME TESTS FAILED                                      ║")
	}
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
}

func testFunnelMetrics(ctx context.Context, client *wb.SnapshotDBClient) bool {
	fmt.Println("\n📋 Test: GetFunnelMetrics")
	fmt.Println("─────────────────────────────────────────")

	// Get some nmIDs from stats
	stats, _ := client.GetStats()
	if stats["unique_nm_ids"] == 0 {
		fmt.Println("  ⏭️  Skipped: No products in snapshot")
		return true
	}

	// Get first 5 products from sales
	db := client.GetDB()
	rows, err := db.Query("SELECT DISTINCT nm_id FROM sales WHERE nm_id > 0 LIMIT 5")
	if err != nil {
		fmt.Printf("  ❌ Failed to get nmIDs: %v\n", err)
		return false
	}

	var nmIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			nmIDs = append(nmIDs, id)
		}
	}
	rows.Close()

	if len(nmIDs) == 0 {
		fmt.Println("  ⏭️  Skipped: No nmIDs found")
		return true
	}

	// Test GetFunnelMetrics
	start := time.Now()
	metrics, err := client.Sales().GetFunnelMetrics(ctx, wb.FunnelRequest{
		NmIDs: nmIDs,
		Period: 7,
	})
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetFunnelMetrics failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetFunnelMetrics completed in %v\n", duration)
	fmt.Printf("     Products returned: %d\n", len(metrics.Products))
	fmt.Printf("     Period: %d days\n", metrics.Period)

	// Show sample data
	for nmID, data := range metrics.Products {
		fmt.Printf("     Sample: nmID=%d, views=%d, cart=%d, orders=%d, buyouts=%d\n",
			nmID, data.OpenCount, data.CartCount, data.OrderCount, data.BuyoutCount)
		break // Only show first
	}

	return true
}

func testCampaigns(ctx context.Context, client *wb.SnapshotDBClient) bool {
	fmt.Println("\n📋 Test: GetCampaigns")
	fmt.Println("─────────────────────────────────────────")

	start := time.Now()
	campaigns, err := client.Advertising().GetCampaigns(ctx)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetCampaigns failed: %v\n", err)
		return false
	}

	// Count total adverts
	totalAdverts := 0
	for _, group := range campaigns {
		totalAdverts += len(group.AdvertList)
	}

	fmt.Printf("  ✅ GetCampaigns completed in %v\n", duration)
	fmt.Printf("     Campaign groups: %d\n", len(campaigns))
	fmt.Printf("     Total adverts: %d\n", totalAdverts)

	// Show sample
	if len(campaigns) > 0 && len(campaigns[0].AdvertList) > 0 {
		fmt.Printf("     Sample: type=%d, advertID=%d\n",
			campaigns[0].Type, campaigns[0].AdvertList[0].AdvertID)
	}

	return true
}

func testCampaignStats(ctx context.Context, client *wb.SnapshotDBClient) bool {
	fmt.Println("\n📋 Test: GetCampaignStats")
	fmt.Println("─────────────────────────────────────────")

	// Get some advert IDs
	db := client.GetDB()
	rows, err := db.Query("SELECT DISTINCT advert_id FROM campaign_stats_daily LIMIT 5")
	if err != nil {
		fmt.Printf("  ❌ Failed to get advertIDs: %v\n", err)
		return false
	}

	var advertIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			advertIDs = append(advertIDs, id)
		}
	}
	rows.Close()

	if len(advertIDs) == 0 {
		fmt.Println("  ⏭️  Skipped: No campaigns with stats")
		return true
	}

	// Test GetCampaignStats
	start := time.Now()
	stats, err := client.Advertising().GetCampaignStats(ctx, advertIDs, "2026-02-01", "2026-02-20")
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetCampaignStats failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetCampaignStats completed in %v\n", duration)
	fmt.Printf("     Daily stats returned: %d\n", len(stats))

	// Show sample
	if len(stats) > 0 {
		s := stats[0]
		fmt.Printf("     Sample: advertID=%d, date=%s, views=%d, clicks=%d, orders=%d\n",
			s.AdvertID, s.StatsDate, s.Views, s.Clicks, s.Orders)
	}

	// Test GetCampaignFullstats
	fmt.Println("\n  📋 Sub-test: GetCampaignFullstats")
	start = time.Now()
	fullstats, err := client.Advertising().GetCampaignFullstats(ctx, wb.CampaignFullstatsRequest{
		IDs:       advertIDs[:min(3, len(advertIDs))],
		BeginDate: "2026-02-01",
		EndDate:   "2026-02-20",
	})
	duration = time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetCampaignFullstats failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetCampaignFullstats completed in %v\n", duration)
	fmt.Printf("     Campaigns returned: %d\n", len(fullstats))

	// Show sample with JSON
	if len(fullstats) > 0 {
		fs := fullstats[0]
		fmt.Printf("     Sample: advertID=%d, views=%d, clicks=%d, orders=%d, days=%d\n",
			fs.AdvertID, fs.Views, fs.Clicks, fs.Orders, len(fs.Days))
	}

	return true
}

func testFeedbacks(ctx context.Context, client *wb.SnapshotDBClient) bool {
	fmt.Println("\n📋 Test: GetFeedbacks")
	fmt.Println("─────────────────────────────────────────")

	start := time.Now()
	resp, err := client.Feedbacks().GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take: 10,
	})
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetFeedbacks failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetFeedbacks completed in %v\n", duration)
	fmt.Printf("     Feedbacks returned: %d\n", len(resp.Data.Feedbacks))
	fmt.Printf("     Unanswered count: %d\n", resp.Data.CountUnanswered)

	// Show sample
	if len(resp.Data.Feedbacks) > 0 {
		fb := resp.Data.Feedbacks[0]
		hasAnswer := "no"
		if fb.Answer != nil {
			hasAnswer = "yes"
		}
		fmt.Printf("     Sample: id=%s, rating=%d, product=%d, answer=%s\n",
			fb.ID, fb.ProductValuation, fb.ProductDetails.NmId, hasAnswer)
	}

	// Test unanswered filter
	fmt.Println("\n  📋 Sub-test: GetFeedbacks (unanswered only)")
	start = time.Now()
	unanswered := false
	unansweredResp, err := client.Feedbacks().GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take:       10,
		IsAnswered: &unanswered,
	})
	duration = time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetFeedbacks (unanswered) failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetFeedbacks (unanswered) completed in %v\n", duration)
	fmt.Printf("     Unanswered feedbacks: %d\n", len(unansweredResp.Data.Feedbacks))

	return true
}

func testQuestions(ctx context.Context, client *wb.SnapshotDBClient) bool {
	fmt.Println("\n📋 Test: GetQuestions")
	fmt.Println("─────────────────────────────────────────")

	start := time.Now()
	resp, err := client.Feedbacks().GetQuestions(ctx, wb.QuestionsRequest{
		Take: 10,
	})
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetQuestions failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetQuestions completed in %v\n", duration)
	fmt.Printf("     Questions returned: %d\n", len(resp.Data.Questions))
	fmt.Printf("     Unanswered count: %d\n", resp.Data.CountUnanswered)

	// Show sample
	if len(resp.Data.Questions) > 0 {
		q := resp.Data.Questions[0]
		hasAnswer := "no"
		if q.Answer != nil {
			hasAnswer = "yes"
		}
		fmt.Printf("     Sample: id=%s, product=%d, answer=%s\n",
			q.ID, q.ProductDetails.NmId, hasAnswer)
	}

	// Test GetUnansweredCounts
	fmt.Println("\n  📋 Sub-test: GetUnansweredCounts")
	start = time.Now()
	counts, err := client.Feedbacks().GetUnansweredCounts(ctx)
	duration = time.Since(start)

	if err != nil {
		fmt.Printf("  ❌ GetUnansweredCounts failed: %v\n", err)
		return false
	}

	fmt.Printf("  ✅ GetUnansweredCounts completed in %v\n", duration)
	fmt.Printf("     Feedbacks unanswered: %d\n", counts.FeedbacksUnanswered)
	fmt.Printf("     Questions unanswered: %d\n", counts.QuestionsUnanswered)

	return true
}

// Helper to print JSON
func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
