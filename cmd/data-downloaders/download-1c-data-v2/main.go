// download-1c-data-v2 downloads product data from 1C/PIM APIs.
//
// V2 architecture: business logic in pkg/onec/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                    # mock mode, no DB
//	go run . --mock --db /tmp/test.db                  # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test  # mock + test PG
//	go run . --dry-run --db /tmp/test.db --config ...  # real API, no writes
//	go run . --config config.yaml                      # production (user only!)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/onec"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Config holds YAML configuration for the 1c-data v2 downloader.
type Config struct {
	OneC    config.OneCConfig      `yaml:"onec"`
	Storage config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	clean := flag.Bool("clean", false, "Clear 1C/PIM tables before loading")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Skip DB writes, show what would be saved")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Load config
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

	// 2. Resolve API URLs (priority: env > config)
	apiURL := resolveURL("ONEC_API_URL", cfg.OneC.APIUrl)
	pimURL := resolveURL("ONEC_PIM_URL", cfg.OneC.PIMUrl)
	if apiURL == "" && !*mockMode {
		log.Fatal("❌ No 1C API URL. Set ONEC_API_URL or configure yaml api_url.")
	}
	if pimURL == "" && !*mockMode {
		log.Fatal("❌ No PIM URL. Set ONEC_PIM_URL or configure yaml pim_url.")
	}

	// Build full endpoint URLs from base
	goodsURL := strings.TrimRight(apiURL, "/") + "/goods/"
	pricesURL := strings.TrimRight(apiURL, "/") + "/prices/"
	snapshotDate := time.Now().Format("2006-01-02")

	// 3. Print header
	maskedAPI := maskURL(apiURL)
	maskedPIM := maskURL(pimURL)
	dllog.PrintHeader("1C Data Downloader v2",
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "1C API", Value: maskedAPI},
		dllog.HeaderField{Key: "PIM API", Value: maskedPIM},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// 4. Create writer — ⚠️ mock safety: writer inside else branch
	var writer onec.Writer
	var cleanup func()

	if *mockMode {
		writer = onec.NewDiscardWriter()
		cleanup = func() {}
	} else {
		writer, cleanup, err = createWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// 5. Create source (real HTTP or mock)
	var source onec.Source
	if *mockMode {
		source = onec.NewMockSource()
	} else {
		source = onec.NewHTTPSource()
	}

	// 6. Run download
	start := time.Now()
	opts := onec.DownloadOptions{
		GoodsURL:     goodsURL,
		PricesURL:    pricesURL,
		PIMURL:       pimURL,
		SnapshotDate: snapshotDate,
		Clean:        *clean,
		DryRun:       *dryRun,
		OnProgress: func(msg string) {
			dllog.Log("%s", msg)
		},
	}

	dl := onec.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)

	// 7. Print summary
	if err != nil && !result.HasErrors() {
		dllog.Error("download: %v", err)
		os.Exit(1)
	}

	dllog.Done(time.Since(start),
		"goods: %d, SKUs: %d, dimensions: %d, prices: %d, PIM: %d",
		result.GoodsCount, result.SKUCount, result.DimensionCount,
		result.PriceCount, result.PIMCount)

	if result.HasErrors() {
		for _, se := range result.StepErrors {
			dllog.Error("step %s: %v", se.Step, se.Err)
		}
		os.Exit(1)
	}
}

// createWriter creates the appropriate onec.Writer based on backend config.
func createWriter(ctx context.Context, cfg config.V2StorageConfig) (onec.Writer, func(), error) {
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

		repo := postgres.NewPgOneCRepo(pool.DB())
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

// resolveURL returns URL with priority: env var > config value.
func resolveURL(envVar, configValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if configValue == "" || strings.HasPrefix(configValue, "${") {
		return ""
	}
	return configValue
}

// maskURL masks credentials in a URL with basic auth.
func maskURL(rawURL string) string {
	if rawURL == "" {
		return "(none)"
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		if len(rawURL) > 12 {
			return rawURL[:5] + "..." + rawURL[len(rawURL)-4:]
		}
		return "***"
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	u.User = url.UserPassword(utils.MaskAPIKey(user), utils.MaskAPIKey(pass))
	return u.String()
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
