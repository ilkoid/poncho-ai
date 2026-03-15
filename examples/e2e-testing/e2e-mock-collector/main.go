// Package main provides E2E Mock Data Collector for WB V2 Tools
//
// Автономная утилита для сбора 1-2 батчей данных из WB API и сохранения
// в SQLite с auto-mapping схемой. Используется для тестирования V2 tools.
//
// Usage:
//
//	cd examples/e2e-mock-collector
//	go run main.go                                     # Mock режим (demo_key)
//	WB_API_KEY=real_key go run main.go                 # Реальный API
//	WB_API_KEY=real_key go run main.go --db ../e2e-twodays.db
//	WB_API_KEY=real_key go run main.go --products 10 --campaigns 5
//
// Output:
//
//	../e2e-twodays.db - SQLite с собранными данными
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
	_ "github.com/mattn/go-sqlite3"
)

// Config represents the collector configuration.
type Config struct {
	// API settings
	APIKey string `yaml:"api_key"`

	// Collection settings
	Products  int `yaml:"products"`   // Number of products to fetch
	Campaigns int `yaml:"campaigns"`  // Number of campaigns to fetch

	// Output settings
	Database string `yaml:"database"` // SQLite database path
}

func main() {
	// Parse command-line flags
	dbPath := flag.String("db", "../e2e-twodays.db", "Path to SQLite database")
	products := flag.Int("products", 10, "Number of products to collect")
	campaigns := flag.Int("campaigns", 10, "Number of campaigns to collect")
	dateFrom := flag.String("from", "2026-02-20", "Start date (YYYY-MM-DD)")
	dateTo := flag.String("to", "2026-02-21", "End date (YYYY-MM-DD)")
	dryRun := flag.Bool("dry-run", false, "Show what would be collected without fetching")
	flag.Parse()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║       E2E Mock Data Collector for WB V2 Tools              ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Get API keys for different APIs
	apiKey := os.Getenv("WB_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("WB_API_CONTENT_KEY")
	}
	if apiKey == "" {
		apiKey = "demo_key"
		fmt.Println("⚠️  No WB_API_KEY found, using demo_key (mock mode)")
	}

	// Statistics API uses separate key (WB_STAT)
	statKey := os.Getenv("WB_STAT")
	if statKey == "" {
		statKey = apiKey // fallback to main key
	}

	fmt.Printf("🔑 API Key: %s\n", maskAPIKey(apiKey))
	if statKey != apiKey {
		fmt.Printf("🔑 Statistics Key: %s\n", maskAPIKey(statKey))
	}
	fmt.Printf("📦 Products: %d\n", *products)
	fmt.Printf("📢 Campaigns: %d\n", *campaigns)
	fmt.Printf("📅 Period: %s to %s\n", *dateFrom, *dateTo)
	fmt.Printf("💾 Database: %s\n", *dbPath)
	fmt.Println()

	if *dryRun {
		fmt.Println("🏃 Dry-run mode - showing what would be collected:")
		showDryRun(*products, *campaigns, *dateFrom, *dateTo)
		return
	}

	// Create WB client
	wbConfig := config.WBConfig{
		APIKey:        apiKey,
		RateLimit:     100,
		BurstLimit:    5,
		RetryAttempts: 3,
		Timeout:       "60s",
	}

	client, err := wb.NewFromConfig(wbConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create WB client: %v", err)
	}

	// Create Statistics API client with separate key (if available)
	var statClient *wb.Client
	if statKey != apiKey && statKey != "demo_key" {
		statConfig := config.WBConfig{
			APIKey:        statKey,
			RateLimit:     60,
			BurstLimit:    5,
			RetryAttempts: 3,
			Timeout:       "60s",
		}
		statClient, err = wb.NewFromConfig(statConfig)
		if err != nil {
			log.Printf("⚠️  Failed to create Statistics client, using main client: %v", err)
			statClient = client
		}
	} else {
		statClient = client
	}

	// Ensure database directory exists
	dbDir := filepath.Dir(*dbPath)
	if dbDir != "." && dbDir != ".." {
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			log.Fatalf("❌ Failed to create database directory: %v", err)
		}
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer db.Close()

	// Create collector
	collector := NewCollector(client, db)

	// Set Statistics API client (may use separate WB_STAT key)
	collector.SetStatClient(statClient)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Step 1: Get product IDs from Content API (REAL nmIDs!)
	fmt.Println("\n📋 STEP 1: Fetching product nmIDs from Content API...")

	nmIDs, err := collector.GetNmIDsFromContentAPI(ctx, *products)
	if err != nil {
		log.Printf("⚠️  Failed to get products from Content API: %v, using mock IDs", err)
		nmIDs = generateMockIDs(*products, 100000)
	}
	fmt.Printf("   ✅ Found %d real product nmIDs\n", len(nmIDs))

	advertIDs, err := collector.GetCampaigns(ctx)
	if err != nil {
		log.Printf("⚠️  Failed to get campaigns: %v, using mock IDs", err)
		advertIDs = generateMockIDs(*campaigns, 1000)
	}
	fmt.Printf("   ✅ Found %d campaigns\n", len(advertIDs))

	// Step 2: Collect data from all endpoints
	fmt.Println("\n📥 STEP 2: Collecting data from WB API endpoints...")

	results := collector.CollectAll(ctx, nmIDs, advertIDs, *dateFrom, *dateTo)

	// Step 2.5: Extract nmIDs from sales (if available)
	salesNmIDs, err := collector.GetNmIDsFromSales(ctx, *dateFrom, *dateTo)
	if err == nil && len(salesNmIDs) > 0 {
		fmt.Printf("   💡 Found %d unique nmIDs in sales data\n", len(salesNmIDs))
	}

	// Step 3: Verify data
	fmt.Println("\n🔍 STEP 3: Verifying collected data...")

	if err := collector.VerifyData(); err != nil {
		log.Printf("⚠️  Verification warning: %v", err)
	}

	// Summary
	fmt.Println("\n" + "╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    COLLECTION COMPLETE                     ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	// Count successes
	successCount := 0
	for _, result := range results {
		if result.Error == nil {
			successCount++
		}
	}

	fmt.Printf("\n✅ Successfully collected: %d/%d endpoints\n", successCount, len(results))
	fmt.Printf("💾 Database: %s\n", *dbPath)

	// Show database stats
	showDatabaseStats(db)

	fmt.Println("\n📌 Next steps:")
	fmt.Println("   1. Run e2e-v2-test to verify V2 tools work correctly")
	fmt.Println("   2. Run e2e-real-test with real API for Stage B verification")
}

// showDryRun displays what would be collected without fetching.
func showDryRun(products, campaigns int, dateFrom, dateTo string) {
	endpoints := []struct {
		name     string
		batch    string
		rate     string
	}{
		{"sales", fmt.Sprintf("%s to %s", dateFrom, dateTo), "60 req/min"},
		{"funnel", fmt.Sprintf("%d products, 7 days", min(5, products)), "3 req/min"},
		{"funnel_history", fmt.Sprintf("%d products, 7 days", min(5, products)), "3 req/min"},
		{"search_positions", fmt.Sprintf("%d products, 7 days", min(5, products)), "3 req/min"},
		{"search_queries", "1 product, 7 days", "3 req/min"},
		{"fullstats", fmt.Sprintf("%d campaigns, 2 days", min(5, campaigns)), "3 req/min"},
		{"feedbacks", "50 items", "1 req/sec"},
		{"questions", "50 items", "1 req/sec"},
		{"attribution", fmt.Sprintf("%d products, %d campaigns, 7 days", min(5, products), min(5, campaigns)), "computed"},
	}

	fmt.Println("\nEndpoint         | Batch Size                    | Rate Limit")
	fmt.Println("-----------------|-------------------------------|------------")
	for _, ep := range endpoints {
		fmt.Printf("%-16s | %-29s | %s\n", ep.name, ep.batch, ep.rate)
	}

	estimatedTime := time.Duration(len(endpoints)-1) * 21 * time.Second // Analytics delay
	fmt.Printf("\n⏱️  Estimated collection time: ~%.0f seconds\n", estimatedTime.Seconds())
}

// showDatabaseStats displays statistics about the collected data.
func showDatabaseStats(db *sql.DB) {
	// Get all tables
	rows, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		fmt.Printf("   ⚠️  Could not read tables: %v\n", err)
		return
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}

	fmt.Printf("\n📊 Database statistics (%d tables):\n", len(tables))

	totalRecords := 0
	for _, table := range tables {
		var count int
		row := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
		if err := row.Scan(&count); err != nil {
			continue
		}
		totalRecords += count
		fmt.Printf("   📋 %-30s: %d records\n", table, count)
	}

	fmt.Printf("\n   📈 Total records: %d\n", totalRecords)
}

// generateMockIDs generates mock IDs for testing.
func generateMockIDs(count, start int) []int {
	ids := make([]int, count)
	for i := 0; i < count; i++ {
		ids[i] = start + i
	}
	return ids
}

// maskAPIKey masks the API key for display.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	if key == "demo_key" {
		return "demo_key (mock mode)"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
