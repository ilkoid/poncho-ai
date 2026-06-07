// download-wb-whremains-v2 downloads warehouse remains reports from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/whremains/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                               # mock mode, no DB
//	go run . --mock --db /tmp/test-whremains.db                   # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test # mock + test PG
//	go run . --dry-run --db /tmp/test-whremains.db                # real API, no writes
//	go run . --config config.yaml                                  # production (user only!)
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
	"github.com/ilkoid/poncho-ai/pkg/wb"
	"github.com/ilkoid/poncho-ai/pkg/whremains"
)

// Config holds YAML configuration for the warehouse remains v2 downloader.
type Config struct {
	WB        config.WBClientConfig  `yaml:"wb"`
	WhRemains config.WhRemainsConfig `yaml:"wh_remains"`
	Storage   config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	date := flag.String("date", "", "Snapshot date YYYY-MM-DD (default: today)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls, no DB writes)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.WhRemains = cfg.WhRemains.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

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
	cfg.Storage = cfg.Storage.GetDefaults()
	if cfg.Storage.DbPath == "" {
		cfg.Storage.DbPath = cfg.WhRemains.DbPath
	}

	snapshotDate := *date
	if snapshotDate == "" {
		snapshotDate = time.Now().Format("2006-01-02")
	}

	rl := cfg.WhRemains.GetDefaults().RateLimits

	dllog.PrintHeader("WB Warehouse Remains Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Date", Value: snapshotDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation goes INSIDE the else branch.
	var writer whremains.WhRemainsWriter
	var cleanup func()

	if *mockMode {
		writer = whremains.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createWhRemainsWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source whremains.WhRemainsSource
	if *mockMode {
		mockSrc := whremains.NewMockSource()
		mockSrc.PopulateItems(50, 3) // 50 items × 3 warehouses
		source = mockSrc
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetHTTPTimeout(5 * time.Minute) // download step: large reports (~10-50 MB)
		// 3 separate rate limiters for 3 ToolIDs
		wbClient.SetRateLimit("wh_remains_create",
			rl.Create, rl.CreateBurst, rl.CreateApi, rl.CreateApiBurst)
		wbClient.SetRateLimit("wh_remains_status",
			rl.Status, rl.StatusBurst, rl.StatusApi, rl.StatusApiBurst)
		wbClient.SetRateLimit("wh_remains_download",
			rl.Download, rl.DownloadBurst, rl.DownloadApi, rl.DownloadApiBurst)
		source = whremains.NewWBSource(wbClient,
			rl.Create, rl.CreateBurst,
			rl.Status, rl.StatusBurst,
			rl.Download, rl.DownloadBurst,
		)
	}

	opts := whremains.DownloadOptions{
		SnapshotDate:    snapshotDate,
		DryRun:          *dryRun,
		Params: whremains.WHRemainsParams{
			GroupByNm:   cfg.WhRemains.GroupByNm,
			GroupBySize: cfg.WhRemains.GroupBySize,
		},
		PollIntervalSec: cfg.WhRemains.PollIntervalSec,
		PollTimeoutMin:  cfg.WhRemains.PollTimeoutMin,
		OnProgress: func() func(string) {
			var step int
			start := time.Now()
			return func(msg string) {
				step++
				dllog.Progress(step, 0, "whremains", msg, start)
			}
		}(),
	}

	dl := whremains.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "status=%s %d rows (task=%s)",
		result.Status, result.TotalRows, result.TaskID)
}

// createWhRemainsWriter creates the appropriate WhRemainsWriter based on backend config.
func createWhRemainsWriter(ctx context.Context, cfg config.V2StorageConfig) (whremains.WhRemainsWriter, func(), error) {
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

		repo := postgres.NewPgWhRemainsRepo(pool.DB())
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
// Priority: WhRemainsConfig.APIKeyEnv > WB_API_ANALYTICS_AND_PROMO_KEY > WB_API_KEY > config value.
func resolveAPIKey(cfg *Config) string {
	if envVar := cfg.WhRemains.APIKeyEnv; envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
