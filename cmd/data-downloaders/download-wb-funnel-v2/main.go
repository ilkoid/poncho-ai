// download-wb-funnel-v2 downloads funnel (conversion) data from WB Analytics API v3.
//
// V2 architecture: business logic in pkg/funnel/, this is a thin CLI driver (~150 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                               # mock mode, no DB
//	go run . --mock --db /tmp/test-funnel.db                      # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test # mock + test PG
//	go run . --dry-run --db /tmp/test-funnel.db --config ...      # real API, no writes
//	go run . --config config.yaml                                 # production (user only!)
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
	"github.com/ilkoid/poncho-ai/pkg/funnel"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the funnel v2 downloader.
type Config struct {
	WB            config.WBClientConfig      `yaml:"wb"`
	Funnel        config.FunnelConfig        `yaml:"funnel"`
	Filter        config.FunnelFilterConfig  `yaml:"filter"`
	Storage       config.V2StorageConfig     `yaml:"storage"`
	RefreshWindow int                        `yaml:"refresh_window"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	days := flag.Int("days", 0, "Number of days (overrides config)")
	beginFlag := flag.String("begin", "", "Start date YYYY-MM-DD (overrides config from)")
	endFlag := flag.String("end", "", "End date YYYY-MM-DD (overrides config to)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls, no DB writes)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.Funnel = cfg.Funnel.GetDefaults()
	cfg.Storage = cfg.Storage.GetDefaults()

	// CLI overrides
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
		cfg.Funnel.Days = *days
	}
	if *beginFlag != "" {
		cfg.Funnel.From = *beginFlag
	}
	if *endFlag != "" {
		cfg.Funnel.To = *endFlag
	}

	dllog.PrintHeader("WB Funnel Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DbPath},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.Funnel.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation goes INSIDE the else branch.
	// --mock mode uses DiscardWriter (zero DB interaction).
	// This prevents --mock + rewrite from touching real data.
	var writer funnel.FunnelWriter
	var cleanup func()

	if *mockMode {
		writer = funnel.NewDiscardWriter()
		cleanup = func() {}
	} else {
		writer, cleanup, err = createFunnelWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source funnel.FunnelSource
	if *mockMode {
		source = &funnel.MockFunnelSource{}
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(funnel.ToolID,
			cfg.Funnel.FunnelRateLimit, cfg.Funnel.FunnelRateLimitBurst,
			cfg.Funnel.FunnelRateLimitApi, cfg.Funnel.FunnelRateLimitApiBurst)
		wbClient.SetAdaptiveParams(
			cfg.Funnel.AdaptiveRecoverAfter,
			cfg.Funnel.AdaptiveProbeAfter,
			cfg.Funnel.MaxBackoffSeconds)
		source = funnel.NewWBSource(wbClient, cfg.Funnel.FunnelRateLimit, cfg.Funnel.FunnelRateLimitBurst)
	}

	refreshWindow := cfg.RefreshWindow
	if refreshWindow == 0 {
		refreshWindow = 4
	}

	opts := funnel.DownloadOptions{
		Days:             cfg.Funnel.Days,
		BatchSize:        cfg.Funnel.BatchSize,
		MaxBatches:       cfg.Funnel.MaxBatches,
		RefreshWindow:    refreshWindow,
		IncrementalHours: cfg.Funnel.IncrementalHours,
		From:             cfg.Funnel.From,
		To:               cfg.Funnel.To,
		Filter:           cfg.Filter,
		DryRun:           *dryRun,
		OnProgress: func() func(string) {
			var batch int
			start := time.Now()
			return func(msg string) {
				batch++
				dllog.Progress(batch, 0, "funnel", msg, start)
			}
		}(),
	}

	dl := funnel.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "%d products, %d metrics, %d batches",
		result.ProductsLoaded, result.MetricsLoaded, result.BatchesTotal)
}

// createFunnelWriter creates the appropriate FunnelWriter based on backend config.
// Returns the writer and a cleanup function.
func createFunnelWriter(ctx context.Context, cfg config.V2StorageConfig) (funnel.FunnelWriter, func(), error) {
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

		repo := postgres.NewPgFunnelRepo(pool.DB())
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
// Priority: analytics_api_key (direct) > api_key_env (env var name via os.Getenv) > empty.
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.AnalyticsAPIKey != "" {
		return cfg.WB.AnalyticsAPIKey
	}
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.WB.APIKeyEnv != "" {
		return os.Getenv(cfg.WB.APIKeyEnv)
	}
	return ""
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
