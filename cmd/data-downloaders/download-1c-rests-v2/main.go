// download-1c-rests-v2 downloads warehouse stock levels from 1C RESTs API.
//
// V2 architecture: business logic in pkg/onec/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses RestsDiscardWriter — ZERO database interaction.
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

// Config holds YAML configuration for the 1c-rests v2 downloader.
type Config struct {
	OneCRests config.OneCRestsConfig `yaml:"onec_rests"`
	Storage   config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	clean := flag.Bool("clean", false, "Clear onec_rests table before loading")
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

	// 2. Resolve API URL (priority: env > config)
	restURL := resolveURL("ONEC_API_REST_URL", cfg.OneCRests.RestURL)
	if restURL == "" && !*mockMode {
		log.Fatal("❌ No 1C RESTs URL. Set ONEC_API_REST_URL or configure yaml rest_url.")
	}

	snapshotDate := time.Now().Format("2006-01-02")

	// 3. Print header
	maskedURL := maskURL(restURL)
	dllog.PrintHeader("1C Rests Downloader v2",
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "1C RESTs API", Value: maskedURL},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Retention", Value: fmt.Sprintf("%d days", cfg.OneCRests.RetentionDays)},
	)

	// 4. Create writer — ⚠️ mock safety: writer inside else branch
	var writer onec.RestsWriter
	var cleanup func()

	if *mockMode {
		writer = onec.NewRestsDiscardWriter()
		cleanup = func() {}
	} else {
		writer, cleanup, err = createWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// 5. Create source (real HTTP or mock)
	var source onec.RestsSource
	if *mockMode {
		source = onec.NewMockRestsSource()
	} else {
		source = onec.NewHTTPRestsSource()
	}

	// 6. Bridge config filter → domain filter
	filter := onec.RestsStorageFilter{
		GUIDs:        cfg.OneCRests.StorageFilter.GUIDs,
		NamePatterns: cfg.OneCRests.StorageFilter.NamePatterns,
	}

	// 7. Run download
	start := time.Now()
	opts := onec.RestsDownloadOptions{
		RestURL:       restURL,
		SnapshotDate:  snapshotDate,
		StorageFilter: filter,
		RetentionDays: cfg.OneCRests.RetentionDays,
		Clean:         *clean,
		DryRun:        *dryRun,
		OnProgress: func(msg string) {
			dllog.Log("%s", msg)
		},
	}

	dl := onec.NewRestsDownloader(source, writer, opts)
	result, err := dl.Run(ctx)

	// 8. Print summary
	if err != nil {
		dllog.Error("download: %v", err)
		os.Exit(1)
	}

	dllog.Done(time.Since(start),
		"goods: %d, rows: %d (filtered: %d), total in DB: %d",
		result.GoodsCount, result.TotalSaved, result.FilteredOut, result.TotalInDB)
}

// createWriter creates the appropriate onec.RestsWriter based on backend config.
func createWriter(ctx context.Context, cfg config.V2StorageConfig) (onec.RestsWriter, func(), error) {
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

		repo := postgres.NewPgOneCRestsRepo(pool.DB())
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
	if configValue == "" || configValue[0] == '$' {
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
