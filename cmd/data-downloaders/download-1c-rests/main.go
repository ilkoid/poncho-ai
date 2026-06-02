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
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

type Config struct {
	OneCRests config.OneCRestsConfig `yaml:"onec_rests"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	clean := flag.Bool("clean", false, "очистить onec_rests перед загрузкой")
	mock := flag.Bool("mock", false, "загрузить тестовые данные без API")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
	flag.Parse()
	if *help {
		printHelp()
		return
	}

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Config load error: %v", err)
	}
	defaults := cfg.OneCRests.GetDefaults()
	if *dbPath != "" {
		defaults.DbPath = *dbPath
	}

	// Get API URL (priority: env > config)
	apiURL := getAPIURL("ONEC_API_REST_URL", defaults.RestURL)
	if !*mock && apiURL == "" {
		log.Fatal("No 1C RESTs API URL. Set ONEC_API_REST_URL or configure yaml rest_url.")
	}

	snapshotDate := time.Now().Format("2006-01-02")

	// Print startup header
	headerFields := []dllog.HeaderField{
		{Key: "DB", Value: defaults.DbPath},
		{Key: "Snapshot", Value: snapshotDate},
	}
	if *mock {
		headerFields = append(headerFields, dllog.HeaderField{Key: "Mode", Value: "Mock"})
	}
	if apiURL != "" {
		headerFields = append(headerFields, dllog.HeaderField{Key: "API URL", Value: maskURL(apiURL)})
	}
	dllog.PrintHeader("1C RESTs Downloader", headerFields...)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigChan; dllog.Log("interrupted"); cancel() }()

	// Open database
	repo, err := sqlite.NewSQLiteSalesRepository(defaults.DbPath)
	if err != nil {
		log.Fatalf("DB open error: %v", err)
	}
	defer repo.Close()

	// Clean if requested
	if *clean {
		dllog.Log("cleaning onec_rests table...")
		if err := repo.CleanOneCRests(ctx); err != nil {
			log.Fatalf("Clean error: %v", err)
		}
	}

	start := time.Now()

	// Download data
	var goodsCount, totalRows, filteredOut int

	if *mock {
		goodsCount, totalRows, err = mockFetchRests(ctx, repo, snapshotDate)
	} else {
		client := NewOneCRestsClient()
		goodsCount, totalRows, filteredOut, err = client.FetchRests(ctx, apiURL, defaults.StorageFilter, repo, snapshotDate)
	}

	if err != nil {
		log.Fatalf("Fetch error: %v", err)
	}

	msg := fmt.Sprintf("%d goods, %d rows", goodsCount, totalRows)
	if filteredOut > 0 {
		msg += fmt.Sprintf(" (filtered: %d)", filteredOut)
	}
	dllog.Done(time.Since(start), "%s", msg)

	// Retention: purge old snapshots
	if defaults.RetentionDays > 0 && !*clean {
		purged, err := repo.PurgeOldRestsSnapshots(ctx, defaults.RetentionDays)
		if err != nil {
			dllog.Error("purge old snapshots: %v", err)
		} else if purged > 0 {
			dllog.Log("purged %d old snapshot rows (retention: %d days)", purged, defaults.RetentionDays)
		}
	}

	// Summary
	count, _ := repo.CountOneCRests(ctx)
	dllog.Log("total in DB: %d rows (snapshot: %s)", count, snapshotDate)
}

// mockFetchRests generates deterministic test data without API calls.
func mockFetchRests(ctx context.Context, repo *sqlite.SQLiteSalesRepository, snapshotDate string) (int, int, error) {
	storages := []struct{ guid, name string }{
		{"mock-storage-1", "Склад Москва"},
		{"mock-storage-2", "Склад Вологда"},
		{"mock-storage-3", "Склад СПб"},
	}

	var batch []sqlite.OneCRestsRow
	goodsCount := 10

	for g := 0; g < goodsCount; g++ {
		guid := fmt.Sprintf("mock-good-%d", g)
		for sku := 0; sku < 2; sku++ {
			skuGUID := fmt.Sprintf("mock-sku-%d-%d", g, sku)
			for _, s := range storages {
				batch = append(batch, sqlite.OneCRestsRow{
					GoodGUID:    guid,
					SKUGUID:     skuGUID,
					StorageGUID: s.guid,
					StorageName: s.name,
					Stock:       g*10 + sku + 1,
					Reserv:      sku,
					Free:        g*10 + sku + 1 - sku,
					FirstStage:  sku == 0,
				})
			}
		}
	}

	saved, err := repo.SaveOneCRests(ctx, batch, snapshotDate)
	return goodsCount, saved, err
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// getAPIURL returns URL with priority: env var > config value.
// Detects unresolved ${ENV} placeholders from YAML expansion.
func getAPIURL(envVar, configValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if configValue == "" || strings.HasPrefix(configValue, "${") {
		return ""
	}
	return configValue
}

// maskURL masks credentials in a URL with basic auth.
// "https://user:password@host/path" → "https://use...ord@host/path"
func maskURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return utils.MaskAPIKey(rawURL)
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	u.User = url.UserPassword(utils.MaskAPIKey(user), utils.MaskAPIKey(pass))
	return u.String()
}

func printHelp() {
	fmt.Println("download-1c-rests — загрузчик остатков товаров из 1C RESTs API")
	fmt.Println()
	fmt.Println("Использование:")
	fmt.Println("  download-1c-rests [опции]")
	fmt.Println()
	fmt.Println("Опции:")
	fmt.Println("  --config PATH   путь к конфигу (default: config.yaml)")
	fmt.Println("  --db PATH       путь к SQLite базе (overrides config)")
	fmt.Println("  --clean         очистить onec_rests перед загрузкой")
	fmt.Println("  --mock          загрузить тестовые данные без API")
	fmt.Println("  --help, -h      справка")
	fmt.Println()
	fmt.Println("Переменные окружения:")
	fmt.Println("  ONEC_API_REST_URL   URL 1C RESTs API (с basic auth)")
	fmt.Println()
	fmt.Println("Retention:")
	fmt.Println("  Автоматически удаляет снепшоты старше N дней от вчерашнего дня.")
	fmt.Println("  retention_days=7 → хранит вчера + 6 дней = 7 снепшотов.")
	fmt.Println("  Сегодняшний снепшот всегда сохраняется, но не входит в retention window.")
}
