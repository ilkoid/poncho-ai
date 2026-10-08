// size-availability-report — доступность размеров на WB vs остаток 1С → HTML.
//
// Отвечает на вопрос «сколько товара недоступно на площадке WB, но лежит на
// складе 1С»: недоступно = представлено менее N размеров (размер считается
// представленным, когда его остаток на складах WB больше M шт).
//
// Двухфазная работа (в отличие от чисто read-only сиблингов):
//  1. собирает снапшот в ANALYTICAL-слой PG: analytical.size_availability_daily
//     (артикул-грейн) + analytical.size_availability_sizes_daily (размер-грейн),
//     upsert по (snapshot_date, …) — перезапуск дня идемпотентен;
//  2. читает снапшоты обратно и рендерит самодостаточный офлайн-HTML
//     (reports/size-availability-<дата>.html) в стиле fbs-supplies-report.
//
// Usage:
//
//	go run ./cmd/data-analyzers/size-availability-report/ [options]
//
//	--db wb_data_test --html auto
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

Доступность размеров на WB vs остаток 1С: артикул → размеры (drill-down).
Недоступно = представлено менее min_sizes_presented размеров; размер
представлен, когда его остаток на складах WB больше min_units_per_size шт.
Пишет analytical.size_availability_daily(+_sizes_daily), затем собирает HTML.

Options:
  --config PATH   Путь к конфигу (default: config.yaml рядом с утилитой)
  --db NAME       БД (overrides storage.pg_database)
  --html PATH     Выходной HTML (default: reports/size-availability-<date>.html;
                  пусто = не собирать)
  --skip-fill     Не наполнять таблицы, только HTML из готового снапшота
  --dry-run       Показать параметры без обращения к БД
  -h, --help      Справка

