// download-wb-region-sales-v2 downloads regional sales data from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/regionsales/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                    # mock mode, no DB
//	go run . --mock --db /tmp/test.db                  # ignored (DiscardWriter)
//	go run . --dry-run --db /tmp/test.db --config ...  # real API, no writes
//	go run . --config config.yaml                      # production (user only!)
//	go run . --backend postgres --pg-database wb_data_test --days 3  # PG test
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
	"github.com/ilkoid/poncho-ai/pkg/regionsales"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the region sales v2 downloader.
type Config struct {
	WB          config.WBClientConfig    `yaml:"wb"`
	RegionSales config.RegionSalesConfig `yaml:"region_sales"`
	Storage     config.V2StorageConfig   `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	days := flag.Int("days", 0, "Days back from yesterday (overrides config)")
	begin := flag.String("begin", "", "Start date YYYY-MM-DD (overrides config)")
	end := flag.String("end", "", "End date YYYY-MM-DD (overrides config)")
	date := flag.String("date", "", "Single date YYYY-MM-DD (sets begin=end)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.RegionSales = cfg.RegionSales.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()
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
		cfg.RegionSales.Days = *days
	}
	if *begin != "" {
		cfg.RegionSales.Begin = *begin
	}
	if *end != "" {
		cfg.RegionSales.End = *end
	}
	// --date overrides begin/end (single date mode)
	if *date != "" {
		cfg.RegionSales.Begin = *date
		cfg.RegionSales.End = *date
	}

	// Resolve date range for header display
	beginDate, endDate := resolvePeriod(cfg)

	dllog.PrintHeader("WB Region Sales Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Period", Value: beginDate + " → " + endDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE the else branch.
	// --mock mode creates DiscardWriter (zero DB interaction).
	var writer regionsales.RegionSalesWriter
	var cleanup func()

	if *mockMode {
		writer = regionsales.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createRegionSalesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source regionsales.RegionSalesSource
	if *mockMode {
		mockSrc := regionsales.NewMockRegionSalesSource()
		mockSrc.Populate(20, 5) // 20 products × 5 regions = 100 items
		source = mockSrc
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("❌ No API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		rl := cfg.RegionSales.RateLimits
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(regionsales.ToolID,
			rl.RegionSale, rl.RegionSaleBurst,
			rl.RegionSaleApi, rl.RegionSaleApiBurst)
		if cfg.RegionSales.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(0, cfg.RegionSales.AdaptiveProbeAfter, cfg.RegionSales.MaxBackoffSeconds)
		}
		source = regionsales.NewWBSource(wbClient, rl.RegionSale, rl.RegionSaleBurst)
	}

	// Map config to download options
	opts := regionsales.DownloadOptions{
		Begin:  cfg.RegionSales.Begin,
		End:    cfg.RegionSales.End,
		Date:   *date,
		Days:   cfg.RegionSales.Days,
		DryRun: *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "region-sales", msg, start)
			}
		}(),
	}

	dl := regionsales.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d rows, %d requests",
		result.TotalRows, result.Requests)
}

// createRegionSalesWriter creates the appropriate RegionSalesWriter based on backend config.
func createRegionSalesWriter(ctx context.Context, cfg config.V2StorageConfig) (regionsales.RegionSalesWriter, func(), error) {
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

		repo := postgres.NewPgRegionSalesRepo(pool.DB())
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

// resolveAPIKey retrieves API key with priority: api_key_env > standard env vars > config value.
func resolveAPIKey(cfg *Config) string {
	if envVar := cfg.RegionSales.APIKeyEnv; envVar != "" {
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

// resolvePeriod returns begin/end strings for display purposes.
func resolvePeriod(cfg *Config) (string, string) {
	if cfg.RegionSales.Begin != "" && cfg.RegionSales.End != "" {
		return cfg.RegionSales.Begin, cfg.RegionSales.End
	}
	if cfg.RegionSales.Days <= 0 {
		cfg.RegionSales.Days = 7
	}
	now := time.Now()
	return now.AddDate(0, 0, -cfg.RegionSales.Days).Format("2006-01-02"),
		now.AddDate(0, 0, -1).Format("2006-01-02")
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
