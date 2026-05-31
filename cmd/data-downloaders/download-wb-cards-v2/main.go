// download-wb-cards-v2 downloads product cards from WB Content API.
//
// V2 architecture: business logic in pkg/cards/, this is a thin CLI driver (~120 lines).
// Supports both SQLite and PostgreSQL backends via config.
//
// Usage:
//
//	go run . --mock                    # mock mode, SQLite
//	go run . --mock --backend=postgres # mock mode, PostgreSQL
//	go run . --resume                  # resume from last cursor
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/cards"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the cards v2 downloader.
type Config struct {
	WB      config.WBClientConfig    `yaml:"wb"`
	Cards   cardsConfig              `yaml:"cards"`
	Storage config.V2StorageConfig   `yaml:"storage"`
	Filter  config.FunnelFilterConfig `yaml:"filter"`
}

// cardsConfig holds cards-specific settings.
type cardsConfig struct {
	APIKeyEnv           string `yaml:"api_key_env"`
	RateLimit           int    `yaml:"rate_limit"`
	BurstLimit          int    `yaml:"burst_limit"`
	AdaptiveProbeAfter  int    `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds   int    `yaml:"max_backoff_seconds"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	resume := flag.Bool("resume", false, "Resume from last saved cursor")
	limit := flag.Int("limit", 0, "Max cards to download (0 = unlimited)")
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
	if *dbPath != "" {
		cfg.Storage.DbPath = *dbPath
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}

	dllog.PrintHeader("WB Cards Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Resume", Value: fmt.Sprintf("%v", *resume)},
	)

	// Create writer based on backend selection
	writer, cleanup, err := createCardsWriter(ctx, cfg.Storage)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer cleanup()

	// Create source (real API or mock)
	var source cards.CardsSource
	if *mockMode {
		source = cards.NewMockCardsSource(250)
	} else {
		apiKey := resolveAPIKey(cfg)
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(cards.ToolID,
			cfg.Cards.RateLimit, cfg.Cards.BurstLimit,
			cfg.Cards.RateLimit, cfg.Cards.BurstLimit) // api floor = desired for cards
		if cfg.Cards.AdaptiveProbeAfter > 0 {
			wbClient.SetAdaptiveParams(5, cfg.Cards.AdaptiveProbeAfter, cfg.Cards.MaxBackoffSeconds)
		}
		source = cards.NewWBSource(wbClient, cfg.Cards.RateLimit, cfg.Cards.BurstLimit)
	}

	opts := cards.DownloadOptions{
		Resume:     *resume,
		DryRun:     *dryRun,
		Limit:      *limit,
		OnProgress: func(msg string) { fmt.Println(msg) },
	}

	dl := cards.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download: %v", err)
	}

	dllog.Done(result.Duration, "%d cards, %d pages, %d API requests",
		result.TotalCards, result.Pages, result.Requests)
}

// createCardsWriter creates the appropriate CardsWriter based on backend config.
// Returns the writer and a cleanup function.
func createCardsWriter(ctx context.Context, cfg config.V2StorageConfig) (cards.CardsWriter, func(), error) {
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

		repo := postgres.NewPgCardsRepo(pool.DB())
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

func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.Cards.APIKeyEnv != "" {
		return cfg.Cards.APIKeyEnv // resolved by config loader via ${VAR} syntax
	}
	return ""
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	// Apply defaults
	if cfg.Cards.RateLimit == 0 {
		cfg.Cards.RateLimit = 100
	}
	if cfg.Cards.BurstLimit == 0 {
		cfg.Cards.BurstLimit = 5
	}
	if cfg.Cards.AdaptiveProbeAfter == 0 {
		cfg.Cards.AdaptiveProbeAfter = 10
	}
	if cfg.Cards.MaxBackoffSeconds == 0 {
		cfg.Cards.MaxBackoffSeconds = 60
	}
	return &cfg, nil
}
