// analyze-card-quality — анализатор качества карточек товаров WB.
//
// Оценивает карточки по 4 категориям (content, characteristics, technical, market),
// выводит детальную статистику по каждому критерию без агрегации.
// Чисто локальный анализатор — читает из wb-sales.db. Без API-вызовов.
//
// Usage:
//
//	go run ./cmd/data-analyzers/analyze-card-quality/ [options]
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
)

// Config — конфигурация анализатора.
type Config struct {
	Source struct {
		DbPath string `yaml:"db_path"`
	} `yaml:"source"`
	Filter struct {
		AllowedYears []int `yaml:"allowed_years"`
	} `yaml:"filter"`
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
		Source: struct {
			DbPath string `yaml:"db_path"`
		}{DbPath: "/var/db/wb-sales.db"},
		Filter: struct {
			AllowedYears []int `yaml:"allowed_years"`
		}{},
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Локальный анализатор качества карточек товаров Wildberries.
Читает из wb-sales.db. Без API-вызовов.

Options:
  --config PATH      Path to config file (default: config.yaml)
  --db PATH          Source database path (overrides config)
  --year YEAR        Filter by card creation year (e.g. 2025)
  --subject NAME     Filter by subject name (e.g. "Футболки")
  -h, --help         Show this help

Examples:
  %s                                # Analyze all cards
  %s --year 2025                    # Only 2025 cards
  %s --subject Футболки             # Only t-shirts
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	dbPath := flag.String("db", "", "Source database path (overrides config)")
	yearFilter := flag.Int("year", 0, "Filter by card creation year")
	subjectFilter := flag.String("subject", "", "Filter by subject name")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	def := defaultConfig()
	if cfg.Source.DbPath == "" {
		cfg.Source.DbPath = def.Source.DbPath
	}

	if *dbPath != "" {
		cfg.Source.DbPath = *dbPath
	}

	// Signal handling
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Header
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println("         АНАЛИЗАТОР КАЧЕСТВА КАРТОЧЕК")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Источник: %s\n", cfg.Source.DbPath)
	if len(cfg.Filter.AllowedYears) > 0 {
		fmt.Printf("Годы:     %v\n", cfg.Filter.AllowedYears)
	}
	if *yearFilter > 0 {
		fmt.Printf("Год:      %d\n", *yearFilter)
	}
	if *subjectFilter != "" {
		fmt.Printf("Предмет:  %s\n", *subjectFilter)
	}
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println()

	start := time.Now()

	// Step 1: Open source DB (read-only)
	repo, err := OpenSourceDB(cfg.Source.DbPath)
	if err != nil {
		log.Fatalf("Failed to open source DB: %v", err)
	}
	defer repo.Close()

	// Step 2: Load cards
	fmt.Print("Загрузка карточек...")
	cards, err := repo.LoadCards(*yearFilter, *subjectFilter)
	if err != nil {
		log.Fatalf("Failed to load cards: %v", err)
	}
	fmt.Printf(" %d\n", len(cards))

	// Step 2b: Keep only vendor_code with exactly 8 characters
	if len(cards) > 0 {
		filtered := make([]CardData, 0, len(cards))
		for _, c := range cards {
			if len(c.VendorCode) == 8 {
				filtered = append(filtered, c)
			}
		}
		fmt.Printf("  Фильтр по длине артикула (8 символов): %d -> %d\n", len(cards), len(filtered))
		cards = filtered
	}

	// Step 2c: Filter by vendor_code production year
	if len(cfg.Filter.AllowedYears) > 0 {
		entries := make([]config.YearEntry, len(cards))
		for i, c := range cards {
			entries[i] = config.YearEntry{NmID: c.NmID, VendorCode: c.VendorCode}
		}
		allowed := config.FilterNmIDsByYear(entries, cfg.Filter.AllowedYears)
		allowedSet := make(map[int]bool, len(allowed))
		for _, id := range allowed {
			allowedSet[id] = true
		}
		filtered2 := make([]CardData, 0, len(allowed))
		for _, c := range cards {
			if allowedSet[c.NmID] {
				filtered2 = append(filtered2, c)
			}
		}
		fmt.Printf("  Фильтр по году артикула %v: %d -> %d\n", cfg.Filter.AllowedYears, len(cards), len(filtered2))
		cards = filtered2
	}

	if len(cards) == 0 {
		log.Fatal("Нет карточек по заданным фильтрам")
	}

	// Step 3: Load characteristics into cards
	cardsMap := make(map[int]*CardData, len(cards))
	for i := range cards {
		cardsMap[cards[i].NmID] = &cards[i]
	}
	fmt.Print("Загрузка характеристик...")
	subjectIDs, err := repo.LoadCharacteristics(cardsMap)
	if err != nil {
		log.Fatalf("Failed to load characteristics: %v", err)
	}
	fmt.Printf(" %d предметов\n", len(subjectIDs))

	// Step 4: Load feedback stats
	fmt.Print("Загрузка отзывов...")
	feedbackStats, err := repo.LoadFeedbackStats()
	if err != nil {
		log.Printf("Warning: feedbacks: %v", err)
		feedbackStats = make(map[int]*FeedbackStats)
	}
	fmt.Printf(" %d товаров с рейтингом\n", len(feedbackStats))

	// Step 5: Build characteristic profiles
	fmt.Print("Расчёт частот характеристик...")
	freqs, err := repo.LoadCharFrequencies()
	if err != nil {
		log.Printf("Warning: char frequencies: %v", err)
		freqs = nil
	}
	profiles := BuildSubjectCharProfiles(freqs)
	fmt.Printf(" %d предметов\n", len(profiles))

	// Step 6: Compute max chars per subject
	maxChars := ComputeSubjectMaxChars(cards)
	for sid, p := range profiles {
		p.MaxChars = maxChars[sid]
	}

	// Step 7: Measure all cards
	fmt.Print("Расчёт метрик...")
	metrics := make([]CardMetric, len(cards))
	for i, card := range cards {
		metrics[i] = MeasureCard(card, profiles[card.SubjectID], feedbackStats[card.NmID])
	}
	fmt.Printf(" готово\n")

	duration := time.Since(start)

	// Step 8: Print report
	printBreakdown(metrics, duration)
}

