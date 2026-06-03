// download-wb-campaigns-v2 downloads campaign data from WB Promotion API.
//
// V2 architecture: business logic in pkg/campaigns/, this is a thin CLI driver (~130 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Covers 3 phases: campaigns → details → fullstats (6 tables).
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

	"github.com/ilkoid/poncho-ai/pkg/campaigns"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the campaigns v2 downloader.
type Config struct {
	WB        config.WBClientConfig     `yaml:"wb"`
	Campaigns config.PromotionConfig    `yaml:"campaigns"`
	Storage   config.V2StorageConfig    `yaml:"storage"`
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
	promCfg := cfg.Campaigns.GetDefaults()

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

	// Resolve date range
	beginDate, endDate := calculateDateRange(cfg)

	dllog.PrintHeader("WB Campaigns Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s -> %s", beginDate, endDate)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch
	var writer campaigns.CampaignsWriter
	var cleanup func()

	if *mockMode {
		writer = campaigns.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createCampaignsWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source campaigns.CampaignsSource
	if *mockMode {
		source = campaigns.NewMockCampaignsSource(10)
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)

		// Set rate limits for 3 ToolIDs
		rl := promCfg.RateLimits
		wbClient.SetRateLimit(campaigns.ToolIDPromotionCount,
			rl.PromotionCount, rl.PromotionCountBurst, rl.PromotionCountApi, rl.PromotionCountApiBurst)
		wbClient.SetRateLimit(campaigns.ToolIDAdvertDetails,
			rl.AdvertDetails, rl.AdvertDetailsBurst, rl.AdvertDetailsApi, rl.AdvertDetailsApiBurst)
		wbClient.SetRateLimit(campaigns.ToolIDFullstats,
			rl.Fullstats, rl.FullstatsBurst, rl.FullstatsApi, rl.FullstatsApiBurst)

		wbClient.SetAdaptiveParams(0, promCfg.AdaptiveProbeAfter, promCfg.MaxBackoffSeconds)
		source = campaigns.NewWBSource(wbClient)
	}

	opts := campaigns.DownloadOptions{
		Begin:          beginDate,
		End:            endDate,
		Statuses:       promCfg.Statuses,
		Resume:         promCfg.Resume,
		SkipDetails:    promCfg.SkipDetails,
		SkipCampaigns:  promCfg.SkipCampaigns,
		SkipStats:      promCfg.SkipStats,
		DryRun:         *dryRun,
		FullstatsRate:  promCfg.RateLimits.Fullstats,
		FullstatsBurst: promCfg.RateLimits.FullstatsBurst,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "campaigns", msg, start)
			}
		}(),
	}

	dl := campaigns.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "campaigns=%d windows=%d daily=%d app=%d nm=%d booster=%d errors=%d",
		result.CampaignsForStats, result.DateWindows,
		result.DailyRows, result.AppRows, result.NmRows, result.BoosterRows, result.Errors)
}

// createCampaignsWriter creates the appropriate CampaignsWriter based on backend config.
func createCampaignsWriter(ctx context.Context, cfg config.V2StorageConfig) (campaigns.CampaignsWriter, func(), error) {
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

		repo := postgres.NewPgCampaignsRepo(pool.DB())
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
func calculateDateRange(cfg *Config) (string, string) {
	if cfg.Campaigns.Begin != "" && cfg.Campaigns.End != "" {
		return cfg.Campaigns.Begin, cfg.Campaigns.End
	}

	days := cfg.Campaigns.Days
	if days == 0 {
		days = 7
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
