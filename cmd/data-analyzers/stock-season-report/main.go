// stock-season-report — отчёт по остаткам WB в разрезе складов × сезонов (xlsx из PostgreSQL).
//
// По срезу snapshot_date из stocks_daily_warehouses собирает остатки в разрезе
// склад × сезон (сезон берётся из 1С: stocks.nm_id → cards.vendor_code = onec_goods.article
// → onec_goods.season). Три типа остатков: на складе (quantity), в пути к клиенту
// (in_way_to_client), в пути от клиента (in_way_from_client). Только штуки — денег
// тут нет (себестоимости в базе нет, отдельная задача). Вывод — xlsx (5 листов).
//
// Только SELECT — запись в БД не производится.
//
// Usage:
//
//	go run ./cmd/data-analyzers/stock-season-report/ [options]
//
//	--config config.yaml --date 2026-07-22 --xlsx /tmp/stock-season.xlsx
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Отчёт по остаткам WB в разрезе складов × сезонов из PostgreSQL (wb_data_prod).
Срез stocks_daily_warehouses на выбранную дату, группировка по складу и сезону 1С.
Три типа остатков: на складе / в пути к клиенту / в пути от клиента. Только штуки.
Только SELECT (read-only). Вывод — xlsx (5 листов).

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --date YYYY-MM-DD  Дата среза (default: последний доступный срез)
  --db NAME       БД (overrides storage.pg_database, напр. wb_data_test)
  --xlsx PATH     Выходной xlsx (default: reports/stock-season-<date>.xlsx)
  --dry-run       Показать параметры без обращения к БД
  -h, --help      Справка