// ── Output helpers ────────────────────────────────────────────────────────

func printBreakdown(metrics []CardMetric, duration time.Duration) {
	n := len(metrics)
	fmt.Println()
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("АНАЛИЗ КАЧЕСТВА КАРТОЧЕК - %d шт.  |  %s\n", n, duration.Round(time.Millisecond))
	fmt.Println(strings.Repeat("=", 72))

	// КОНТЕНТ
	fmt.Println("\nКОНТЕНТ")
	fmt.Println(strings.Repeat("-", 72))

	fmt.Println("  Заголовок:")
	pct("Пустой", cnt(metrics, func(m CardMetric) bool { return m.TitleLen == 0 }), n)
	pct("Короткий (<20)", cnt(metrics, func(m CardMetric) bool { return m.TitleLen > 0 && m.TitleLen < 20 }), n)
	pct("Нормальный (>=20)", cnt(metrics, func(m CardMetric) bool { return m.TitleLen >= 20 }), n)

	fmt.Println("  Описание:")
	pct("Нет", cnt(metrics, func(m CardMetric) bool { return m.DescLen == 0 }), n)
	pct("Короткое (<100)", cnt(metrics, func(m CardMetric) bool { return m.DescLen > 0 && m.DescLen < 100 }), n)
	pct("Среднее (100-500)", cnt(metrics, func(m CardMetric) bool { return m.DescLen >= 100 && m.DescLen < 500 }), n)
	pct("Хорошее (500-1000)", cnt(metrics, func(m CardMetric) bool { return m.DescLen >= 500 && m.DescLen < 1000 }), n)
	pct("Полное (1000+)", cnt(metrics, func(m CardMetric) bool { return m.DescLen >= 1000 }), n)

	fmt.Println("  Фото:")
	pct("0", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount == 0 }), n)
	pct("1", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount == 1 }), n)
	pct("2-4", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount >= 2 && m.PhotoCount <= 4 }), n)
	pct("5-7", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount >= 5 && m.PhotoCount <= 7 }), n)
	pct("8+", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount >= 8 }), n)

	fmt.Println("  Видео:")
	pct("Есть", cnt(metrics, func(m CardMetric) bool { return m.HasVideo }), n)
	pct("Нет", cnt(metrics, func(m CardMetric) bool { return !m.HasVideo }), n)

	fmt.Println("  Бренд:")
	pct("Заполнен", cnt(metrics, func(m CardMetric) bool { return m.HasBrand }), n)
	pct("Пустой", cnt(metrics, func(m CardMetric) bool { return !m.HasBrand }), n)

	// ХАРАКТЕРИСТИКИ
	fmt.Println("\nХАРАКТЕРИСТИКИ")
	fmt.Println(strings.Repeat("-", 72))

	fmt.Println("  Ожидаемые (>=50% предмета):")
	pct("Нет профиля", cnt(metrics, func(m CardMetric) bool { return !m.HasProfile }), n)
	pct("Низкий (<50%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.ExpectedPct < 50 }), n)
	pct("Средний (50-80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.ExpectedPct >= 50 && m.ExpectedPct < 80 }), n)
	pct("Высокий (>=80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.ExpectedPct >= 80 }), n)

	fmt.Println("  Обязательные (>=90% предмета):")
	pct("Низкий (<50%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.CommonPct > 0 && m.CommonPct < 50 }), n)
	pct("Средний (50-80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.CommonPct >= 50 && m.CommonPct < 80 }), n)
	pct("Высокий (>=80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.CommonPct >= 80 }), n)

	fmt.Println("  Плотность (от максимума в предмете):")
	pct("Низкая (<50%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.DensityPct > 0 && m.DensityPct < 50 }), n)
	pct("Средняя (50-80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.DensityPct >= 50 && m.DensityPct < 80 }), n)
	pct("Высокая (>=80%)", cnt(metrics, func(m CardMetric) bool { return m.HasProfile && m.DensityPct >= 80 }), n)

	// ТЕХНИЧЕСКОЕ
	fmt.Println("\nТЕХНИЧЕСКОЕ")
	fmt.Println(strings.Repeat("-", 72))

	fmt.Println("  Габариты:")
	pct("Указаны", cnt(metrics, func(m CardMetric) bool { return m.DimHasValues }), n)
	pct("Не указаны", cnt(metrics, func(m CardMetric) bool { return !m.DimHasValues }), n)

	fmt.Println("  Размерная сетка:")
	pct("0 размеров", cnt(metrics, func(m CardMetric) bool { return m.SizeCount == 0 }), n)
	pct("1 размер", cnt(metrics, func(m CardMetric) bool { return m.SizeCount == 1 }), n)
	pct("2+ размеров", cnt(metrics, func(m CardMetric) bool { return m.SizeCount >= 2 }), n)

	fmt.Println("  Достаточность фото:")
	pct("<5 фото", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount < 5 }), n)
	pct(">=5 фото", cnt(metrics, func(m CardMetric) bool { return m.PhotoCount >= 5 }), n)

	// РЫНОК
	fmt.Println("\nРЫНОК")
	fmt.Println(strings.Repeat("-", 72))

	fmt.Println("  Рейтинг:")
	pct("Без отзывов", cnt(metrics, func(m CardMetric) bool { return !m.HasFeedbacks }), n)
	pct("<3.0", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AvgRating < 3.0 }), n)
	pct("3.0-4.0", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AvgRating >= 3.0 && m.AvgRating < 4.0 }), n)
	pct("4.0-4.5", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AvgRating >= 4.0 && m.AvgRating < 4.5 }), n)
	pct("4.5-5.0", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AvgRating >= 4.5 }), n)

	fmt.Println("  Количество отзывов:")
	pct("0", cnt(metrics, func(m CardMetric) bool { return !m.HasFeedbacks || m.FeedbackCount == 0 }), n)
	pct("1-2", cnt(metrics, func(m CardMetric) bool { return m.FeedbackCount >= 1 && m.FeedbackCount <= 2 }), n)
	pct("3-10", cnt(metrics, func(m CardMetric) bool { return m.FeedbackCount >= 3 && m.FeedbackCount <= 10 }), n)
	pct("11-50", cnt(metrics, func(m CardMetric) bool { return m.FeedbackCount >= 11 && m.FeedbackCount <= 50 }), n)
	pct("51+", cnt(metrics, func(m CardMetric) bool { return m.FeedbackCount >= 51 }), n)

	fmt.Println("  Доля ответов:")
	pct("Без отзывов", cnt(metrics, func(m CardMetric) bool { return !m.HasFeedbacks }), n)
	pct("<50%", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AnswerRate < 0.5 }), n)
	pct("50-99%", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AnswerRate >= 0.5 && m.AnswerRate < 1.0 }), n)
	pct("100%", cnt(metrics, func(m CardMetric) bool { return m.HasFeedbacks && m.AnswerRate >= 1.0 }), n)

	fmt.Println("\n" + strings.Repeat("=", 72))
}

func cnt(metrics []CardMetric, fn func(CardMetric) bool) int {
	c := 0
	for _, m := range metrics {
		if fn(m) {
			c++
		}
	}
	return c
}

func pct(label string, count, total int) {
	p := float64(count) / float64(total) * 100
	fmt.Printf("    %-20s %6d (%5.1f%%)\n", label, count, p)
}
