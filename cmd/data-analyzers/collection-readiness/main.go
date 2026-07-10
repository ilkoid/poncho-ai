// collection-readiness — отчёт готовности карточек по коллекции (xlsx из PostgreSQL).
//
// По списку коллекций 1С (config.yaml или --collections) собирает из wb_data_prod
// сводку готовности карточек: 1С → nmID на WB → остатки/склады → заказы/выкупы →
// карточный рейтинг WB 0-10. Аномалии воронки (нет nmID; заблокирован в 1С, но жив
// на WB) подсвечиваются. Только SELECT — запись в БД не производится.
//
// Usage:
//
//	go run ./cmd/data-analyzers/collection-readiness/ [options]
//
//	--config config.yaml --collections "CLASSIC 2026 girls Tween"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/image/webp" // регистрация WebP-декодера для image.Decode (миниатюры WB .webp)

	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Отчёт готовности карточек по коллекции из PostgreSQL (wb_data_prod).
Только SELECT (read-only). Вывод — xlsx.

Options:
  --config PATH        Путь к конфигу (default: config.yaml)
  --collections A,B    Список коллекций 1С (overrides config)
  --seasons A,B        Список сезонов 1С (overrides config; сезон надёжнее коллекции для школы)
  --xlsx PATH          Выходной xlsx (пусто → report-<slug>-YYYYMMDD.xlsx)
  --limit N            Ограничить число строк (0 = все)
  --exclude-lengths A,B  Исключить артикулы заданных длин (overrides config; напр. 6,7)
  --db NAME            БД (overrides storage.pg_database, напр. wb_data_test)
  --dry-run            Показать параметры запроса без обращения к БД
  --mock               Сгенерировать демо-xlsx без БД
  --no-photos          Не встраивать миниатюры (только колонка-ссылка на фото)
  --mail               Отправить готовый xlsx по почте (секция email в config.yaml)
  -h, --help           Справка

