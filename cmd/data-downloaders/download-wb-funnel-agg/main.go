// Package main provides WB Aggregated Funnel Downloader utility.
//
// Утилита для загрузки агрегированной воронки Wildberries (Analytics API v3).
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
	"gopkg.in/yaml.v3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config represents YAML configuration.
type Config struct {
	WB                config.WBClientConfig         `yaml:"wb"`
	FunnelAggregated config.FunnelAggregatedConfig `yaml:"funnel_aggregated"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	// Validate API key
	apiKey := cfg.WB.AnalyticsAPIKey
	if apiKey == "" || apiKey == "${WB_API_ANALYTICS_AND_PROMO_KEY}" {
		apiKey = cfg.WB.APIKey
		if apiKey != "" && apiKey != "${WB_STAT}" {
			fmt.Println("⚠️  WB_API_ANALYTICS_AND_PROMO_KEY not set, using WB_STAT (may fail)")
		}
	}
	if apiKey == "" || apiKey == "${WB_API_ANALYTICS_AND_PROMO_KEY}" || apiKey == "${WB_STAT}" {
		log.Fatal("❌ No API key. Use: WB_API_ANALYTICS_AND_PROMO_KEY=your_key go run .")
	}

	// Context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано")
		cancel()
	}()

	printHeader(cfg, *configPath)

	// Show download start time
	fmt.Printf("🕐 Начало загрузки: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(repeat("=", 71))

	// Create repository
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.FunnelAggregated.DBPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	// Apply defaults
	defaults := cfg.FunnelAggregated.GetDefaults()

	fmt.Printf("📊 AGGREGATED FUNNEL MODE: Загрузка агрегированных данных\n")
	fmt.Printf("📅 Selected период: %s → %s\n", defaults.SelectedStart, defaults.SelectedEnd)
	if defaults.PastStart != "" && defaults.PastEnd != "" {
		fmt.Printf("📅 Past период:    %s → %s\n", defaults.PastStart, defaults.PastEnd)
	}
	fmt.Printf("📦 Размер страницы: %d товаров\n", defaults.PageSize)
	fmt.Printf("⏱️  Rate limit: %d req/min, burst: %d\n", defaults.RateLimit, defaults.BurstLimit)
	fmt.Println(repeat("=", 71))

	// Show which key is being used
	fmt.Printf("🔑 API Key: %s (Analytics)\n", maskAPIKey(apiKey))

	// Create WB client
	wbClient := wb.New(apiKey)
	// Setup adaptive rate limiting
	rl := defaults.RateLimits
	wbClient.SetRateLimit("get_wb_funnel_aggregated",
		rl.FunnelAggregated, rl.FunnelAggregatedBurst,
		rl.FunnelAggregatedApi, rl.FunnelAggregatedApiBurst)
	wbClient.SetAdaptiveParams(
		0, // adaptive_recover_after: deprecated
		defaults.AdaptiveProbeAfter,
		defaults.MaxBackoffSeconds,
	)

	// Load data
	loaderCfg := AggregatedLoaderConfig{
		Client: wbClient,
		Repo:   repo,
		Config: defaults,
	}

	result, err := LoadAggregatedFunnel(ctx, loaderCfg)
	if err != nil {
		log.Fatalf("❌ Loading failed: %v", err)
	}

	PrintAggregatedSummary(result, defaults.SelectedStart, defaults.SelectedEnd)
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
	fmt.Println("📥 WB Aggregated Funnel Downloader - Загрузка агрегированной воронки")
	fmt.Println(repeat("=", 71))
	fmt.Printf("Config:     %s\n", configPath)
	fmt.Printf("База:       %s\n", cfg.FunnelAggregated.DBPath)
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
	fmt.Println("📥 WB Aggregated Funnel Downloader - Загрузка агрегированной воронки Wildberries")
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
	fmt.Println("  funnel_aggregated.selected_start    Начало периода YYYY-MM-DD (required)")
	fmt.Println("  funnel_aggregated.selected_end      Конец периода YYYY-MM-DD (required)")
	fmt.Println("  funnel_aggregated.past_start        Начало past периода (optional)")
	fmt.Println("  funnel_aggregated.past_end          Конец past периода (optional)")
	fmt.Println("  funnel_aggregated.page_size         Товаров за запрос (default: 100)")
	fmt.Println("  funnel_aggregated.rate_limit        Запросов в минуту (default: 3)")
	fmt.Println("  funnel_aggregated.db_path           Путь к SQLite базе")
	fmt.Println()
	fmt.Println("ПРИМЕРЫ:")
	fmt.Println("  # С конфигом по умолчанию")
	fmt.Println("  WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run .")
	fmt.Println()
	fmt.Println("  # С кастомным конфигом")
	fmt.Println("  go run . --config /path/to/config.yaml")
}
