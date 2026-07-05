package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config — конфигурация утилиты.
type Config struct {
	WB      config.WBClientConfig `yaml:"wb"`
	Sync    SyncConfig            `yaml:"sync"`
	Filters Filters               `yaml:"filters"` // не из YAML, для CLI
}

// SyncConfig — настройки синхронизации.
type SyncConfig struct {
	DbPath           string `yaml:"db_path"`
	APIKeyEnv        string `yaml:"api_key_env"`
	BatchSize        int    `yaml:"batch_size"`
	UpdatePerMin     int    `yaml:"update_per_min"`
	UpdateBurst      int    `yaml:"update_burst"`
	UpdateApiFloor   int    `yaml:"update_api_floor"`
	UpdateApiBurst   int    `yaml:"update_api_burst"`
	AdaptiveProbeAfter int  `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int  `yaml:"max_backoff_seconds"`
}

// GetDefaults возвращает дефолтные значения.
func (c SyncConfig) GetDefaults() SyncConfig {
	r := c
	if r.DbPath == "" {
		r.DbPath = "/var/db/wb-sales.db"
	}
	if r.APIKeyEnv == "" {
		r.APIKeyEnv = "WB_API_KEY"
	}
	if r.BatchSize == 0 {
		r.BatchSize = 3000
	}
	if r.UpdatePerMin == 0 {
		r.UpdatePerMin = 10
	}
	if r.UpdateBurst == 0 {
		r.UpdateBurst = 5
	}
	if r.UpdateApiFloor == 0 {
		r.UpdateApiFloor = 10
	}
	if r.UpdateApiBurst == 0 {
		r.UpdateApiBurst = 5
	}
	if r.AdaptiveProbeAfter == 0 {
		r.AdaptiveProbeAfter = 10
	}
	if r.MaxBackoffSeconds == 0 {
		r.MaxBackoffSeconds = 60
	}
	return r
}

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	apply := flag.Bool("apply", false, "применить изменения (default: dry-run)")
	dryRun := flag.Bool("dry-run", false, "показать полный WB payload без отправки")
	article := flag.String("article", "", "фильтр по артикулу поставщика")
	nmID := flag.Int("nm-id", 0, "фильтр по nmID")
	nds := flag.Int("nds", 0, "фильтр по ставке 1C (10 или 22)")
	mock := flag.Bool("mock", false, "mock режим (без API вызовов)")
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
		cfg = &Config{}
	}
	cfg.WB = cfg.WB.GetDefaults()
	cfg.Sync = cfg.Sync.GetDefaults()

	// Apply CLI overrides
	if *dbPath != "" {
		cfg.Sync.DbPath = *dbPath
	}

	// Build filters
	cfg.Filters = Filters{
		Article: *article,
		NmID:    *nmID,
		NDS:     *nds,
	}

	// Print header
	mode := "DRY-RUN"
	if *apply && !*mock {
		mode = "APPLY"
	} else if *dryRun {
		mode = "DRY-RUN (payload dump)"
	}
	printHeader(cfg, mode, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано")
		cancel()
	}()

	// Open database
	db, err := sql.Open("sqlite3", cfg.Sync.DbPath+"?mode=ro")
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer db.Close()

	// Create client (real or mock)
	var client SyncClient
	if *mock {
		client = &mockSyncClient{}
		fmt.Println("Mode:      Mock (no API calls)")
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" && *apply {
			log.Fatal("❌ No API key. Set WB_API_KEY or configure api_key_env")
		}
		if apiKey == "" {
			fmt.Println("⚠️  No API key — dry-run only (set WB_API_KEY for --apply)")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit("cards_content",
			cfg.Sync.UpdatePerMin, cfg.Sync.UpdateBurst,
			cfg.Sync.UpdateApiFloor, cfg.Sync.UpdateApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.Sync.AdaptiveProbeAfter, cfg.Sync.MaxBackoffSeconds)
		client = wbClient
		if apiKey != "" {
			fmt.Printf("API Key:   %s\n", maskAPIKey(apiKey))
		}
	}
	fmt.Println()

	// Run sync
	result, err := RunSync(ctx, db, client, cfg, *apply && !*mock, *dryRun, cfg.Filters)
	if err != nil {
		log.Fatalf("❌ Sync failed: %v", err)
	}

	// Summary
	printSummary(result, *apply && !*mock)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func getAPIKey(cfg *Config) string {
	envVar := cfg.Sync.APIKeyEnv
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
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

func printHeader(cfg *Config, mode string, mock bool) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Cards VAT Sync — синхронизация НДС из 1C")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.Sync.DbPath)
	fmt.Printf("Mode:      %s\n", mode)
	fmt.Printf("BatchSize: %d\n", cfg.Sync.BatchSize)
	fmt.Println(strings.Repeat("=", 60))
}

func printHelp() {
	fmt.Print(`WB Cards VAT Sync — синхронизация ставки НДС из 1C на WB карточки

Usage:
  go run . [options]

Options:
  --config PATH     Путь к конфигу (default: config.yaml)
  --db PATH         Путь к базе (overrides config)
  --apply           Применить изменения (default: dry-run)
  --dry-run         Показать полный WB payload (vendorCode/brand/.../sizes) без отправки.
                    Проверка что rewrite безопасен (частичный payload обнулил бы карточку).
  --article VALUE   Фильтр по артикулу поставщика
  --nm-id VALUE     Фильтр по nmID
  --nds 10|22       Фильтр по ставке 1C
  --mock            Mock режим (без API вызовов)
  --help            Справка

Examples:
  # Dry-run: показать расхождения
  go run . --db /var/db/wb-sales.db

  # Проверить полный payload для одной карточки перед --apply
  go run . --nm-id 12345678 --dry-run --db /var/db/wb-sales.db

  # Фильтр по одному артикулу
  go run . --article 126210 --db /var/db/wb-sales.db

  # Применить изменения для одной карточки
  go run . --nm-id 12345678 --apply --db /var/db/wb-sales.db

  # Массовое обновление
  WB_API_KEY=xxx go run . --apply --db /var/db/wb-sales.db

`)
}

// mockSyncClient — mock для --mock режима.
type mockSyncClient struct{}

func (m *mockSyncClient) UpdateCards(_ context.Context, _ string, _, _ int, cards []wb.CardUpdateItem) (string, string, error) {
	fmt.Printf("  [MOCK] Would update %d cards\n", len(cards))
	return "", "", nil
}
