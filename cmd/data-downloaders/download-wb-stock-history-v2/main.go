// download-wb-stock-history-v2 downloads stock history data from WB Analytics via async CSV reports.
//
// V2 architecture: business logic in pkg/stockhistory/, this is a thin CLI driver (~160 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
// This is a critical safety improvement over v1 stock-history downloader.
//
// Usage:
//
//	go run . --mock                                                # mock mode, no DB
//	go run . --mock --backend sqlite --db /tmp/test.db            # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test # mock + test PG
//	go run . --dry-run --db /tmp/test.db --config ...             # real API, no writes
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
	"github.com/ilkoid/poncho-ai/pkg/stockhistory"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the stock-history v2 downloader.
type Config struct {
	WB          config.WBClientConfig      `yaml:"wb"`
	StockHistory config.StockHistoryConfig  `yaml:"stock_history"`
	Storage     config.V2StorageConfig     `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	days := flag.Int("days", 0, "Number of days (overrides config)")
	reportType := flag.String("report-type", "", "Report type: metrics|daily (overrides config)")
	stockType := flag.String("stock-type", "", "Stock type: |wb|mp (overrides config)")
	pollInterval := flag.Int("poll-interval", 0, "Poll interval in seconds (overrides config)")
	pollTimeout := flag.Int("poll-timeout", 0, "Poll timeout in minutes (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	resume := flag.Bool("resume", false, "Resume if report already exists")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.StockHistory = cfg.StockHistory.GetDefaults()
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
		cfg.StockHistory.Days = *days
	}
	if *reportType != "" {
		cfg.StockHistory.ReportType = *reportType
	}
	if *stockType != "" {
		cfg.StockHistory.StockType = *stockType
	}
	if *pollInterval > 0 {
		cfg.StockHistory.PollIntervalSec = *pollInterval
	}
	if *pollTimeout > 0 {
		cfg.StockHistory.PollTimeoutMin = *pollTimeout
	}
	if *resume {
		cfg.StockHistory.Resume = true
	}

	dllog.PrintHeader("WB Stock History Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Report", Value: cfg.StockHistory.ReportType},
		dllog.HeaderField{Key: "StockType", Value: cfg.StockHistory.StockType},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.StockHistory.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Resume", Value: fmt.Sprintf("%v", cfg.StockHistory.Resume)},
	)

	// ⚠️ Mock safety — CRITICAL difference from v1:
	// --mock mode creates DiscardWriter (zero DB interaction).
	// Writer creation is INSIDE the else branch — never opened when mocking.
	var writer stockhistory.StockHistoryWriter
	var cleanup func()

	if *mockMode {
		writer = stockhistory.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source stockhistory.StockHistorySource
	if *mockMode {
		if cfg.StockHistory.ReportType == "daily" {
			source = &stockhistory.MockDailySource{}
		} else {
			source = &stockhistory.MockMetricsSource{}
		}
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		rl := cfg.StockHistory.RateLimits
		wbClient.SetRateLimit("stock_history_create", rl.Create, rl.CreateBurst, rl.CreateApi, rl.CreateApiBurst)
		wbClient.SetRateLimit("stock_history_status", rl.StatusCheck, rl.StatusCheckBurst, rl.StatusCheckApi, rl.StatusCheckApiBurst)
		wbClient.SetRateLimit("stock_history_download", rl.Download, rl.DownloadBurst, rl.DownloadApi, rl.DownloadApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.StockHistory.AdaptiveProbeAfter, cfg.StockHistory.MaxBackoffSeconds)
		source = stockhistory.NewWBSource(wbClient, rl.Create, rl.CreateBurst)
	}

	opts := stockhistory.DownloadOptions{
		ReportType:      cfg.StockHistory.ReportType,
		StockType:       cfg.StockHistory.StockType,
		Days:            cfg.StockHistory.Days,
		From:            cfg.StockHistory.Begin,
		To:              cfg.StockHistory.End,
		DryRun:          *dryRun,
		Resume:          cfg.StockHistory.Resume,
		PollIntervalSec: cfg.StockHistory.PollIntervalSec,
		PollTimeoutMin:  cfg.StockHistory.PollTimeoutMin,
		OnProgress: func() func(string) {
			var step int
			start := time.Now()
			return func(msg string) {
				step++
				dllog.Progress(step, 0, "stock-history", msg, start)
			}
		}(),
	}

	dl := stockhistory.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "status=%s download_id=%s rows=%d",
		result.Status, result.DownloadID, result.RowsCount)
}

// createWriter creates the appropriate StockHistoryWriter based on backend config.
func createWriter(ctx context.Context, cfg config.V2StorageConfig) (stockhistory.StockHistoryWriter, func(), error) {
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

		repo := postgres.NewPgStockHistoryRepo(pool.DB())
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
// Priority: api_key_env (env var) > analytics_api_key > api_key > WB_API_ANALYTICS_AND_PROMO_KEY fallback.
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKeyEnv != "" {
		if key := os.Getenv(cfg.WB.APIKeyEnv); key != "" {
			return key
		}
	}
	if cfg.WB.AnalyticsAPIKey != "" {
		return cfg.WB.AnalyticsAPIKey
	}
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
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
