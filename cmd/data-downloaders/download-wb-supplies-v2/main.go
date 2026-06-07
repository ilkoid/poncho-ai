// download-wb-supplies-v2 downloads FBW supply data from WB Supplies API.
//
// V2 architecture: business logic in pkg/supplies/, this is a thin CLI driver (~140 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Covers 3 phases: reference → supplies list → per-supply details (goods + packages).
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                    # mock mode, no DB
//	go run . --mock --db /tmp/test.db                  # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test
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
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/supplies"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the supplies v2 downloader.
type Config struct {
	WB      config.WBClientConfig  `yaml:"wb"`
	Supply  config.SupplyConfig    `yaml:"supply"`
	Storage config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	days := flag.Int("days", 0, "Days from today (default: 30)")
	begin := flag.String("begin", "", "Start date YYYY-MM-DD")
	end := flag.String("end", "", "End date YYYY-MM-DD")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	skipRef := flag.Bool("skip-reference", false, "Skip warehouse/tariff download")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()
	supplyCfg := cfg.Supply.GetDefaults()

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
	if *days > 0 {
		supplyCfg.Days = *days
	}
	if *begin != "" {
		supplyCfg.Begin = *begin
	}
	if *end != "" {
		supplyCfg.End = *end
	}

	// Resolve date range
	beginDate, endDate := calculateDateRange(supplyCfg)

	dllog.PrintHeader("WB Supplies Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s -> %s (filter: %s)", beginDate, endDate, supplyCfg.DateFilterType)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch
	var writer supplies.Writer
	var cleanup func()

	if *mockMode {
		writer = supplies.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createSuppliesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source supplies.Source
	if *mockMode {
		source = supplies.NewMockSource(25)
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_KEY")
		}

		wbClient := wb.New(apiKey)
		rl := supplyCfg.RateLimits

		// Set rate limits for 3 ToolIDs
		wbClient.SetRateLimit(supplies.ToolIDWarehouses,
			rl.Ref, rl.RefBurst, rl.RefApi, rl.RefApiBurst)
		wbClient.SetRateLimit(supplies.ToolIDTransitTariffs,
			rl.Ref, rl.RefBurst, rl.RefApi, rl.RefApiBurst)
		wbClient.SetRateLimit(supplies.ToolIDSupplyOps,
			rl.SupplyOps, rl.SupplyOpsBurst, rl.SupplyOpsApi, rl.SupplyOpsApiBurst)
		// Share rate limiter across 4 supply endpoints
		wbClient.ShareRateLimit(supplies.ToolIDSupplyOps,
			"get_supplies", "get_supply_goods", "get_supply_packages", "get_supply_details")
		wbClient.SetAdaptiveParams(0, supplyCfg.AdaptiveProbeAfter, supplyCfg.MaxBackoffSeconds)

		source = supplies.NewWBSource(wbClient, rl.Ref, rl.RefBurst, rl.SupplyOps, rl.SupplyOpsBurst)
	}

	opts := supplies.DownloadOptions{
		Begin:          beginDate,
		End:            endDate,
		DateFilterType: supplyCfg.DateFilterType,
		SkipReference:  *skipRef,
		DryRun:         *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "supplies", msg, start)
			}
		}(),
	}

	dl := supplies.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "warehouses=%d tariffs=%d supplies=%d goods=%d packages=%d api=%d errors=%d",
		result.Warehouses, result.Tariffs, result.Supplies, result.Goods, result.Packages,
		result.APICalls, result.Errors)
}

// createSuppliesWriter creates the appropriate Writer based on backend config.
func createSuppliesWriter(ctx context.Context, cfg config.V2StorageConfig) (supplies.Writer, func(), error) {
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

		repo := postgres.NewPgSuppliesRepo(pool.DB())
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
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.WB.APIKeyEnv != "" {
		return os.Getenv(cfg.WB.APIKeyEnv)
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return ""
}

// calculateDateRange resolves the date range from config.
func calculateDateRange(cfg config.SupplyConfig) (string, string) {
	if cfg.Begin != "" && cfg.End != "" {
		return cfg.Begin, cfg.End
	}

	days := cfg.Days
	if days == 0 {
		days = 30
	}

	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02") // Exclude today (incomplete)
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

// loadConfig reads YAML config and applies defaults.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	if cfg.WB.Timeout == "" {
		cfg.WB.Timeout = "30s"
	}
	return &cfg, nil
}
