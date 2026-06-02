// download-wb-orders-v2 downloads order data from WB Statistics API.
//
// V2 architecture: business logic in pkg/orders/, this is a thin CLI driver (~130 lines).
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
	"github.com/ilkoid/poncho-ai/pkg/orders"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the orders v2 downloader.
type Config struct {
	WB      config.WBClientConfig     `yaml:"wb"`
	Orders  config.DownloadConfig     `yaml:"orders"`
	Storage config.V2StorageConfig    `yaml:"storage"`
	Filter  config.FunnelFilterConfig `yaml:"filter"`
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

	dllog.PrintHeader("WB Orders Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — КРИТИЧЕСКОЕ ОТЛИЧИЕ от cards/sales:
	// --mock mode creates DiscardWriter (zero DB interaction).
	// Writer creation is INSIDE the else branch — never opened when mocking.
	var writer orders.OrdersWriter
	var cleanup func()

	if *mockMode {
		writer = orders.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createOrdersWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source orders.OrdersSource
	if *mockMode {
		source = orders.NewMockOrdersSource(250)
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(orders.ToolID,
			cfg.WB.RateLimit, cfg.WB.BurstLimit,
			cfg.WB.RateLimit, cfg.WB.BurstLimit) // api floor = desired for orders (already slow)
		if cfg.Orders.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.Orders.AdaptiveProbeAfter, cfg.Orders.MaxBackoffSeconds)
		}
		source = orders.NewWBSource(wbClient, cfg.WB.RateLimit, cfg.WB.BurstLimit)
	}

	opts := orders.DownloadOptions{
		Days:    cfg.Orders.Days,
		From:    cfg.Orders.From,
		To:      cfg.Orders.To,
		Rewrite: cfg.Orders.Rewrite,
		DryRun:  *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "orders", msg, start)
			}
		}(),
	}

	dl := orders.NewDownloader(source, writer, opts, cfg.Filter)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d orders, %d pages",
		result.TotalOrders, result.TotalPages)
}

// createOrdersWriter creates the appropriate OrdersWriter based on backend config.
// Returns the writer and a cleanup function.
func createOrdersWriter(ctx context.Context, cfg config.V2StorageConfig) (orders.OrdersWriter, func(), error) {
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

		repo := postgres.NewPgOrdersRepo(pool.DB())
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

// resolveAPIKey resolves the WB Statistics API key from config.
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	// Default: WB_STAT_API_KEY (Statistics API uses a separate key)
	return os.Getenv("WB_STAT_API_KEY")
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	// Apply defaults
	if cfg.WB.RateLimit == 0 {
		cfg.WB.RateLimit = 1 // 1 req/min (WB Statistics API)
	}
	if cfg.WB.BurstLimit == 0 {
		cfg.WB.BurstLimit = 10
	}
	if cfg.Orders.Days == 0 {
		cfg.Orders.Days = 90
	}
	if cfg.Orders.AdaptiveRecoverAfter == 0 {
		cfg.Orders.AdaptiveRecoverAfter = 5
	}
	if cfg.Orders.AdaptiveProbeAfter == 0 {
		cfg.Orders.AdaptiveProbeAfter = 10
	}
	if cfg.Orders.MaxBackoffSeconds == 0 {
		cfg.Orders.MaxBackoffSeconds = 60
	}
	return &cfg, nil
}
