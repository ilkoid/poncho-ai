// Package main provides a utility to download WB Promotion API data to SQLite.
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

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config represents the YAML configuration.
// REFACTORED 2026-02-21: Uses pkg/config types instead of local duplicates.
type Config struct {
	WB        config.WBClientConfig `yaml:"wb"`
	Promotion config.PromotionConfig `yaml:"promotion"`
}

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mock := flag.Bool("mock", false, "Use mock client (no API calls)")
	clean := flag.Bool("clean", false, "Delete database before download")
	begin := flag.String("begin", "", "Begin date (YYYY-MM-DD)")
	end := flag.String("end", "", "End date (YYYY-MM-DD)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	statuses := flag.String("statuses", "", "Filter by status (comma-separated: 9,11)")
	resume := flag.Bool("resume", false, "Resume mode: continue from last date")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	help := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("⚠️  Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Apply CLI overrides
	if *begin != "" {
		cfg.Promotion.Begin = *begin
	}
	if *end != "" {
		cfg.Promotion.End = *end
	}
	if *days > 0 {
		cfg.Promotion.Days = *days
	}
	if *statuses != "" {
		cfg.Promotion.Statuses = parseStatuses(*statuses)
	}
	if *resume {
		cfg.Promotion.Resume = true
	}
	if *dbPath != "" {
		cfg.Promotion.DbPath = *dbPath
	}

	// Calculate date range
	beginDate, endDate := calculateDateRange(cfg)

	// Print header
	printHeader(cfg, beginDate, endDate, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted!")
		cancel()
	}()

	// Delete database if --clean
	if *clean {
		if err := os.Remove(cfg.Promotion.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("❌ Failed to delete database: %v", err)
		}
		fmt.Println("🗑️  Database deleted")
	}

	// Create repository
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Promotion.DbPath)
	if err != nil {
		log.Fatalf("❌ Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create client
	var client PromotionClient
	if *mock {
		client = NewMockPromotionClient()
		PopulateMockData(client.(*MockPromotionClient), 10, cfg.Promotion.Days)
		fmt.Println("🎭 Mock mode - using simulated data")
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("❌ No API key. Set WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY")
		}
		wbClient := wb.New(apiKey)
		applyRateLimits(wbClient, cfg.Promotion.GetDefaults().RateLimits)
		wbClient.SetAdaptiveParams(
			0, // adaptive_recover_after: deprecated, limiter drops to api floor immediately
			cfg.Promotion.GetDefaults().AdaptiveProbeAfter,
			cfg.Promotion.GetDefaults().MaxBackoffSeconds,
		)
		client = wbClient
		fmt.Printf("🔑 API Key: %s...%s\n", maskKey(apiKey), "")
	}

	// Download campaigns (or reuse from DB)
	var allCampaigns, campaigns []int
	var summary StatsSummary
	var t0 time.Time

	totalSteps := 0
	if !cfg.Promotion.SkipCampaigns {
		totalSteps++
	}
	if !cfg.Promotion.SkipDetails {
		totalSteps++
	}
	if !cfg.Promotion.SkipStats {
		totalSteps++
	}
	if totalSteps == 0 {
		fmt.Println("⚠️  Nothing to do (all steps skipped)")
		return
	}
	stepNum := 0

	if cfg.Promotion.SkipCampaigns {
		// Load IDs from database
		fmt.Printf("\n[%d/%d] 📥 Loading campaigns from database... ", stepNum+1, totalSteps)
		stepNum++
		t0 := time.Now()
		allCampaigns, err = repo.GetCampaignIDsByStatus(ctx, cfg.Promotion.Statuses)
		if err != nil {
			log.Fatalf("❌ Failed to load campaigns from DB: %v", err)
		}
		if len(cfg.Promotion.Statuses) > 0 {
			campaigns = allCampaigns // statuses already filtered by DB query
		} else {
			campaigns = allCampaigns
		}
		fmt.Printf("✅ %d campaigns (%d for stats) from DB (%s)\n", len(allCampaigns), len(campaigns), time.Since(t0).Truncate(time.Second))
	} else {
		fmt.Printf("\n[%d/%d] 📥 Downloading campaigns... ", stepNum+1, totalSteps)
		stepNum++
		t0 := time.Now()
		allCampaigns, campaigns, err = DownloadCampaigns(ctx, client, repo, cfg.Promotion.Statuses)
		if err != nil {
			log.Fatalf("❌ Failed to download campaigns: %v", err)
		}
		fmt.Printf("✅ %d campaigns (%d for stats) (%s)\n", len(allCampaigns), len(campaigns), time.Since(t0).Truncate(time.Second))
	}

	if len(allCampaigns) == 0 {
		fmt.Println("⚠️  No campaigns found")
		return
	}

	// Download campaign details (metadata from /api/advert/v2/adverts)
	if cfg.Promotion.SkipDetails {
		fmt.Printf("[%d/%d] 📋 Skipping campaign details\n", stepNum+1, totalSteps)
		stepNum++
	} else {
		detailsBatches := (len(allCampaigns) + 49) / 50
		fmt.Printf("[%d/%d] 📋 Downloading campaign details (%d batches)...\n", stepNum+1, totalSteps, detailsBatches)
		stepNum++
		t0 = time.Now()
		detailsLoaded, err := DownloadCampaignDetails(ctx, client, repo, allCampaigns)
		if err != nil {
			log.Fatalf("❌ Failed to download campaign details: %v", err)
		}
		fmt.Printf("   ✅ %d/%d (%s)\n", detailsLoaded, len(allCampaigns), time.Since(t0).Truncate(time.Second))
	}

	// Download stats
	if cfg.Promotion.SkipStats {
		fmt.Printf("[%d/%d] 📊 Skipping stats\n", stepNum+1, totalSteps)
		stepNum++
	} else {
		statsBatches := (len(campaigns) + 49) / 50
		dateWindows := countDateWindows(beginDate, endDate)
		totalAPICalls := statsBatches * dateWindows
		fmt.Printf("[%d/%d] 📊 Downloading stats (%s → %s, %d batches × %d windows = %d API calls)...\n",
			stepNum+1, totalSteps, beginDate, endDate, statsBatches, dateWindows, totalAPICalls)
		stepNum++
		t0 = time.Now()
		rl := cfg.Promotion.GetDefaults().RateLimits
		summary, err = DownloadCampaignStats(ctx, client, repo, campaigns, beginDate, endDate, cfg.Promotion.Resume, rl.Fullstats, rl.FullstatsBurst)
		if err != nil {
			log.Fatalf("❌ Failed to download stats: %v", err)
		}
		fmt.Printf("   ✅ Done (%s)\n", time.Since(t0).Truncate(time.Second))
	}

	// Rebuild campaign_products materialized view
	fmt.Println("\n🔄 Rebuilding campaign_products...")
	if err := repo.PopulateCampaignProducts(ctx); err != nil {
		log.Fatalf("❌ Failed to populate campaign_products: %v", err)
	}

	// Update aggregates
	fmt.Println("\n📈 Updating campaign aggregates...")
	for _, id := range campaigns {
		if err := repo.UpdateCampaignAggregates(ctx, id); err != nil {
			log.Printf("⚠️  Failed to update aggregates for campaign %d: %v", id, err)
		}
	}

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 71))
	fmt.Println("🎉 Download complete!")
	fmt.Printf("   Campaigns: %d\n", len(campaigns))
	fmt.Printf("   Windows:   %d\n", summary.DateWindows)
	fmt.Printf("   Daily:     %d rows\n", summary.DailyRows)
	fmt.Printf("   App:       %d rows\n", summary.AppRows)
	fmt.Printf("   Nm:        %d rows\n", summary.NmRows)
	fmt.Printf("   Booster:   %d rows\n", summary.BoosterRows)
	fmt.Printf("   Database:  %s\n", cfg.Promotion.DbPath)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func countDateWindows(beginDate, endDate string) int {
	return len(splitDateRanges(beginDate, endDate))
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.WB.RateLimit = 20
	cfg.WB.BurstLimit = 1
	cfg.WB.Timeout = "30s"
	cfg.Promotion.DbPath = "promotion.db"
	cfg.Promotion.Days = 7
	return cfg
}

