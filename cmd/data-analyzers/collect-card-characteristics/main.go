// collect-card-characteristics — сбор исторических характеристик WB по предметам.
//
// Читает все характеристики из card_characteristics, группирует по subject_name,
// собирает уникальные значения с дедупликацией, выгружает в XLSX.
// Фильтрация через pkg/filter (годы, subject_id, vendor_codes, сезоны и т.д.).
//
// Usage:
//
//	go run ./cmd/data-analyzers/collect-card-characteristics/ [options]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/filter"
)

// Version — версия утилиты.
const Version = "v1.6"

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	DBPath       string        `yaml:"db_path"`
	Filters      filter.Filter `yaml:"filters"`
	Output       string        `yaml:"output"`
	ExportAll    bool          `yaml:"export_all"`
	ItemsPerFile int           `yaml:"items_per_file"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		DBPath: "/var/db/wb-sales.db",
		Filters: filter.Filter{
			AllowedYears: []int{24, 25, 26},
		},
		Output:       "./characteristics_report.xlsx",
		ItemsPerFile: 30,
	}
}

func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.DBPath == "" {
		c.DBPath = d.DBPath
	}
	if c.Output == "" {
		c.Output = d.Output
	}
	if len(c.Filters.AllowedYears) == 0 {
		c.Filters.AllowedYears = d.Filters.AllowedYears
	}
	if c.ItemsPerFile <= 0 {
		c.ItemsPerFile = d.ItemsPerFile
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Сбор исторических характеристик WB по предметам (subject_name).
Читает из wb-sales.db (read-only). Без API-вызовов.
Фильтрация через config.yaml (pkg/filter) и CLI флаги.

Options:
  --config PATH        Путь к конфигурационному файлу (default: config.yaml)
  --db PATH            Путь к базе данных (overrides config)
  --output PATH        Путь к XLSX-файлу (overrides config)
  --years 24,25,26     Года из vendor_code, через запятую (overrides config)
  --subject NAME       Предмет WB, точное совпадение (overrides config)
  --subject-ids 540    ID предметов WB, через запятую (overrides config)
  --vendor-codes A,B   Артикулы продавца, через запятую (overrides config)
  --nm-ids 123,456     nm_id, через запятую (overrides config)
  --seasons зима,лето  Сезоны, через запятую (overrides config)
  --export-all         Пакетный экспорт: разбить на несколько XLSX файлов
  --items-per-file N   Предметов на файл (default: 30, overrides config)
  --list-subjects Q    Вывести список предметов WB и выйти.
                       "all" = все, "кув" = поиск по подстроке
  --mock               Тестовые данные (без реальной БД)
  --dry-run            Вывод в консоль, без XLSX
  --version            Вывести версию и выйти
  -h, --help           Показать справку

Фильтры (через config.yaml → filters):
  vendor_codes         — конкретные артикулы
  allowed_years        — год из позиций 2-3 артикула (напр. [24, 25, 26])
  exclude_lengths      — исключить артикулы указанной длины
  exclude_vendor_codes — исключить конкретные артикулы
  vendor_code_prefix   — первая цифра артикула (напр. "1")
  subject              — предмет WB (точное совпадение)
  subject_ids          — ID предметов WB
  seasons              — сезон из характеристик (напр. ["зима"])
  in_stock             — только с остатками
  onec_type            — тип 1C (Обувь/Одежда/Аксессуары)
  category_level1/2    — 1C категории
  active_only          — исключить заблокированные в 1C

Examples:
  %s                                                       # Все предметы, годы 24-26
  %s --list-subjects кросс                                # Найти ID для кроссовок
  %s --subject-ids 540 --dry-run                          # Консольный вывод для предмета 540
  %s --years 25,26 --output /tmp/chars.xlsx               # Только 25-26 годы
  %s --mock --dry-run                                     # Тестовый прогон
  %s --export-all --output /tmp/chars.xlsx                # Пакетный экспорт (30/файл)
  %s --export-all --items-per-file 20 --mock --dry-run    # План без файлов
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигурационному файлу")
	dbPath := flag.String("db", "", "Путь к базе данных (overrides config)")
	outputPath := flag.String("output", "", "Путь к XLSX-файлу (overrides config)")
	yearsStr := flag.String("years", "", "Года через запятую, напр. 24,25,26 (overrides config)")
	subjectName := flag.String("subject", "", "Предмет WB (overrides config)")
	subjectIDsStr := flag.String("subject-ids", "", "ID предметов WB через запятую (overrides config)")
	vendorCodesStr := flag.String("vendor-codes", "", "Артикулы через запятую (overrides config)")
	nmIDsStr := flag.String("nm-ids", "", "nm_id через запятую (overrides config)")
	seasonsStr := flag.String("seasons", "", "Сезоны через запятую (overrides config)")
	listSubjects := flag.String("list-subjects", "", "Вывести список предметов WB и выйти (all / подстрока)")
	exportAll := flag.Bool("export-all", false, "Пакетный экспорт: разбить на несколько XLSX файлов")
	itemsPerFile := flag.Int("items-per-file", 0, "Предметов на файл (default: 30)")
	mock := flag.Bool("mock", false, "Тестовые данные")
	dryRun := flag.Bool("dry-run", false, "Вывод в консоль, без XLSX")
	showVersion := flag.Bool("version", false, "Вывести версию и выйти")
	help := flag.Bool("help", false, "Показать справку")
	flag.BoolVar(help, "h", false, "Показать справку")
	flag.Parse()

	if *showVersion {
		fmt.Printf("collect-card-characteristics %s\n", Version)
		os.Exit(0)
	}

	if *help {
		printHelp()
		os.Exit(0)
	}

	// --- Load config ---
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Конфиг не найден (%s), используем defaults: %v", *configPath, err)
		cfg = defaultConfig()
	}
	cfg.applyDefaults()

	// --- CLI overrides ---
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *outputPath != "" {
		cfg.Output = *outputPath
	}
	if *yearsStr != "" {
		cfg.Filters.AllowedYears = parseIntList(*yearsStr)
	}
	if *subjectName != "" {
		cfg.Filters.SubjectName = *subjectName
	}
	if *subjectIDsStr != "" {
		cfg.Filters.SubjectIDs = parseIntList(*subjectIDsStr)
	}
	if *vendorCodesStr != "" {
		cfg.Filters.VendorCodes = strings.Split(*vendorCodesStr, ",")
	}
	if *nmIDsStr != "" {
		cfg.Filters.NmIDs = parseIntList(*nmIDsStr)
	}
	if *seasonsStr != "" {
		cfg.Filters.Seasons = strings.Split(*seasonsStr, ",")
	}
	if *exportAll {
		cfg.ExportAll = true
	}
	if *itemsPerFile > 0 {
		cfg.ItemsPerFile = *itemsPerFile
	}

	// --- Graceful shutdown ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n  Прервано!")
		cancel()
	}()

	// --- Header ---
	sep := strings.Repeat("═", 72)
	fmt.Println(sep)
	fmt.Println("     СБОР ХАРАКТЕРИСТИК КАРТОЧЕК WB")
	fmt.Println(sep)
	fmt.Printf("Источник: %s\n", cfg.DBPath)

	if !cfg.Filters.Empty() {
		yearStr := "все"
		if len(cfg.Filters.AllowedYears) > 0 {
			parts := make([]string, len(cfg.Filters.AllowedYears))
			for i, y := range cfg.Filters.AllowedYears {
				parts[i] = strconv.Itoa(y)
			}
			yearStr = strings.Join(parts, ",")
		}
		fmt.Printf("Фильтр:   годы=%s", yearStr)
		if len(cfg.Filters.SubjectIDs) > 0 {
			fmt.Printf("  subject_ids=%v", cfg.Filters.SubjectIDs)
		}
		if cfg.Filters.SubjectName != "" {
			fmt.Printf("  subject=%s", cfg.Filters.SubjectName)
		}
		if len(cfg.Filters.Seasons) > 0 {
			fmt.Printf("  seasons=%v", cfg.Filters.Seasons)
		}
		fmt.Println()
	}
	if cfg.ExportAll {
		fmt.Printf("Режим:    --export-all (%d предметов/файл)\n", cfg.ItemsPerFile)
	}
	fmt.Println(sep)
	fmt.Println()

	start := time.Now()

	// --- --list-subjects mode ---
	if *listSubjects != "" {
		runListSubjects(ctx, cfg.DBPath, *listSubjects)
		return
	}

	// --- Load data ---
	var data []SubjectData

	if *mock {
		data = mockData()
		// For --export-all demo: generate enough subjects to show batching
		if cfg.ExportAll && len(data) < cfg.ItemsPerFile*2 {
			data = generateMockSubjects(cfg.ItemsPerFile*2 + 3)
		}
		fmt.Printf("  Режим --mock: %d тестовых предметов\n\n", len(data))
	} else {
		repo, err := NewSourceRepo(cfg.DBPath)
		if err != nil {
			log.Fatalf("Ошибка открытия БД: %v", err)
		}
		defer repo.Close()

		fmt.Print("  Загрузка характеристик...")
		data, err = repo.LoadCharacteristics(ctx, &cfg.Filters)
		if err != nil {
			log.Fatalf("Ошибка загрузки: %v", err)
		}
		fmt.Printf(" %d предметов\n\n", len(data))
	}

	if len(data) == 0 {
		fmt.Println("  Нет данных (попробуй изменить фильтры).")
		return
	}

	// --- Console output ---
	printSummary(data, sep)

	// --- Dry-run: console only ---
	if *dryRun {
		printSubjectsDetail(data)
		if cfg.ExportAll {
			printBatchPlan(data, resolveOutputPath(cfg), cfg.ItemsPerFile)
		}
		fmt.Printf("\n  Готово за %s (--dry-run, без XLSX)\n", time.Since(start).Round(time.Second))
		return
	}

	// --- XLSX export ---
	if cfg.ExportAll {
		fmt.Printf("\n  Пакетный экспорт XLSX (%d предметов/файл)...\n", cfg.ItemsPerFile)
		output := resolveOutputPath(cfg)
			files, err := ExportXLSXBatch(data, output, cfg.ItemsPerFile)
		if err != nil {
			log.Fatalf("  Ошибка: %v", err)
		}
		fmt.Printf("  Создано файлов: %d\n", len(files))
		for _, f := range files {
			fmt.Printf("    - %s\n", f)
		}
		fmt.Printf("\n  Готово за %s (%d предметов, %d файлов)\n",
			time.Since(start).Round(time.Second), len(data), len(files))
	} else {
		fmt.Printf("\n  Экспорт XLSX: %s...", cfg.Output)
		if err := ExportXLSX(data, cfg.Output); err != nil {
			log.Fatalf(" Ошибка: %v", err)
		}
		fmt.Println(" ok")
		fmt.Printf("\n  Готово за %s (%d предметов, %d листов)\n",
			time.Since(start).Round(time.Second), len(data), len(data)+1)
	}
}

// --- --list-subjects mode ---

func runListSubjects(ctx context.Context, dbPath, query string) {
	repo, err := NewSourceRepo(dbPath)
	if err != nil {
		log.Fatalf("Ошибка открытия БД: %v", err)
	}
	defer repo.Close()

	all, err := repo.LoadAllSubjects(ctx)
	if err != nil {
		log.Fatalf("Ошибка загрузки предметов: %v", err)
	}

	var subjects []SubjectEntry
	if query == "all" {
		subjects = all
	} else {
		q := strings.ToLower(query)
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.SubjectName), q) {
				subjects = append(subjects, s)
			}
		}
	}

	if len(subjects) == 0 {
		fmt.Printf("  Нет предметов по запросу %q\n", query)
		return
	}

	fmt.Printf("  %-8s  %s\n", "ID", "Subject Name")
	fmt.Printf("  %-8s  %s\n", "--------", "------------")
	for _, s := range subjects {
		fmt.Printf("  %-8d  %s\n", s.SubjectID, s.SubjectName)
	}
	fmt.Printf("\n  Total: %d subjects\n", len(subjects))
}

// --- Console output ---

func printSummary(data []SubjectData, sep string) {
	dash := strings.Repeat("─", 72)
	fmt.Println(sep)
	fmt.Println("     СВОДКА")
	fmt.Println(sep)
	fmt.Printf("  %-6s  %-30s  %8s  %10s  %10s\n",
		"ID", "Предмет", "Карточек", "Характер.", "Значений")
	fmt.Println(dash)

	var totalCards, totalChars, totalVals int
	for _, sd := range data {
		tv := totalValues(sd)
		totalCards += sd.CardCount
		totalChars += len(sd.Characteristics)
		totalVals += tv
		fmt.Printf("  %-6d  %-30s  %8d  %10d  %10d\n",
			sd.SubjectID, truncateRune(sd.SubjectName, 30), sd.CardCount,
			len(sd.Characteristics), tv)
	}
	fmt.Println(dash)
	fmt.Printf("  %-6s  %-30s  %8d  %10d  %10d\n",
		"", "ИТОГО:", totalCards, totalChars, totalVals)
	fmt.Println(sep)
}

func printSubjectsDetail(data []SubjectData) {
	for i, sd := range data {
		fmt.Printf("\n  [%d/%d] %s (id=%d, %d карточек)\n",
			i+1, len(data), sd.SubjectName, sd.SubjectID, sd.CardCount)
		fmt.Printf("  %-30s  %-8s  %s\n", "Характеристика", "Уникальных", "Значения")
		fmt.Printf("  %s\n", strings.Repeat("─", 70))
		for _, ch := range sd.Characteristics {
			vals := strings.Join(ch.Values, ", ")
			if len([]rune(vals)) > 60 {
				vals = string([]rune(vals)[:57]) + "..."
			}
			fmt.Printf("  %-30s  %-8d  %s\n", truncateRune(ch.Name, 30), len(ch.Values), vals)
		}
	}
}

// printBatchPlan shows the export-all plan without creating files.
func printBatchPlan(data []SubjectData, outputPath string, itemsPerFile int) {
	chunks := chunkCount(len(data), itemsPerFile)
	fmt.Printf("\n  --export-all plan:\n")
	fmt.Printf("    %d предметов → %d файлов (%d предметов/файл)\n",
		len(data), chunks, itemsPerFile)

	ext := filepath.Ext(outputPath)
	base := strings.TrimSuffix(outputPath, ext)

	for i := 0; i < len(data); i += itemsPerFile {
		end := min(i+itemsPerFile, len(data))
		fileIdx := i/itemsPerFile + 1
		partName := fmt.Sprintf("%s_part_%02d%s", filepath.Base(base), fileIdx, ext)
		fmt.Printf("    - %-40s  (предметы %d-%d: %s...%s)\n",
			partName, i+1, end,
			truncateRune(data[i].SubjectName, 15),
			truncateRune(data[end-1].SubjectName, 15))
	}
}

// chunkCount returns the number of files needed for total items.
func chunkCount(total, perFile int) int {
	if perFile <= 0 {
		perFile = 30
	}
	return (total + perFile - 1) / perFile
}

// --- Mock data ---

func mockData() []SubjectData {
	return []SubjectData{
		{
			SubjectName: "Кроссовки", SubjectID: 540, CardCount: 42,
			Characteristics: []CharRow{
				{CharID: 101, Name: "Сезон", Values: []string{"демисезон", "лето", "осень"}, CardCount: 42},
				{CharID: 102, Name: "Цвет", Values: []string{"белый", "черный", "синий"}, CardCount: 38},
				{CharID: 103, Name: "Материал верха", Values: []string{"иск. кожа", "нат. кожа", "текстиль"}, CardCount: 35},
				{CharID: 104, Name: "Пол", Values: []string{"мужской", "унисекс"}, CardCount: 42},
			},
		},
		{
			SubjectName: "Ботинки", SubjectID: 541, CardCount: 28,
			Characteristics: []CharRow{
				{CharID: 101, Name: "Сезон", Values: []string{"зима", "осень"}, CardCount: 28},
				{CharID: 102, Name: "Цвет", Values: []string{"черный", "коричневый"}, CardCount: 25},
				{CharID: 105, Name: "Подкладка", Values: []string{"мех", "шерсть"}, CardCount: 20},
			},
		},
		{
			SubjectName: "Сандалии детские", SubjectID: 542, CardCount: 15,
			Characteristics: []CharRow{
				{CharID: 101, Name: "Сезон", Values: []string{"лето"}, CardCount: 15},
				{CharID: 102, Name: "Цвет", Values: []string{"голубой", "розовый", "желтый"}, CardCount: 12},
			},
		},
	}
}

// generateMockSubjects creates n mock subjects for --export-all demo.
func generateMockSubjects(n int) []SubjectData {
	templates := []struct {
		name string
		id   int
	}{
		{"Кроссовки", 540}, {"Ботинки", 541}, {"Сандалии детские", 542},
		{"Туфли женские", 543}, {"Сапоги", 544}, {"Кеды", 545},
		{"Балетки", 546}, {"Мокасины", 547}, {"Босоножки", 548},
		{"Полуботинки", 549}, {"Резиновые сапоги", 550},
		{"Кроссовки детские", 551}, {"Ботильоны", 552},
		{"Слипоны", 553}, {"Эспадрильи", 554}, {"Лоферы", 555},
		{"Угги", 556}, {"Валенки", 557}, {"Биркенштоки", 558},
		{"Шлепанцы", 559}, {"Сникерсы", 560}, {"Топсайдеры", 561},
		{"Дезерты", 562}, {"Челси", 563}, {"Оксфорды", 564},
		{"Дерби", 565}, {"Монки", 566}, {"Казаки", 567},
		{"Вьетнамки", 568}, {"Кроксы", 569}, {"Мюли", 570},
		{"Сабо", 571}, {"Тимберленды", 572}, {"Найк", 573},
		{"Адидас", 574}, {"Пума", 575}, {"Рибок", 576},
		{"Кеды на платформе", 577}, {"Кроссовки мужские", 578},
		{"Ботинки женские", 579}, {"Сандалии мужские", 580},
		{"Туфли мужские", 581}, {"Сапоги женские", 582},
		{"Полусапоги", 583}, {"Ботфорты", 584},
	}

	result := make([]SubjectData, 0, n)
	for i := 0; i < n; i++ {
		t := templates[i%len(templates)]
		suffix := ""
		if i >= len(templates) {
			suffix = fmt.Sprintf(" %d", i/len(templates)+1)
		}
		cardCount := 10 + (i*7)%50
		result = append(result, SubjectData{
			SubjectName: t.name + suffix,
			SubjectID:   t.id + (i/len(templates))*100,
			CardCount:   cardCount,
			Characteristics: []CharRow{
				{CharID: 101, Name: "Сезон", Values: []string{"демисезон", "лето"}, CardCount: cardCount},
				{CharID: 102, Name: "Цвет", Values: []string{"белый", "черный"}, CardCount: 8 + (i*3)%20},
			},
		})
	}
	return result
}

// --- Helpers ---

// resolveOutputPath returns the output path for --export-all.
// If the user didn't specify --output explicitly, generates a timestamped name.
func resolveOutputPath(cfg *Config) string {
	if cfg.ExportAll && cfg.Output == defaultConfig().Output {
		return fmt.Sprintf("characteristics_%s.xlsx", time.Now().Format("2006-01-02"))
	}
	return cfg.Output
}

// parseIntList parses a comma-separated list of integers.
func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	var result []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			log.Printf("WARN: пропускаю невалидное число %q", p)
			continue
		}
		result = append(result, v)
	}
	return result
}

// truncateRune truncates a string to maxLen runes, appending "..." if needed.
func truncateRune(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
