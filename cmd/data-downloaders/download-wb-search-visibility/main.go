// Package main provides a utility to download organic search visibility data from WB Seller Analytics API.
//
// Downloads search positions (avg position, visibility %) and top search queries per product.
// Stores daily snapshots in SQLite for trend analysis and correlation with sales.
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
	"strconv"
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
	WB              config.WBClientConfig          `yaml:"wb"`
	SearchVisibility config.SearchVisibilityConfig `yaml:"search_visibility"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mock := flag.Bool("mock", false, "Use mock client (no API calls)")
	begin := flag.String("begin", "", "Begin date (YYYY-MM-DD)")
	end := flag.String("end", "", "End date (YYYY-MM-DD)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	nmIDsFlag := flag.String("nm-ids", "", "Comma-separated nmID list (overrides auto-detection)")
	limit := flag.Int("limit", 0, "Max search queries per product (default: 30)")
	skipPositions := flag.Bool("skip-positions", false, "Skip search positions download")
	skipQueries := flag.Bool("skip-queries", false, "Skip search queries download")
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
		cfg.SearchVisibility.Begin = *begin
	}
	if *end != "" {
		cfg.SearchVisibility.End = *end
	}
	if *days > 0 {
		cfg.SearchVisibility.Days = *days
	}
	if *dbPath != "" {
		cfg.SearchVisibility.DbPath = *dbPath
	}
	if *limit > 0 {
		cfg.SearchVisibility.Limit = *limit
	}
	if *skipPositions {
		cfg.SearchVisibility.SkipPositions = true
	}
	if *skipQueries {
		cfg.SearchVisibility.SkipQueries = true
	}

	cfg.SearchVisibility = cfg.SearchVisibility.GetDefaults()
	beginDate, endDate := calculateDateRange(cfg)
	snapshotDate := time.Now().Format("2006-01-02")
	printHeader(cfg, beginDate, endDate, snapshotDate, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.SearchVisibility.DbPath)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	var client SearchVisibilityClient
	if *mock {
		if strings.Contains(cfg.SearchVisibility.DbPath, "/var/db/") {
			log.Fatalf("FATAL: --mock mode with production database (%s). Use --db test.db or run from a directory with a local config.", cfg.SearchVisibility.DbPath)
		}
		client = NewMockSearchVisibilityClient()
		fmt.Println("Mock mode - using simulated data")
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient, err := wb.NewFromConfig(config.WBConfig{
			APIKey:        apiKey,
			Timeout:       cfg.WB.Timeout,
			RetryAttempts: 3,
		})
		if err != nil {
			log.Fatalf("Failed to create WB client: %v", err)
		}
		applyRateLimits(wbClient, cfg.SearchVisibility.RateLimits)
		wbClient.SetAdaptiveParams(0, cfg.SearchVisibility.AdaptiveProbeAfter, cfg.SearchVisibility.MaxBackoffSeconds)
		client = wbClient
		fmt.Printf("API Key: %s...\n", maskKey(apiKey))
	}

	// Load nmIDs
	nmIDs, err := loadNmIDs(ctx, repo, *nmIDsFlag)
	if err != nil {
		log.Fatalf("Failed to load nmIDs: %v", err)
	}
	if len(nmIDs) == 0 {
		log.Fatal("No nmIDs found. Load sales data first or use --nm-ids flag.")
	}
	fmt.Printf("Loaded %d nmIDs\n", len(nmIDs))

	// Apply filters (same pattern as funnel_loader.go)
	filter := cfg.SearchVisibility.Filter

	if len(filter.ExcludeLengths) > 0 || len(filter.AllowedYears) > 0 {
		before := len(nmIDs)

		articlesMap, err := repo.GetSupplierArticlesByNmIDs(ctx, nmIDs)
		if err != nil {
			log.Fatalf("Failed to get supplier articles for filtering: %v", err)
		}

		contains := func(slice []int, value int) bool {
			for _, v := range slice {
				if v == value {
					return true
				}
			}
			return false
		}

		var filtered []int
		for _, nmID := range nmIDs {
			article := articlesMap[nmID]
			if article == "" {
				continue
			}

			if contains(filter.ExcludeLengths, len(article)) {
				continue
			}

			if len(filter.AllowedYears) > 0 && len(article) >= 3 {
				yearDigits := article[1:3]
				year, err := strconv.Atoi(yearDigits)
				if err == nil && !contains(filter.AllowedYears, year) {
					continue
				}
			}

			filtered = append(filtered, nmID)
		}

		nmIDs = filtered
		fmt.Printf("  After filter: %d products (excluded %d)\n", len(nmIDs), before-len(nmIDs))
	}

	if filter.ActiveDays > 0 {
		before := len(nmIDs)
		nmIDs, err = repo.FilterActiveNmIDs(ctx, nmIDs, filter.ActiveDays)
		if err != nil {
			log.Fatalf("Failed to filter active products: %v", err)
		}
		fmt.Printf("  Active (%d days): %d products (excluded %d inactive)\n", filter.ActiveDays, len(nmIDs), before-len(nmIDs))
	}

	fmt.Println()

	rl := cfg.SearchVisibility.RateLimits
	totalSteps := 0
	if !cfg.SearchVisibility.SkipPositions {
		totalSteps++
	}
	if !cfg.SearchVisibility.SkipQueries {
		totalSteps++
	}
	if totalSteps == 0 {
		fmt.Println("Nothing to do (all steps skipped)")
		return
	}

	stepNum := 0

	// Phase 1: Search Positions
	if !cfg.SearchVisibility.SkipPositions {
		stepNum++
		fmt.Printf("[%d/%d] Search Positions...\n", stepNum, totalSteps)
		if err := DownloadSearchPositions(ctx, client, repo, nmIDs, beginDate, endDate, snapshotDate, rl.SearchReport, rl.SearchReportBurst); err != nil {
			log.Printf("Warning: Search Positions failed: %v", err)
		}
	}

	// Phase 2: Search Queries
	if !cfg.SearchVisibility.SkipQueries {
		stepNum++
		fmt.Printf("[%d/%d] Search Queries (limit=%d)...\n", stepNum, totalSteps, cfg.SearchVisibility.Limit)
		if err := DownloadSearchQueries(ctx, client, repo, nmIDs, beginDate, endDate, snapshotDate, cfg.SearchVisibility.Limit, rl.SearchTexts, rl.SearchTextsBurst); err != nil {
			log.Printf("Warning: Search Queries failed: %v", err)
		}
	}

	// Summary
	posCount, _ := repo.CountSearchPositions(ctx)
	qCount, _ := repo.CountSearchQueries(ctx)

	fmt.Println("\n" + strings.Repeat("=", 71))
	fmt.Println("Download complete!")
	fmt.Printf("   Database:   %s\n", cfg.SearchVisibility.DbPath)
	fmt.Printf("   Positions:  %d rows\n", posCount)
	fmt.Printf("   Queries:    %d rows\n", qCount)
	fmt.Println(strings.Repeat("=", 71))
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
	cfg.SearchVisibility.DbPath = "sales.db"
	cfg.SearchVisibility.Days = 7
	cfg.SearchVisibility.Limit = 30
	return cfg
}

func calculateDateRange(cfg *Config) (string, string) {
	if cfg.SearchVisibility.Begin != "" && cfg.SearchVisibility.End != "" {
		return cfg.SearchVisibility.Begin, cfg.SearchVisibility.End
	}
	days := cfg.SearchVisibility.Days
	if days == 0 {
		days = 7
	}
	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func loadNmIDs(ctx context.Context, repo *sqlite.SQLiteSalesRepository, flag string) ([]int, error) {
	if flag != "" {
		var ids []int
		for _, part := range strings.Split(flag, ",") {
			var id int
			fmt.Sscanf(strings.TrimSpace(part), "%d", &id)
			if id != 0 {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	return repo.GetDistinctNmIDs(ctx)
}

func getAPIKey(cfg *Config) string {
	if key := os.Getenv(cfg.SearchVisibility.APIKeyEnv); key != "" {
		return key
	}
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

func applyRateLimits(client *wb.Client, rl config.SearchVisibilityRateLimits) {
	client.SetRateLimit("search_report", rl.SearchReport, rl.SearchReportBurst, rl.SearchReportApi, rl.SearchReportApiBurst)
	client.SetRateLimit("search_texts", rl.SearchTexts, rl.SearchTextsBurst, rl.SearchTextsApi, rl.SearchTextsApiBurst)
}

func printHeader(cfg *Config, beginDate, endDate, snapshotDate string, mock bool) {
	fmt.Println(strings.Repeat("=", 71))
	fmt.Println("WB Search Visibility Downloader (positions, queries, visibility)")
	fmt.Println(strings.Repeat("=", 71))
	fmt.Printf("Database:      %s\n", cfg.SearchVisibility.DbPath)
	fmt.Printf("Period:        %s -> %s\n", beginDate, endDate)
	fmt.Printf("Snapshot:      %s\n", snapshotDate)
	fmt.Printf("Query limit:   %d per product\n", cfg.SearchVisibility.Limit)
	if mock {
		fmt.Println("Mode:          Mock")
	}
	fmt.Println(strings.Repeat("=", 71))
}

func printHelp() {
	fmt.Print(`WB Search Visibility Downloader - Organic search position and query tracking

Usage:
  go run main.go [options]

Options:
  --config PATH         Config file path (default: config.yaml)
  --mock                Use mock client (no API calls)
  --begin DATE          Begin date (YYYY-MM-DD)
  --end DATE            End date (YYYY-MM-DD)
  --days N              Days from today (alternative to begin/end, default: 7)
  --db PATH             Database path (overrides config)
  --nm-ids LIST         Comma-separated nmID list (overrides auto-detection)
  --limit N             Max search queries per product (default: 30, max: 100)
  --skip-positions      Skip search positions download
  --skip-queries        Skip search queries download
  --help                Show this help

API Endpoints:
  POST /api/v2/search-report/report            — positions, visibility
  POST /api/v2/search-report/product/search-texts — top queries per product

Rate Limit: 3 req/min for all search-report endpoints.

Examples:
  # Download last 7 days
  WB_API_KEY=xxx go run main.go --days=7

  # Specific products with higher query limit
  go run main.go --nm-ids=123456,789012 --limit=50

  # Positions only (skip slow query download)
  go run main.go --skip-queries --days=7

  # Mock mode for testing
  go run main.go --mock --days=7

`)
}
