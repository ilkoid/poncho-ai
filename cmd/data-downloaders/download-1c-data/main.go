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
)

type Config struct {
	OneC config.OneCConfig `yaml:"onec"`
}

func main() {
	// 1. Parse flags
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	clean := flag.Bool("clean", false, "очистить таблицы 1C/PIM перед загрузкой")
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
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}
	defaults := cfg.OneC.GetDefaults()
	if *dbPath != "" {
		defaults.DbPath = *dbPath
	}

	// 3. Get API URLs (priority: env > config)
	apiURL := getAPIURL("ONEC_API_URL", defaults.APIUrl)
	pimURL := getAPIURL("ONEC_PIM_URL", defaults.PIMUrl)
	if apiURL == "" {
		log.Fatal("❌ Нет URL для 1C API. Установите ONEC_API_URL или настройте yaml api_url.")
	}
	if pimURL == "" {
		log.Fatal("❌ Нет URL для PIM API. Установите ONEC_PIM_URL или настройте yaml pim_url.")
	}

	// 4. Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigChan; fmt.Println("\n⚠️  Прервано"); cancel() }()

	// 5. Open database
	repo, err := sqlite.NewSQLiteSalesRepository(defaults.DbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка открытия базы: %v", err)
	}
	defer repo.Close()

	// 6. Clean if requested
	if *clean {
		fmt.Println("🧹 Очистка таблиц 1C/PIM...")
		if err := repo.CleanOneCData(ctx); err != nil {
			log.Fatalf("❌ Ошибка очистки: %v", err)
		}
	}

	// 7. Sequential download: goods → prices → PIM
	client := NewOneCClient()
	snapshotDate := time.Now().Format("2006-01-02")
	totalStart := time.Now()

	// Build full endpoint URLs from base: /feeds/ones → /feeds/ones/goods/, /feeds/ones/prices/
	goodsURL := strings.TrimRight(apiURL, "/") + "/goods/"
	pricesURL := strings.TrimRight(apiURL, "/") + "/prices/"

	// Step 1: Goods + SKUs
	fmt.Println(repeat("═", 71))
	fmt.Println("📦 Шаг 1/3: Загрузка товаров 1C...")
	fmt.Println(repeat("═", 71))
	goodsStart := time.Now()
	goodsCount, skuCount, err := client.FetchGoods(ctx, goodsURL, repo)
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки товаров: %v", err)
	}
	fmt.Printf("✅ Товары: %d, SKU: %d (%.1fs)\n", goodsCount, skuCount, time.Since(goodsStart).Seconds())

	if ctx.Err() != nil {
		return
	}

	// Step 2: Prices
	fmt.Println(repeat("═", 71))
	fmt.Println("💰 Шаг 2/3: Загрузка цен 1C...")
	fmt.Println(repeat("═", 71))
	pricesStart := time.Now()
	priceRows, priceProducts, err := client.FetchPrices(ctx, pricesURL, snapshotDate, repo)
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки цен: %v", err)
	}
	fmt.Printf("✅ Цены: %d строк от %d товаров (%.1fs)\n", priceRows, priceProducts, time.Since(pricesStart).Seconds())

	if ctx.Err() != nil {
		return
	}

	// Step 3: PIM Goods
	fmt.Println(repeat("═", 71))
	fmt.Println("📋 Шаг 3/3: Загрузка PIM атрибутов...")
	fmt.Println(repeat("═", 71))
	pimStart := time.Now()
	pimCount, err := client.FetchPIMGoods(ctx, pimURL, repo)
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки PIM: %v", err)
	}
	fmt.Printf("✅ PIM товары: %d (%.1fs)\n", pimCount, time.Since(pimStart).Seconds())

	// Summary
	fmt.Println(repeat("═", 71))
	fmt.Println("📊 Итого:")
	fmt.Printf("   1C товары:  %d\n", goodsCount)
	fmt.Printf("   1C SKU:     %d\n", skuCount)
	fmt.Printf("   1C цены:    %d строк (snapshot: %s)\n", priceRows, snapshotDate)
	fmt.Printf("   PIM товары: %d\n", pimCount)
	fmt.Printf("   Время:      %.1fs\n", time.Since(totalStart).Seconds())
	fmt.Println(repeat("═", 71))
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

func printHelp() {
	fmt.Println("download-1c-data — загрузчик данных из 1С/PIM API")
	fmt.Println()
	fmt.Println("Использование:")
	fmt.Println("  download-1c-data [опции]")
	fmt.Println()
	fmt.Println("Опции:")
	fmt.Println("  --config PATH   путь к конфигу (default: config.yaml)")
	fmt.Println("  --db PATH       путь к SQLite базе (overrides config)")
	fmt.Println("  --clean         очистить таблицы 1C/PIM перед загрузкой")
	fmt.Println("  --help, -h      справка")
	fmt.Println()
	fmt.Println("Переменные окружения:")
	fmt.Println("  ONEC_API_URL    URL 1C Goods+Prices API (с basic auth)")
	fmt.Println("  ONEC_PIM_URL    URL PIM Goods API (с basic auth)")
}

func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
