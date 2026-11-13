// card-early-diagnostics — ранняя диагностика карточек WB без заказов (процесс гендира, 08.11).
//
// Ежедневный мониторинг: карточки, у которых нет заказов N полных дней с момента
// фактического начала продаж (первый день остатка>0). В xlsx попадают ТОЛЬКО строки
// по критериям гендира — то, что продаётся, фильтровать не нужно. Диагностика
// (видимость, наименование, контент-количество, размерный ряд WB vs 1С, продвижение)
// без рекомендаций и статусов: единственная сущность — ежедневный срез
// analytical.card_health_daily (32 колонки), отчёт рендерится из него же.
//
// Основа — collection-readiness (донор стилевой машины xlsx, фото, почты); тут другой
// двигатель: якорь начала продаж, счётчик полных дней, дерево гипотез.
//
// Отладка на Маке: --db wb_data_test (default) — полный цикл записи в тест-БД;
// read-only реализм: --dry-run --db wb_data_prod (без DDL и записи).
//
// Usage:
//
//	go run ./cmd/data-analyzers/card-early-diagnostics/ [options]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/image/webp" // регистрация WebP-декодера для image.Decode (миниатюры WB .webp)

	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Ранняя диагностика карточек WB без заказов (процесс гендира).
В xlsx — только карточки по критериям гендира. Анализ без рекомендаций.

Options:
  --config PATH     Путь к конфигу (default: config.yaml)
  --xlsx PATH       Выходной xlsx (пусто → card-diagnostics-YYYYMMDD.xlsx)
  --db NAME         БД (overrides storage.pg_database; напр. wb_data_prod)
  --years A,B       Годы коллекции, 2- или 4-значные (26 или 2026; overrides config)
  --limit N         Ограничить вотчлист (0 = все; для отладки)
  --dry-run         Read-only: без DDL и записи в БД (реализм без side-эффектов)
  --mock            Демо-xlsx без БД (дымовой тест рендера)
  --no-photos       Не встраивать миниатюры (только колонка-ссылка)
  --mail            Отправить готовый xlsx по почте (секция email в config.yaml)
  -h, --help        Справка

