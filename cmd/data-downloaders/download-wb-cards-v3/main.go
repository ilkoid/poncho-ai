// download-wb-cards-v3 downloads product cards from WB Content API with
// preventive substring scrubbing at load time.
//
// v3 = v2 architecture + a scrubbing Writer decorator. It is a SEPARATE utility
// so that download-wb-cards-v2 and pkg/cards remain untouched. Scrubbing rewrites
// sensitive substrings (e.g. brand names) in card fields before persistence; the
// rules come from a YAML file referenced by storage.scrub_rules_path (pkg/scrub).
// When scrub_rules_path is empty, v3 behaves identically to v2 (no scrubbing).
//
// Usage:
//
//	go run . --mock                    # mock mode, SQLite
//	go run . --mock --backend=postgres # mock mode, PostgreSQL
//	go run . --config <v3-config.yaml> # production (scrub enabled via config)
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

	"github.com/ilkoid/poncho-ai/pkg/cards"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/scrub"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the cards v3 downloader.
type Config struct {
	WB      config.WBClientConfig  `yaml:"wb"`
	Cards   cardsConfig            `yaml:"cards"`
	Storage config.V2StorageConfig `yaml:"storage"`
}

// cardsConfig holds cards-specific settings.
type cardsConfig struct {
	APIKeyEnv          string `yaml:"api_key_env"`
	RateLimit          int    `yaml:"rate_limit"`
	BurstLimit         int    `yaml:"burst_limit"`
	AdaptiveProbeAfter int    `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int    `yaml:"max_backoff_seconds"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
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

	dllog.PrintHeader("WB Cards Downloader v3",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// v3: load preventive scrub rules (substring masking at load time). Empty path = off.
	var scrubber *scrub.Replacer
	if cfg.Storage.ScrubRulesPath != "" {
		scrubber, err = scrub.Load(cfg.Storage.ScrubRulesPath)
		if err != nil {
			log.Fatalf("scrub rules: %v", err)
		}
		dllog.Log("scrub: %s (%d rules)", cfg.Storage.ScrubRulesPath, scrubber.Len())
	} else {
		dllog.Log("scrub: off (scrub_rules_path not set)")
	}

	// Create source + writer (mock mode: no DB interaction at all)
	var source cards.CardsSource
	var writer cards.CardsWriter
	var cleanup func()

	if *mockMode {
		source = cards.NewMockCardsSource(250)
		writer = cards.NewDiscardWriter()
		cleanup = func() {}
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

		var err error
		writer, cleanup, err = createCardsWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}

	// v3: wrap the writer with the scrubbing decorator. Applies in both mock and
	// real modes (in mock it scrubs then discards — harmless, exercises wiring).
	// pkg/cards stays untouched: the Downloader sees a cards.CardsWriter like any other.
	if scrubber != nil {
		writer = newScrubCardsWriter(writer, scrubber)
	}

	defer cleanup()

	opts := cards.DownloadOptions{
		DryRun: *dryRun,
		Limit:  *limit,
		OnProgress: func() func(string) {
			var page int
			start := time.Now()
			return func(msg string) {
				page++
				dllog.Progress(page, 0, "cards", msg, start)
			}
		}(),
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
		return os.Getenv(cfg.Cards.APIKeyEnv)
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