Требует сырьё download-all: public.stocks_daily_warehouses, public.cards,
public.card_sizes, public.onec_goods, public.onec_rests (download-1c-rests-v2).
`, os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	htmlPath := flag.String("html", "", "Выходной HTML (пусто = не собирать)")
	skipFill := flag.Bool("skip-fill", false, "Только HTML из готового снапшота")
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
	fmt.Println("  ДОСТУПНОСТЬ РАЗМЕРОВ · WB ↔ 1С")
	fmt.Println(sep)
	fmt.Printf("  База:        %s\n", cfg.Storage.DisplayDB())
	fmt.Printf("  Определение: представлен = остаток WB > %d шт; недоступно = представлено < %d размеров\n",
		cfg.MinUnitsPerSize, cfg.MinSizesPresented)
	fmt.Printf("  Склад 1С:    %s (мера free; reserv — колонкой)\n", cfg.OnecStorage)
	fmt.Println(sep)

	if *dryRun {
		fmt.Println("\n  --dry-run: параметры (без обращения к БД):")
		fmt.Printf("    backend: %s\n    база:    %s\n    пороги:  >%d шт / <%d размеров\n    склад:   %s\n",
			cfg.Storage.Backend, cfg.Storage.DisplayDB(), cfg.MinUnitsPerSize, cfg.MinSizesPresented, cfg.OnecStorage)
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

	fmt.Print("  Схема analytical (создание при первом прогоне)...")
	if err := postgres.InitSizeAvailabilitySchema(ctx, pool.DB()); err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Println(" ok")

	cov, err := loadCoverage(ctx, pool.DB())
	if err != nil {
		log.Fatalf("  %v\n  Данных ждёт от download-all (stocks/cards/onec_*).", err)
	}
	if cov.StocksDate == "" {
		log.Fatalf("  public.stocks_daily_warehouses пуста — сначала прогоните download-wb-stocks-v2.")
	}
	fmt.Printf("  Свежесть:    стоки %s · остатки 1С %s · справочник 1С %s · размеров карточек %s\n",
		cov.StocksDate, cov.RestsDate, cov.OnecGoodsDate, fmtInt(cov.CardSizes))

	if !*skipFill {
		n, err := fillDaily(ctx, pool.DB(), cov.StocksDate, cfg)
		if err != nil {
			log.Fatalf("  Наполнение size_availability_daily: %v", err)
		}
		fmt.Printf("  analytical.size_availability_daily: %s артикулов (снапшот %s)\n", fmtInt(n), cov.StocksDate)

		m, err := fillSizes(ctx, pool.DB(), cov.StocksDate, cfg.MinUnitsPerSize)
		if err != nil {
			log.Fatalf("  Наполнение size_availability_sizes_daily: %v", err)
		}
		fmt.Printf("  analytical.size_availability_sizes_daily: %s размеров\n", fmtInt(m))

		deleted := trimRetention(ctx, pool.DB(), cfg.RetentionDays)
		if deleted > 0 {
			fmt.Printf("  Retention: удалено снапшотов старше %d сут\n", deleted)
		}
	} else {
		fmt.Println("  --skip-fill: таблицы не трогаю, читаю готовый снапшот")
	}

	fmt.Print("  Чтение снапшота (payload)...")
	cube, err := loadCube(ctx, pool.DB(), cov.StocksDate, cfg, cov)
	if err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Printf(" ok (%d артикулов, %d размеров)\n", len(cube.Arts.Article), len(cube.Sizes.Qty))

	if cfg.HTML == "" {
		cfg.HTML = filepath.Join("reports", fmt.Sprintf("size-availability-%s.html", cov.StocksDate))
	}
	fmt.Print("  Сборка HTML-дашборда...")
	size, err := exportHTML(cube, cfg.HTML)
	if err != nil {
		log.Fatalf("  HTML: %v", err)
	}
	fmt.Printf(" ok (%.1f МБ) → %s\n", float64(size)/1024/1024, cfg.HTML)

	unavail, nocard, zero, freeStuck := summarize(cube)
	fmt.Println("\n  ── СВОДКА ──")
	fmt.Printf("    Артикулов со свободным остатком 1С («%s»): %s\n", cfg.OnecStorage, fmtInt(int64(len(cube.Arts.Article))))
	fmt.Printf("    Недоступны (<%d размеров >%d шт): %s, на 1С по ним свободно %s шт\n",
		cfg.MinSizesPresented, cfg.MinUnitsPerSize, fmtInt(unavail), fmtInt(freeStuck))
	fmt.Printf("    Из них: без карточки WB %s · с карточкой, но 0 представленных размеров %s\n",
		fmtInt(nocard), fmtInt(zero))
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Second), cfg.HTML)
}

// ── Конфигурация ──

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	// MinUnitsPerSize — размер «представлен», если его остаток WB строго больше N шт.
	MinUnitsPerSize int `yaml:"min_units_per_size"`
	// MinSizesPresented — карточка «недоступна», если представлено менее N размеров.
	MinSizesPresented int `yaml:"min_sizes_presented"`
	// OnecStorage — имя склада 1С для меры остатка (onec_rests.storage_name).
	OnecStorage string `yaml:"onec_storage"`
	// RetentionDays — сколько суток истории снапшотов хранить в analytical.*.
	RetentionDays int `yaml:"retention_days"`
	// Storage — параметры подключения к БД.
	Storage config.V2StorageConfig `yaml:"storage"`
	// HTML — путь к выходному дашборду; "auto" = reports/size-availability-<дата>.html.
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
		MinUnitsPerSize:   2,
		MinSizesPresented: 4,
		OnecStorage:       "Внуково",
		RetentionDays:     90,
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
		HTML: "auto",
	}
}

func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.MinUnitsPerSize <= 0 {
		c.MinUnitsPerSize = d.MinUnitsPerSize
	}
	if c.MinSizesPresented <= 0 {
		c.MinSizesPresented = d.MinSizesPresented
	}
	if c.OnecStorage == "" {
		c.OnecStorage = d.OnecStorage
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = d.RetentionDays
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

// nowMoscow — текущее время в МСК (шапка дашборда). Фолбэк — фиксированная
// зона UTC+3, а не локальное время машины (конвенция fbs-supplies-report).
func nowMoscow() time.Time {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.Now().In(time.FixedZone("MSK", 3*3600))
	}
	return time.Now().In(loc)
}

// fmtInt — тысячи с пробелом (консольная сводка).
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
