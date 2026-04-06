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

// Config wraps configuration for the prices downloader.
type Config struct {
	WB     config.WBClientConfig `yaml:"wb"`
	Prices config.PricesConfig   `yaml:"prices"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	clean := flag.Bool("clean", false, "clean database before download")
	mockMode := flag.Bool("mock", false, "mock mode (no API calls)")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
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
	cfg.Prices = cfg.Prices.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// Apply CLI overrides
	if *dbPath != "" {
		cfg.Prices.DbPath = *dbPath
	}

	// Clean database if requested
	if *clean {
		if err := os.Remove(cfg.Prices.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("Failed to clean database: %v", err)
		}
		fmt.Println("Database cleaned")
	}

	// Snapshot date = today
	snapshotDate := time.Now().Format("2006-01-02")

	// Print header
	printHeader(cfg, *mockMode, snapshotDate)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted")
		cancel()
	}()

	// Open database
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Prices.DbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer repo.Close()

	// Check if snapshot already exists for today
	hasSnapshot, err := repo.HasSnapshotForDate(ctx, snapshotDate)
	if err != nil {
		log.Printf("Warning: could not check existing snapshot: %v", err)
	}
	if hasSnapshot {
		fmt.Printf("Snapshot for %s already exists — will be replaced (INSERT OR REPLACE)\n", snapshotDate)
	}

	// Create client (real or mock)
	rl := cfg.Prices.RateLimits
	const pageSize = 1000

	var client PricesClient
	if *mockMode {
		mockClient := NewMockPricesClient()
		PopulateMockPrices(mockClient, 2500) // 2500 = 3 pages with limit 1000
		client = mockClient
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_KEY")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit("get_prices", rl.PricesList, rl.PricesListBurst, rl.PricesListApi, rl.PricesListApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.Prices.AdaptiveProbeAfter, cfg.Prices.MaxBackoffSeconds)
		client = wbClient
		fmt.Printf("API Key: %s\n", maskAPIKey(apiKey))
	}

	// Download prices
	result, err := DownloadPrices(ctx, client, repo.SavePrices, snapshotDate, rl.PricesList, rl.PricesListBurst, pageSize)
	if err != nil {
		log.Fatalf("Failed to download prices: %v", err)
	}

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Download complete!")
	fmt.Printf("  Products:  %d\n", result.TotalProducts)
	fmt.Printf("  Pages:     %d\n", result.Pages)
	fmt.Printf("  Requests:  %d\n", result.Requests)
	fmt.Printf("  Date:      %s\n", snapshotDate)
	fmt.Printf("  Duration:  %s\n", result.Duration.Round(time.Second))
	fmt.Printf("  Database:  %s\n", cfg.Prices.DbPath)

	count, _ := repo.CountPrices(ctx)
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
	return &Config{}
}

// getAPIKey retrieves API key with priority: configured env var > standard env vars > config value.
func getAPIKey(cfg *Config) string {
	envVar := cfg.Prices.APIKeyEnv
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func printHelp() {
	fmt.Print(`WB Product Prices Downloader — загрузка текущих цен товаров

Usage:
  go run . [options]

Options:
  --config PATH     Путь к конфигу (default: config.yaml)
  --db PATH         Путь к базе (overrides config)
  --clean           Clean database before download
  --mock            Mock mode (no API calls)
  --help            Справка

Examples:
  # Download current prices (snapshot)
  WB_API_KEY=xxx go run .

  # Mock mode (testing)
  go run . --mock

  # Custom database path
  WB_API_KEY=xxx go run . --db /path/to/prices.db

  # Clean and redownload
  WB_API_KEY=xxx go run . --clean

`)
}

func printHeader(cfg *Config, mock bool, snapshotDate string) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Product Prices Downloader")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.Prices.DbPath)
	fmt.Printf("Date:      %s\n", snapshotDate)
	if mock {
		fmt.Println("Mode:      Mock")
	}
	fmt.Println(strings.Repeat("=", 60))
}