Examples:
  %s --seasons "Школа" --xlsx /tmp/school.xlsx
  %s -c config.yaml --collections "CLASSIC 2026 girls Tween"
  %s --mock
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигу")
	flag.StringVar(configPath, "c", "config.yaml", "Путь к конфигу (short)")
	collectionsStr := flag.String("collections", "", "Список коллекций через запятую (overrides config)")
	seasonsStr := flag.String("seasons", "", "Список сезонов через запятую (overrides config)")
	xlsxPath := flag.String("xlsx", "", "Выходной xlsx (overrides config)")
	limit := flag.Int("limit", 0, "Ограничить число строк (0 = все)")
	excludeLengthsStr := flag.String("exclude-lengths", "", "Исключить артикулы заданных длин через запятую (overrides config; напр. 6,7)")
	dbName := flag.String("db", "", "БД (overrides storage.pg_database)")
	dryRun := flag.Bool("dry-run", false, "Показать параметры запроса без обращения к БД")
	mock := flag.Bool("mock", false, "Сгенерировать демо-xlsx без БД")
	noPhotos := flag.Bool("no-photos", false, "Не встраивать миниатюры (только колонка-ссылка на фото)")
	mail := flag.Bool("mail", false, "Отправить готовый xlsx по почте (секция email в config.yaml)")
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
		log.Printf("Конфиг не найден (%s), используем defaults: %v", *configPath, err)
		cfg = defaultConfig()
	}
	cfg.applyDefaults()

	// CLI overrides.
	if *collectionsStr != "" {
		cfg.Collections = splitAndTrim(*collectionsStr)
	}
	if *seasonsStr != "" {
		cfg.Seasons = splitAndTrim(*seasonsStr)
	}
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
	}
	if *limit != 0 {
		cfg.Limit = *limit
	}
	if *excludeLengthsStr != "" {
		cfg.ExcludeLengths = parseIntList(*excludeLengthsStr)
	}
	if *dbName != "" {
		cfg.Storage.PgDatabase = *dbName
	}

	if len(cfg.Collections) == 0 && len(cfg.Seasons) == 0 {
		fmt.Println("  Не заданы ни коллекции, ни сезоны. Укажите collections/seasons в config.yaml или --collections/--seasons.")
		os.Exit(1)
	}

	// Имя выходного файла по умолчанию (по первому из заданных фильтров).
	if cfg.XLSX == "" {
		first := ""
		if len(cfg.Collections) > 0 {
			first = cfg.Collections[0]
		} else {
			first = cfg.Seasons[0]
		}
		cfg.XLSX = fmt.Sprintf("report-%s-%s.xlsx", slugify(first), time.Now().Format("20060102"))
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
	fmt.Println("  ОТЧЁТ ГОТОВНОСТИ КАРТОЧЕК ПО КОЛЛЕКЦИИ")
	fmt.Println(sep)
	if len(cfg.Collections) > 0 {
		fmt.Printf("  Коллекции: %s\n", strings.Join(cfg.Collections, ", "))
	}
	if len(cfg.Seasons) > 0 {
		fmt.Printf("  Сезоны:    %s\n", strings.Join(cfg.Seasons, ", "))
	}
	fmt.Printf("  База:      %s\n", cfg.Storage.DisplayDB())
	if cfg.Limit > 0 {
		fmt.Printf("  Лимит:     %d строк\n", cfg.Limit)
	}
	fmt.Println(sep)

	start := time.Now()

	// ── --mock: демо-xlsx без БД ──
	if *mock {
		fmt.Println("\n  Режим --mock: генерация демо-xlsx без обращения к БД.")
		rows := mockRows()
		if err := exportXLSX(rows, cfg.XLSX, cfg.Collections, cfg.Seasons, nil, false); err != nil {
			log.Fatalf("  Ошибка экспорта: %v", err)
		}
		fmt.Printf("  → %s  (%d строк, %s)\n", cfg.XLSX, len(rows), time.Since(start).Round(time.Millisecond))
		maybeMail(ctx, cfg, *mail)
		return
	}

	// ── --dry-run: параметры без БД ──
	if *dryRun {
		fmt.Println("\n  --dry-run: параметры запроса (без обращения к БД):")
		fmt.Printf("    коллекций: %d\n", len(cfg.Collections))
		fmt.Printf("    backend:   %s\n", cfg.Storage.Backend)
		fmt.Printf("    база:      %s\n", cfg.Storage.DisplayDB())
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

	// ── Запрос ──
	fmt.Print("  Загрузка данных...")
	rows, err := loadRows(ctx, pool.DB(), cfg.Collections, cfg.Seasons, cfg.Limit)
	if err != nil {
		log.Fatalf("  Запрос: %v", err)
	}
	fmt.Printf(" %d строк\n", len(rows))

	if len(rows) == 0 {
		fmt.Println("\n  Нет данных по заданным коллекциям. Проверьте названия (точное совпадение onec_goods.collection).")
		return
	}

	// ── Фильтр по длине артикула (exclude_lengths): убрать легаси-нумерацию ──
	if len(cfg.ExcludeLengths) > 0 {
		kept := make([]Row, 0, len(rows))
		for _, r := range rows {
			if !slices.Contains(cfg.ExcludeLengths, len(r.Article)) {
				kept = append(kept, r)
			}
		}
		fmt.Printf("  Фильтр длин %v: исключено %d, осталось %d\n",
			cfg.ExcludeLengths, len(rows)-len(kept), len(kept))
		rows = kept
	}

	// ── Фото (card_photos): URL + опциональное скачивание миниатюр ──
	var photoBytes map[int64][]byte
	embed := cfg.EmbedPhotos && !*noPhotos
	if nmIDs := collectNmIDs(rows); len(nmIDs) > 0 {
		fmt.Print("  URL фото...")
		photoURLs, err := loadPhotoURLs(ctx, pool.DB(), nmIDs)
		if err != nil {
			log.Printf("\n  WARN: фото URL не загружены (%v) — продолжаем без фото", err)
		} else {
			fmt.Printf(" %d\n", len(photoURLs))
			for i := range rows {
				if rows[i].NmID == nil {
					continue
				}
				if pu, ok := photoURLs[*rows[i].NmID]; ok {
					rows[i].PhotoTM, rows[i].PhotoBig = pu.TM, pu.Big
				}
			}
			if embed {
				urlMap := make(map[int64]string, len(photoURLs))
				for nmID, pu := range photoURLs {
					urlMap[nmID] = pu.TM
				}
				fmt.Printf("  Миниатюры (%d)...", len(urlMap))
				photoBytes = downloadThumbnails(ctx, urlMap)
				fmt.Printf(" %d ok\n", len(photoBytes))
			}
		}
	}

	// ── Экспорт ──
	fmt.Printf("  Экспорт XLSX: %s...", cfg.XLSX)
	if err := exportXLSX(rows, cfg.XLSX, cfg.Collections, cfg.Seasons, photoBytes, embed); err != nil {
		log.Fatalf("  Экспорт: %v", err)
	}
	fmt.Println(" ok")

	// Краткая сводка в консоль.
	var noCard, blocked int
	for _, r := range rows {
		if !r.HasWBCard() {
			noCard++
		}
		if r.ArticleBlocked || r.ModelCancelled {
			blocked++
		}
	}
	fmt.Printf("\n  Всего: %d | без nmID (нет на WB): %d | заблокировано в 1С: %d\n",
		len(rows), noCard, blocked)
	fmt.Printf("  Готово за %s → %s\n", time.Since(start).Round(time.Millisecond), cfg.XLSX)

	// Отправка по почте (если включено в config.yaml или флагом --mail).
	maybeMail(ctx, cfg, *mail)
}

// maybeMail отправляет готовый xlsx по почте, когда email включён (секция email в
// config.yaml ИЛИ флаг --mail). Файл уже сохранён на диск, поэтому ошибка отправки
// не теряет отчёт — лишь завершает процесс с ненулевым кодом (заметно для .sh с set -e).
func maybeMail(ctx context.Context, cfg *Config, mailFlag bool) {
	if !(cfg.Email.Enabled || mailFlag) {
		return
	}
	to := cfg.Email.Recipients.To
	if len(to) == 0 {
		log.Fatalf("  Отправка письма: нет получателей (заполните email.recipients.to в config.yaml)")
	}
	fmt.Printf("\n  Отправка письма → %s ...\n", strings.Join(to, ", "))
	if err := sendReport(ctx, cfg.Email, cfg.XLSX, cfg.Collections, cfg.Seasons); err != nil {
		log.Fatalf("  %v", err)
	}
	fmt.Println("  ok — письмо отправлено.")
}

// splitAndTrim делит строку по запятым и обрезает пробелы.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseIntList парсит список целых через запятую ("6,7" → [6,7]); некорректные токены пропускаются.
func parseIntList(s string) []int {
	parts := splitAndTrim(s)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// slugify превращает имя коллекции в безопасный кусок имени файла.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	b := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, r)
		case r >= 'а' && r <= 'я', r == 'ё':
			b = append(b, r) // кириллицу оставляем — она валидна в путях
		default:
			if len(b) > 0 && b[len(b)-1] != '-' {
				b = append(b, '-')
			}
		}
	}
	return strings.Trim(string(b), "-")
}

