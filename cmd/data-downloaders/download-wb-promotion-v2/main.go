// Package main provides a utility to download extended WB Promotion API data to SQLite.
//
// V2 extends V1 with: normquery stats/bids/minus, bid recommendations, finance, calendar.
// Reads campaign IDs and nm_ids from V1 tables, writes to new V2 tables in the same DB.
//
// Usage:
//
//	WB_API_KEY=your_key go run main.go --days=7
//	go run main.go --mock --days=7    # Mock mode
//	go run main.go --help             # Show help
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config represents the YAML configuration.
type Config struct {
	WB           config.WBClientConfig `yaml:"wb"`
	PromotionV2  config.PromotionV2Config `yaml:"promotion_v2"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mock := flag.Bool("mock", false, "Use mock client (no API calls)")
	begin := flag.String("begin", "", "Begin date (YYYY-MM-DD)")
	end := flag.String("end", "", "End date (YYYY-MM-DD)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	statuses := flag.String("statuses", "", "Filter by status (comma-separated: 9,11)")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	help := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Apply CLI overrides
	if *begin != "" {
		cfg.PromotionV2.Begin = *begin
	}
	if *end != "" {
		cfg.PromotionV2.End = *end
	}
	if *days > 0 {
		cfg.PromotionV2.Days = *days
	}
	if *statuses != "" {
		cfg.PromotionV2.Statuses = parseStatuses(*statuses)
	}
	if *dbPath != "" {
		cfg.PromotionV2.DbPath = *dbPath
	}

	cfg.PromotionV2 = cfg.PromotionV2.GetDefaults()
	beginDate, endDate := calculateDateRange(cfg)
	printHeader(cfg, beginDate, endDate, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.PromotionV2.DbPath)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	var client V2Client
	if *mock {
		client = NewMockV2Client()
		fmt.Println("Mock mode - using simulated data")
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY")
		}
		wbClient, err := wb.NewFromConfig(config.WBConfig{
			APIKey:        apiKey,
			Timeout:       cfg.WB.Timeout,
			RetryAttempts: 3,
		})
		if err != nil {
			log.Fatalf("Failed to create WB client: %v", err)
		}
		applyRateLimits(wbClient, cfg.PromotionV2.RateLimits)
		wbClient.SetAdaptiveParams(0, cfg.PromotionV2.AdaptiveProbeAfter, cfg.PromotionV2.MaxBackoffSeconds)
		client = wbClient
		fmt.Printf("API Key: %s...\n", maskKey(apiKey))
	}

	// Load (advert_id, nm_id) pairs from V1 tables
	rl := cfg.PromotionV2.RateLimits
	productIDs, err := repo.GetCampaignProductIDs(ctx, cfg.PromotionV2.Statuses)
	if err != nil {
		log.Fatalf("Failed to load campaign product IDs: %v", err)
	}
	fmt.Printf("Loaded %d (advert_id, nm_id) pairs from V1 tables\n\n", len(productIDs))

	if len(productIDs) == 0 {
		fmt.Println("No campaign products found. Run V1 downloader first.")
		return
	}

	// Execute phases
	totalSteps := 0
	if !cfg.PromotionV2.SkipBids {
		totalSteps++
	}
	if !cfg.PromotionV2.SkipNormquery {
		totalSteps += 4
	}
	if !cfg.PromotionV2.SkipRecommendations {
		totalSteps++
	}
	if !cfg.PromotionV2.SkipFinance {
		totalSteps += 3
	}
	if !cfg.PromotionV2.SkipCalendar {
		totalSteps += 3 // list + details + nomenclatures
	}
	// Budget + MinBids always run (depend on productIDs, not skip flags)
	totalSteps += 2
	if totalSteps == 0 {
		fmt.Println("Nothing to do (all steps skipped)")
		return
	}

	stepNum := 0

	// Phase 1: Campaign Bids (from AdvertDetails)
	if !cfg.PromotionV2.SkipBids {
		stepNum++
		fmt.Printf("[%d/%d] Campaign Bids...\n", stepNum, totalSteps)
		if err := DownloadCampaignBids(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			log.Printf("Warning: Campaign Bids failed: %v", err)
		}
	}

	// Phase 2-5: Normquery
	if !cfg.PromotionV2.SkipNormquery {
		stepNum++
		fmt.Printf("[%d/%d] Normquery Stats...\n", stepNum, totalSteps)
		if err := DownloadNormqueryStats(ctx, client, repo, productIDs, beginDate, endDate, rl.NormqueryStats, rl.NormqueryStatsBurst); err != nil {
			log.Printf("Warning: Normquery Stats failed: %v", err)
		}

		stepNum++
		fmt.Printf("[%d/%d] Normquery Clusters...\n", stepNum, totalSteps)
		if err := DownloadNormqueryClusters(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			log.Printf("Warning: Normquery Clusters failed: %v", err)
		}

		stepNum++
		fmt.Printf("[%d/%d] Normquery Bids...\n", stepNum, totalSteps)
		if err := DownloadNormqueryBids(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			log.Printf("Warning: Normquery Bids failed: %v", err)
		}

		stepNum++
		fmt.Printf("[%d/%d] Normquery Minus...\n", stepNum, totalSteps)
		if err := DownloadNormqueryMinus(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			log.Printf("Warning: Normquery Minus failed: %v", err)
		}
	}

	// Phase 6: Bid Recommendations
	if !cfg.PromotionV2.SkipRecommendations {
		stepNum++
		fmt.Printf("[%d/%d] Bid Recommendations...\n", stepNum, totalSteps)
		if err := DownloadBidRecommendations(ctx, client, repo, productIDs, rl.BidRec, rl.BidRecBurst); err != nil {
			log.Printf("Warning: Bid Recommendations failed: %v", err)
		}
	}

	// Phase 7-9: Finance
	if !cfg.PromotionV2.SkipFinance {
		stepNum++
		fmt.Printf("[%d/%d] Expenses...\n", stepNum, totalSteps)
		if err := DownloadExpenses(ctx, client, repo, beginDate, endDate, rl.Finance, rl.FinanceBurst); err != nil {
			log.Printf("Warning: Expenses failed: %v", err)
		}

		stepNum++
		fmt.Printf("[%d/%d] Balance...\n", stepNum, totalSteps)
		if err := DownloadBalance(ctx, client, repo, rl.Finance, rl.FinanceBurst); err != nil {
			log.Printf("Warning: Balance failed: %v", err)
		}

		stepNum++
		fmt.Printf("[%d/%d] Payments...\n", stepNum, totalSteps)
		if err := DownloadPayments(ctx, client, repo, beginDate, endDate, rl.Finance, rl.FinanceBurst); err != nil {
			log.Printf("Warning: Payments failed: %v", err)
		}
	}

	// Phase 10: Calendar
	if !cfg.PromotionV2.SkipCalendar {
		stepNum++
		fmt.Printf("[%d/%d] Calendar Promotions...\n", stepNum, totalSteps)
		if err := DownloadCalendarPromotions(ctx, client, repo, rl.Calendar, rl.CalendarBurst); err != nil {
			log.Printf("Warning: Calendar failed: %v", err)
		}

			stepNum++
			fmt.Printf("[%d/%d] Calendar Details...\n", stepNum, totalSteps)
			if err := DownloadCalendarPromotionDetails(ctx, client, repo, rl.Calendar, rl.CalendarBurst); err != nil {
				log.Printf("Warning: Calendar Details failed: %v", err)
			}

			stepNum++
			fmt.Printf("[%d/%d] Calendar Nomenclatures...\n", stepNum, totalSteps)
			if err := DownloadCalendarPromotionNomenclatures(ctx, client, repo, rl.Calendar, rl.CalendarBurst); err != nil {
				log.Printf("Warning: Calendar Nomenclatures failed: %v", err)
			}
	}

	// Phase 11: Campaign Budgets
	stepNum++
	fmt.Printf("[%d/%d] Campaign Budgets...\n", stepNum, totalSteps)
	if err := DownloadCampaignBudgets(ctx, client, repo, productIDs, rl.Finance, rl.FinanceBurst); err != nil {
		log.Printf("Warning: Campaign Budgets failed: %v", err)
	}

	// Phase 12: Minimum Bids
	stepNum++
	fmt.Printf("[%d/%d] Minimum Bids...\n", stepNum, totalSteps)
	if err := DownloadMinBids(ctx, client, repo, productIDs, rl.MinBids, rl.MinBidsBurst); err != nil {
		log.Printf("Warning: Minimum Bids failed: %v", err)
	}

	fmt.Println("\n" + strings.Repeat("=", 71))
	fmt.Println("Download complete!")
	fmt.Printf("   Database:  %s\n", cfg.PromotionV2.DbPath)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.WB.Timeout = "120s"
	cfg.PromotionV2.DbPath = "wb-sales.db"
	cfg.PromotionV2.Days = 7
	cfg.PromotionV2.Statuses = []int{9, 11}
	return cfg
}

func calculateDateRange(cfg *Config) (string, string) {
	if cfg.PromotionV2.Begin != "" && cfg.PromotionV2.End != "" {
		return cfg.PromotionV2.Begin, cfg.PromotionV2.End
	}
	days := cfg.PromotionV2.Days
	if days == 0 {
		days = 7
	}
	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func parseStatuses(s string) []int {
	if s == "" {
		return nil
	}
	var result []int
	for _, part := range strings.Split(s, ",") {
		var status int
		fmt.Sscanf(strings.TrimSpace(part), "%d", &status)
		if status != 0 {
			result = append(result, status)
		}
	}
	return result
}

func getAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func maskKey(key string) string {
	if len(key) < 10 {
		return key
	}
	return key[:5] + "..." + key[len(key)-3:]
}

func printHeader(cfg *Config, beginDate, endDate string, mock bool) {
	fmt.Println(strings.Repeat("=", 71))
	fmt.Println("WB Promotion V2 Downloader (normquery, bids, finance, calendar)")
	fmt.Println(strings.Repeat("=", 71))
	fmt.Printf("Database:  %s\n", cfg.PromotionV2.DbPath)
	fmt.Printf("Period:    %s -> %s\n", beginDate, endDate)
	if len(cfg.PromotionV2.Statuses) > 0 {
		fmt.Printf("Statuses:  %v\n", cfg.PromotionV2.Statuses)
	}
	if mock {
		fmt.Println("Mode:      Mock")
	}
	fmt.Println(strings.Repeat("=", 71))
}

func applyRateLimits(client *wb.Client, rl config.PromotionV2RateLimits) {
	client.SetRateLimit("normquery_stats", rl.NormqueryStats, rl.NormqueryStatsBurst, rl.NormqueryStatsApi, rl.NormqueryStatsApiBurst)
	client.SetRateLimit("normquery_list", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("normquery_bids", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("normquery_minus", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("bid_recommendations", rl.BidRec, rl.BidRecBurst, rl.BidRecApi, rl.BidRecApiBurst)
	client.SetRateLimit("expenses", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("balance", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("payments", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("calendar_promotions", rl.Calendar, rl.CalendarBurst, rl.CalendarApi, rl.CalendarApiBurst)
	client.SetRateLimit("get_advert_details", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("budget", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("min_bids", rl.MinBids, rl.MinBidsBurst, rl.MinBidsApi, rl.MinBidsApiBurst)
}

func printHelp() {
	fmt.Print(`WB Promotion V2 Downloader - Extended promotion data collection

Usage:
  go run main.go [options]

Options:
  --config PATH     Config file path (default: config.yaml)
  --mock            Use mock client (no API calls)
  --begin DATE      Begin date (YYYY-MM-DD)
  --end DATE        End date (YYYY-MM-DD)
  --days N          Days from today (alternative to begin/end)
  --statuses LIST   Filter by status (comma-separated: 9,11)
  --db PATH         Database path (overrides config)
  --help            Show this help

Requires V1 data: run download-wb-promotion first to populate campaigns table.

Examples:
  # Download last 7 days
  WB_API_KEY=xxx go run main.go --days=7

  # Active campaigns only
  go run main.go --statuses=9 --days=7

  # Mock mode for testing
  go run main.go --mock --days=7

`)
}
