// Package main demonstrates WbService usage with mock client.
//
// This E2E utility verifies the service layer implementation by:
// - Creating a mock client with demo_key
// - Using WbService to get funnel metrics
// - Verifying the response structure
//
// Run: go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	fmt.Println("=== WB Service Demo (E2E Verification) ===")
	fmt.Println()

	// 1. Create mock client with demo_key
	client := wb.New("demo_key")

	// Verify mock mode
	if !client.IsDemoKey() {
		fmt.Println("❌ ERROR: Expected demo_key to enable mock mode")
		return
	}
	fmt.Println("✅ Mock client created with demo_key")

	// 2. Create WbService
	svc := wb.NewService(client)
	fmt.Println("✅ WbService created")

	// 3. Test SalesService.GetFunnelMetrics
	fmt.Println()
	fmt.Println("--- Testing SalesService.GetFunnelMetrics ---")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nmIDs := []int{123456, 234567}
	period := 7

	metrics, err := svc.Sales().GetFunnelMetrics(ctx, wb.FunnelRequest{
		NmIDs:  nmIDs,
		Period: period,
	})
	if err != nil {
		fmt.Printf("❌ GetFunnelMetrics failed: %v\n", err)
		return
	}

	// Verify response
	if len(metrics.Products) != len(nmIDs) {
		fmt.Printf("❌ Expected %d products, got %d\n", len(nmIDs), len(metrics.Products))
		return
	}

	fmt.Printf("✅ GetFunnelMetrics returned %d products\n", len(metrics.Products))

	for nmID, data := range metrics.Products {
		fmt.Printf("   Product %d: OpenCount=%d, OrderCount=%d, Conversion=%.1f%%\n",
			nmID, data.OpenCount, data.OrderCount, data.ConversionRate)
	}

	// 4. Test SalesService.GetFunnelHistory
	fmt.Println()
	fmt.Println("--- Testing SalesService.GetFunnelHistory ---")

	now := time.Now()
	history, err := svc.Sales().GetFunnelHistory(ctx, wb.FunnelHistoryRequest{
		NmIDs:    nmIDs,
		DateFrom: now.AddDate(0, 0, -period),
		DateTo:   now,
	})
	if err != nil {
		fmt.Printf("❌ GetFunnelHistory failed: %v\n", err)
		return
	}

	fmt.Printf("✅ GetFunnelHistory returned %d days of data\n", len(history.DailyMetrics))

	// 5. Test AdvertisingService.GetCampaignFullstats
	fmt.Println()
	fmt.Println("--- Testing AdvertisingService.GetCampaignFullstats ---")

	campaignStats, err := svc.Advertising().GetCampaignFullstats(ctx, wb.CampaignFullstatsRequest{
		IDs:       []int{1001, 1002},
		BeginDate: now.AddDate(0, 0, -7).Format("2006-01-02"),
		EndDate:   now.Format("2006-01-02"),
	})
	if err != nil {
		fmt.Printf("❌ GetCampaignFullstats failed: %v\n", err)
		return
	}

	fmt.Printf("✅ GetCampaignFullstats returned %d campaigns\n", len(campaignStats))
	for _, cs := range campaignStats {
		fmt.Printf("   Campaign %d: Views=%d, Clicks=%d, Orders=%d, Sum=%.2f\n",
			cs.AdvertID, cs.Views, cs.Clicks, cs.Orders, cs.Sum)
	}

	// 6. Test AttributionService.GetAttributionSummary
	fmt.Println()
	fmt.Println("--- Testing AttributionService.GetAttributionSummary ---")

	attribution, err := svc.Attribution().GetAttributionSummary(ctx, wb.AttributionRequest{
		NmIDs:     nmIDs,
		AdvertIDs: []int{1001, 1002},
		Period:    period,
	})
	if err != nil {
		fmt.Printf("❌ GetAttributionSummary failed: %v\n", err)
		return
	}

	fmt.Printf("✅ GetAttributionSummary:\n")
	fmt.Printf("   Total Orders: %d (Organic: %d, Ad: %d)\n",
		attribution.TotalOrders, attribution.OrganicOrders, attribution.AdOrders)
	fmt.Printf("   Total Views: %d (Organic: %d, Ad: %d)\n",
		attribution.TotalViews, attribution.OrganicViews, attribution.AdViews)
	fmt.Printf("   Ad Spend: %.2f RUB\n", attribution.AdSpent)

	// 7. Test FeedbackService.GetFeedbacks
	fmt.Println()
	fmt.Println("--- Testing FeedbackService.GetFeedbacks ---")

	feedbacks, err := svc.Feedbacks().GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take:    5,
		Noffset: 0,
	})
	if err != nil {
		fmt.Printf("❌ GetFeedbacks failed: %v\n", err)
		return
	}

	fmt.Printf("✅ GetFeedbacks returned %d feedbacks\n", len(feedbacks.Data.Feedbacks))

	// 8. Summary
	fmt.Println()
	fmt.Println("=== E2E Verification Summary ===")
	fmt.Println("✅ SalesService: GetFunnelMetrics, GetFunnelHistory")
	fmt.Println("✅ AdvertisingService: GetCampaignFullstats")
	fmt.Println("✅ AttributionService: GetAttributionSummary")
	fmt.Println("✅ FeedbackService: GetFeedbacks")
	fmt.Println()
	fmt.Println("🎉 All service layer methods verified successfully!")

	// Pretty print sample response
	fmt.Println()
	fmt.Println("=== Sample FunnelMetrics JSON ===")
	sampleJSON, _ := json.MarshalIndent(metrics, "", "  ")
	fmt.Println(string(sampleJSON))
}
