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
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	// 1. Parse flags
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	days := flag.Int("days", 0, "дней от сегодня (default: 30)")
	begin := flag.String("begin", "", "начало периода YYYY-MM-DD")
	end := flag.String("end", "", "конец периода YYYY-MM-DD")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	mockMode := flag.Bool("mock", false, "mock mode (no API calls)")
	skipRef := flag.Bool("skip-reference", false, "не скачивать справочники")
	clean := flag.Bool("clean", false, "удалить данные перед скачиванием")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// 2. Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}
	supplyCfg := cfg.Supply.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// 3. Apply CLI overrides
	if *days > 0 {
		supplyCfg.Days = *days
	}
	if *begin != "" {
		supplyCfg.Begin = *begin
	}
	if *end != "" {
		supplyCfg.End = *end
	}
	if *dbPath != "" {
		supplyCfg.DbPath = *dbPath
	}

	// 4. Calculate dates: CLI flags > config from/to > config days > default 30
	if supplyCfg.Begin == "" || supplyCfg.End == "" {
		if supplyCfg.Days == 0 {
			supplyCfg.Days = 30
		}
		beginDate, endDate := calculateDateRange(supplyCfg.Days)
		supplyCfg.Begin = beginDate
		supplyCfg.End = endDate
	}

	fmt.Printf("=== download-wb-supplies ===\n")
	fmt.Printf("Период: %s — %s (фильтр: %s)\n", supplyCfg.Begin, supplyCfg.End, supplyCfg.DateFilterType)

	// 5. Get API key
	apiKey := getAPIKey(cfg)
	if apiKey == "" && !*mockMode {
		log.Fatal("❌ No API key. Set WB_API_KEY or configure yaml api_key.")
	}

	// 6. Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано")
		cancel()
	}()

	// 7. Open database
	if *clean {
		os.Remove(supplyCfg.DbPath)
		fmt.Println("  База удалена (--clean)")
	}
	repo, err := sqlite.NewSQLiteSalesRepository(supplyCfg.DbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	start := time.Now()

	if *mockMode {
		fmt.Println("🧪 MOCK MODE")
		runMock(ctx, repo, supplyCfg)
	} else {
		// 8. Create client with adaptive rate limiting
		wbClient := wb.New(apiKey)
		rl := supplyCfg.RateLimits
		wbClient.SetRateLimit("get_warehouses", rl.Ref, rl.RefBurst, rl.RefApi, rl.RefApiBurst)
		wbClient.SetRateLimit("get_transit_tariffs", rl.Ref, rl.RefBurst, rl.RefApi, rl.RefApiBurst)
		wbClient.SetRateLimit("get_supplies", rl.List, rl.ListBurst, rl.ListApi, rl.ListApiBurst)
		wbClient.SetRateLimit("get_supply_goods", rl.Goods, rl.GoodsBurst, rl.GoodsApi, rl.GoodsApiBurst)
		wbClient.SetRateLimit("get_supply_packages", rl.Package, rl.PackageBurst, rl.PackageApi, rl.PackageApiBurst)
		wbClient.SetRateLimit("get_supply_details", rl.Details, rl.DetailsBurst, rl.DetailsApi, rl.DetailsApiBurst)
		wbClient.SetAdaptiveParams(0, supplyCfg.AdaptiveProbeAfter, supplyCfg.MaxBackoffSeconds)

		runDownload(ctx, wbClient, repo, supplyCfg, *skipRef)
	}

	elapsed := time.Since(start)
	fmt.Printf("\n=== Готово за %s ===\n", elapsed.Round(time.Second))
}

