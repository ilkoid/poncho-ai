// download-wb-funnel-agg-v2 downloads aggregated funnel metrics from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/funnelagg/, this is a thin CLI driver (~130 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Aggregated funnel returns period-level metrics for ALL products via offset pagination.
// No Reader interface needed (unlike regular funnel which batches by nmIDs).
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
	"github.com/ilkoid/poncho-ai/pkg/funnelagg"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the funnel-agg v2 downloader.
type Config struct {
	WB                config.WBClientConfig         `yaml:"wb"`
	FunnelAggregated config.FunnelAggregatedConfig `yaml:"funnel_aggregated"`
	Storage           config.V2StorageConfig        `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	days := flag.Int("days", 0, "Period in days (alternative to config begin/end)")
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
	aggCfg := cfg.FunnelAggregated.GetDefaults()

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
		aggCfg.Days = *days
	}

	// Resolve date range
	beginDate, endDate, pastStart, pastEnd := calculateDateRange(aggCfg)

	dllog.PrintHeader("WB Funnel-Agg Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s -> %s", beginDate, endDate)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch
	var writer funnelagg.Writer
	var cleanup func()

	if *mockMode {
		writer = funnelagg.NewDiscardWriter()
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
	var source funnelagg.Source
	if *mockMode {
		source = &funnelagg.MockSource{ProductCount: 5, TotalPages: 3}
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(funnelagg.ToolID,
			aggCfg.RateLimits.FunnelAggregated, aggCfg.RateLimits.FunnelAggregatedBurst,
			aggCfg.RateLimits.FunnelAggregatedApi, aggCfg.RateLimits.FunnelAggregatedApiBurst)
		wbClient.SetAdaptiveParams(0, aggCfg.AdaptiveProbeAfter, aggCfg.MaxBackoffSeconds)
		source = funnelagg.NewWBSource(wbClient, aggCfg.RateLimits.FunnelAggregated, aggCfg.RateLimits.FunnelAggregatedBurst)
	}

	opts := funnelagg.DownloadOptions{
		SelectedStart:      beginDate,
		SelectedEnd:        endDate,
		PastStart:          pastStart,
		PastEnd:            pastEnd,
		PageSize:           aggCfg.PageSize,
		RateLimit:          aggCfg.RateLimits.FunnelAggregated,
		Burst:              aggCfg.RateLimits.FunnelAggregatedBurst,
		NmIDs:              aggCfg.NmIDs,
		BrandNames:         aggCfg.BrandNames,
		SubjectIDs:         aggCfg.SubjectIDs,
		TagIDs:             aggCfg.TagIDs,
		SkipDeletedNm:      aggCfg.SkipDeletedNm,
		OrderByField:       aggCfg.OrderByField,
		OrderByMode:        aggCfg.OrderByMode,
		DryRun:             *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "funnel-agg", msg, start)
			}
		}(),
	}

	dl := funnelagg.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "products=%d pages=%d errors=%d",
		result.ProductsLoaded, result.PagesLoaded, result.Errors)
}

// createWriter creates the appropriate funnelagg.Writer based on backend config.
func createWriter(ctx context.Context, cfg config.V2StorageConfig) (funnelagg.Writer, func(), error) {
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

		repo := postgres.NewPgFunnelAggRepo(pool.DB())
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
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return ""
}

// calculateDateRange resolves the date range from config.
// Returns (selectedStart, selectedEnd, pastStart, pastEnd).
func calculateDateRange(cfg config.FunnelAggregatedConfig) (string, string, string, string) {
	if cfg.SelectedStart != "" && cfg.SelectedEnd != "" {
		return cfg.SelectedStart, cfg.SelectedEnd, cfg.PastStart, cfg.PastEnd
	}

	days := cfg.Days
	if days == 0 {
		days = 7
	}

	now := time.Now()
	endDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	beginDate := now.AddDate(0, 0, -days).Format("2006-01-02")
	pastEnd := now.AddDate(0, 0, -days-1).Format("2006-01-02")
	pastStart := now.AddDate(0, 0, -days*2).Format("2006-01-02")

	return beginDate, endDate, pastStart, pastEnd
}

// loadConfig reads YAML config and applies defaults.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