Examples:
  %s --db wb_data_test                       # полный цикл в тест-БД
  %s --dry-run --db wb_data_prod             # read-only контрольный прогон по прод-данным
  %s --mock
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	xlsxPath := flag.String("xlsx", "", "Выходной xlsx (overrides config)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	yearsStr := flag.String("years", "", "Годы коллекции через запятую, 2- или 4-значные (26 или 2026; overrides config)")
	limit := flag.Int("limit", 0, "Ограничить вотчлист (0 = все)")
	dryRun := flag.Bool("dry-run", false, "Read-only: без DDL и записи в БД")
	mock := flag.Bool("mock", false, "Демо-xlsx без БД")
	noPhotos := flag.Bool("no-photos", false, "Не встраивать миниатюры")
	mail := flag.Bool("mail", false, "Отправить xlsx по почте")
	help := flag.Bool("help", false, "Справка")
	flag.BoolVar(help, "h", false, "Справка")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Отсутствие конфига — норма (defaults); БИТЫЙ конфиг — fatal: молчаливая
	// подмена дефолтами прогоняла бы монитор не на тех порогах, которые правит
	// ОЭК в yaml (аудит 09.11, P3).
	var cfg *Config
	if _, statErr := os.Stat(*configPath); statErr != nil {
		log.Printf("  Конфиг не найден (%s) — defaults: %v", *configPath, statErr)
		cfg = defaultConfig()
	} else {
		var err error
		if cfg, err = loadConfig(*configPath); err != nil {
			log.Fatalf("  Конфиг %s битый: %v — чините yaml, тихие дефолты тут опасны", *configPath, err)
		}
	}
	cfg.applyDefaults()
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}
	if *yearsStr != "" {
		cfg.Watch.AllowedYears = parseIntList(*yearsStr)
	}

	today := todayMoscow() // один вызов: имя файла и snapshot_date не разъезжаются через полночь
	if cfg.XLSX == "" {
		cfg.XLSX = fmt.Sprintf("card-diagnostics-%s.xlsx", today)
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

	sep := strings.Repeat("═", 60)
	fmt.Println(sep)
	fmt.Println("  РАННЯЯ ДИАГНОСТИКА КАРТОЧЕК БЕЗ ЗАКАЗОВ")
	fmt.Println(sep)
	fmt.Printf("  База:      %s%s\n", cfg.Storage.DisplayDB(), map[bool]string{true: " (dry-run: только чтение)", false: ""}[*dryRun])
	fmt.Printf("  Критерий:  без заказов ≥%d полных дней (stall ≥%d)\n",
		cfg.Watch.NoOrdersDays, cfg.Watch.StallDays)
	if len(cfg.Watch.AllowedYears) > 0 {
		fmt.Printf("  Коллекция: годы %s (символы 2-3 артикула)\n", joinYears(cfg.Watch.AllowedYears))
	} else {
		fmt.Println("  Коллекция: все годы (фильтр allowed_years пуст)")
	}
	fmt.Printf("  1С склады: %s\n", strings.Join(cfg.CascadeStorages(), " → "))
	fmt.Printf("  Срез:      %s\n", today)
	fmt.Printf("  Ревизия:   %s\n", codeRevision())
	fmt.Println(sep)

	start := time.Now()

	// ── --mock: демо-xlsx без БД ──
	if *mock {
		fmt.Println("\n  Режим --mock: генерация демо-xlsx без обращения к БД.")
		rows := mockRows(cfg, today)
		sortRows(rows)
		if err := exportXLSX(rows, cfg.XLSX, cfg, nil, false, today, codeRevision()); err != nil {
			log.Fatalf("  Ошибка экспорта: %v", err)
		}
		fmt.Printf("  → %s  (%d строк, %s)\n", cfg.XLSX, len(rows), time.Since(start).Round(time.Millisecond))
		if cfg.Email.Enabled || *mail {
			fmt.Println("\n  Почта пропущена: --mock — демо-прогон без side-эффектов (аудит 09.11)")
		}
		return
	}

	// ── Подключение ──
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

	// ── Свежесть источников + fail-fast 1С-склада (до пустого среза) ──
	cov, err := loadCoverage(ctx, pool.DB(), cfg.CascadeStorages())
	if err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Printf("  Источники: onec_rests %s (%d строк складов каскада), stocks %s, cards %d\n",
		cov.RestsDate, cov.StorageRows, cov.StocksDate, cov.Cards)

	// ── Выборка и обогащение ──
	funnelFrom := shiftDate(today, -cfg.Watch.FunnelWindowDays+1)
	fmt.Print("  Выборка популяции...")
	raw, err := loadRaw(ctx, pool.DB(), cfg, funnelFrom)
	if err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Printf(" %d карточек популяции\n", len(raw))

	rows := make([]Row, 0, 64)
	for _, r := range raw {
		if er := EnrichRow(r, cfg, today); er != nil {
			rows = append(rows, *er)
		}
	}
	fmt.Printf("  Вотчлист по критериям гендира: %d\n", len(rows))

	if *limit > 0 && len(rows) > *limit {
		sortRows(rows)
		rows = rows[:*limit]
		fmt.Printf("  --limit %d: усечён\n", *limit)
	}
	if len(rows) == 0 {
		fmt.Println("\n  Вотчлист пуст — карточек без заказов по критериям нет. Отчёт не собирается.")
		return
	}
	sortRows(rows)

	// ── Запись среза (кроме dry-run) ──
	if !*dryRun {
		fmt.Print("  Схема analytical...")
		if err := ensureSchema(ctx, pool.DB()); err != nil {
			log.Fatalf("  %v", err)
		}
		fmt.Println(" ok")
		fmt.Printf("  Запись среза %s...", today)
		saved, err := saveSnapshot(ctx, pool.DB(), rows)
		if err != nil {
			log.Fatalf("  %v", err)
		}
		fmt.Printf(" %d строк (идемпотентно: DELETE+INSERT)\n", saved)
		if n, err := trimRetention(ctx, pool.DB(), cfg.RetentionDays); err != nil {
			log.Printf("  WARN: retention: %v", err)
		} else if n > 0 {
			fmt.Printf("  Retention %d дней: удалено срезов %d\n", cfg.RetentionDays, n)
		}
	}

	// ── Фото (после фильтрации — вотчлист мал) ──
	var photoBytes map[int64][]byte
	embed := cfg.EmbedPhotos && !*noPhotos
	if embed {
		nmIDs := make([]int64, 0, len(rows))
		for i := range rows {
			nmIDs = append(nmIDs, rows[i].NmID)
		}
		urls, err := loadPhotoURLs(ctx, pool.DB(), nmIDs)
		if err != nil {
			log.Printf("  WARN: фото URL не загружены (%v) — продолжаем без фото", err)
			embed = false
		} else {
			for i := range rows {
				if u, ok := urls[rows[i].NmID]; ok {
					rows[i].PhotoTM, rows[i].PhotoBig = u.TM, u.Big
				}
			}
			urlMap := make(map[int64]string, len(urls))
			for nm, u := range urls {
				urlMap[nm] = u.TM
			}
			fmt.Printf("  Миниатюры (%d)...", len(urlMap))
			photoBytes = downloadThumbnails(ctx, urlMap)
			fmt.Printf(" %d ok\n", len(photoBytes))
		}
	}

	// ── Экспорт ──
	fmt.Printf("  Экспорт XLSX: %s...", cfg.XLSX)
	if err := exportXLSX(rows, cfg.XLSX, cfg, photoBytes, embed, today, codeRevision()); err != nil {
		log.Fatalf("  Экспорт: %v", err)
	}
	fmt.Println(" ok")

	printSummary(rows)
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Millisecond), cfg.XLSX)

	// Почта — не в dry-run: сухой прогон обещает read-only без side-эффектов
	// (аудит 09.11, P3) — контрольный прогон по прод-данным не должен рассылать письма.
	if *dryRun {
		if cfg.Email.Enabled || *mail {
			fmt.Println("\n  Почта пропущена: dry-run (read-only, без side-эффектов)")
		}
		return
	}
	escalated := 0
	for _, r := range rows {
		if r.FullDays >= 7 {
			escalated++
		}
	}
	maybeMail(ctx, cfg, *mail, len(rows), escalated)
}