func runDownload(
	ctx context.Context,
	client *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	supplyCfg config.SupplyConfig,
	skipRef bool,
) {
	rl := supplyCfg.RateLimits

	// Step 1: Reference data (warehouses, tariffs)
	if !skipRef {
		fmt.Println("\n--- Справочники ---")
		whSaved, tSaved, err := DownloadReference(ctx, client, repo, rl)
		if err != nil {
			fmt.Printf("❌ Ошибка справочников: %v\n", err)
		} else {
			fmt.Printf("✅ Справочники: %d складов, %d тарифов\n", whSaved, tSaved)
		}
	}

	// Step 2: Download supplies list
	fmt.Println("\n--- Поставки ---")
	filter := wb.SuppliesFilterRequest{
		Dates: []wb.DateFilter{
			{
				From: supplyCfg.Begin,
				Till: supplyCfg.End,
				Type: supplyCfg.DateFilterType,
			},
		},
		StatusIDs: []int{1, 2, 3, 4, 5, 6}, // All statuses
	}

	supplies, supplyReqs, err := DownloadSupplies(ctx, client, rl, filter)
	if err != nil {
		fmt.Printf("❌ Ошибка поставок: %v\n", err)
		return
	}

	// Save supplies to DB
	if len(supplies) > 0 {
		now := time.Now().Format("2006-01-02 15:04:05")
		rows := make([]sqlite.SupplyRow, 0, len(supplies))
		for i := range supplies {
			rows = append(rows, SupplyRowFromAPI(&supplies[i], now))
		}
		saved, err := repo.SaveSupplies(ctx, rows)
		if err != nil {
			fmt.Printf("❌ Ошибка сохранения поставок: %v\n", err)
			return
		}
		fmt.Printf("✅ Поставок: %d (сохранено: %d)\n", len(supplies), saved)
	} else {
		fmt.Println("  Поставок не найдено")
	}

	// Step 3: Download goods and packages for each supply
	fmt.Println("\n--- Товары и упаковка ---")
	pairs := make([]sqlite.SupplyIDPair, 0, len(supplies))
	for _, s := range supplies {
		supplyID := int64(0)
		if s.SupplyID != nil {
			supplyID = *s.SupplyID
		}
		pairs = append(pairs, sqlite.SupplyIDPair{SupplyID: supplyID, PreorderID: s.PreorderID})
	}

	goods, packages, detailReqs, err := DownloadSupplyDetails(ctx, client, repo, rl, pairs)
	if err != nil {
		fmt.Printf("❌ Ошибка деталей: %v\n", err)
	}
	fmt.Printf("✅ Товаров: %d, Упаковок: %d\n", goods, packages)

	totalReqs := supplyReqs + detailReqs
	if !skipRef {
		totalReqs += 2 // warehouses + tariffs
	}
	fmt.Printf("\nЗапросов: %d\n", totalReqs)
}

func printHelp() {
	fmt.Println("download-wb-supplies — загрузка поставок FBW с WB Supplies API")
	fmt.Println()
	fmt.Println("Флаги:")
	fmt.Println("  --config PATH         путь к конфигу (default: config.yaml)")
	fmt.Println("  --days N              дней от сегодня (default: 30)")
	fmt.Println("  --begin YYYY-MM-DD    начало периода")
	fmt.Println("  --end YYYY-MM-DD      конец периода")
	fmt.Println("  --db PATH             путь к базе (overrides config)")
	fmt.Println("  --mock                mock режим (без API)")
	fmt.Println("  --skip-reference      не скачивать справочники")
	fmt.Println("  --clean               удалить базу перед скачиванием")
	fmt.Println("  --help                справка")
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// getAPIKey retrieves API key with priority: env var > config value.
func getAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	apiKey := cfg.WB.APIKey
	if apiKey == "" || strings.HasPrefix(apiKey, "${") {
		return ""
	}
	return apiKey
}

// calculateDateRange computes date range from today.
// days=N means last N complete days, excluding today.
func calculateDateRange(days int) (string, string) {
	now := time.Now()
	endDate := now.AddDate(0, 0, -1).Format("2006-01-02") // yesterday
	beginDate := now.AddDate(0, 0, -days).Format("2006-01-02")
	return beginDate, endDate
}
