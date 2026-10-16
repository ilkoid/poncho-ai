// download-wb-item-rating-v2 downloads the WB item rating report («Оценка товара»)
// from WB Seller Analytics API: POST /api/analytics/v2/item-rating.
//
// V2 architecture: business logic in pkg/itemrating/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// Full-capture RAW: every spec field becomes a column; each row/block also keeps
// its exact JSON (raw_item / raw_seller / raw_fb_increase) — fields added by WB
// between spec refreshes are not lost.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                              # mock mode, no API, no DB
//	go run . --dry-run --backend postgres --pg-database wb_data_test  # real API, no writes
//	go run . --config config.yaml                                # production (user only!)
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
	"github.com/ilkoid/poncho-ai/pkg/itemrating"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the item rating v2 downloader.
type Config struct {
	WB         config.WBClientConfig   `yaml:"wb"`
	ItemRating config.ItemRatingConfig `yaml:"item_rating"`
	Storage    config.V2StorageConfig  `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	days := flag.Int("days", 0, "Current period window length in days (default from config; 1 = yesterday)")
	begin := flag.String("begin", "", "Explicit period start YYYY-MM-DD (overrides --days)")
	end := flag.String("end", "", "Explicit period end YYYY-MM-DD (overrides --days)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()
	cfg.ItemRating = cfg.ItemRating.GetDefaults()

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
		cfg.ItemRating.Days = *days
	}
	if *begin != "" {
		cfg.ItemRating.Begin = *begin
	}
	if *end != "" {
		cfg.ItemRating.End = *end
	}
	// Default Days in main.go only when explicit dates are absent (Rule 6).
	if cfg.ItemRating.Days == 0 && cfg.ItemRating.Begin == "" && cfg.ItemRating.End == "" {
		cfg.ItemRating.Days = 1
	}

	periodStart, periodEnd, pastStart, pastEnd := resolvePeriod(cfg.ItemRating)

	dllog.PrintHeader("WB Item Rating Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s → %s (past: %s → %s)", periodStart, periodEnd, pastStart, pastEnd)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer creation INSIDE else branch.
	// --mock mode creates DiscardWriter (zero DB interaction).
	var writer itemrating.Writer
	var cleanup func()
	if *mockMode {
		writer = itemrating.NewDiscardWriter()
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
	var source itemrating.Source
	if *mockMode {
		source = itemrating.NewMockSource()
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatalf("API key is empty: set wb.api_key or wb.api_key_env in config")
		}
		wbClient := wb.New(apiKey)
		rl := cfg.ItemRating.RateLimits
		wbClient.SetRateLimit(wb.ToolIDItemRating,
			rl.ItemRating, rl.ItemRatingBurst,
			rl.ItemRatingApi, rl.ItemRatingApiBurst)
		if cfg.ItemRating.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.ItemRating.AdaptiveProbeAfter, cfg.ItemRating.MaxBackoffSeconds)
		}
		source = itemrating.NewWBSource(wbClient, rl.ItemRating, rl.ItemRatingBurst)
	}

	opts := itemrating.DownloadOptions{
		PeriodStart:  periodStart,
		PeriodEnd:    periodEnd,
		PastStart:    pastStart,
		PastEnd:      pastEnd,
		OrderByField: cfg.ItemRating.OrderByField,
		OrderByMode:  cfg.ItemRating.OrderByMode,
		PageSize:     cfg.ItemRating.PageSize,
		RateLimit:    cfg.ItemRating.RateLimits.ItemRating,
		Burst:        cfg.ItemRating.RateLimits.ItemRatingBurst,
		DryRun:       *dryRun,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "item-rating", msg, start)
			}
		}(),
	}

	dl := itemrating.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d items, %d pages, stored %d (errors: %d)",
		result.ItemsLoaded, result.PagesLoaded, result.DBCount, result.Errors)
	if result.Errors > 0 {
		log.Fatalf("honest FAIL: %d errors during download", result.Errors)
	}
}

// resolvePeriod computes the current and past period windows.
// Explicit Begin/End win over Days; Days=1 means "yesterday".
// The past window is the equal-length window immediately before the current one
// (enables the API-side dynamics fields).
func resolvePeriod(cfg config.ItemRatingConfig) (periodStart, periodEnd, pastStart, pastEnd string) {
	if cfg.Begin != "" && cfg.End != "" {
		periodStart, periodEnd = cfg.Begin, cfg.End
		days := daysBetween(periodStart, periodEnd)
		pastEnd = shiftDate(periodStart, -1)
		pastStart = shiftDate(pastEnd, -(days - 1))
		return periodStart, periodEnd, pastStart, pastEnd
	}

	days := cfg.Days
	if days <= 0 {
		days = 1
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()) // локальная полночь
	endDate := today.AddDate(0, 0, -1)              // вчера — исключаем сегодня (незавершённый день)
	startDate := endDate.AddDate(0, 0, -(days - 1)) // окно длины days
	pastEndDate := startDate.AddDate(0, 0, -1)
	pastStartDate := pastEndDate.AddDate(0, 0, -(days - 1))

	return startDate.Format("2006-01-02"), endDate.Format("2006-01-02"),
		pastStartDate.Format("2006-01-02"), pastEndDate.Format("2006-01-02")
}

// daysBetween returns the inclusive day count between two YYYY-MM-DD dates.
func daysBetween(start, end string) int {
	s, err := time.Parse("2006-01-02", start)
	if err != nil {
		return 1
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return 1
	}
	d := int(e.Sub(s).Hours()/24) + 1
	if d < 1 {
		return 1
	}
	return d
}

// shiftDate shifts a YYYY-MM-DD date by n days.
func shiftDate(date string, n int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// createWriter creates the appropriate itemrating.Writer based on backend config.
func createWriter(ctx context.Context, cfg config.V2StorageConfig) (itemrating.Writer, func(), error) {
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
		repo := postgres.NewPgItemRatingRepo(pool.DB())
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
	return &cfg, nil
}
