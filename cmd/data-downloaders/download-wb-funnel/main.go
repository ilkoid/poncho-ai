// Package main provides WB Funnel Downloader utility.
//
// Утилита для загрузки аналитики воронки Wildberries (Analytics API v3).
//
// ИСПОЛЬЗОВАНИЕ:
//
//	go run . --config config.yaml
//	WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run .
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration structure.
type Config struct {
	WB      config.WBClientConfig `yaml:"wb"`
	Funnel  config.FunnelConfig   `yaml:"funnel"`
	Storage StorageConfig         `yaml:"storage"`
}

// StorageConfig holds storage-related configuration.
type StorageConfig struct {
	DBPath              string `yaml:"db_path"`               // Путь к БД с таблицей sales
	FunnelRefreshWindow int    `yaml:"funnel_refresh_window"` // Дней для обновления (0 = всегда REPLACE)
}

func main() {
	// Parse flags
	configPath := flag.String("config", "config.yaml", "путь к YAML конфигу")
	help := flag.Bool("help", false, "показать справку")
	flag.BoolVar(help, "h", false, "показать справку")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	// Validate API key
	analyticsKey := cfg.WB.AnalyticsAPIKey
	if analyticsKey == "" || analyticsKey == "${WB_API_ANALYTICS_AND_PROMO_KEY}" {
		// Fallback to APIKey if analytics key not set
		analyticsKey = cfg.WB.APIKey
		if analyticsKey != "" && analyticsKey != "${WB_STAT}" {
			fmt.Println("⚠️  WB_API_ANALYTICS_AND_PROMO_KEY not set, using WB_STAT (may fail)")
		}
	}

	if analyticsKey == "" || analyticsKey == "${WB_API_ANALYTICS_AND_PROMO_KEY}" || analyticsKey == "${WB_STAT}" {
		log.Fatal("❌ No API key set. Use: WB_API_ANALYTICS_AND_PROMO_KEY=your_key go run .")
	}

	// Create context with cancellation for Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано пользователем")
		cancel()
	}()

	// Print header
	printHeader(cfg, *configPath)

	// Show download start time
	fmt.Printf("🕐 Начало загрузки: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(repeat("=", 71))

	// Create SalesRepository
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	// Apply defaults for funnel config
	funnelDefaults := cfg.Funnel.GetDefaults()
	funnelDays := funnelDefaults.Days
	funnelBatchSize := funnelDefaults.BatchSize

	fmt.Printf("📊 FUNNEL MODE: Загрузка аналитики воронки\n")
	// Period info is shown in LoadFunnelHistory (handles from/to vs days)
	fmt.Printf("📦 Batch size: %d товаров\n", funnelBatchSize)
	fmt.Printf("⏱️  Rate limit: %d req/min (desired), %d req/min (api floor), burst: %d\n",
		funnelDefaults.FunnelRateLimit, funnelDefaults.FunnelRateLimitApi, funnelDefaults.FunnelRateLimitBurst)
	fmt.Println(repeat("=", 71))

	// Show which key is being used
	fmt.Printf("🔑 API Key: %s (Analytics)\n", maskAPIKey(analyticsKey))

	// Create WB client for Analytics API with funnel-specific rate limits
	wbCfg := config.WBConfig{
		APIKey:        analyticsKey,
		RateLimit:     funnelDefaults.RateLimit,
		BurstLimit:    funnelDefaults.BurstLimit,
		Timeout:       cfg.WB.Timeout,
		RetryAttempts: 3,
	}
	wbCfg = wbCfg.GetDefaults()
	wbClient, err := wb.NewFromConfig(wbCfg)
	if err != nil {
		log.Fatalf("❌ Failed to create WB client: %v", err)
	}

	// Apply two-level adaptive rate limiting (see dev_limits.md)
	wbClient.SetRateLimit("get_wb_product_funnel_history",
		funnelDefaults.FunnelRateLimit, funnelDefaults.FunnelRateLimitBurst,
		funnelDefaults.FunnelRateLimitApi, funnelDefaults.FunnelRateLimitApiBurst)
	wbClient.SetAdaptiveParams(
		0, // adaptive_recover_after: deprecated, limiter drops to api floor immediately
		funnelDefaults.AdaptiveProbeAfter,
		funnelDefaults.MaxBackoffSeconds)

	// Load funnel history
	funnelCfg := FunnelLoaderConfig{
		Client:        wbClient,
		Repo:          repo,
		Days:          funnelDays,
		BatchSize:     funnelBatchSize,
		RateLimit:     funnelDefaults.FunnelRateLimit,
		BurstLimit:    funnelDefaults.FunnelRateLimitBurst,
		RefreshWindow: cfg.Storage.FunnelRefreshWindow,
		From:          funnelDefaults.From,
		To:            funnelDefaults.To,
		MaxBatches:    funnelDefaults.MaxBatches,
	}
	funnelResult, err := LoadFunnelHistory(ctx, funnelCfg)
	if err != nil {
		log.Fatalf("❌ Funnel loading failed: %v", err)
	}

	PrintFunnelSummary(funnelResult, repo)
}

// loadConfig loads configuration from YAML file with ENV expansion.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables like ${VAR}
	dataWithEnv := []byte(os.ExpandEnv(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(dataWithEnv, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// printHeader prints utility header with configuration info.
func printHeader(cfg *Config, configPath string) {
	fmt.Println("📥 WB Funnel Downloader - Загрузка аналитики воронки")
	fmt.Println(repeat("=", 71))
	fmt.Printf("Config:     %s\n", configPath)
	fmt.Printf("База:       %s\n", cfg.Storage.DBPath)
	fmt.Printf("Refresh:    %d дней\n", cfg.Storage.FunnelRefreshWindow)
}

// maskAPIKey hides most of the API key for security.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// printHelp prints usage information.
func printHelp() {
	fmt.Println("📥 WB Funnel Downloader - Загрузка аналитики воронки Wildberries")
	fmt.Println()
	fmt.Println("ИСПОЛЬЗОВАНИЕ:")
	fmt.Println("  go run . [опции]")
	fmt.Println()
	fmt.Println("ОПЦИИ:")
	fmt.Println("  --config PATH   Путь к YAML конфигу (default: config.yaml)")
	fmt.Println("  --help, -h      Показать справку")
	fmt.Println()
	fmt.Println("ПЕРЕМЕННЫЕ ОКРУЖЕНИЯ:")
	fmt.Println("  WB_API_ANALYTICS_AND_PROMO_KEY   API токен Analytics API")
	fmt.Println()
	fmt.Println("КОНФИГУРАЦИЯ (config.yaml):")
	fmt.Println("  funnel.days              Дней истории (1-365)")
	fmt.Println("  funnel.batch_size        Товаров на запрос (max 20)")
	fmt.Println("  funnel.rate_limit        Запросов в минуту (default: 3)")
	fmt.Println("  funnel.burst             Burst для rate limiter (default: 3)")
	fmt.Println("  storage.db_path          Путь к SQLite базе с таблицей sales")
	fmt.Println("  storage.funnel_refresh_window  Дней для обновления (0 = всегда REPLACE)")
	fmt.Println()
	fmt.Println("ПРИМЕРЫ:")
	fmt.Println("  # С конфигом по умолчанию")
	fmt.Println("  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run .")
	fmt.Println()
	fmt.Println("  # С кастомным конфигом")
	fmt.Println("  go run . --config /path/to/config.yaml")
}
