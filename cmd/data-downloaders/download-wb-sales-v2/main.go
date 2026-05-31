package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/sales"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the sales v2 downloader.
type Config struct {
	WB       config.WBClientConfig  `yaml:"wb"`
	Download config.DownloadConfig  `yaml:"download"`
	Storage  config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	days := flag.Int("days", 0, "Number of days to download")
	rewrite := flag.Bool("rewrite", false, "Rewrite mode")
	resume := flag.Bool("resume", false, "Resume mode")
	mockMode := flag.Bool("mock", false, "Use mock client (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.Download = cfg.Download.GetDefaults()
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
	if *days > 0 {
		cfg.Download.Days = *days
	}
	if *rewrite {
		cfg.Download.Rewrite = true
	}
	if *resume {
		cfg.Download.Resume = true
	}

	dllog.PrintHeader("WB Sales Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.Download.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// Create writer based on backend selection
	writer, cleanup, err := createSalesWriter(ctx, cfg.Storage)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer cleanup()

	var source sales.SalesSource
	if *mockMode {
		source = &sales.MockSalesSource{}
	} else {
		wbClient := wb.New(cfg.WB.APIKey)
		wbClient.SetRateLimit("sales",
			cfg.WB.RateLimit, cfg.WB.BurstLimit,
			cfg.WB.RateLimit, cfg.WB.BurstLimit)
		wbClient.SetAdaptiveParams(
			cfg.Download.AdaptiveRecoverAfter,
			cfg.Download.AdaptiveProbeAfter,
			cfg.Download.MaxBackoffSeconds)
		source = wbClient
	}

	opts := sales.DownloadOptions{
		RateLimit:          cfg.WB.RateLimit,
		Burst:              cfg.WB.BurstLimit,
		SkipServiceRecords: cfg.Download.SkipServiceRecords,
		DryRun:             *dryRun,
		OnProgress:         func(msg string) { fmt.Println(msg) },
	}

	dl := sales.NewDownloader(source, writer, opts)

	end := time.Now()
	begin := end.AddDate(0, 0, -cfg.Download.Days)
	ranges := wb.SplitPeriod(begin, end)

	result, err := dl.Run(ctx, ranges, cfg.Download.Resume, cfg.Download.Rewrite)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "%d rows, %d periods", result.TotalRows, result.PeriodsCount)
}

// createSalesWriter creates the appropriate SalesWriter based on backend config.
// Returns the writer and a cleanup function.
func createSalesWriter(ctx context.Context, cfg config.V2StorageConfig) (sales.SalesWriter, func(), error) {
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

		repo := postgres.NewPgSalesRepo(pool.DB())
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

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