func calculateDateRange(cfg *Config) (string, string) {
	if cfg.Promotion.Begin != "" && cfg.Promotion.End != "" {
		return cfg.Promotion.Begin, cfg.Promotion.End
	}

	days := cfg.Promotion.Days
	if days == 0 {
		days = 7
	}

	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02") // Exclude today (incomplete)
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
	// Priority: env > config
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

func printHelp() {
	fmt.Print(`WB Promotion Downloader - Download campaign data from WB Promotion API

Usage:
  go run main.go [options]

Options:
  --config PATH     Config file path (default: config.yaml)
  --mock            Use mock client (no API calls)
  --clean           Delete database before download
  --begin DATE      Begin date (YYYY-MM-DD)
  --end DATE        End date (YYYY-MM-DD)
  --days N          Days from today (alternative to begin/end)
  --statuses LIST   Filter by status (comma-separated: 9,11)
  --resume          Resume mode: continue from last date
  --db PATH         Database path (overrides config)
  --help            Show this help

Status Codes:
  -1 = Deleted    4 = Ready    7 = Finished
  8 = Canceled    9 = Active   11 = Paused

Examples:
  # Download last 7 days
  WB_API_KEY=xxx go run main.go --days=7

  # Download active campaigns only
  go run main.go --statuses=9 --days=30

  # Mock mode for testing
  go run main.go --mock --days=7

`)
}

func printHeader(cfg *Config, beginDate, endDate string, mock bool) {
	fmt.Println(strings.Repeat("=", 71))
	fmt.Println("📥 WB Promotion Downloader")
	fmt.Println(strings.Repeat("=", 71))
	fmt.Printf("Config:     %s\n", cfg.Promotion.DbPath)
	fmt.Printf("Period:     %s → %s\n", beginDate, endDate)

	if len(cfg.Promotion.Statuses) > 0 {
		fmt.Printf("Statuses:   %v\n", cfg.Promotion.Statuses)
	}

	if cfg.Promotion.Resume {
		fmt.Println("Resume:     ✓")
	}

	if mock {
		fmt.Println("Mode:       🎭 Mock")
	}

	fmt.Println(strings.Repeat("=", 71))
}

// applyRateLimits pre-sets rate limiters on wb.Client from config values.
// Must be called before any API methods that use these toolIDs.
// Each endpoint gets: desired rate (aggressive) + api rate (swagger floor for recovery).
func applyRateLimits(client *wb.Client, rl config.PromotionRateLimits) {
	client.SetRateLimit("get_promotion_count", rl.PromotionCount, rl.PromotionCountBurst, rl.PromotionCountApi, rl.PromotionCountApiBurst)
	client.SetRateLimit("get_advert_details", rl.AdvertDetails, rl.AdvertDetailsBurst, rl.AdvertDetailsApi, rl.AdvertDetailsApiBurst)
	client.SetRateLimit("get_campaign_fullstats", rl.Fullstats, rl.FullstatsBurst, rl.FullstatsApi, rl.FullstatsApiBurst)
}
