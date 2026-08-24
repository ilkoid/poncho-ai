// stock-warehouse-report — отчёт по остаткам FBO (склады WB) → XLSX, по листу на каждый склад.
//
// По срезу snapshot_date из stocks_daily_warehouses собирает гранулярные остатки
// (склад × артикул × SKU/размер × 3 типа остатков) и выгружает в XLSX:
//
//   - лист «Сводка» — итоги по каждому складу (Σ свободный / в пути к / в пути от / Итого);
//   - по одному листу на каждый склад — Артикул продавца / Бренд / Предмет / Штрихкод /
//     Размер / 3 типа остатков / Итого.
//
// Источник — public.stocks_daily_warehouses (FBO/склады WB; наполняется от endpoint'а
// /api/analytics/v1/stocks-report/wb-warehouses, swagger 11-analytics.yaml:1257).
// Справочник cards (vendor_code/brand/subject_name) и card_sizes (tech_size/wb_size +
// skus_json со штрихкодами) подтягиваются LEFT JOIN.
//
// Только SELECT — запись в БД не производится.
//
// Usage:
//
//	go run ./cmd/data-analyzers/stock-warehouse-report/ [options]
//
//	--config config.yaml --date 2026-08-04 --xlsx /tmp/stock-by-warehouse.xlsx
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

Отчёт по остаткам FBO (склады WB) из PostgreSQL (wb_data_prod).
Срез stocks_daily_warehouses на выбранную дату, гранулярность: склад × артикул продавца
× SKU/размер × штрихкод. Три типа остатков в отдельных колонках: свободный / в пути к
клиенту / в пути от клиента + Итого. Вывод — xlsx: лист «Сводка» + по листу на каждый склад.
Только SELECT (read-only).

Options:
  --config PATH      Путь к конфигу (default: config.yaml рядом с утилитой)
  --date YYYY-MM-DD  Дата среза (default: последний доступный срез)
  --db NAME          БД (overrides storage.pg_database, напр. wb_data_test)
  --xlsx PATH        Выходной xlsx (default: reports/stock-by-warehouse-<date>.xlsx)
  --dry-run          Показать параметры без обращения к БД
  -h, --help         Справка

Examples:
  %s --date 2026-08-04
  %s --date 2026-08-04 --xlsx /tmp/stock-by-warehouse.xlsx
  %s --db wb_data_test --date 2026-08-01
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
		// Внешний config.yaml не обязателен — используем defaults.
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
	fmt.Println("  ОСТАТКИ FBO (СКЛАДЫ WB) → XLSX ПО СКЛАДАМ")
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
		log.Fatalf("  Срез на %s отсутствует в stocks_daily_warehouses.", date)
	}

	// ── Загрузка гранулярных остатков ──
	fmt.Printf("  Загрузка остатков (%s, ~%d строк)...", date, nRows)
	rows, err := loadStocks(ctx, pool.DB(), date)
	if err != nil {
		log.Fatalf("  Загрузка остатков: %v", err)
	}
	fmt.Printf(" %d строк\n", len(rows))

	// ── Группировка по складам ──
	groups := groupByWarehouse(rows)
	fmt.Printf("  Группировка: %d складов\n", len(groups))

	// ── Контрольная сумма ──
	vOn, vTo, vFrom, err := verifyTotal(ctx, pool.DB(), date)
	if err != nil {
		log.Printf("  WARN: контрольная сумма не получена: %v", err)
	} else {
		var sumOn, sumTo, sumFrom int64
		for _, g := range groups {
			sumOn += g.OnStock
			sumTo += g.InWayToClient
			sumFrom += g.InWayFromClient
		}
		fmt.Printf("  Сверка: свободный %d (%s), к клиенту %d (%s), от клиента %d (%s)\n",
			sumOn, checkMark(sumOn, vOn),
			sumTo, checkMark(sumTo, vTo),
			sumFrom, checkMark(sumFrom, vFrom))
	}

	// ── Имя файла по умолчанию ──
	if cfg.XLSX == "" {
		cfg.XLSX = filepath.Join("reports", fmt.Sprintf("stock-by-warehouse-%s.xlsx", date))
	}
	if dir := filepath.Dir(cfg.XLSX); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("  Создание папки %s: %v", dir, err)
		}
	}

	// ── Экспорт ──
	fmt.Printf("  Экспорт XLSX (%d листов): %s...", len(groups)+1, cfg.XLSX)
	if err := exportXLSX(groups, date, cfg.XLSX); err != nil {
		log.Fatalf("  Экспорт: %v", err)
	}
	fmt.Println(" ok")

	// Краткая сводка в консоль.
	var grandOn, grandTo, grandFrom int64
	for _, g := range groups {
		grandOn += g.OnStock
		grandTo += g.InWayToClient
		grandFrom += g.InWayFromClient
	}
	fmt.Printf("\n  Складов: %d | строк: %d | ИТОГО: свободный %d, к клиенту %d, от клиента %d, всего %d\n",
		len(groups), len(rows), grandOn, grandTo, grandFrom, grandOn+grandTo+grandFrom)
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
	// XLSX — путь к выходному файлу. Пусто → reports/stock-by-warehouse-<date>.xlsx.
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
