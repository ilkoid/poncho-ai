// fbs-orders-report — объём, свежесть и происхождение сборочных заданий FBS → XLSX.
//
// Источники (read-only SELECT, все в PG wb_data_prod):
//
//   - public.fbs_orders / public.fbs_orders_status — одноразовый снимок
//     GET /api/v3/orders + POST /api/v3/orders/status (fetch-fbs-orders.sh,
//     swagger docs/wb_api_swagger/03-orders-fbs.yaml:401);
//   - public.orders — Statistics API «Заказы» (rid == srid, 03-orders-fbs.yaml:3526,
//     12-reports.yaml:2341): order_date, warehouse_type («Склад WB»=FBO / «Склад
//     продавца»=FBS, 12-reports.yaml:2257);
//   - public.operational_sales — выкуп по srid;
//   - public.cards — артикул продавца / предмет для читаемости.
//
// Классификация происхождения (origin):
//
//   - native      — лаг created_at − order_date ≤ lag_hours: заказ сразу размещён
//                   как FBS («Склад продавца»);
//   - migrated    — лаг > lag_hours и/или warehouse_type = «Склад WB»: задание
//                   создано намного позже заказа — кандидат в переведённые FBO→FBS;
//   - no_stats_row — строка в Statistics API не найдена (вся корзина отсутствует).
//
// Тяжёлый джойн fbs_orders × orders выполняется один раз в TEMP-таблицу (на одном
// соединении пула), дальше все листы читают её.
//
// Usage:
//
//	go run ./cmd/data-analyzers/fbs-orders-report/ [options]
//
//	--lag-hours 24 --old-days 7 --xlsx reports/fbs-orders-2026-08-16.xlsx
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

Отчёт по сборочным заданиям FBS (объём, свежесть, происхождение) из PostgreSQL.
Классификация: native (заказ сразу FBS) / migrated (лаг создания задания > N ч —
переведённые из FBO) / no_stats_row (нет строки в Statistics API). Возраст активных
заданий (new/confirm), детализация старых невыполненных. Только SELECT.

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --lag-hours N   Порог лага created_at−order_date для класса migrated (default 24)
  --old-days N    «Старое» активное задание: возраст > N дней (default 7)
  --db NAME       БД (overrides storage.pg_database)
  --xlsx PATH     Выходной xlsx (default: reports/fbs-orders-report-<date>.xlsx)
  --dry-run       Показать параметры без обращения к БД
  -h, --help      Справка

Требует staged-данные: public.fbs_orders + public.fbs_orders_status
(import-fbs-snapshot.sh после fetch-fbs-orders.sh).
`, os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	lagHours := flag.Int("lag-hours", 0, "Порог лага (ч) для migrated")
	oldDays := flag.Int("old-days", 0, "Возраст (дн) «старого» активного задания")
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

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Конфиг %s не найден (%v), используем значения по умолчанию.", *configPath, err)
		cfg = defaultConfig()
	}
	cfg.applyDefaults()
	if *lagHours > 0 {
		cfg.LagHours = *lagHours
	}
	if *oldDays > 0 {
		cfg.OldDays = *oldDays
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
	}

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
	fmt.Println("  СБОРОЧНЫЕ ЗАДАНИЯ FBS: ОБЪЁМ, СВЕЖЕСТЬ, ПРОИСХОЖДЕНИЕ")
	fmt.Println(sep)
	fmt.Printf("  База:      %s\n", cfg.Storage.DisplayDB())
	fmt.Printf("  Пороги:    migrated = лаг > %d ч; «старое» = возраст > %d дн\n", cfg.LagHours, cfg.OldDays)
	fmt.Println(sep)

	if *dryRun {
		fmt.Println("\n  --dry-run: параметры запроса (без обращения к БД):")
		fmt.Printf("    backend: %s\n", cfg.Storage.Backend)
		fmt.Printf("    база:    %s\n", cfg.Storage.DisplayDB())
		fmt.Printf("    lag-hours=%d, old-days=%d\n", cfg.LagHours, cfg.OldDays)
		return
	}

	start := time.Now()

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

	// TEMP-таблица живёт на одном соединении — держим его до конца отчёта.
	conn, err := pool.DB().Acquire(ctx)
	if err != nil {
		log.Fatalf("  Соединение: %v", err)
	}
	defer conn.Release()
	defer dropJoined(ctx, conn)

	fmt.Printf("  Объединение снимка со статистикой (тяжёлый джойн, ~2–4 мин)...")
	if err := buildJoined(ctx, conn, cfg.LagHours); err != nil {
		log.Fatalf("  Джойн: %v", err)
	}
	fmt.Println(" ok")

	fmt.Print("  Агрегаты (origin, динамика, возраст, статусы)...")
	data, err := loadAll(ctx, conn, cfg.OldDays)
	if err != nil {
		log.Fatalf("  Агрегаты: %v", err)
	}
	fmt.Println(" ok")

	fmt.Printf("  Детализация старых/мигрированных: %d строк\n", len(data.Detail))

	if cfg.XLSX == "" {
		cfg.XLSX = filepath.Join("reports", fmt.Sprintf("fbs-orders-report-%s.xlsx", time.Now().Format("2006-01-02")))
	}
	if dir := filepath.Dir(cfg.XLSX); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("  Создание папки %s: %v", dir, err)
		}
	}
	fmt.Printf("  Экспорт XLSX: %s...", cfg.XLSX)
	if err := exportXLSX(data, cfg, cfg.XLSX); err != nil {
		log.Fatalf("  Экспорт: %v", err)
	}
	fmt.Println(" ok")

	// Консольная сводка.
	fmt.Println("\n  ── СВОДКА ──")
	for _, o := range data.Origins {
		fmt.Printf("    %-14s заданий %6d | активных %6d | выкуплено %6d | лаг (медиана) %6.1f ч\n",
			o.Origin, o.Tasks, o.Active, o.Sold, o.MedLagH)
	}
	fmt.Printf("    Активных (new/confirm): %d, из них старше %d дней: %d\n",
		data.ActiveTotal(), cfg.OldDays, data.OldActiveCount)
	fmt.Printf("    Покрытие снимка: %s … %s, строк в статистике: %.1f%%\n",
		data.Coverage.MinDate, data.Coverage.MaxDate, data.Coverage.MatchPct())
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Second), cfg.XLSX)
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// LagHours — порог лага created_at−order_date (часы) для класса migrated.
	LagHours int `yaml:"lag_hours"`
	// OldDays — возраст (дней), с которого активное задание считается «старым».
	OldDays int `yaml:"old_days"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
	// XLSX — путь к выходному файлу.
	XLSX string `yaml:"xlsx"`
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if err := config.LoadYAML(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		LagHours: 24,
		OldDays:  7,
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
	}
}

func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.LagHours <= 0 {
		c.LagHours = d.LagHours
	}
	if c.OldDays <= 0 {
		c.OldDays = d.OldDays
	}
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
