// download-wb-fbs-orders-v2 downloads FBS assembly tasks + statuses + order feed.
//
// V2 architecture: business logic in pkg/fbsorders/, this is a thin CLI driver.
// PG-only домен (прецедент wbscraper): SQLite-бэкенд не поддерживается.
//
// Три фазы (см. pkg/fbsorders/downloader.go):
//  1. GET /api/v3/orders        → public.fbs_orders
//  2. POST /api/v3/orders/status → public.fbs_orders_status + fbs_orders_status_log
//  3. POST /api/analytics/v1/order-feed → public.order_feed (отключается --no-feed)
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                              # mock, no DB, no API
//	go run . --dry-run --pg-database wb_data_test                # real API, no writes
//	go run . --config cmd/.configs/download-all/download-wb-fbs-orders-PG.yaml  # production (user only!)
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

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/fbsorders"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FBSSection — специфичные для FBS-загрузчика параметры.
type FBSSection struct {
	Days             int    `yaml:"days"`               // глубина заданий (default 90)
	From             string `yaml:"from"`               // YYYY-MM-DD, приоритет над days
	To               string `yaml:"to"`                 // YYYY-MM-DD (включительно)
	StatusWindowDays int    `yaml:"status_window_days"` // окно безусловного обновления статусов (default 90)
	FeedEnabled      *bool  `yaml:"feed_enabled"`       // лента заказов (default true)
	FeedDays         int    `yaml:"feed_days"`          // глубина ленты, ≤31 (default 7)
	FeedMpOnly       *bool  `yaml:"feed_mp_only"`       // только FBS/DBS, без FBW (default true)
	RateLimit        int    `yaml:"rate_limit"`         // desired для v3 endpoints (default 120)
	BurstLimit       int    `yaml:"burst"`              // (default 20)
	FeedRateLimit    int    `yaml:"feed_rate_limit"`    // desired для order-feed (default 1)
	FeedBurstLimit   int    `yaml:"feed_burst"`         // (default 1)
}

