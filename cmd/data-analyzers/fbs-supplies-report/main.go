// fbs-supplies-report — SLA-дашборд поставок на сортировочный центр (FBS) → HTML.
//
// Зеркальный взгляд к fbs-funnel-report: не «что стало с заказами дня»,
// а поставка как объект. Для каждой поставки: день формирования, размер
// (сборочных заданий), момент первого задания (старт SLA-часов), закрытие,
// приёмка на СЦ (scan_dt — обязательства выполнены). Вердикт: уложилась ли
// в 24ч от первого задания; внутри непопавшей — % заданий, отработавших
// в срок по своим часам (приёмка − создание задания ≤ порога).
//
// Только read-only SELECT; результат — самодостаточный офлайн-HTML
// (reports/fbs-supplies-<дата>.html). Ничего в БД не пишет.
//
// Usage:
//
//	go run ./cmd/data-analyzers/fbs-supplies-report/ [options]
//
//	--days 30 --db wb_data_prod --html auto
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func printHelp() {
	fmt.Printf(`Usage: %s [options]

SLA-дашборд поставок на СЦ (FBS): день → поставки дня → деталь поставки.
Вердикт ≤24ч от первого сборочного задания; доля заданий в срок по каждому
заданию (приёмка − создание). Только SELECT, выход — офлайн HTML.

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --days N        Поставки, сформированные за последние N сут МСК
                  (default 0 = весь диапазон данных)
  --db NAME       БД (overrides storage.pg_database)
  --html PATH     Выходной HTML (default: reports/fbs-supplies-<date>.html;
                  пусто = не собирать)
  --dry-run       Показать параметры без обращения к БД
  -h, --help      Справка

Требует данные download-wb-fbs-orders-v2:
public.fbs_supplies + public.fbs_orders (+ fbs_orders_status_log для отмен).
`, os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	days := flag.Int("days", -1, "Окно в сутках (0 = весь диапазон)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	htmlPath := flag.String("html", "", "Выходной HTML (пусто = не собирать)")
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
	if *days >= 0 {
		cfg.Days = *days
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *htmlPath != "" {
		cfg.HTML = *htmlPath
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
	fmt.Println("  ПОСТАВКИ НА СЦ · SLA-ДАШБОРД (FBS)")
	fmt.Println(sep)
	fmt.Printf("  База:      %s\n", cfg.Storage.DisplayDB())
	window := "весь диапазон"
	if cfg.Days > 0 {
		window = fmt.Sprintf("последние %d сут", cfg.Days)
	}
	fmt.Printf("  Отбор:     формирования (МСК) | окно: %s\n", window)
	fmt.Println(sep)

	if *dryRun {
		fmt.Println("\n  --dry-run: параметры (без обращения к БД):")
		fmt.Printf("    backend: %s\n    база:    %s\n    days=%d\n",
			cfg.Storage.Backend, cfg.Storage.DisplayDB(), cfg.Days)
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
		log.Fatalf("  Пул БД: %v (таблицы создаёт download-wb-fbs-orders-v2; проверьте pg_database)", err)
	}
	defer pool.Close()
	fmt.Println(" ok")

	cov, err := loadCoverage(ctx, pool.DB())
	if err != nil {
		log.Fatalf("  %v\n  Данных ждёт от download-wb-fbs-orders-v2 (fbs_supplies/fbs_orders).", err)
	}
	if cov.Supplies == 0 {
		log.Fatalf("  public.fbs_supplies пуста — сначала прогоните download-wb-fbs-orders-v2 (фаза поставок).")
	}

	since := ""
	if cfg.Days > 0 {
		since = nowMoscow().AddDate(0, 0, -cfg.Days).Format("2006-01-02")
	}

	fmt.Print("  Куб поставок (read-only)...")
	rows, err := loadSupplies(ctx, pool.DB(), since)
	if err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Printf(" ok (%d поставок)\n", len(rows))
	if len(rows) == 0 {
		log.Fatalf("  Куб пуст: за окном %s нет поставок с заданиями (supply_id='' не считается).", window)
	}

	cube := assembleCube(rows, cfg.Storage.PgDatabase)

	if cfg.HTML == "" {
		cfg.HTML = filepath.Join("reports", fmt.Sprintf("fbs-supplies-%s.html", time.Now().Format("2006-01-02")))
	}
	fmt.Print("  Сборка HTML-дашборда...")
	size, err := exportHTML(cube, cfg.HTML)
	if err != nil {
		log.Fatalf("  HTML: %v", err)
	}
	fmt.Printf(" ok (%.1f МБ) → %s\n", float64(size)/1024/1024, cfg.HTML)

	// Консольная сводка.
	fmt.Println("\n  ── СВОДКА ──")
	fmt.Printf("    Поставок %d, заданий %d, ждут приёмки %d\n",
		cube.Meta.Supplies, cube.Meta.Tasks, cube.Meta.Open)
	scanned := 0
	fit24 := 0
	var lags []float64
	for i := range cube.Sups.ID {
		// Открытость определяется по отсутствию scan_dt, а не по лагу:
		// реальный отрицательный лаг (аномалия) не должен молча
		// «превращаться» в открытую поставку и наоборот.
		if cube.Sups.Scanned[i] == "" {
			continue
		}
		scanned++
		if cube.Sups.LagH[i] < 0 {
			fmt.Printf("    АНОМАЛИЯ: %s — приёмка раньше первого задания (лаг %.1f ч)\n",
				cube.Sups.ID[i], cube.Sups.LagH[i])
		}
		if cube.Sups.LagH[i] <= 24 {
			fit24++
		}
		lags = append(lags, cube.Sups.LagH[i])
	}
	if scanned > 0 {
		fmt.Printf("    Уложились ≤24ч от 1-го задания: %d из %d (%.0f%%)\n",
			fit24, scanned, 100.0*float64(fit24)/float64(scanned))
		sort.Float64s(lags)
		fmt.Printf("    Лаг до приёмки: медиана %.1f ч\n", lags[len(lags)/2])
	}
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Second), cfg.HTML)
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// Days — окно отчёта в сутках (0 = весь диапазон данных).
	Days int `yaml:"days"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
	// HTML — путь к выходному дашборду; "auto" = reports/fbs-supplies-<дата>.html.
	HTML string `yaml:"html"`
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
		Days: 0,
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
	}
}

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

// nowMoscow — текущее время в МСК (заголовок дашборда, окно --days).
// Фолбэк — фиксированная зона UTC+3, а не локальное время машины:
// молчаливый сдвиг окна --days на часы хуже явной привязки к МСК.
func nowMoscow() time.Time {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.Now().In(time.FixedZone("MSK", 3*3600))
	}
	return time.Now().In(loc)
}
