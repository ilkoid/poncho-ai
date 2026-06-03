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
)

func main() {
	dbPath := flag.String("db", "/var/db/wb-sales.db", "Path to SQLite database (READ-ONLY)")
	fromDate := flag.String("from", "", "Start date YYYY-MM-DD (default: 30 days ago)")
	toDate := flag.String("to", "", "End date YYYY-MM-DD (default: yesterday)")
	outputPath := flag.String("output", "promo-calendar.xlsx", "Output xlsx file path")
	articlesFlag := flag.String("articles", "", "Comma-separated vendor_codes filter (default: all)")
	categoryFlag := flag.String("category", "", "Comma-separated 1C categories filter (default: all)")
	includeZeroSpend := flag.Bool("include-zero-spend", false, "Include articles that had zero spend all days (default: only articles with spend)")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Resolve date range
	from, to := resolveDateRange(*fromDate, *toDate)

	// Parse filters
	articles := parseList(*articlesFlag)
	categories := parseList(*categoryFlag)

	// Signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Header
	sep := strings.Repeat("=", 72)
	fmt.Println(sep)
	fmt.Println("     КАЛЕНДАРЬ РЕКЛАМЫ ПО АРТИКУЛАМ")
	fmt.Println(sep)
	fmt.Printf("Источник: %s\n", *dbPath)
	fmt.Printf("Период:   %s — %s\n", from, to)
	if *includeZeroSpend {
		fmt.Printf("Режим:    все артикулы (включая без расхода)\n")
	} else {
		fmt.Printf("Режим:    только с расходом за период\n")
	}
	if len(articles) > 0 {
		fmt.Printf("Фильтр:   %d артикулов\n", len(articles))
	}
	if len(categories) > 0 {
		fmt.Printf("Катег.1С: %s\n", strings.Join(categories, ", "))
	}
	fmt.Println(sep)
	fmt.Println()

	start := time.Now()

	// Step 1: Open source DB (read-only)
	repo, err := NewSourceRepo(*dbPath)
	if err != nil {
		log.Fatalf("Не удалось открыть БД: %v", err)
	}
	defer repo.Close()

	// Step 2: Load dates for column headers
	fmt.Print("Загрузка дат...")
	dates, err := repo.ListDates(ctx, from, to)
	if err != nil {
		log.Fatalf("Ошибка загрузки дат: %v", err)
	}
	fmt.Printf(" %d дней\n", len(dates))

	if len(dates) == 0 {
		fmt.Println("\n⚠ Нет данных за указанный период.")
		fmt.Printf("\nГотово за %s\n", time.Since(start).Round(time.Second))
		os.Exit(0)
	}

	// Step 3: Load promo data
	fmt.Print("Загрузка промо-данных...")
	days, err := repo.LoadPromoDays(ctx, from, to, articles, categories, *includeZeroSpend)
	if err != nil {
		log.Fatalf("Ошибка загрузки промо: %v", err)
	}

	// Count unique articles
	uniqueArticles := make(map[string]bool)
	var totalSpend float64
	for _, d := range days {
		uniqueArticles[d.VendorCode] = true
		totalSpend += d.TotalSpend
	}
	fmt.Printf(" %d записей, %d артикулов, %.1fK₽ расход\n", len(days), len(uniqueArticles), totalSpend/1000)

	if len(days) == 0 {
		fmt.Println("\n⚠ Нет промо-данных за указанный период.")
		fmt.Printf("\nГотово за %s\n", time.Since(start).Round(time.Second))
		os.Exit(0)
	}

	// Step 4: Export to xlsx
	fmt.Printf("Экспорт xlsx: %s...", *outputPath)
	if err := ExportXLSX(days, dates, *outputPath); err != nil {
		log.Fatalf("Ошибка экспорта: %v", err)
	}
	fmt.Println(" ok")

	// Summary
	fmt.Printf("\nАртикулов в рекламе: %d\n", len(uniqueArticles))
	fmt.Printf("Дней с данными:     %d\n", len(dates))
	fmt.Printf("Общий расход:       %s\n", formatTotalSpend(totalSpend))
	fmt.Printf("\nГотово за %s → %s\n", time.Since(start).Round(time.Second), *outputPath)
}

// parseList splits a comma-separated string into trimmed non-empty items.
func parseList(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func resolveDateRange(from, to string) (string, string) {
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	if to == "" {
		to = yesterday.Format("2006-01-02")
	}
	if from == "" {
		d30 := yesterday.AddDate(0, 0, -29)
		from = d30.Format("2006-01-02")
	}
	return from, to
}

func formatTotalSpend(v float64) string {
	abs := v
	switch {
	case abs >= 1e6:
		return fmt.Sprintf("%.1fM₽", v/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.1fK₽", v/1e3)
	default:
		return fmt.Sprintf("%.0f₽", v)
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Календарь рекламы по артикулам Wildberries.
Показывает по каким дням артикул был в рекламе, с указанием расхода и ДРР.
Читает из wb-sales.db. Без API-вызовов.

Столбцы: Артикул, Название, Категория 1С, затем даты.

Логика отбора:
  По умолчанию — только артикулы, у которых хотя бы один день за период
  был с реальным расходом на рекламу. Все дни таких артикулов показываются
  (включая дни с нулевым расходом).

  Ячейки в отчёте:
    Зелёный  — реальный расход (sum > 0), ДРР точный
    Жёлтый   — числился в кампании, расхода не было (sum = 0)
    Пустой   — не был в рекламе

Options:
  --db PATH               Source database (default: /var/db/wb-sales.db)
  --from DATE             Start date YYYY-MM-DD (default: 30 days ago)
  --to DATE               End date YYYY-MM-DD (default: yesterday)
  --output PATH           Output xlsx file (default: promo-calendar.xlsx)
  --articles LIST         Comma-separated vendor_codes (default: all)
  --category LIST         Comma-separated 1C categories (default: all)
  --include-zero-spend    Include articles with zero spend all days
  -h, --help              Show this help

Examples:
  %s                                                              # Only articles with spend
  %s --include-zero-spend                                         # All articles in campaigns
  %s --from 2026-05-01 --to 2026-05-31                           # May 2026
  %s --category "Сандалии" --output /tmp/sandals.xlsx              # By 1C category
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}
