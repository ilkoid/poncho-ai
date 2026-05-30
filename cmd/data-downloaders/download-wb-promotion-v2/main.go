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
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
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

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		dllog.Log("interrupted!")
		cancel()
	}()

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.PromotionV2.DbPath)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	var client V2Client
	var apiKey string
	if *mock {
		client = NewMockV2Client()
	} else {
		apiKey = getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY")
		}
		wbClient, err := wb.NewFromConfig(config.WBConfig{
			APIKey:        apiKey,
			Timeout:       cfg.WB.Timeout,
			RetryAttempts: 5,
		})
		if err != nil {
			log.Fatalf("Failed to create WB client: %v", err)
		}
		applyRateLimits(wbClient, cfg.PromotionV2.RateLimits)
		wbClient.SetAdaptiveParams(0, cfg.PromotionV2.AdaptiveProbeAfter, cfg.PromotionV2.MaxBackoffSeconds)
		if calendarKey := getCalendarAPIKey(cfg); calendarKey != "" {
			wbClient.SetCalendarKey(calendarKey)
		}
		client = wbClient
	}

	// Print startup header
	headerFields := []dllog.HeaderField{
		{Key: "DB", Value: cfg.PromotionV2.DbPath},
		{Key: "Period", Value: beginDate + " -> " + endDate},
	}
	if apiKey != "" {
		headerFields = append(headerFields, dllog.HeaderField{Key: "API Key", Value: utils.MaskAPIKey(apiKey)})
	}
	if calendarKey := getCalendarAPIKey(cfg); calendarKey != "" {
		headerFields = append(headerFields, dllog.HeaderField{Key: "Calendar Key", Value: utils.MaskAPIKey(calendarKey)})
	}
	if len(cfg.PromotionV2.Statuses) > 0 {
		headerFields = append(headerFields, dllog.HeaderField{Key: "Statuses", Value: fmt.Sprintf("%v", cfg.PromotionV2.Statuses)})
	}
	if *mock {
		headerFields = append(headerFields, dllog.HeaderField{Key: "Mode", Value: "Mock"})
	}
	dllog.PrintHeader("WB Promotion V2 Downloader", headerFields...)

	// Load (advert_id, nm_id) pairs from V1 tables
	rl := cfg.PromotionV2.RateLimits

	cutoff := ""
	if cfg.PromotionV2.ChangedDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -cfg.PromotionV2.ChangedDays).Format("2006-01-02T15:04:05")
	} else {
		lastRun, _ := repo.GetNormqueryLastRun(ctx)
		if lastRun != "" {
			cutoff = lastRun
		}
	}

	if cutoff != "" {
		dllog.Log("incremental mode: campaigns changed since %s", cutoff)
	} else {
		dllog.Log("full scan mode: all matching campaigns")
	}

	productIDs, err := repo.GetCampaignProductIDs(ctx, cfg.PromotionV2.Statuses, cutoff)
	if err != nil {
		log.Fatalf("Failed to load campaign product IDs: %v", err)
	}
	dllog.Log("loaded %d (advert_id, nm_id) pairs from V1 tables", len(productIDs))

	hasV1Data := len(productIDs) > 0
	if !hasV1Data {
		dllog.Log("no V1 campaign products — skipping V1-dependent phases, running calendar only")
	}

	// Execute phases
	totalSteps := 0
	if hasV1Data && !cfg.PromotionV2.SkipBids {
		totalSteps++
	}
	if hasV1Data && !cfg.PromotionV2.SkipNormquery {
		totalSteps += 4
	}
	if hasV1Data && !cfg.PromotionV2.SkipRecommendations {
		totalSteps++
	}
	if !cfg.PromotionV2.SkipFinance {
		totalSteps += 3
	}
	if !cfg.PromotionV2.SkipCalendar {
		totalSteps += 3 // list + details + nomenclatures
	}
	if hasV1Data && !cfg.PromotionV2.SkipBudgets {
		totalSteps++
	}
	if hasV1Data && !cfg.PromotionV2.SkipMinBids {
		totalSteps++
	}
	if totalSteps == 0 {
		dllog.Log("nothing to do (all steps skipped)")
		return
	}

	stepNum := 0
	t0 := time.Now()

	// Phase 1: Campaign Bids (from AdvertDetails)
	if hasV1Data && !cfg.PromotionV2.SkipBids {
		stepNum++
		dllog.Log("[%d/%d] Campaign Bids...", stepNum, totalSteps)
		if err := DownloadCampaignBids(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Campaign Bids: %v", err)
		}
	}

	// Phase 2-5: Normquery
	if hasV1Data && !cfg.PromotionV2.SkipNormquery {
		stepNum++
		dllog.Log("[%d/%d] Normquery Stats...", stepNum, totalSteps)
		if err := DownloadNormqueryStats(ctx, client, repo, productIDs, beginDate, endDate, rl.NormqueryStats, rl.NormqueryStatsBurst); err != nil {
			dllog.Error("Normquery Stats: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Normquery Clusters...", stepNum, totalSteps)
		if err := DownloadNormqueryClusters(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Clusters: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Normquery Bids...", stepNum, totalSteps)
		if err := DownloadNormqueryBids(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Bids: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Normquery Minus...", stepNum, totalSteps)
		if err := DownloadNormqueryMinus(ctx, client, repo, productIDs, rl.Normquery, rl.NormqueryBurst); err != nil {
			dllog.Error("Normquery Minus: %v", err)
		}
	}

	// Phase 6: Bid Recommendations
	if hasV1Data && !cfg.PromotionV2.SkipRecommendations {
		stepNum++
		dllog.Log("[%d/%d] Bid Recommendations...", stepNum, totalSteps)
		if err := DownloadBidRecommendations(ctx, client, repo, productIDs, rl.BidRec, rl.BidRecBurst); err != nil {
			dllog.Error("Bid Recommendations: %v", err)
		}
	}

	// Phase 7-9: Finance
	if !cfg.PromotionV2.SkipFinance {
		stepNum++
		dllog.Log("[%d/%d] Expenses...", stepNum, totalSteps)
		if err := DownloadExpenses(ctx, client, repo, beginDate, endDate, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Expenses: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Balance...", stepNum, totalSteps)
		if err := DownloadBalance(ctx, client, repo, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Balance: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Payments...", stepNum, totalSteps)
		if err := DownloadPayments(ctx, client, repo, beginDate, endDate, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Payments: %v", err)
		}
	}

	// Phase 10: Calendar
	if !cfg.PromotionV2.SkipCalendar {
		calBegin, calEnd := calculateCalendarDateRange(cfg)
		stepNum++
		dllog.Log("[%d/%d] Calendar Promotions (%s -> %s)...", stepNum, totalSteps, calBegin, calEnd)
		if err := DownloadCalendarPromotions(ctx, client, repo, calBegin, calEnd, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Calendar Details...", stepNum, totalSteps)
		if err := DownloadCalendarPromotionDetails(ctx, client, repo, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar Details: %v", err)
		}

		stepNum++
		dllog.Log("[%d/%d] Calendar Nomenclatures...", stepNum, totalSteps)
		if err := DownloadCalendarPromotionNomenclatures(ctx, client, repo, rl.Calendar, rl.CalendarBurst); err != nil {
			dllog.Error("Calendar Nomenclatures: %v", err)
		}
	}

	// Phase 13: Campaign Budgets
	if hasV1Data && !cfg.PromotionV2.SkipBudgets {
		stepNum++
		dllog.Log("[%d/%d] Campaign Budgets...", stepNum, totalSteps)
		if err := DownloadCampaignBudgets(ctx, client, repo, productIDs, rl.Finance, rl.FinanceBurst); err != nil {
			dllog.Error("Campaign Budgets: %v", err)
		}
	}

	// Phase 14: Minimum Bids
	if hasV1Data && !cfg.PromotionV2.SkipMinBids {
		stepNum++
		dllog.Log("[%d/%d] Minimum Bids...", stepNum, totalSteps)
		if err := DownloadMinBids(ctx, client, repo, productIDs, rl.MinBids, rl.MinBidsBurst); err != nil {
			dllog.Error("Minimum Bids: %v", err)
		}
	}

	dllog.Done(time.Since(t0), "%d/%d phases completed, DB: %s", stepNum, totalSteps, cfg.PromotionV2.DbPath)
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

func calculateCalendarDateRange(cfg *Config) (string, string) {
	now := time.Now()
	begin := now.AddDate(0, 0, -cfg.PromotionV2.CalendarDaysPast).Format("2006-01-02")
	end := now.AddDate(0, 0, cfg.PromotionV2.CalendarDaysFuture).Format("2006-01-02")
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

func getCalendarAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_MARKET_KEY"); key != "" {
		return key
	}
	return cfg.WB.CalendarAPIKey
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
