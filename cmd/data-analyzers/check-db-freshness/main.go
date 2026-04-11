// check-db-freshness — утилита для проверки свежести данных в SQLite базе.
//
// Проверяет последнюю дату данных в каждой таблице и сравнивает с порогами.
// Использует конфигурацию из YAML или флаги CLI.
//
// Правила из dev_manifest.md:
//   - Rule 2: Configuration — YAML с ENV поддержкой
//   - Rule 6: cmd/ содержит только orchestration, бизнес-логика в pkg/
//   - Rule 9: Утилита для верификации данных (data-analyzers категория)
//   - Rule 11: Context propagation для отмены через Ctrl+C
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

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// Config — структура конфигурации утилиты.
type Config struct {
	Freshness config.FreshnessConfig `yaml:"freshness"`
}

// loadConfig загружает конфигурацию из YAML файла.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// defaultConfig возвращает конфигурацию по умолчанию.
func defaultConfig() *Config {
	fc := config.FreshnessConfig{}
	return &Config{
		Freshness: fc.GetDefaults(),
	}
}

// printHelp выводит справку.
func printHelp() {
	fmt.Printf(`Usage: %s [options]

Проверяет свежесть данных в SQLite базе wb-sales.db.

Options:
  --config PATH     Путь к конфигурационному файлу (default: config.yaml)
  --db PATH         Путь к базе данных (переопределяет config)
  --warn DAYS       Порог предупреждения в днях (default: 7)
  --critical DAYS   Порог критичности в днях (default: 14)
  --tables LIST     Список таблиц через запятую (пусто = все)
  --verbose         Подробный вывод

  -h, --help        Показать эту справку

Examples:
  # Проверка с настройками по умолчанию
  %s

  # Проверка другой базы
  %s --db /path/to/other.db

  # Только таблицы продаж и аналитики
  %s --tables sales,fbw_sales,funnel_metrics_daily

  # Кастомные пороги
  %s --warn 3 --critical 7

Exit codes:
  0  — все таблицы свежие
  1  — есть устаревшие или критические таблицы
  2  — ошибка выполнения
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

// printHeader выводит заголовок отчёта.
func printHeader(cfg *Config) {
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("           DATABASE FRESHNESS CHECKER")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("Database:  %s\n", cfg.Freshness.DbPath)
	fmt.Printf("Thresholds: Warning: %d days, Critical: %d days\n",
		cfg.Freshness.WarnAgeDays, cfg.Freshness.CritAgeDays)
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println()
}

// printSummary выводит итоговую статистику.
func printSummary(results []sqlite.FreshnessResult, duration time.Duration) {
	var fresh, empty, stale, critical, errors int

	for _, r := range results {
		switch r.Status {
		case sqlite.StatusFresh:
			fresh++
		case sqlite.StatusEmpty:
			empty++
		case sqlite.StatusStale:
			stale++
		case sqlite.StatusCritical:
			critical++
		case sqlite.StatusError:
			errors++
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("SUMMARY")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("Tables checked:  %d\n", len(results))
	fmt.Printf("Fresh (✅):      %d\n", fresh)
	if empty > 0 {
		fmt.Printf("Empty (📭):      %d\n", empty)
	}
	fmt.Printf("Stale (⚠️ ):       %d\n", stale)
	fmt.Printf("Critical (❌):   %d\n", critical)
	if errors > 0 {
		fmt.Printf("Errors (💥):     %d\n", errors)
	}
	fmt.Printf("Duration:        %s\n", duration.Round(time.Millisecond))

	// Список таблиц, требующих внимания
	if empty > 0 || stale > 0 || critical > 0 {
		fmt.Println()
		fmt.Println("⚠️  Tables requiring attention:")
		for _, r := range results {
			if r.IsEmpty() || r.IsStale() || r.IsCritical() {
				emoji := sqlite.StatusIndicator(r.Status)
				fmt.Printf("   %s %s (%s)\n", emoji, r.Table, sqlite.FormatAge(r.AgeDays))
			}
		}
	}

	fmt.Println(strings.Repeat("═", 60))
}

// runCheck выполняет проверку свежести данных.
func runCheck(ctx context.Context, cfg *Config) ([]sqlite.FreshnessResult, error) {
	// Создаём проверщик
	checker, err := sqlite.NewFreshnessChecker(cfg.Freshness.DbPath)
	if err != nil {
		return nil, fmt.Errorf("create checker: %w", err)
	}
	defer checker.Close()

	// Получаем спецификации таблиц
	allSpecs := sqlite.AllTableSpecs()
	specs := sqlite.FilterTableSpecs(allSpecs, cfg.Freshness.Tables)

	if cfg.Freshness.Verbose {
		log.Printf("Checking %d tables...\n", len(specs))
	}

	// Проверяем все таблицы
	results := checker.CheckAll(
		ctx,
		specs,
		cfg.Freshness.WarnAgeDays,
		cfg.Freshness.CritAgeDays,
	)

	return results, nil
}

// printResults выводит результаты проверки.
func printResults(results []sqlite.FreshnessResult, verbose bool) {
	sqlite.PrintHeader()

	for _, r := range results {
		if r.Status == sqlite.StatusError {
			if verbose {
				fmt.Printf("💥 %s.%s: %v\n", r.Category, r.Table, r.Error)
			}
			continue
		}

		// Для таблиц без колонки даты показываем количество записей
		if r.DateColumn == "N/A" {
			countStr := sqlite.FormatRecordCount(r.RecordCount)
			sqlite.PrintRowWithCount(
				r.Category,
				r.Table,
				countStr,
				string(r.Status),
			)
		} else {
			sqlite.PrintRow(
				r.Category,
				r.Table,
				sqlite.FormatDate(r.LatestDate),
				sqlite.FormatAge(r.AgeDays),
				string(r.Status),
			)
		}
	}
}

func main() {
	// Парсим флаги
	configPath := flag.String("config", "config.yaml", "Path to config file")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	warnAge := flag.Int("warn", 0, "Warning threshold in days")
	critAge := flag.Int("critical", 0, "Critical threshold in days")
	tablesList := flag.String("tables", "", "Comma-separated table list")
	verbose := flag.Bool("verbose", false, "Detailed output")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Загружаем конфигурацию
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Применяем дефолтные значения
	cfg.Freshness = cfg.Freshness.GetDefaults()

	// CLI override для db path
	if *dbPath != "" {
		cfg.Freshness.DbPath = *dbPath
	}

	// CLI override для порогов
	if *warnAge > 0 {
		cfg.Freshness.WarnAgeDays = *warnAge
	}
	if *critAge > 0 {
		cfg.Freshness.CritAgeDays = *critAge
	}

	// CLI override для списка таблиц
	if *tablesList != "" {
		tables := strings.Split(*tablesList, ",")
		for i := range tables {
			tables[i] = strings.TrimSpace(tables[i])
		}
		cfg.Freshness.Tables = tables
	}

	// CLI override для verbose
	if *verbose {
		cfg.Freshness.Verbose = true
	}

	// Выводим заголовок
	printHeader(cfg)

	// Создаём context с отменой
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Обработка Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted!")
		cancel()
	}()

	// Запускаем проверку
	start := time.Now()
	results, err := runCheck(ctx, cfg)
	if err != nil {
		log.Fatalf("❌ Check failed: %v", err)
	}
	duration := time.Since(start)

	// Выводим результаты
	printResults(results, cfg.Freshness.Verbose)

	// Выводим summary
	printSummary(results, duration)

	// Определяем exit code
	exitCode := 0
	for _, r := range results {
		if r.IsEmpty() || r.IsCritical() || r.IsStale() {
			exitCode = 1
			break
		}
		if r.Status == sqlite.StatusError {
			exitCode = 2
			break
		}
	}

	os.Exit(exitCode)
}
