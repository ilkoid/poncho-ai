// build-region-sales-daily — «продажи по регионам» из уже скачанных данных.
//
// Замена WB-отчёта «Продажи по регионам» (GET /api/v1/analytics/region-sale
// отключается 3 ноября 2026, release-notes id=590). Новый загрузчик не нужен:
// география и статусы — в public.order_feed (лента заказов, качается
// download-wb-fbs-orders-v2 с feed_mp_only=false), регион/страна — в
// public.orders, деньги — в public.operational_sales (сшивка по srid).
//
// Пишет analytical.region_sales_daily (grain: день-статуса × nm × город ×
// ФО × регион × страна × is_mp), идемпотентно по окну: повторный прогон дня
// удаляет и пересобирает свои строки. Семантика дня — дата ТЕКУЩЕГО статуса
// юнита (order_feed.updated_at, МСК): лента отдаёт события именно так.
//
// Деньги несут ДВЕ семантики: seller_price_sum — валовая цена продавца по
// выкупам; price_with_disc_sum / for_pay_* — статистика WB (упрощённый
// нетто, продажи 'S*' минус возвраты 'R*'). Точного «к перечислению за
// товар» старого отчёта (saleInvoiceCostPrice) нет ни в одном живом
// эндпоинте — утрата задокументирована, эмуляции нет.
//
// Usage:
//
//	go run ./cmd/data-analyzers/build-region-sales-daily/ [options]
//
//	--days 7 --db wb_data_test
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
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func printHelp() {
	fmt.Printf(`Usage: %s [options]

«Продажи по регионам» без отчёта region-sale (off 3.11.2026): агрегат
analytical.region_sales_daily из order_feed ⋈ orders ⋈ operational_sales.
Идемпотентно по окну: строки окна удаляются и пересобираются.

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --db NAME       БД (overrides storage.pg_database)
  --days N        Окно_refresh в сутках назад, от сегодня-1 (default: конфиг, 7)
  --from DATE     Явное окно 'YYYY-MM-DD' (с --to; перекрывает --days)
  --to DATE       Конец окна 'YYYY-MM-DD' (default: вчера)
  --rebuild-all   Окно = вся история ленты в order_feed (min(updated_at))
  --dry-run       Показать окно и план без обращения к БД
  -h, --help      Справка

Требует сырьё download-all: public.order_feed (download-wb-fbs-orders-v2,
feed_mp_only=false), public.orders, public.operational_sales.
`, os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	days := flag.Int("days", 0, "Окно в сутках (default: конфиг)")
	fromDay := flag.String("from", "", "Начало окна 'YYYY-MM-DD'")
	toDay := flag.String("to", "", "Конец окна 'YYYY-MM-DD'")
	rebuildAll := flag.Bool("rebuild-all", false, "Вся история ленты order_feed")
	dryRun := flag.Bool("dry-run", false, "Показать окно без обращения к БД")
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
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *days > 0 {
		cfg.Days = *days
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
	fmt.Println("  ПРОДАЖИ ПО РЕГИОНАМ · замена region-sale (off 3.11.2026)")
	fmt.Println(sep)
	fmt.Printf("  База:     %s\n", cfg.Storage.DisplayDB())
	fmt.Printf("  Истоки:   order_feed ⋈ orders ⋈ operational_sales (srid)\n")

	// Окно: по умолчанию [сегодня−days, вчера] по МСК. Лента качается с
	// feed_days=7, статусы старше окна не осведомлены — глубже --days без
	// --rebuild-all нет смысла (кроме осознанного бэкфилла).
	loc := moscowLoc()
	today := time.Now().In(loc)
	var from, to time.Time
	switch {
	case *rebuildAll:
		// from определится по min(order_feed.updated_at) после подключения.
		to = today.AddDate(0, 0, -1)
	case *fromDay != "":
		parsed, err := time.ParseInLocation("2006-01-02", *fromDay, loc)
		if err != nil {
			log.Fatalf("  --from: ожидается YYYY-MM-DD, получено %q", *fromDay)
		}
		from = parsed
		to = today.AddDate(0, 0, -1)
		if *toDay != "" {
			parsedTo, err := time.ParseInLocation("2006-01-02", *toDay, loc)
			if err != nil {
				log.Fatalf("  --to: ожидается YYYY-MM-DD, получено %q", *toDay)
			}
			to = parsedTo
		}
	default:
		from = today.AddDate(0, 0, -cfg.Days)
		to = today.AddDate(0, 0, -1)
	}
	if !from.IsZero() && from.After(to) {
		log.Fatalf("  Окно вывернуто: from %s > to %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	fmt.Printf("  Окно:     %s … %s (МСК, дни текущего статуса юнита)\n",
		dayOr(from, "<min ленты>"), to.Format("2006-01-02"))

	if *dryRun {
		fmt.Println("\n  --dry-run: к БД не обращаюсь.")
		fmt.Printf("    plan: DELETE stat_day IN окно → INSERT из order_feed (окно updated_at)\n")
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

	fmt.Print("  Схема analytical + индекс opsales(srid)...")
	if err := postgres.InitRegionSalesDailySchema(ctx, pool.DB()); err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Println(" ok")

	if *rebuildAll {
		minTS, err := minFeedTS(ctx, pool.DB())
		if err != nil {
			log.Fatalf("  min(order_feed.updated_at): %v", err)
		}
		if minTS == "" {
			log.Fatalf("  public.order_feed пуста — сначала прогоните download-wb-fbs-orders-v2 (feed_mp_only=false).")
		}
		from, _ = time.ParseInLocation("2006-01-02 15:04:05", minTS, loc)
		fmt.Printf("  История ленты с %s\n", from.Format("2006-01-02"))
	} else if from.IsZero() {
		log.Fatalf("  --rebuild-all без истории ленты: order_feed пуста.")
	}

	txStats, err := fillWindow(ctx, pool.DB(), from, to)
	if err != nil {
		log.Fatalf("  Наполнение analytical.region_sales_daily: %v", err)
	}
	fmt.Printf("  analytical.region_sales_daily: окно %s…%s — %s строк, выкупы %s, возвраты %s\n",
		from.Format("2006-01-02"), to.Format("2006-01-02"),
		fmtInt(txStats.Rows), fmtInt(txStats.Buyouts), fmtInt(txStats.Returns))

	if top, err := topRegions(ctx, pool.DB(), from, to); err == nil && len(top) > 0 {
		fmt.Println("\n  ── ТОП-5 РЕГИОНОВ ПО ВЫКУПАМ (окно) ──")
		for _, r := range top {
			fmt.Printf("    %-28s %6s шт   нетто≈%s ₽\n", r.Region, fmtInt(r.Qty), fmtRub(r.Net))
		}
	}

	fmt.Printf("\n  Готово за %s\n", time.Since(start).Round(time.Second))
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// Days — окно обновления в сутках назад (статусы ленты живут в пределах
	// feed_days окна загрузчика; глубже — только осознанный --rebuild-all).
	Days int `yaml:"days"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
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
		Days: 7,
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
	}
}

func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.Days <= 0 {
		c.Days = d.Days
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

// ── Служебное ──

// moscowLoc — зона МСК; фолбэк — фиксированная UTC+3 (конвенция дашбордов
// репо, см. size-availability-report).
func moscowLoc() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}

func dayOr(t time.Time, alt string) string {
	if t.IsZero() {
		return alt
	}
	return t.Format("2006-01-02")
}

// fmtInt — тысячи с пробелом.
func fmtInt(v int64) string {
	if v < 0 {
		return "-" + fmtInt(-v)
	}
	s := fmt.Sprintf("%d", v)
	var b []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ' ')
		}
		b = append(b, c)
	}
	return string(b)
}

// fmtRub — рубли с пробел-тысячами без копеек.
func fmtRub(v float64) string {
	return fmtInt(int64(v))
}
