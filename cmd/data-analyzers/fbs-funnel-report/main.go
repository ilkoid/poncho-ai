// fbs-funnel-report — аналитика FBS-воронки (заказ → выкуп/отмена/возврат) → XLSX.
//
// Считает по сырым таблицам загрузчика download-wb-fbs-orders-v2 (read-only
// SELECT, никаких записей в БД):
//
//   - public.order_feed        — события и деньги (updated_at = дата текущего
//     статуса, поэтому «Динамика» = переходы по дням);
//   - public.fbs_orders/_status— когорты по v3-снимку + кросс-проверка ленты;
//   - public.cards             — читаемые названия номенклатур.
//
// Главный вопрос отчёта: реальная выручка периода, которая зависит от процента
// выкупа (выкуплено ₽ / упущено ₽), и его динамика по дням и когортам.
//
// Usage:
//
//	go run ./cmd/data-analyzers/fbs-funnel-report/ [options]
//
//	--days 30 --db wb_data_prod --xlsx reports/fbs-funnel-2026-08-25.xlsx
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

Аналитический отчёт FBS-воронки (заказ → выкуп/отмена/возврат) из PostgreSQL
в XLSX: события по дням, когорты с зрелостью, причины отмен, скорость цикла
заказ→выкуп, география, номенклатуры, кросс-проверка v3↔лента, деньги
(выручка = выкуплено; упущено = отмены+возвраты). Только SELECT.

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --days N        Окно в сутках: события/когорты за последние N сут МСК
                  (default 0 = весь диапазон данных)
  --all-models    Учитывать все модели выполнения (incl. FBW), а не только
                  склад продавца (is_mp)
  --db NAME       БД (overrides storage.pg_database)
  --xlsx PATH     Выходной xlsx (default: reports/fbs-funnel-<date>.xlsx)
  --html PATH     Самодостаточный HTML-дашборд (фильтры в браузере, офлайн):
                  auto = reports/fbs-dashboard-<date>.html; пусто = не собирать
  --dry-run       Показать параметры без обращения к БД
  -h, --help      Справка

Требует данные загрузчика download-wb-fbs-orders-v2:
public.fbs_orders + public.fbs_orders_status + public.order_feed.
`, os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	days := flag.Int("days", -1, "Окно в сутках (0 = весь диапазон)")
	allModels := flag.Bool("all-models", false, "Все модели выполнения (incl. FBW)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	xlsxPath := flag.String("xlsx", "", "Выходной xlsx (overrides config)")
	htmlPath := flag.String("html", "", "HTML-дашборд: auto|путь (overrides config; пусто = не собирать)")
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
	if *allModels {
		cfg.AllModels = true
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
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
	fmt.Println("  FBS-ВОРОНКА: ЗАКАЗ → ВЫКУП / ОТМЕНА / ВОЗВРАТ")
	fmt.Println(sep)
	fmt.Printf("  База:      %s\n", cfg.Storage.DisplayDB())
	scope := "склад продавца (FBS/DBS)"
	if cfg.AllModels {
		scope = "все модели (incl. FBW)"
	}
	window := "весь диапазон"
	if cfg.Days > 0 {
		window = fmt.Sprintf("последние %d сут", cfg.Days)
	}
	fmt.Printf("  Отбор:     %s | окно: %s\n", scope, window)
	fmt.Println(sep)

	if *dryRun {
		fmt.Println("\n  --dry-run: параметры (без обращения к БД):")
		fmt.Printf("    backend: %s\n    база:    %s\n    days=%d, all_models=%v\n",
			cfg.Storage.Backend, cfg.Storage.DisplayDB(), cfg.Days, cfg.AllModels)
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

	q := queryParams{allModels: cfg.AllModels}
	if cfg.Days > 0 {
		q.since = nowMoscow().AddDate(0, 0, -cfg.Days).Format("2006-01-02")
	}

	fmt.Print("  Запросы воронки (read-only)...")
	data, err := loadAll(ctx, pool.DB(), q)
	if err != nil {
		log.Fatalf("  %v\n  Данных ждёт от download-wb-fbs-orders-v2 (fbs_orders, fbs_orders_status, order_feed).", err)
	}
	fmt.Println(" ok")
	data.Days = cfg.Days

	if data.Coverage.FbsOrders == 0 && data.Coverage.FeedAll == 0 {
		log.Fatalf("  Таблицы FBS пусты — сначала прогоните download-wb-fbs-orders-v2.")
	}
	if data.Coverage.FeedMp == 0 && !cfg.AllModels {
		fmt.Println("  ⚠️  order_feed пуст по is_mp — листы ленты будут пусты (прогоните загрузчик с feed).")
	}

	if cfg.XLSX == "" {
		cfg.XLSX = filepath.Join("reports", fmt.Sprintf("fbs-funnel-%s.xlsx", time.Now().Format("2006-01-02")))
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

	// HTML-дашборд: те же фильтры (--days/--all-models/--db), данные кубом.
	if cfg.HTML != "" {
		fmt.Print("  Сборка HTML-дашборда (куб + словари)...")
		cube, err := loadCube(ctx, pool.DB(), q, cfg.Storage.PgDatabase)
		if err != nil {
			log.Fatalf("  %v", err)
		}
		if cfg.HTML == "auto" {
			cfg.HTML = filepath.Join("reports", fmt.Sprintf("fbs-dashboard-%s.html", time.Now().Format("2006-01-02")))
		}
		size, err := exportHTML(cube, cfg.HTML)
		if err != nil {
			log.Fatalf("  HTML: %v", err)
		}
		fmt.Printf(" ok (%d фактов, %.1f МБ) → %s\n",
			len(cube.Facts.Cnt), float64(size)/1024/1024, cfg.HTML)
	}

	// Консольная сводка.
	fmt.Println("\n  ── СВОДКА ──")
	bp := data.Totals.BuyoutPct()
	if bp < 0 {
		bp = 0
	}
	fmt.Printf("    Лента: %d строк, выкуп %d (%.1f%% завершённых), отмена %d, возвраты %d, в пути %d\n",
		data.Totals.Rows, data.Totals.Buyout, bp, data.Totals.Cancel,
		data.Totals.Returns, data.Totals.InFlight)
	fmt.Printf("    Деньги: выручка %.0f ₽ | упущено %.0f ₽ | заказано %.0f ₽\n",
		data.Totals.BuyoutRub, data.Totals.LostRub, data.Totals.OrderedRub)
	if data.Lifecycle.MedianH != nil {
		fmt.Printf("    Цикл заказ→выкуп: медиана %.1f ч, p90 %.1f ч\n",
			*data.Lifecycle.MedianH, ptrOr(data.Lifecycle.P90H, 0))
	}
	if data.V3 != nil {
		fmt.Printf("    v3-снимок: %d заданий (sold %d, отмен %d, в работе %d)\n",
			data.V3.Orders, data.V3.Sold, data.V3.Canceled, data.V3.InFlight)
	}
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Second), cfg.XLSX)
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// Days — окно отчёта в сутках (0 = весь диапазон данных).
	Days int `yaml:"days"`
	// AllModels — учитывать все модели выполнения (incl. FBW), а не только is_mp.
	AllModels bool `yaml:"all_models"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
	// XLSX — путь к выходному файлу.
	XLSX string `yaml:"xlsx"`
	// HTML — самодостаточный дашборд: "" = не собирать, "auto" = стандартное имя.
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

func ptrOr(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}
