// download-wb-prices-v2 downloads product prices from WB Discounts-Prices API.
//
// V2 architecture: business logic in pkg/prices/, this is a thin CLI driver (~150 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
// This is a critical safety improvement over cards/sales downloaders.
//
// Usage:
//
//	go run . --mock                                    # mock mode, no DB
//	go run . --mock --db /tmp/test.db                  # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test  # mock + test PG
//	go run . --dry-run --db /tmp/test.db --config ...  # real API, no writes
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
	"github.com/ilkoid/poncho-ai/pkg/prices"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the prices v2 downloader.
type Config struct {
	WB      config.WBClientConfig  `yaml:"wb"`
	Prices  pricesConfig           `yaml:"prices"`
	Storage config.V2StorageConfig `yaml:"storage"`
}

// pricesConfig holds prices-specific settings.
// Prices API uses offset-based pagination (snapshot on moment of run, no date range).
type pricesConfig struct {
	APIKeyEnv          string `yaml:"api_key_env"`
	RateLimit          int    `yaml:"rate_limit"`
	BurstLimit         int    `yaml:"burst_limit"`
	APIRateLimit       int    `yaml:"api_rate_limit"`
	APIBurstLimit      int    `yaml:"api_burst_limit"`
	AdaptiveProbeAfter int    `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int    `yaml:"max_backoff_seconds"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()

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

	snapshotDate := time.Now().Format("2006-01-02")

	dllog.PrintHeader("WB Prices Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Snapshot", Value: snapshotDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — КРИТИЧЕСКОЕ ОТЛИЧИЕ от cards/sales:
	// --mock mode creates DiscardWriter (zero DB interaction).
	// Writer creation is INSIDE the else branch — never opened when mocking.
	var writer prices.PricesWriter
	var cleanup func()

	if *mockMode {
		writer = prices.NewDiscardWriter()
		cleanup = func() {}
	} else {
		writer, cleanup, err = createPricesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source prices.PricesSource
	if *mockMode {
		source = prices.NewMockPricesSource(2500)
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(prices.ToolID,
			cfg.Prices.RateLimit, cfg.Prices.BurstLimit,
			cfg.Prices.APIRateLimit, cfg.Prices.APIBurstLimit)
		if cfg.Prices.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.Prices.AdaptiveProbeAfter, cfg.Prices.MaxBackoffSeconds)
		}
		source = prices.NewWBSource(wbClient, cfg.Prices.RateLimit, cfg.Prices.BurstLimit)
	}

	opts := prices.DownloadOptions{
		SnapshotDate: snapshotDate,
		DryRun:       *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "prices", msg, start)
			}
		}(),
	}

	dl := prices.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d prices, %d pages, %d requests",
		result.TotalProducts, result.Pages, result.Requests)
}

// createPricesWriter creates the appropriate PricesWriter based on backend config.
// Returns the writer and a cleanup function.
func createPricesWriter(ctx context.Context, cfg config.V2StorageConfig) (prices.PricesWriter, func(), error) {
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

		repo := postgres.NewPgPricesRepo(pool.DB())
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

// resolveAPIKey resolves the WB API key from config.
// Priority: wb.api_key > prices.api_key_env > WB_API_KEY env var.
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.Prices.APIKeyEnv != "" {
		return os.Getenv(cfg.Prices.APIKeyEnv)
	}
	return os.Getenv("WB_API_KEY")
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	// Apply defaults — two-level adaptive rate limiting
	// Swagger: 10 req/6sec (~100 req/min), burst 5
	if cfg.Prices.APIRateLimit == 0 {
		cfg.Prices.APIRateLimit = 100
	}
	if cfg.Prices.APIBurstLimit == 0 {
		cfg.Prices.APIBurstLimit = 5
	}
	if cfg.Prices.RateLimit == 0 {
		cfg.Prices.RateLimit = cfg.Prices.APIRateLimit
	}
	if cfg.Prices.BurstLimit == 0 {
		cfg.Prices.BurstLimit = cfg.Prices.APIBurstLimit
	}
	if cfg.Prices.APIKeyEnv == "" {
		cfg.Prices.APIKeyEnv = "WB_API_KEY"
	}
	if cfg.Prices.AdaptiveProbeAfter == 0 {
		cfg.Prices.AdaptiveProbeAfter = 10
	}
	if cfg.Prices.MaxBackoffSeconds == 0 {
		cfg.Prices.MaxBackoffSeconds = 60
	}
	return &cfg, nil
}
