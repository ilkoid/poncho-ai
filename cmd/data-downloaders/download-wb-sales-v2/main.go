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
	beginFlag := flag.String("begin", "", "Start date YYYY-MM-DD (overrides config from)")
	endFlag := flag.String("end", "", "End date YYYY-MM-DD (overrides config to)")
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
	if *beginFlag != "" {
		cfg.Download.From = *beginFlag
	}
	if *endFlag != "" {
		cfg.Download.To = *endFlag
	}

	// Resolve date range BEFORE PrintHeader (needs begin/end for display)
	begin, end, err := resolveDateRange(cfg.Download)
	if err != nil {
		log.Fatalf("date range: %v", err)
	}

	dllog.PrintHeader("WB Sales Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s → %s", begin.Format("2006-01-02"), end.Format("2006-01-02"))},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — КРИТИЧЕСКОЕ ОТЛИЧИЕ:
	// --mock mode creates DiscardWriter (zero DB interaction).
	// Writer creation is INSIDE the else branch — never opened when mocking.
	var writer sales.SalesWriter
	var cleanup func()

	if *mockMode {
		writer = sales.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createSalesWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	var source sales.SalesSource
	if *mockMode {
		source = &sales.MockSalesSource{}
	} else {
		wbClient, err := wb.NewFromConfig(cfg.WB.ToWBConfig())
			if err != nil {
				log.Fatalf("wb client: %v", err)
			}
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
	ranges := wb.SplitPeriod(begin, end)

	result, err := dl.Run(ctx, ranges, cfg.Download.Resume, cfg.Download.Rewrite)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "%d rows, %d periods", result.TotalRows, result.PeriodsCount)
}

// resolveDateRange determines the download period with priority:
//
//	CLI --begin/--end > config from/to > config days
//
// In days mode, "end" is yesterday (today's sales are still incomplete).
// In from/to mode, exact dates are used as-is.
func resolveDateRange(cfg config.DownloadConfig) (time.Time, time.Time, error) {
	// Priority 1: explicit from/to dates (config or CLI --begin/--end)
	if cfg.From != "" && cfg.To != "" {
		from, err := parseFlexDate(cfg.From)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parse 'from': %w", err)
		}
		to, err := parseFlexDate(cfg.To)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parse 'to': %w", err)
		}
		return from, to, nil
	}

	// Priority 2: days-based — N days of data ending at YESTERDAY
	// days=1 → yesterday only, days=5 → 5 days ending yesterday
	if cfg.Days <= 0 {
		cfg.Days = 7
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	end := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 23, 59, 59, 0, yesterday.Location())
	beginDay := yesterday.AddDate(0, 0, -(cfg.Days - 1))
	begin := time.Date(beginDay.Year(), beginDay.Month(), beginDay.Day(), 0, 0, 0, 0, beginDay.Location())
	return begin, end, nil
}

// parseFlexDate parses both YYYY-MM-DD and RFC3339 formats.
func parseFlexDate(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
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