// Config holds YAML configuration for the FBS orders v2 downloader.
type Config struct {
	WB      config.WBClientConfig  `yaml:"wb"`
	FBS     FBSSection             `yaml:"fbs"`
	Storage config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	backend := flag.String("backend", "", "Storage backend: postgres (only, overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls, no DB)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	days := flag.Int("days", 0, "Orders lookback days (overrides config)")
	statusWindow := flag.Int("status-window-days", 0, "Status refresh window days (overrides config)")
	noFeed := flag.Bool("no-feed", false, "Disable order-feed phase (overrides config)")
	feedAllModels := flag.Bool("feed-all-models", false, "Save FBW rows too (default: FBS/DBS only, overrides config)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()

	// CLI flag overrides
	if *backend != "" {
		cfg.Storage.Backend = *backend
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}
	if *days > 0 {
		cfg.FBS.Days = *days
	}
	if *statusWindow > 0 {
		cfg.FBS.StatusWindowDays = *statusWindow
	}
	if *noFeed {
		cfg.FBS.FeedEnabled = new(bool) // false
	}
	if *feedAllModels {
		f := false
		cfg.FBS.FeedMpOnly = &f
	}

	feedEnabled := cfg.FBS.FeedEnabled == nil || *cfg.FBS.FeedEnabled

	dllog.PrintHeader("WB FBS Orders Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Feed", Value: fmt.Sprintf("%v", feedEnabled)},
	)

	// ⚠️ Mock safety: --mock создаёт DiscardWriter (ноль взаимодействий с БД).
	// Writer создаётся ТОЛЬКО в else-ветке — БД не открывается при моке.
	var writer fbsorders.Writer
	var cleanup func()

	if *mockMode {
		writer = fbsorders.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createFBSWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	opts := fbsorders.DownloadOptions{
		Days:             cfg.FBS.Days,
		From:             cfg.FBS.From,
		To:               cfg.FBS.To,
		StatusWindowDays: cfg.FBS.StatusWindowDays,
		DisableFeed:      !feedEnabled,
		FeedMpOnly:       cfg.FBS.FeedMpOnly, // nil = default true (FBS/DBS only)
		FeedDays:         cfg.FBS.FeedDays,
		DryRun:           *dryRun,
		OnProgress: func() func(string) {
			var step int
			start := time.Now()
			return func(msg string) {
				step++
				dllog.ProgressDT(step, 0, "fbs", msg, start)
			}
		}(),
	}

	// Mock: полностью синтетический прогон.
	if *mockMode {
		source := fbsorders.NewMockSource(1200)
		result, err := fbsorders.NewDownloader(source, writer, opts).Run(ctx)
		if err != nil {
			log.Fatalf("download: %v", err)
		}
		dllog.Done(result.Duration, "%d orders, %d statuses, %d feed rows (mock)",
			result.TotalOrders, result.TotalStatuses, result.FeedRows)
		return
	}

	// Real API: основной ключ → при 401/403 один повтор с другим ключом.
	// Эмпирика: marketplace-api/v3 принимает WB_API_KEY (fetch-fbs-orders.sh,
	// 2026-08-16); контент-ключ получает 403 "scope is not allowed" (2026-08-25).
	primary := resolveAPIKey(cfg)
	fallback := fallbackAPIKey(primary)

	result, err := runWithKey(ctx, primary, cfg, writer, opts)
	if err != nil && fallback != "" && isAuthError(err) {
		log.Printf("⚠️  основной ключ отклонён (401/403), повтор с fallback-ключом")
		result, err = runWithKey(ctx, fallback, cfg, writer, opts)
	}
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	if result.FeedErr != "" {
		log.Printf("⚠️  лента заказов не загружена (нефатально): %s", result.FeedErr)
	}
	dllog.Done(result.Duration, "%d orders; statuses: %d/%d candidates in %d batches; %d feed rows",
		result.TotalOrders, result.TotalStatuses, result.StatusCandidates, result.StatusBatches, result.FeedRows)
}

// runWithKey выполняет полный прогон с указанным API-ключом.
func runWithKey(ctx context.Context, apiKey string, cfg *Config, writer fbsorders.Writer, opts fbsorders.DownloadOptions) (*fbsorders.DownloadResult, error) {
	client := wb.New(apiKey)

	// Swagger: v3 задания+статусы = один общий бакет 300 req/min, burst 20.
	// ShareRateLimit объединяет оба toolID в один лимитер (суммарно ≤ desired).
	client.SetRateLimit(wb.FBSOrdersToolID,
		cfg.FBS.RateLimit, cfg.FBS.BurstLimit,
		300, 20) // api floor = swagger
	client.ShareRateLimit(wb.FBSOrdersToolID, wb.FBSStatusToolID)

	// Swagger: order-feed 1 req/min (basic-токен 2 req/24h).
	client.SetRateLimit(wb.OrderFeedToolID,
		cfg.FBS.FeedRateLimit, cfg.FBS.FeedBurstLimit,
		1, 1)

	source := fbsorders.NewWBSource(client)
	return fbsorders.NewDownloader(source, writer, opts).Run(ctx)
}

// isAuthError определяет 401/403 для fallback-повтора ключа.
func isAuthError(err error) bool {
	c := wb.New("") // ClassifyError — чистая функция над строкой ошибки
	return c.ClassifyError(err) == wb.ErrAuthFailed
}

// createFBSWriter creates the PostgreSQL writer. Домен PG-only.
func createFBSWriter(ctx context.Context, cfg config.V2StorageConfig) (fbsorders.Writer, func(), error) {
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

		repo := postgres.NewPgFBSOrdersRepo(pool.DB())
		if err := repo.InitSchema(ctx); err != nil {
			pool.Close()
			return nil, func() {}, fmt.Errorf("postgres schema: %w", err)
		}
		return repo, pool.Close, nil

	default:
		return nil, func() {}, fmt.Errorf("backend %q not supported: FBS orders downloader is PG-only", cfg.Backend)
	}
}

// resolveAPIKey: api_key (direct) > api_key_env (default WB_API_KEY).
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.WB.APIKeyEnv == "" {
		return os.Getenv("WB_API_KEY")
	}
	return os.Getenv(cfg.WB.APIKeyEnv)
}

// fallbackAPIKey — любой ДРУГОЙ доступный ключ для повтора при 401/403.
// Не зависит от api_key_env: если основной ключ взят из api_key_env,
// прежняя схема возвращала его же и fallback молча не срабатывал.
func fallbackAPIKey(primary string) string {
	for _, env := range []string{"WB_API_KEY", "WB_API_CONTENT_KEY"} {
		if v := os.Getenv(env); v != "" && v != primary {
			return v
		}
	}
	return ""
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	// Defaults
	if cfg.WB.APIKeyEnv == "" {
		// marketplace-api/v3: WB_API_KEY работает, контент-ключ без scope (403).
		cfg.WB.APIKeyEnv = "WB_API_KEY"
	}
	if cfg.FBS.Days == 0 {
		cfg.FBS.Days = 90 // глубина API
	}
	if cfg.FBS.StatusWindowDays == 0 {
		cfg.FBS.StatusWindowDays = 90
	}
	if cfg.FBS.FeedDays == 0 {
		cfg.FBS.FeedDays = 7 // ≤ 31
	}
	if cfg.FBS.RateLimit == 0 {
		cfg.FBS.RateLimit = 120 // консервативно против swagger 300
	}
	if cfg.FBS.BurstLimit == 0 {
		cfg.FBS.BurstLimit = 20
	}
	if cfg.FBS.FeedRateLimit == 0 {
		cfg.FBS.FeedRateLimit = 1
	}
	if cfg.FBS.FeedBurstLimit == 0 {
		cfg.FBS.FeedBurstLimit = 1
	}
	if cfg.Storage.Backend == "" {
		cfg.Storage.Backend = "postgres"
	}
	return &cfg, nil
}
