// download-wb-opsales-v2 downloads operational sales data from WB Statistics API.
//
// V2 architecture: business logic in pkg/opsales/, this is a thin CLI driver.
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
	"github.com/ilkoid/poncho-ai/pkg/opsales"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the opsales v2 downloader.
type Config struct {
	WB      config.WBClientConfig     `yaml:"wb"`
	Opsales config.DownloadConfig     `yaml:"opsales"`
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

	dllog.PrintHeader("WB Operational Sales Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — КРИТИЧЕСКОЕ ОТЛИЧИЕ от cards/sales:
	// --mock mode creates DiscardWriter (zero DB interaction).
	// Writer creation is INSIDE the else branch — never opened when mocking.
	var writer opsales.OpsalesWriter
	var cleanup func()

	if *mockMode {
		writer = opsales.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createOpsalesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source opsales.OpsalesSource
	if *mockMode {
		source = opsales.NewMockOpsalesSource(250)
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(opsales.ToolID,
			cfg.WB.RateLimit, cfg.WB.BurstLimit,
			cfg.WB.RateLimit, cfg.WB.BurstLimit) // api floor = desired for opsales (already slow)
		if cfg.Opsales.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.Opsales.AdaptiveProbeAfter, cfg.Opsales.MaxBackoffSeconds)
		}
		source = opsales.NewWBSource(wbClient, cfg.WB.RateLimit, cfg.WB.BurstLimit)
	}

	opts := opsales.DownloadOptions{
		Days:    cfg.Opsales.Days,
		From:    cfg.Opsales.From,
		To:      cfg.Opsales.To,
		Rewrite: cfg.Opsales.Rewrite,
		DryRun:  *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "opsales", msg, start)
			}
		}(),
	}

	dl := opsales.NewDownloader(source, writer, opts, cfg.Filter)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d sales, %d pages",
		result.TotalSales, result.TotalPages)
}

// createOpsalesWriter creates the appropriate OpsalesWriter based on backend config.
// Returns the writer and a cleanup function.
func createOpsalesWriter(ctx context.Context, cfg config.V2StorageConfig) (opsales.OpsalesWriter, func(), error) {
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

		repo := postgres.NewPgOpsalesRepo(pool.DB())
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
// Priority: api_key (direct) > api_key_env (env var name) > empty.
func resolveAPIKey(cfg *Config) string {
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
	// Apply defaults
	if cfg.WB.RateLimit == 0 {
		cfg.WB.RateLimit = 1 // 1 req/min (WB Statistics API)
	}
	if cfg.WB.BurstLimit == 0 {
		cfg.WB.BurstLimit = 1 // burst=1: prevents burst-fire 429 on slow APIs (Statistics: 1 req/min)
	}
	if cfg.Opsales.Days == 0 {
		cfg.Opsales.Days = 90
	}
	if cfg.Opsales.AdaptiveRecoverAfter == 0 {
		cfg.Opsales.AdaptiveRecoverAfter = 5
	}
	if cfg.Opsales.AdaptiveProbeAfter == 0 {
		cfg.Opsales.AdaptiveProbeAfter = 10
	}
	if cfg.Opsales.MaxBackoffSeconds == 0 {
		cfg.Opsales.MaxBackoffSeconds = 60
	}
	return &cfg, nil
}
