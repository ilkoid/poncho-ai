// Package main provides a utility to download WB warehouse stock snapshots to SQLite.
//
// Usage:
//
//	WB_API_KEY=your_key go run . --mock
//	go run . --mock                    # Mock mode
//	go run . --help                     # Show help
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

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mock := flag.Bool("mock", false, "Use mock client (no API calls)")
	clean := flag.Bool("clean", false, "Delete database before download")
	date := flag.String("date", "", "Snapshot date YYYY-MM-DD (default: today)")
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
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Apply defaults
	cfg.Stocks = cfg.Stocks.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// Apply CLI overrides
	if *dbPath != "" {
		cfg.Stocks.DbPath = *dbPath
	}

	// Determine snapshot date
	snapshotDate := *date
	if snapshotDate == "" {
		snapshotDate = time.Now().Format("2006-01-02")
	}

	// Print header
	printHeader(cfg, snapshotDate, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Delete database if --clean
	if *clean {
		if err := os.Remove(cfg.Stocks.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("Failed to delete database: %v", err)
		}
		fmt.Println("Database deleted")
	}

	// Create repository
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Stocks.DbPath)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create client
	rl := cfg.Stocks.GetDefaults().RateLimits

	var client StocksClient
	if *mock {
		mockClient := NewMockStocksClient()
		PopulateMockStocks(mockClient, 100, 3) // 100 products × 3 warehouses
		client = mockClient
		fmt.Println("Mode: Mock")
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit("get_stocks_warehouses", rl.Warehouse, rl.WarehouseBurst, rl.WarehouseApi, rl.WarehouseApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.Stocks.GetDefaults().AdaptiveProbeAfter, cfg.Stocks.GetDefaults().MaxBackoffSeconds)
		client = wbClient // *wb.Client satisfies StocksClient directly
		fmt.Printf("API Key: %s...\n", maskKey(apiKey))
	}

	// Gap detection
	if cfg.Stocks.FirstDate != "" {
		fmt.Println("\nChecking for gaps...")
		gaps, err := DetectGaps(ctx, repo, cfg.Stocks.FirstDate)
		if err != nil {
			log.Printf("Gap detection failed: %v", err)
		} else {
			PrintGapReport(gaps)
		}
	}

	// Download snapshot
	fmt.Printf("\nDownloading stock snapshot for %s...\n", snapshotDate)
	result, err := DownloadStockSnapshot(ctx, client, repo, snapshotDate, rl.Warehouse, rl.WarehouseBurst)
	if err != nil {
		log.Fatalf("Failed to download stocks: %v", err)
	}

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Download complete!")
	fmt.Printf("  Snapshot:  %s\n", snapshotDate)
	fmt.Printf("  Rows:      %d\n", result.TotalRows)
	fmt.Printf("  Pages:     %d\n", result.Pages)
	fmt.Printf("  Database:  %s\n", cfg.Stocks.DbPath)

	// Verify count
	count, _ := repo.CountStocks(ctx)
	fmt.Printf("  DB total:  %d\n", count)
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
	cfg.WB.RateLimit = 3
	cfg.WB.BurstLimit = 1
	cfg.WB.Timeout = "60s"
	cfg.Stocks.DbPath = "sales.db"
	return cfg
}

func getAPIKey(cfg *Config) string {
	// Priority: configured env var > WB_API_ANALYTICS_AND_PROMO_KEY > WB_API_KEY > config value
	envVar := cfg.Stocks.APIKeyEnv
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	// Fallback to standard keys for backward compatibility
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
	fmt.Print(`WB Stocks Warehouse Downloader - Download warehouse stock snapshots from WB API

Usage:
  go run . [options]

Options:
  --config PATH     Config file path (default: config.yaml)
  --mock            Use mock client (no API calls)
  --clean           Delete database before download
  --date DATE       Snapshot date YYYY-MM-DD (default: today)
  --db PATH         Database path (overrides config)
  --help            Show this help

Examples:
  # Download today's snapshot
  WB_API_KEY=xxx go run .

  # Specific date
  WB_API_KEY=xxx go run . --date=2026-03-28

  # Mock mode for testing
  go run . --mock

  # Clean + custom DB
  go run . --clean --db=test-stocks.db

`)
}

func printHeader(cfg *Config, snapshotDate string, mock bool) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Stocks Warehouse Downloader")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.Stocks.DbPath)
	fmt.Printf("Date:      %s\n", snapshotDate)
	if cfg.Stocks.FirstDate != "" {
		fmt.Printf("FirstDate: %s\n", cfg.Stocks.FirstDate)
	}
	if mock {
		fmt.Println("Mode:      Mock")
	}
	fmt.Println(strings.Repeat("=", 60))
}
