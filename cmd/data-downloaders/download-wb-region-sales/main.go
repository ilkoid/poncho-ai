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

// Config wraps configuration for the region sales downloader.
type Config struct {
	WB           config.WBClientConfig    `yaml:"wb"`
	RegionSales  config.RegionSalesConfig `yaml:"region_sales"`
}

func main() {
	// 1. Parse flags
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	days := flag.Int("days", 0, "дней от сегодня (default: 7)")
	begin := flag.String("begin", "", "начало периода YYYY-MM-DD")
	end := flag.String("end", "", "конец периода YYYY-MM-DD")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	date := flag.String("date", "", "одна дата YYYY-MM-DD (begin=end)")
	mockMode := flag.Bool("mock", false, "mock mode (no API calls)")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
	flag.Parse()
	if *help {
		printHelp()
		return
	}

	// 2. Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// 3. Apply defaults
	cfg.RegionSales = cfg.RegionSales.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// 4. Apply CLI overrides
	if *dbPath != "" {
		cfg.RegionSales.DbPath = *dbPath
	}
	if *days > 0 {
		cfg.RegionSales.Days = *days
	}
	if *begin != "" {
		cfg.RegionSales.Begin = *begin
	}
	if *end != "" {
		cfg.RegionSales.End = *end
	}
	// --date overrides begin/end (single date mode)
	if *date != "" {
		cfg.RegionSales.Begin = *date
		cfg.RegionSales.End = *date
	}

	// 5. Calculate dates: CLI flags > config begin/end > config days > default 7
	if cfg.RegionSales.Begin == "" || cfg.RegionSales.End == "" {
		if cfg.RegionSales.Days == 0 {
			cfg.RegionSales.Days = 7
		}
		b, e := calculateDateRange(cfg.RegionSales.Days)
		cfg.RegionSales.Begin = b
		cfg.RegionSales.End = e
	}

	// 6. Print header
	printHeader(cfg, *mockMode)

	// 7. Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано")
		cancel()
	}()

	// 8. Open database
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.RegionSales.DbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	// 9. Create client (real or mock)
	rl := cfg.RegionSales.RateLimits

	var client RegionSalesClient
	if *mockMode {
		mockClient := NewMockRegionSalesClient()
		PopulateMockRegionSales(mockClient, 20, 5)
		client = mockClient
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("❌ No API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit("get_region_sale", rl.RegionSale, rl.RegionSaleBurst, rl.RegionSaleApi, rl.RegionSaleApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.RegionSales.AdaptiveProbeAfter, cfg.RegionSales.MaxBackoffSeconds)
		client = wbClient
		fmt.Printf("API Key: %s\n", maskAPIKey(apiKey))
	}

	// 10. Download data
	result, err := DownloadRegionSales(ctx, client, repo, cfg.RegionSales.Begin, cfg.RegionSales.End, rl.RegionSale, rl.RegionSaleBurst)
	if err != nil {
		log.Fatalf("❌ Failed to download region sales: %v", err)
	}

	// 11. Summary
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✅ Download complete!")
	fmt.Printf("  Period:    %s to %s\n", cfg.RegionSales.Begin, cfg.RegionSales.End)
	fmt.Printf("  Rows:      %d\n", result.TotalRows)
	fmt.Printf("  Requests:  %d\n", result.Requests)
	fmt.Printf("  Duration:  %s\n", result.Duration.Round(time.Second))
	fmt.Printf("  Database:  %s\n", cfg.RegionSales.DbPath)

	count, _ := repo.CountRegionSales(ctx)
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
// Pattern B from dev_downloader_development.md.
func getAPIKey(cfg *Config) string {
	envVar := cfg.RegionSales.APIKeyEnv
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

// calculateDateRange computes date range from today.
// days=7 means last 7 complete days, excluding today.
func calculateDateRange(days int) (string, string) {
	now := time.Now()
	endDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	beginDate := now.AddDate(0, 0, -days).Format("2006-01-02")
	return beginDate, endDate
}

func printHelp() {
	fmt.Print(`WB Region Sales Downloader — загрузка данных о продажах по регионам

Usage:
  go run . [options]

Options:
  --config PATH     Путь к конфигу (default: config.yaml)
  --days N          Дней от сегодня (default: 7)
  --begin DATE      Начало периода YYYY-MM-DD
  --end DATE        Конец периода YYYY-MM-DD
  --date DATE       Одна дата YYYY-MM-DD (begin=end)
  --db PATH         Путь к базе (overrides config)
  --mock            Mock mode (no API calls)
  --help            Справка

Examples:
  # Последние 7 дней (default)
  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run .

  # Одна дата
  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run . --date=2026-03-28

  # Диапазон
  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run . --begin=2026-03-01 --end=2026-03-31

  # 90 дней (auto-split на 31-дневные чанки)
  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run . --days=90

  # Mock mode
  go run . --mock

`)
}

func printHeader(cfg *Config, mock bool) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Region Sales Downloader")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.RegionSales.DbPath)
	fmt.Printf("Period:    %s to %s\n", cfg.RegionSales.Begin, cfg.RegionSales.End)
	if mock {
		fmt.Println("Mode:      Mock")
	}
	fmt.Println(strings.Repeat("=", 60))
}
