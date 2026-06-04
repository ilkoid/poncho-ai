// download-wb-search-vis-v2 downloads organic search visibility data from WB Seller Analytics API.
//
// V2 architecture: business logic in pkg/searchvis/, this is a thin CLI driver (~160 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Covers 2 phases: positions (visibility %, avg position) → queries (top search texts).
// Unique: requires nmIDs from DB as input — uses Reader interface for dual-backend consistency.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter + MockReader — ZERO database interaction.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/searchvis"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the search-visibility v2 downloader.
type Config struct {
	WB              config.WBClientConfig          `yaml:"wb"`
	SearchVis       config.SearchVisibilityConfig  `yaml:"search_visibility"`
	Storage         config.V2StorageConfig         `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	nmIDsFlag := flag.String("nm-ids", "", "Comma-separated nmID list (overrides auto-detection)")
	beginFlag := flag.String("begin", "", "Begin date YYYY-MM-DD (overrides config)")
	endFlag := flag.String("end", "", "End date YYYY-MM-DD (overrides config)")
	daysFlag := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	limit := flag.Int("limit", 0, "Max search queries per product (default: 30)")
	skipPositions := flag.Bool("skip-positions", false, "Skip positions download")
	skipQueries := flag.Bool("skip-queries", false, "Skip queries download")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()
	svCfg := cfg.SearchVis.GetDefaults()

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
	if *beginFlag != "" {
		svCfg.Begin = *beginFlag
	}
	if *endFlag != "" {
		svCfg.End = *endFlag
	}
	if *daysFlag > 0 {
		svCfg.Days = *daysFlag
	}
	if *limit > 0 {
		svCfg.Limit = *limit
	}
	if *skipPositions {
		svCfg.SkipPositions = true
	}
	if *skipQueries {
		svCfg.SkipQueries = true
	}

	beginDate, endDate := calculateDateRange(svCfg)
	snapshotDate := time.Now().Format("2006-01-02")

	dllog.PrintHeader("WB Search Visibility Downloader v2",
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s -> %s", beginDate, endDate)},
		dllog.HeaderField{Key: "Snapshot", Value: snapshotDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer + reader creation INSIDE if/else branches
	var writer searchvis.Writer
	var reader searchvis.Reader
	var cleanup func()

	if *mockMode {
		writer = searchvis.NewDiscardWriter()
		reader = searchvis.NewMockReader() // synthetic nmIDs, no DB
		cleanup = func() {}
	} else {
		var err error
		writer, reader, cleanup, err = createBackend(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Load nmIDs via Reader (same backend as Writer — dual-backend consistency)
	nmIDs, err := loadNmIDs(ctx, reader, *nmIDsFlag, svCfg.Filter)
	if err != nil {
		log.Fatalf("load nmIDs: %v", err)
	}
	if len(nmIDs) == 0 {
		log.Fatal("no nmIDs found. Load sales/orders data first or use --nm-ids flag.")
	}
	dllog.Log("loaded %d nmIDs", len(nmIDs))

	// Create source (real API or mock)
	var source searchvis.Source
	if *mockMode {
		source = searchvis.NewMockSource()
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)

		// Set rate limits — both endpoints share 3 req/min
		rl := svCfg.RateLimits
		wbClient.SetRateLimit(searchvis.ToolIDReport,
			rl.SearchReport, rl.SearchReportBurst, rl.SearchReportApi, rl.SearchReportApiBurst)
		wbClient.ShareRateLimit(searchvis.ToolIDReport, searchvis.ToolIDSearchTexts)
		wbClient.SetAdaptiveParams(0, svCfg.AdaptiveProbeAfter, svCfg.MaxBackoffSeconds)

		source = searchvis.NewWBSource(wbClient, rl.SearchReport, rl.SearchReportBurst)
	}

	opts := searchvis.DownloadOptions{
		NmIDs:         nmIDs,
		BeginDate:     beginDate,
		EndDate:       endDate,
		SnapshotDate:  snapshotDate,
		QueryLimit:    svCfg.Limit,
		SkipPositions: svCfg.SkipPositions,
		SkipQueries:   svCfg.SkipQueries,
		DryRun:        *dryRun,
		RateLimit:     svCfg.RateLimits.SearchReport,
		Burst:         svCfg.RateLimits.SearchReportBurst,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "searchvis", msg, start)
			}
		}(),
	}

	dl := searchvis.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "positions=%d queries=%d errors=%d",
		result.PositionRows, result.QueryRows, result.Errors)
}

// createBackend creates Writer + Reader from the same backend.
// One repo object implements both interfaces.
func createBackend(ctx context.Context, cfg config.V2StorageConfig) (
	searchvis.Writer, searchvis.Reader, func(), error) {
	switch cfg.Backend {
	case "postgres", "postgresql":
		dsn, err := cfg.GetEffectiveDSN()
		if err != nil {
			return nil, nil, func() {}, fmt.Errorf("postgres DSN: %w", err)
		}

		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, nil, func() {}, fmt.Errorf("postgres pool: %w", err)
		}

		repo := postgres.NewPgSearchVisRepo(pool.DB())
		if err := repo.InitSchema(ctx); err != nil {
			pool.Close()
			return nil, nil, func() {}, fmt.Errorf("postgres schema: %w", err)
		}
		return repo, repo, pool.Close, nil // repo implements BOTH Writer + Reader

	default: // "sqlite"
		repo, err := sqlite.NewSQLiteSalesRepository(cfg.DbPath)
		if err != nil {
			return nil, nil, func() {}, fmt.Errorf("open SQLite: %w", err)
		}
		return repo, repo, func() { repo.Close() }, nil // repo implements BOTH Writer + Reader
	}
}

// loadNmIDs resolves nmIDs from DB via Reader, applies article/activity filters.
func loadNmIDs(ctx context.Context, reader searchvis.Reader, nmIDsFlag string, filter config.FunnelFilterConfig) ([]int, error) {
	if nmIDsFlag != "" {
		return parseCommaInts(nmIDsFlag), nil
	}

	nmIDs, err := reader.GetDistinctNmIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get distinct nmIDs: %w", err)
	}

	// Filter by article length and year digits
	if len(filter.ExcludeLengths) > 0 || len(filter.AllowedYears) > 0 {
		before := len(nmIDs)

		articlesMap, err := reader.GetSupplierArticlesByNmIDs(ctx, nmIDs)
		if err != nil {
			return nil, fmt.Errorf("get supplier articles: %w", err)
		}

		var filtered []int
		for _, nmID := range nmIDs {
			article := articlesMap[nmID]
			if article == "" {
				continue
			}
			if containsInt(filter.ExcludeLengths, len(article)) {
				continue
			}
			if len(filter.AllowedYears) > 0 && len(article) >= 3 {
				yearDigits := article[1:3]
				year, err := strconv.Atoi(yearDigits)
				if err == nil && !containsInt(filter.AllowedYears, year) {
					continue
				}
			}
			filtered = append(filtered, nmID)
		}
		nmIDs = filtered
		dllog.Log("after filter: %d products (excluded %d)", len(nmIDs), before-len(nmIDs))
	}

	// Filter by activity
	if filter.ActiveDays > 0 {
		before := len(nmIDs)
		nmIDs, err = reader.FilterActiveNmIDs(ctx, nmIDs, filter.ActiveDays)
		if err != nil {
			return nil, fmt.Errorf("filter active: %w", err)
		}
		dllog.Log("active (%d days): %d products (excluded %d inactive)", filter.ActiveDays, len(nmIDs), before-len(nmIDs))
	}

	return nmIDs, nil
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
func calculateDateRange(svCfg config.SearchVisibilityConfig) (string, string) {
	if svCfg.Begin != "" && svCfg.End != "" {
		return svCfg.Begin, svCfg.End
	}
	days := svCfg.Days
	if days == 0 {
		days = 7
	}
	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
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
		cfg.WB.Timeout = "120s"
	}
	return &cfg, nil
}

// parseCommaInts parses "123,456,789" → []int{123, 456, 789}.
func parseCommaInts(s string) []int {
	var ids []int
	for _, part := range strings.Split(s, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && id != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// containsInt checks if slice contains value.
func containsInt(slice []int, value int) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}
