// download-wb-stock-products-v2 downloads product-level stock metrics from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/stockproducts/, this is a thin CLI driver (~130 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Endpoint: POST /api/v2/stocks-report/products/products
// Rate limit: 3 req/min (shared with other stocks-report/search-report endpoints).
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                    # mock mode, no DB
//	go run . --dry-run --config config.yaml            # real API, no writes
//	go run . --config config.yaml                      # production (user only!)
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

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/stockproducts"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the stock products v2 downloader.
type Config struct {
	WB            config.WBClientConfig       `yaml:"wb"`
	StockProducts config.StockProductsConfig  `yaml:"stock_products"`
	Storage       config.V2StorageConfig      `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	dateFlag := flag.String("date", "", "Snapshot date YYYY-MM-DD (default: yesterday)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Apply defaults — single call per config type (rules.md Фаза 4)
	cfg.Storage = cfg.Storage.GetDefaults()
	spCfg := cfg.StockProducts.GetDefaults()

	// CLI flag overrides
	if *backend != "" {
		cfg.Storage.Backend = *backend
	}
	if *dbPath != "" {
		cfg.Storage.DbPath = *dbPath
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}

	// Default date = yesterday (rules.md: today's data is incomplete)
	snapshotDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if *dateFlag != "" {
		snapshotDate = *dateFlag
	}

	dllog.PrintHeader("WB Stock Products Downloader v2",
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Date", Value: snapshotDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch
	var writer stockproducts.StockProductsWriter
	var cleanup func()

	if *mockMode {
		writer = stockproducts.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createBackend(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source stockproducts.StockProductsSource
	if *mockMode {
		source = stockproducts.NewMockStockProductsSource()
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)

		// Set rate limits explicitly — wb.New() does NOT configure rate limiting
		rl := spCfg.RateLimits
		wbClient.SetRateLimit(wb.ToolIDStockProducts,
			rl.StockProducts, rl.StockProductsBurst,
			rl.StockProductsApi, rl.StockProductsApiBurst)
		wbClient.SetAdaptiveParams(0, spCfg.AdaptiveProbeAfter, spCfg.MaxBackoffSeconds)

		source = stockproducts.NewWBSource(wbClient, rl.StockProducts, rl.StockProductsBurst)
	}

	opts := stockproducts.DownloadOptions{
		SnapshotDate: snapshotDate,
		PeriodStart:  snapshotDate,
		PeriodEnd:    snapshotDate,
		PageSize:     spCfg.PageSize,
		DryRun:       *dryRun,
		RateLimit:    spCfg.RateLimits.StockProducts,
		Burst:        spCfg.RateLimits.StockProductsBurst,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "stockproducts", msg, start)
			}
		}(),
	}

	dl := stockproducts.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "rows=%d pages=%d", result.TotalRows, result.Pages)
}

// createBackend creates a StockProductsWriter from the selected backend.
// Returns writer, cleanup function, and error.
func createBackend(ctx context.Context, cfg config.V2StorageConfig) (
	stockproducts.StockProductsWriter, func(), error) {
	switch cfg.Backend {
	case "postgres", "postgresql":
		dsn, err := cfg.GetEffectiveDSN()
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres DSN: %w", err)
		}

		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres pool: %w", err)
		}

		repo := postgres.NewPgStockProductsRepo(pool.DB())
		if err := repo.InitSchema(ctx); err != nil {
			pool.Close()
			return nil, func() {}, fmt.Errorf("postgres schema: %w", err)
		}
		return repo, pool.Close, nil

	default: // "sqlite"
		repo, err := sqlite.NewSQLiteSalesRepository(cfg.DbPath)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open SQLite: %w", err)
		}
		return repo, func() { repo.Close() }, nil
	}
}

// resolveAPIKey resolves the WB API key from config and environment.
// Uses cfg.WB.APIKeyEnv (NOT cfg.StockProducts.APIKeyEnv — Bug from cards-v2).
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.WB.APIKeyEnv != "" {
		if key := os.Getenv(cfg.WB.APIKeyEnv); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return ""
}

// loadConfig reads YAML config and applies defaults.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
