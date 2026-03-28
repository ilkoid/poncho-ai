// Package main provides WB Sales Downloader utility.
//
// Usage:
//
//	cd cmd/download-wb-sales
//	WB_API_KEY=your_key go run main.go
package main

import (
	"context"
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
// REFACTORED 2026-02-21: Uses pkg/config types instead of local duplicates.
type Config struct {
	WB       config.WBClientConfig  `yaml:"wb"`
	Download config.DownloadConfig  `yaml:"download"`
	Funnel   config.FunnelConfig    `yaml:"funnel"`
	Storage  config.StorageConfig   `yaml:"storage"`
}

func main() {
	// Parse flags
	mockMode := false
	cleanDB := false
	funnelMode := false
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "--mock" || arg == "-m" {
			mockMode = true
		}
		if arg == "--clean" || arg == "-c" {
			cleanDB = true
		}
		if arg == "--funnel" || arg == "-f" {
			funnelMode = true
		}
		if arg == "--help" || arg == "-h" {
			printHelp()
			return
		}
	}

	// Parse config file path (optional: default config.yaml)
	configPath := "config.yaml"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		configPath = args[0]
	}

	// Load configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	// Clean database if --clean flag is set
	if cleanDB {
		dbPath := cfg.Storage.DbPath
		if err := os.Remove(dbPath); err != nil {
			fmt.Printf("⚠️  Не удалось удалить %s: %v\n", dbPath, err)
		} else {
			fmt.Printf("🗑️  База удалена: %s\n", dbPath)
		}
	}

	// In mock mode, skip API key validation
	if !mockMode {
		// Validate API key
		if cfg.WB.APIKey == "" || cfg.WB.APIKey == "${WB_STAT}" {
			log.Fatal("❌ WB_STAT not set. Use: WB_STAT=your_key go run main.go")
		}
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
	printHeader(cfg)

	// Show download start time
	fmt.Printf("🕐 Начало загрузки: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(repeat("=", 71))

	// Parse date range (supports both YYYY-MM-DD and RFC3339 formats)
	from, err := ParseDateTime(cfg.Download.From)
	if err != nil {
		log.Fatalf("❌ Invalid from date %q: %v", cfg.Download.From, err)
	}
	to, err := ParseDateTime(cfg.Download.To)
	if err != nil {
		log.Fatalf("❌ Invalid to date %q: %v", cfg.Download.To, err)
	}

	// Set interval days from config (for handling large data volumes)
	if cfg.Download.IntervalDays > 0 {
		maxDaysPerPeriod = cfg.Download.IntervalDays
	}

	// Split period into intervals
	ranges := SplitPeriod(from, to)
	fmt.Printf("Интервалы:  %d (по %d дней)\n", len(ranges), maxDaysPerPeriod)
	fmt.Println(repeat("=", 71))

	// Create SalesRepository (needed for both real and mock mode)
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	// ========== FUNNEL MODE ==========
	if funnelMode {
		// Apply defaults for funnel config
		funnelDays := cfg.Funnel.Days
		if funnelDays <= 0 || funnelDays > 365 {
			funnelDays = 7
		}
		funnelBatchSize := cfg.Funnel.BatchSize
		if funnelBatchSize <= 0 || funnelBatchSize > 20 {
			funnelBatchSize = 20
		}

		fmt.Println("📊 FUNNEL MODE: Загрузка аналитики воронки")
		fmt.Printf("📅 Дней истории: %d\n", funnelDays)
		fmt.Println(repeat("=", 71))

		var funnelResult *FunnelLoadResult

		if mockMode {
			fmt.Println("🧪 MOCK MODE: Генерация тестовых данных")
			funnelResult, err = FunnelMockLoader(ctx, repo, funnelDays, cfg.Storage.FunnelRefreshWindow)
		} else {
			// Use Analytics API key (separate from Statistics API key)
			analyticsKey := cfg.WB.AnalyticsAPIKey
			if analyticsKey == "" || analyticsKey == "${WB_API_ANALYTICS_AND_PROMO_KEY}" {
				// Fallback to WB_STAT if analytics key not set
				analyticsKey = cfg.WB.APIKey
				fmt.Println("⚠️  WB_API_ANALYTICS_AND_PROMO_KEY not set, using WB_STAT (may fail)")
			}

			if analyticsKey == "" || analyticsKey == "${WB_STAT}" {
				log.Fatal("❌ No API key set. Use: WB_API_ANALYTICS_AND_PROMO_KEY=your_key go run main.go --funnel")
			}

			// Show which key is being used
			fmt.Printf("🔑 API Key: %s (Analytics)\n", maskAPIKey(analyticsKey))

			// Get funnel config with defaults
			funnelDefaults := cfg.Funnel.GetDefaults()

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

			// Pre-set adaptive rate limiter for funnel endpoint (see dev_limits.md)
			wbClient.SetRateLimit("get_wb_product_funnel_history",
				funnelDefaults.FunnelRateLimit, funnelDefaults.FunnelRateLimitBurst,
				funnelDefaults.FunnelRateLimitApi, funnelDefaults.FunnelRateLimitApiBurst)
			wbClient.SetAdaptiveParams(
				funnelDefaults.AdaptiveRecoverAfter,
				funnelDefaults.AdaptiveProbeAfter,
				funnelDefaults.MaxBackoffSeconds)

			// Load funnel history
			funnelCfg := FunnelLoaderConfig{
				Client:        wbClient,
				Repo:          repo,
				Days:          funnelDays,
				BatchSize:     funnelBatchSize,
				RefreshWindow: cfg.Storage.FunnelRefreshWindow,
				RateLimit:     funnelDefaults.FunnelRateLimit,
				Burst:         funnelDefaults.FunnelRateLimitBurst,
			}
			funnelResult, err = LoadFunnelHistory(ctx, funnelCfg)
		}

		if err != nil {
			log.Fatalf("❌ Funnel loading failed: %v", err)
		}

		PrintFunnelSummary(funnelResult, repo)
		return
	}

	// ========== SALES MODE ==========
	// Show resume status if enabled
	if cfg.Download.Resume {
		ctxCount, cancelCount := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCount()
		if count, err := repo.Count(ctxCount); err == nil && count > 0 {
			fmt.Printf("Resume:      true (база содержит %d строк)\n", count)
		}
	}

	var result *DownloadResult

	if mockMode {
		// Mock mode: use generated data instead of API calls
		fmt.Println("🧪 MOCK MODE: Симуляция данных (без API вызовов)")
		result, err = DownloadSalesWithMock(ctx, repo, ranges)
		if err != nil {
			log.Fatalf("❌ Mock download failed: %v", err)
		}
	} else {
		// Real mode: create WB client and fetch from API
		// Dependency Injection Container (Rule 6: DIP)
		wbCfg := config.WBConfig{
			APIKey:        cfg.WB.APIKey,
			RateLimit:     cfg.WB.RateLimit,
			BurstLimit:    cfg.WB.BurstLimit,
			Timeout:       cfg.WB.Timeout,
			RetryAttempts: 3, // Default
		}
		wbCfg = wbCfg.GetDefaults() // Apply defaults for empty fields
		wbClient, err := wb.NewFromConfig(wbCfg)
		if err != nil {
			log.Fatalf("❌ Failed to create WB client: %v", err)
		}

		// Download configuration
		downloadCfg := DownloadConfig{
			Client:    wbClient,
			Repo:      repo,
			RateLimit: cfg.WB.RateLimit,
			Burst:     cfg.WB.BurstLimit,
		}

		// Execute download
		result, err = DownloadSales(ctx, downloadCfg, ranges, cfg.Download.Resume)
		if err != nil {
			log.Fatalf("❌ Download failed: %v", err)
		}
	}

	// Get actual total count from database (for resume mode)
	ctxCount, cancelCount := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCount()
	if totalCount, err := repo.Count(ctxCount); err == nil {
		result.TotalRows = totalCount // Use actual database count
	}

	// Print summary with delivery method analysis
	PrintSummary(result, repo)
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
func printHeader(cfg *Config) {
	fmt.Println("📥 WB Sales Downloader")
	fmt.Println(repeat("=", 71))
	fmt.Printf("Config:     config.yaml\n")
	fmt.Printf("Период:     %s → %s\n", cfg.Download.From, cfg.Download.To)
	fmt.Printf("База:       %s\n", cfg.Storage.DbPath)
	fmt.Printf("FBW only:   %v\n", cfg.Download.FBWOnly)
	fmt.Printf("Resume:     %v\n", cfg.Download.Resume)
	fmt.Printf("API Key:    %s\n", maskAPIKey(cfg.WB.APIKey))
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
	fmt.Println("📥 WB Sales Downloader - Загрузка данных о продажах и аналитике Wildberries")
	fmt.Println()
	fmt.Println("ИСПОЛЬЗОВАНИЕ:")
	fmt.Println("  go run . [опции]")
	fmt.Println()
	fmt.Println("ОПЦИИ:")
	fmt.Println("  --clean, -c      Удалить БД перед загрузкой (чистая база)")
	fmt.Println("  --mock, -m       Режим симуляции (генерация моковых данных без API вызовов)")
	fmt.Println("  --funnel, -f     Загрузить данные воронки аналитики (вместо продаж)")
	fmt.Println("  --help, -h       Показать справку")
	fmt.Println()
	fmt.Println("РЕЖИМЫ:")
	fmt.Println("  По умолчанию     Загрузка продаж (Statistics API)")
	fmt.Println("  --funnel         Загрузка воронки аналитики (Analytics API v3)")
	fmt.Println()
	fmt.Println("ПЕРЕМЕННЫЕ ОКРУЖЕНИЯ:")
	fmt.Println("  WB_STAT                        API токен Statistics API (продажи)")
	fmt.Println("  WB_API_ANALYTICS_AND_PROMO_KEY API токен Analytics API (воронка)")
	fmt.Println()
	fmt.Println("КОНФИГУРАЦИЯ (config.yaml):")
	fmt.Println("  download.from/to   Период загрузки продаж")
	fmt.Println("  storage.db_path    Путь к SQLite базе")
	fmt.Println("  funnel.days        Дней истории для воронки (1-365)")
	fmt.Println("  funnel.batch_size  Товаров на запрос (max 20)")
	fmt.Println()
	fmt.Println("ПРИМЕРЫ:")
	fmt.Println("  # Загрузка продаж")
	fmt.Println("  WB_STAT=your_key go run .")
	fmt.Println()
	fmt.Println("  # Загрузка воронки аналитики")
	fmt.Println("  WB_API_ANALYTICS_AND_PROMO_KEY=your_key go run . --funnel")
	fmt.Println()
	fmt.Println("  # Режим симуляции воронки (без API ключа)")
	fmt.Println("  go run . --funnel --mock")
}