Examples:
  %s --date 2026-07-22
  %s --date 2026-07-22 --xlsx /tmp/stock-season.xlsx
  %s --db wb_data_test --date 2026-07-20
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	dateFlag := flag.String("date", "", "Дата среза YYYY-MM-DD (default: последний срез)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	xlsxPath := flag.String("xlsx", "", "Выходной xlsx (overrides config)")
	dryRun := flag.Bool("dry-run", false, "Показать параметры без обращения к БД")
	help := flag.Bool("help", false, "Справка")
	flag.BoolVar(help, "h", false, "Справка")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// ── Конфиг ──
	cfg, err := loadConfig(*configPath)
	if err != nil {
		// Внешний config.yaml не обязателен — используем defaults (как collection-readiness).
		log.Printf("Конфиг %s не найден (%v), используем значения по умолчанию.", *configPath, err)
		cfg = defaultConfig()
	}
	cfg.applyDefaults()

	// CLI overrides.
	if *dateFlag != "" {
		cfg.Date = *dateFlag
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
	}

	// Graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n  Прервано!")
		cancel()
	}()

	sep := strings.Repeat("═", 64)
	fmt.Println(sep)
	fmt.Println("  ОСТАТКИ WB В РАЗРЕЗЕ СКЛАДОВ × СЕЗОНОВ")
	fmt.Println(sep)
	fmt.Printf("  База:   %s\n", cfg.Storage.DisplayDB())
	fmt.Printf("  Дата:   %s\n", dateOrLatest(cfg.Date))
	fmt.Println(sep)

	start := time.Now()

	// ── --dry-run: параметры без БД ──
	if *dryRun {
		fmt.Println("\n  --dry-run: параметры запроса (без обращения к БД):")
		fmt.Printf("    backend: %s\n", cfg.Storage.Backend)
		fmt.Printf("    база:    %s\n", cfg.Storage.DisplayDB())
		fmt.Printf("    дата:    %s\n", dateOrLatest(cfg.Date))
		return
	}

	// ── PG connect (read-only) ──
	dsn, err := cfg.Storage.GetEffectiveDSN()
	if err != nil {
		log.Fatalf("  DSN: %v", err)
	}
	fmt.Print("\n  Подключение к PostgreSQL...")
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		log.Fatalf("  Пул БД: %v", err)
	}
	defer pool.Close()
	fmt.Println(" ok")

	// ── Резолв даты среза ──
	date := cfg.Date
	if date == "" {
		fmt.Print("  Поиск последнего среза...")
		date, err = latestSnapshotDate(ctx, pool.DB())
		if err != nil {
			log.Fatalf("  Поиск среза: %v", err)
		}
		if date == "" {
			log.Fatalf("  Таблица stocks_daily_warehouses пуста — нет данных для отчёта.")
		}
		fmt.Printf(" %s\n", date)
	}

	// ── Проверка наличия среза ──
	nRows, err := snapshotExists(ctx, pool.DB(), date)
	if err != nil {
		log.Fatalf("  Проверка среза: %v", err)
	}
	if nRows == 0 {
		log.Fatalf("  Срез на %s отсутствует в stocks_daily_warehouses. Доступные даты — см. SELECT MAX(snapshot_date).", date)
	}

	// ── Агрегат ──
	fmt.Printf("  Агрегация склад × сезон (%s, %d строк)...", date, nRows)
	agg, err := loadAggregate(ctx, pool.DB(), date)
	if err != nil {
		log.Fatalf("  Агрегация: %v", err)
	}
	fmt.Printf(" %d строк\n", len(agg))

	// ── Drill-down детали ──
	fmt.Print("  Drill-down по артикулам...")
	details, err := loadDetails(ctx, pool.DB(), date)
	if err != nil {
		log.Printf("\n  WARN: детали не загружены (%v) — отчёт без листа «Детали»", err)
		details = nil
	} else {
		fmt.Printf(" %d строк\n", len(details))
	}

	// ── Контрольная сумма (для сверки в консоли) ──
	vOn, vTo, vFrom, err := verifyTotal(ctx, pool.DB(), date)
	if err != nil {
		log.Printf("  WARN: контрольная сумма не получена: %v", err)
	} else {
		var sumOn, sumTo, sumFrom int64
		for _, r := range agg {
			sumOn += r.OnStock
			sumTo += r.InWayToClient
			sumFrom += r.InWayFromClient
		}
		fmt.Printf("  Сверка: на складе %d (%s), к клиенту %d (%s), от клиента %d (%s)\n",
			sumOn, checkMark(sumOn, vOn),
			sumTo, checkMark(sumTo, vTo),
			sumFrom, checkMark(sumFrom, vFrom))
	}

	// ── Имя файла по умолчанию ──
	if cfg.XLSX == "" {
		cfg.XLSX = filepath.Join("reports", fmt.Sprintf("stock-season-%s.xlsx", date))
	}
	if dir := filepath.Dir(cfg.XLSX); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("  Создание папки %s: %v", dir, err)
		}
	}

	// ── Экспорт ──
	fmt.Printf("  Экспорт XLSX: %s...", cfg.XLSX)
	if err := exportXLSX(agg, details, date, cfg.XLSX); err != nil {
		log.Fatalf("  Экспорт: %v", err)
	}
	fmt.Println(" ok")

	// Краткая сводка в консоль.
	t := totalsBySeason(agg)
	var grand seasonTotals
	for _, s := range allSeasons(agg) {
		x := t[s]
		grand.OnStock += x.OnStock
		grand.InWayToClient += x.InWayToClient
		grand.InWayFromClient += x.InWayFromClient
		grand.Total += x.Total
	}
	fmt.Printf("\n  Сезонов: %d | Складов: %d | ИТОГО: на складе %d, к клиенту %d, от клиента %d, всего %d\n",
		len(t), countWarehouses(agg), grand.OnStock, grand.InWayToClient, grand.InWayFromClient, grand.Total)
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Millisecond), cfg.XLSX)
}

// dateOrLatest — текст для баннера: дату или пометку «(последний срез)».
func dateOrLatest(date string) string {
	if date == "" {
		return "(последний срез)"
	}
	return date
}

// checkMark возвращает ✓ при совпадении или расхождение в скобках — для строки сверки.
func checkMark(got, want int64) string {
	if got == want {
		return "✓"
	}
	return fmt.Sprintf("✗ ждём %d", want)
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// Date — дата среза YYYY-MM-DD. Пусто → последний доступный срез.
	Date string `yaml:"date"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
	// XLSX — путь к выходному файлу. Пусто → reports/stock-season-<date>.xlsx.
	XLSX string `yaml:"xlsx"`
}

// loadConfig загружает конфигурацию из YAML-файла. Предынициализация defaultConfig()
// сохраняет корректные дефолты для полей, не упомянутых в YAML.
func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if err := config.LoadYAML(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultConfig возвращает конфигурацию по умолчанию.
func defaultConfig() *Config {
	return &Config{
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
	}
}

// applyDefaults заполняет пустые поля значениями по умолчанию.
func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.Storage.Backend == "" {
		c.Storage.Backend = d.Storage.Backend
	}
	if c.Storage.PgDatabase == "" {
		c.Storage.PgDatabase = d.Storage.PgDatabase
	}
	if c.Storage.PgPasswordEnv == "" {
		c.Storage.PgPasswordEnv = d.Storage.PgPasswordEnv
	}
}