// codeRevision — короткий git-хеш + признак несохранённых правок каталога
// утилиты: файл отчёта с одной датой может быть собран разной логикой
// («мы эволюционируем»), ревизия делает сборку отличимой. При недоступном
// git (не репо / не установлен) — «неизвестно», прогон не ломается.
func codeRevision() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "неизвестно"
	}
	rev := strings.TrimSpace(string(out))
	st, err := exec.Command("git", "status", "--porcelain",
		"cmd/data-analyzers/card-early-diagnostics").Output()
	if err == nil && strings.TrimSpace(string(st)) != "" {
		return rev + "+правки"
	}
	return rev
}

// printSummary — консольная сводка прогона.
func printSummary(rows []Row) {
	var slow, stall, pre, d7 int
	hypo := map[string]int{}
	for _, r := range rows {
		switch r.Signal {
		case SignalSlowStart:
			slow++
		case SignalStall:
			stall++
		case SignalPreSales:
			pre++
		}
		if r.FullDays >= 7 {
			d7++
		}
		hypo[r.HypoCode]++
	}
	top := make([]string, 0, len(hypo))
	for code, n := range hypo {
		top = append(top, fmt.Sprintf("%s=%d", code, n))
	}
	fmt.Printf("\n  Всего: %d | медленный старт: %d | остановка продаж: %d | pre_sales: %d | 7+ дней: %d\n",
		len(rows), slow, stall, pre, d7)
	if len(top) > 0 {
		fmt.Printf("  Гипотезы: %s\n", strings.Join(top, ", "))
	}
}

// maybeMail отправляет готовый xlsx, когда email включён (config ИЛИ --mail).
func maybeMail(ctx context.Context, cfg *Config, mailFlag bool, watchTotal, escalated int) {
	if !(cfg.Email.Enabled || mailFlag) {
		return
	}
	to := cfg.Email.Recipients.To
	if len(to) == 0 {
		log.Fatalf("  Отправка письма: нет получателей (email.recipients.to в config.yaml)")
	}
	fmt.Printf("\n  Отправка письма → %s ...\n", strings.Join(to, ", "))
	if err := sendReport(ctx, cfg.Email, cfg.XLSX, watchTotal, escalated); err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Println("  ok — письмо отправлено.")
}

// shiftDate — дата ±N дней в каноническом формате.
func shiftDate(d string, days int) string {
	t, err := time.ParseInLocation(dateLayout, d, mskTZ)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, days).Format(dateLayout)
}

