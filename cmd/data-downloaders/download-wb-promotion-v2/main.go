// download-wb-promotion-v2 downloads extended WB Promotion API data to SQLite or PostgreSQL.
//
// V2 architecture: business logic in pkg/promotion/, this is a thin CLI driver (~150 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Covers 14 phases: campaign bids, normquery stats/bids/minus/clusters,
// bid recommendations, expenses, balance, payments, calendar, budgets, min bids.
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
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/promotion"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the promotion V2 downloader.
type Config struct {
	WB          config.WBClientConfig    `yaml:"wb"`
	PromotionV2 config.PromotionV2Config `yaml:"promotion_v2"`
	Storage     config.V2StorageConfig   `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	beginFlag := flag.String("begin", "", "Begin date YYYY-MM-DD (overrides config)")
	endFlag := flag.String("end", "", "End date YYYY-MM-DD (overrides config)")
	daysFlag := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	statusesFlag := flag.String("statuses", "", "Filter by status (comma-separated: 9,11)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}
	cfg.Storage = cfg.Storage.GetDefaults()
	pCfg := cfg.PromotionV2.GetDefaults()

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
	// Backward compat: if storage.db_path empty but promotion_v2.db_path set
	if cfg.Storage.DbPath == "" && pCfg.DbPath != "" {
		cfg.Storage.DbPath = pCfg.DbPath
	}
	if *beginFlag != "" {
		pCfg.Begin = *beginFlag
	}
	if *endFlag != "" {
		pCfg.End = *endFlag
	}
	if *daysFlag > 0 {
		pCfg.Days = *daysFlag
	}
	if *statusesFlag != "" {
		pCfg.Statuses = parseStatuses(*statusesFlag)
	}

	beginDate, endDate := calculateDateRange(pCfg)
	calBegin, calEnd := calculateCalendarDateRange(pCfg)

	dllog.PrintHeader("WB Promotion V2 Downloader",
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Period", Value: beginDate + " -> " + endDate},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// ⚠️ Mock safety — writer + reader creation INSIDE if/else branches
	var writer promotion.Writer
	var reader promotion.Reader
	var cleanup func()

	if *mockMode {
		writer = promotion.NewDiscardWriter()
		reader = promotion.NewMockReader()
		cleanup = func() {}
	} else {
		var err error
		writer, reader, cleanup, err = createBackend(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Load (advert_id, nm_id) pairs via Reader (same backend as Writer)
	productIDs, _, err := loadProductIDs(ctx, reader, pCfg)
	if err != nil {
		log.Fatalf("load product IDs: %v", err)
	}
	hasV1Data := len(productIDs) > 0
	if !hasV1Data {
		dllog.Log("no V1 campaign products — skipping V1-dependent phases, running calendar/finance only")
	}

	// Create source (real API or mock)
	var source promotion.Source
	if *mockMode {
		source = promotion.NewMockSource()
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("no API key. Set WB_API_ANALYTICS_AND_PROMO_KEY or WB_API_KEY")
		}
		wbClient, err := wb.NewFromConfig(config.WBConfig{
			APIKey:        apiKey,
			Timeout:       cfg.WB.Timeout,
			RetryAttempts: 5,
		})
		if err != nil {
			log.Fatalf("create WB client: %v", err)
		}
		applyRateLimits(wbClient, pCfg.RateLimits)
		wbClient.SetAdaptiveParams(0, pCfg.AdaptiveProbeAfter, pCfg.MaxBackoffSeconds)
		if calKey := resolveCalendarKey(cfg); calKey != "" {
			wbClient.SetCalendarKey(calKey)
		}
		source = promotion.NewWBSource(wbClient)
	}

	// Map config rate limits to promotion.RateLimits
	rl := pCfg.RateLimits

	opts := promotion.DownloadOptions{
		ProductIDs:    productIDs,
		BeginDate:     beginDate,
		EndDate:       endDate,
		CalendarBegin: calBegin,
		CalendarEnd:   calEnd,
		RateLimits: promotion.RateLimits{
			Normquery:      rl.Normquery,
			NormqueryBurst: rl.NormqueryBurst,
			NormqueryStats:      rl.NormqueryStats,
			NormqueryStatsBurst: rl.NormqueryStatsBurst,
			BidRec:      rl.BidRec,
			BidRecBurst: rl.BidRecBurst,
			Finance:      rl.Finance,
			FinanceBurst: rl.FinanceBurst,
			Calendar:      rl.Calendar,
			CalendarBurst: rl.CalendarBurst,
			MinBids:      rl.MinBids,
			MinBidsBurst: rl.MinBidsBurst,
		},
		SkipBids:            pCfg.SkipBids,
		SkipNormquery:       pCfg.SkipNormquery,
		SkipRecommendations: pCfg.SkipRecommendations,
		SkipFinance:         pCfg.SkipFinance,
		SkipCalendar:        pCfg.SkipCalendar,
		SkipBudgets:         pCfg.SkipBudgets,
		SkipMinBids:         pCfg.SkipMinBids,
		DryRun:              *dryRun,
	}

	dl := promotion.NewDownloader(source, writer, reader, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d/%d phases completed (%d errors)",
		result.CompletedSteps, result.TotalSteps, result.Errors)
}

// ============================================================================
// Backend factory — Writer + Reader from the same backend
// ============================================================================

// createBackend creates Writer + Reader from the same backend.
// One repo object implements both interfaces.
func createBackend(ctx context.Context, cfg config.V2StorageConfig) (
	promotion.Writer, promotion.Reader, func(), error) {
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

		repo := postgres.NewPgPromotionRepo(pool.DB())
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

// ============================================================================
// Helper functions
// ============================================================================

// loadProductIDs resolves (advert_id, nm_id) pairs and incremental cutoff.
func loadProductIDs(ctx context.Context, reader promotion.Reader, pCfg config.PromotionV2Config) ([]wb.NormqueryItem, string, error) {
	cutoff := ""
	if pCfg.ChangedDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -pCfg.ChangedDays).Format("2006-01-02T15:04:05")
	} else {
		lastRun, _ := reader.GetNormqueryLastRun(ctx)
		if lastRun != "" {
			cutoff = lastRun
		}
	}

	if cutoff != "" {
		dllog.Log("incremental mode: campaigns changed since %s", cutoff)
	} else {
		dllog.Log("full scan mode: all matching campaigns")
	}

	ids, err := reader.GetCampaignProductIDs(ctx, pCfg.Statuses, cutoff)
	if err != nil {
		return nil, "", fmt.Errorf("get campaign product IDs: %w", err)
	}
	dllog.Log("loaded %d (advert_id, nm_id) pairs", len(ids))
	return ids, cutoff, nil
}

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

func defaultConfig() *Config {
	return &Config{
		WB: config.WBClientConfig{Timeout: "120s"},
		PromotionV2: config.PromotionV2Config{
			DbPath:   "wb-sales.db",
			Days:     7,
			Statuses: []int{9, 11},
		},
	}
}

func calculateDateRange(pCfg config.PromotionV2Config) (string, string) {
	if pCfg.Begin != "" && pCfg.End != "" {
		return pCfg.Begin, pCfg.End
	}
	days := pCfg.Days
	if days == 0 {
		days = 7
	}
	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func calculateCalendarDateRange(pCfg config.PromotionV2Config) (string, string) {
	now := time.Now()
	begin := now.AddDate(0, 0, -pCfg.CalendarDaysPast).Format("2006-01-02")
	end := now.AddDate(0, 0, pCfg.CalendarDaysFuture).Format("2006-01-02")
	return begin, end
}

func parseStatuses(s string) []int {
	if s == "" {
		return nil
	}
	var result []int
	for _, part := range strings.Split(s, ",") {
		var status int
		fmt.Sscanf(strings.TrimSpace(part), "%d", &status)
		if status != 0 {
			result = append(result, status)
		}
	}
	return result
}

func resolveAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func resolveCalendarKey(cfg *Config) string {
	if key := os.Getenv("WB_API_MARKET_KEY"); key != "" {
		return key
	}
	return cfg.WB.CalendarAPIKey
}

func applyRateLimits(client *wb.Client, rl config.PromotionV2RateLimits) {
	client.SetRateLimit("normquery_stats", rl.NormqueryStats, rl.NormqueryStatsBurst, rl.NormqueryStatsApi, rl.NormqueryStatsApiBurst)
	client.SetRateLimit("normquery_list", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("normquery_bids", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("normquery_minus", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("bid_recommendations", rl.BidRec, rl.BidRecBurst, rl.BidRecApi, rl.BidRecApiBurst)
	client.SetRateLimit("expenses", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("balance", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("payments", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("calendar_promotions", rl.Calendar, rl.CalendarBurst, rl.CalendarApi, rl.CalendarApiBurst)
	client.SetRateLimit("get_advert_details", rl.Normquery, rl.NormqueryBurst, rl.NormqueryApi, rl.NormqueryApiBurst)
	client.SetRateLimit("budget", rl.Finance, rl.FinanceBurst, rl.FinanceApi, rl.FinanceApiBurst)
	client.SetRateLimit("min_bids", rl.MinBids, rl.MinBidsBurst, rl.MinBidsApi, rl.MinBidsApiBurst)
}

// Suppress unused import warning for utils.
var _ = utils.MaskAPIKey