// collectNmIDs возвращает список ненулевых nmID из строк (для батч-запроса фото).
func collectNmIDs(rows []Row) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.NmID != nil {
			out = append(out, *r.NmID)
		}
	}
	return out
}

// mockRows возвращает детерминированные демо-строки для --mock.
func mockRows() []Row {
	nmA := int64(478081133)
	nmB := int64(153317462)
	return []Row{
		{
			Article: "22527124", ArticleNum: "22527124", Sex: "Женский", Collection: "CLASSIC 2026 girls Tween",
			AgeSegment: "Tween", NameIM: "Платье трикотажное для девочек", Category: "Платья", ProductionYear: 2025,
			Color: "тёмно-синий", SizeRange: "128;134;140;146;152;158;164", ModelStatus: "Утверждена к отгрузке",
			NmID: &nmA, WBName: "Платье школьное", HasDescription: true,
			Description: "Платье школьное для девочек. Состав: хлопок 95%, эластан 5%. Подходит для повседневной носки. Длина по спинке — 70 см.",
			PhotoTM: "https://basket-01.wbbasket.ru/vol478/part478081/images/tm/1.webp",
			PhotoBig: "https://basket-01.wbbasket.ru/vol478/part478081/images/big/1.webp",
			ProductRating: 10, FeedbackRating: 5, WHWithStock: 6, OrdersCount: 6, BuyoutCount: 1,
			WBStock: 221, OneCReserv: 78, OneCFree: 0,
		},
		{
			Article: "22527760", ArticleNum: "22527760", Sex: "Женский", Collection: "CLASSIC 2026 girls Tween",
			AgeSegment: "Tween", NameIM: "Сапожки для разогрева", Category: "Обувь", ProductionYear: 2025,
			Color: "черный", SizeRange: "29-30;31-32;33-34;35-36;37-38;39-40", ModelStatus: "В производстве",
			NmID: nil, WBName: "", HasDescription: false,
			ProductRating: 0, FeedbackRating: 0, WHWithStock: 0, OrdersCount: 0, BuyoutCount: 0,
			WBStock: 0, OneCReserv: 0, OneCFree: 0,
		},
		{
			Article: "32210216", ArticleNum: "32210216", Sex: "Мужской", Collection: "CLASSIC 2026 boys Tween",
			AgeSegment: "Tween", NameIM: "Комплект: Футболка, шорты", Category: "Комплекты одежды", ProductionYear: 2022,
			Color: "белый,черный", SizeRange: "128;134;140;146;152;158;164;170;176", ModelStatus: "Утверждена к отгрузке",
			ArticleBlocked: true, NmID: &nmB, WBName: "Костюм летний подростковый", HasDescription: true,
			PhotoTM: "https://basket-01.wbbasket.ru/vol153/part153317/images/tm/1.webp",
			PhotoBig: "https://basket-01.wbbasket.ru/vol153/part153317/images/big/1.webp",
			ProductRating: 8, FeedbackRating: 4.5, WHWithStock: 12, OrdersCount: 29, BuyoutCount: 10,
			WBStock: 55, OneCReserv: 6, OneCFree: 56,
		},
	}
}
