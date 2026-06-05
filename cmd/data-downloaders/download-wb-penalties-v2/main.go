// download-wb-penalties-v2 downloads measurement penalties data from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/penalties/, this is a thin CLI driver (~120 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
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
	"github.com/ilkoid/poncho-ai/pkg/penalties"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the penalties v2 downloader.
type Config struct {
	WB        config.WBClientConfig        `yaml:"wb"`
	Penalties config.DownloadConfig        `yaml:"penalties"`
	Storage   config.V2StorageConfig       `yaml:"storage"`
	Filter    config.PenaltiesFilterConfig `yaml:"filter"`
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

	dllog.PrintHeader("WB Measurement Penalties Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DbPath},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch.
	// --mock mode creates DiscardWriter (zero DB interaction).
	var writer penalties.PenaltiesWriter
	var cleanup func()

	if *mockMode {
		writer = penalties.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createPenaltiesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source penalties.PenaltiesSource
	if *mockMode {
		source = penalties.NewMockPenaltiesSource(350)
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(penalties.ToolID,
			cfg.WB.RateLimit, cfg.WB.BurstLimit,
			cfg.WB.RateLimit, cfg.WB.BurstLimit) // api floor = desired (1 req/min already slow)
		if cfg.Penalties.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.Penalties.AdaptiveProbeAfter, cfg.Penalties.MaxBackoffSeconds)
		}
		source = penalties.NewWBSource(wbClient, cfg.WB.RateLimit, cfg.WB.BurstLimit)
	}

	opts := penalties.DownloadOptions{
		Days:    cfg.Penalties.Days,
		From:    cfg.Penalties.From,
		To:      cfg.Penalties.To,
		Rewrite: cfg.Penalties.Rewrite,
		DryRun:  *dryRun,
		Filter:  cfg.Filter,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "penalties", msg, start)
			}
		}(),
	}

	dl := penalties.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d penalties, %d pages",
		result.TotalPenalties, result.TotalPages)
}

// createPenaltiesWriter creates the appropriate PenaltiesWriter based on backend config.
// Returns the writer and a cleanup function.
func createPenaltiesWriter(ctx context.Context, cfg config.V2StorageConfig) (penalties.PenaltiesWriter, func(), error) {
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

		repo := postgres.NewPgPenaltiesRepo(pool.DB())
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
		cfg.WB.RateLimit = 1 // 1 req/min (WB Seller Analytics API)
	}
	if cfg.WB.BurstLimit == 0 {
		cfg.WB.BurstLimit = 1
	}
	if cfg.Penalties.Days == 0 {
		cfg.Penalties.Days = 90
	}
	if cfg.Penalties.AdaptiveRecoverAfter == 0 {
		cfg.Penalties.AdaptiveRecoverAfter = 5
	}
	if cfg.Penalties.AdaptiveProbeAfter == 0 {
		cfg.Penalties.AdaptiveProbeAfter = 10
	}
	if cfg.Penalties.MaxBackoffSeconds == 0 {
		cfg.Penalties.MaxBackoffSeconds = 60
	}
	return &cfg, nil
}