// parseIntList парсит список целых через запятую ("26,25" → [26,25]);
// некорректные токены пропускаются (копия конвенции collection-readiness).
func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// joinYears — компактное перечисление годов коллекции (нормализация к 20XX).
func joinYears(y []int) string {
	parts := make([]string, len(y))
	for i, v := range y {
		if v > 100 {
			v %= 100
		}
		parts[i] = strconv.Itoa(2000 + v)
	}
	return strings.Join(parts, ", ")
}

// mockRows — детерминированные демо-строки для --mock: проходят полный конвейер
// EnrichRow (сырые → якорь → сигнал → гипотеза), поэтому демо-xlsx не может
// разойтись с боевым рендером.
func mockRows(cfg *Config, today string) []Row {
	nmA, nmB, nmC, nmD := int64(1536932431), int64(441002233), int64(441002234), int64(441002235)
	start := shiftDate(today, -11) // 11 дней назад
	raws := []rawRow{
		// Кейс гендира: «Ботинки», показы есть (98), корзин нет, наименование-дубликат.
		{NmID: nmA, Article: "32616702", Title: "Ботинки", Subject: "Ботинки", Category1C: "Обувь",
			Season1C: "Осень-Зима", NameIM: "Ботинки зимние детские", CardCreated: "2026-09-07T14:26:05Z",
			DescriptionLen: 850, HasVideo: true,
			FirstPortal: &start, Opens7d: 98, Carts7d: 0, PhotoCount: 8, CharCount: 10,
			PortalQty: int64Ptr(61), PortalAvail: "nonLiquid", Sizes1C: intPtr(6), FboStorage: "Внуково",
			Campaigns: 0},
		// Стоп-кейс: разгонялась (12 заказов за жизнь), тишина 4 дня → stall.
		{NmID: nmB, Article: "32588011", Title: "Кроссовки детские PLAYToday для школы", Subject: "Кроссовки",
			Category1C: "Обувь", Season1C: "Школа", NameIM: "Кроссовки", CardCreated: "2026-08-01T10:00:00Z",
			DescriptionLen: 1200, HasVideo: false,
			FirstStock: &start, LastOrder: strPtr(shiftDate(today, -5)), OrdersTotal: 12, Opens7d: 12, Carts7d: 2,
			PhotoCount: 9, CharCount: 15, StockQty: int64Ptr(140), StockSizes: intPtr(4),
			WhrQty: nil, Sizes1C: intPtr(6), FboStorage: "РВБ ФБО", Campaigns: 1},
		// Скрытая из каталога (0 заказов → slow_start); описания нет.
		{NmID: nmC, Article: "32599045", Title: "Сапоги демисезонные для девочки", Subject: "Сапоги",
			Category1C: "Обувь", Season1C: "Осень", NameIM: "Сапожки", CardCreated: "2026-09-20T08:00:00Z",
			DescriptionLen: 0, HasVideo: false,
			FirstPortal: &start, Opens7d: 0, Carts7d: 0, PhotoCount: 6, CharCount: 12,
			PortalQty: int64Ptr(3), PortalAvail: "invalidData", Sizes1C: intPtr(5), FboStorage: "Внуково",
			IsShadowed: boolPtr(true), Campaigns: 0},
		// Мало фото + неполный ряд; продалось чуть-чуть (2 заказа) → slow_start.
		{NmID: nmD, Article: "32601122", Title: "Сандалии детские лёгкие", Subject: "Сандалии",
			Category1C: "Обувь", Season1C: "Лето", NameIM: "Сандалии", CardCreated: "2026-09-25T08:00:00Z",
			DescriptionLen: 300, HasVideo: true,
			FirstPortal: &start, OrdersTotal: 2, LastOrder: strPtr(shiftDate(today, -6)), Opens7d: 40, Carts7d: 3,
			PhotoCount: 2, CharCount: 9,
			StockQty: int64Ptr(20), StockSizes: intPtr(2), Sizes1C: intPtr(6), FboStorage: "Внуково",
			PortalAvail: "balanced", Campaigns: 0},
	}
	rows := make([]Row, 0, len(raws))
	for _, r := range raws {
		if er := EnrichRow(r, cfg, today); er != nil {
			rows = append(rows, *er)
		}
	}
	return rows
}

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }
func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
