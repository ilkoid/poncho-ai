// analyze-category-sales — анализатор распределения продаж по категориям WB.
//
// Парето-классификация SKU, коэффициент Джини, скорость продаж, тренды,
// ценовые сегменты, мёртвый каталог. Читает из wb-sales.db, пишет в category-sales.db.
//
// Usage:
//
//	go run ./cmd/data-analyzers/analyze-category-sales/ [options]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sort"
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
	Results struct {
		DbPath string `yaml:"db_path"`
	} `yaml:"results"`
	Analysis struct {
		PeriodDays    int       `yaml:"period_days"`
		VelocityHot   float64   `yaml:"velocity_hot"`
		VelocityWarm  float64   `yaml:"velocity_warm"`
		TrendThresh   float64   `yaml:"trend_threshold"`
		Pareto        ParetoCfg `yaml:"pareto"`
		PriceBrackets []int     `yaml:"price_brackets"`
	} `yaml:"analysis"`
}

type ParetoCfg struct {
	A float64 `yaml:"a"`
	B float64 `yaml:"b"`
	C float64 `yaml:"c"`
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
		Results: struct {
			DbPath string `yaml:"db_path"`
		}{DbPath: "/var/db/category-sales.db"},
		Analysis: struct {
			PeriodDays    int       `yaml:"period_days"`
			VelocityHot   float64   `yaml:"velocity_hot"`
			VelocityWarm  float64   `yaml:"velocity_warm"`
			TrendThresh   float64   `yaml:"trend_threshold"`
			Pareto        ParetoCfg `yaml:"pareto"`
			PriceBrackets []int     `yaml:"price_brackets"`
		}{
			PeriodDays:   30,
			VelocityHot:  2.0,
			VelocityWarm: 0.5,
			TrendThresh:  20.0,
			Pareto:       ParetoCfg{A: 80, B: 95, C: 99},
			PriceBrackets: []int{0, 500, 1000, 1500, 2000, 3000, 5000},
		},
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Анализатор распределения продаж по категориям Wildberries.
Читает из wb-sales.db. Без API-вызовов.

Options:
  --config PATH      Path to config file (default: config.yaml)
  --db PATH          Source database path (overrides config)
  --output PATH      Results database path (overrides config)
  --subject NAME     Analyze single category (default: all categories)
  --period DAYS      Analysis period in days (default: 30)
  --csv PATH         Export results to CSV
  --dry-run          Console only, no DB write
  -h, --help         Show this help

Examples:
  %s                                          # All categories, 30 days
  %s --subject Футболки                       # Single category
  %s --period 7 --dry-run                     # Quick 7-day console check
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	dbPath := flag.String("db", "", "Source database path (overrides config)")
	outputPath := flag.String("output", "", "Results database path (overrides config)")
	subjectFilter := flag.String("subject", "", "Analyze single category")
	periodDays := flag.Int("period", 0, "Analysis period in days")
	csvPath := flag.String("csv", "", "Export results to CSV file")
	xlsxPath := flag.String("xlsx", "", "Export results to XLSX file")
	dryRun := flag.Bool("dry-run", false, "Console only, no DB write")
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
	if cfg.Results.DbPath == "" {
		cfg.Results.DbPath = def.Results.DbPath
	}
	if cfg.Analysis.PeriodDays == 0 {
		cfg.Analysis.PeriodDays = def.Analysis.PeriodDays
	}
	if cfg.Analysis.VelocityHot == 0 {
		cfg.Analysis.VelocityHot = def.Analysis.VelocityHot
	}
	if cfg.Analysis.VelocityWarm == 0 {
		cfg.Analysis.VelocityWarm = def.Analysis.VelocityWarm
	}
	if cfg.Analysis.TrendThresh == 0 {
		cfg.Analysis.TrendThresh = def.Analysis.TrendThresh
	}
	if cfg.Analysis.Pareto == (ParetoCfg{}) {
		cfg.Analysis.Pareto = def.Analysis.Pareto
	}
	if len(cfg.Analysis.PriceBrackets) == 0 {
		cfg.Analysis.PriceBrackets = def.Analysis.PriceBrackets
	}

	// CLI overrides
	if *dbPath != "" {
		cfg.Source.DbPath = *dbPath
	}
	if *outputPath != "" {
		cfg.Results.DbPath = *outputPath
	}
	if *periodDays > 0 {
		cfg.Analysis.PeriodDays = *periodDays
	}

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
	dash := strings.Repeat("-", 72)
	fmt.Println(sep)
	fmt.Println("     АНАЛИЗ РАСПРЕДЕЛЕНИЯ ПРОДАЖ ПО КАТЕГОРИЯМ")
	fmt.Println(sep)
	fmt.Printf("Источник: %s\n", cfg.Source.DbPath)
	fmt.Printf("Период:   %d дней\n", cfg.Analysis.PeriodDays)
	if *subjectFilter != "" {
		fmt.Printf("Категория: %s\n", *subjectFilter)
	}
	fmt.Println(sep)
	fmt.Println()

	start := time.Now()

	// Step 1: Open source DB
	repo, err := NewSourceRepo(cfg.Source.DbPath)
	if err != nil {
		log.Fatalf("Failed to open source DB: %v", err)
	}
	defer repo.Close()

	// Step 2: Get category list
	fmt.Print("Загрузка списка категорий...")
	var categories []string
	if *subjectFilter != "" {
		categories = []string{*subjectFilter}
	} else {
		categories, err = repo.ListCategories(ctx, cfg.Analysis.PeriodDays)
		if err != nil {
			log.Fatalf("Failed to list categories: %v", err)
		}
	}
	fmt.Printf(" %d\n\n", len(categories))

	// Step 3: Open results DB (if not dry-run)
	var results *ResultsRepo
	if !*dryRun {
		results, err = NewResultsRepo(cfg.Results.DbPath)
		if err != nil {
			log.Fatalf("Failed to open results DB: %v", err)
		}
		defer results.Close()
	}

	// Step 4: Process each category
	summaryLines := make([]string, 0, len(categories))

	for i, cat := range categories {
		fmt.Printf("  [%d/%d] %s...", i+1, len(categories), cat)

		// Q1: current period sales
		skus, err := repo.LoadCategorySales(ctx, cat, cfg.Analysis.PeriodDays)
		if err != nil {
			log.Printf("  ERR: %v", err)
			continue
		}

		// Q2: previous period for trend
		prevPeriod, err := repo.LoadPeriodSales(ctx, cat,
			cfg.Analysis.PeriodDays*2, cfg.Analysis.PeriodDays)
		if err != nil {
			log.Printf("  ERR prev period: %v", err)
			prevPeriod = nil
		}

		// Compute all analytics
		rankings := computePareto(skus, cfg.Analysis.Pareto)
		dist := computeDistribution(skus)
		velocities := computeVelocity(skus, cfg.Analysis.PeriodDays, cfg.Analysis.VelocityHot, cfg.Analysis.VelocityWarm)
		trends := computeTrend(skus, prevPeriod, cfg.Analysis.TrendThresh)
		brackets := computePriceBrackets(skus, cfg.Analysis.PriceBrackets)

		fmt.Printf(" %d SKU\n", len(skus))

		// Save to results DB
		if results != nil {
			if err := results.SaveSKURankings(ctx, rankings, cat); err != nil {
				log.Printf("  ERR save rankings: %v", err)
			}
			if err := results.SaveDistribution(ctx, dist, cat); err != nil {
				log.Printf("  ERR save distribution: %v", err)
			}
			if err := results.SaveVelocity(ctx, velocities, cat); err != nil {
				log.Printf("  ERR save velocity: %v", err)
			}
			if err := results.SaveTrend(ctx, trends, cat); err != nil {
				log.Printf("  ERR save trend: %v", err)
			}
			if err := results.SavePriceBrackets(ctx, brackets, cat); err != nil {
				log.Printf("  ERR save brackets: %v", err)
			}
		}

		// Collect summary line
		summaryLines = append(summaryLines, formatSummaryLine(cat, dist, rankings))

		// Print category detail
		printCategoryDetail(cat, dist, rankings, brackets, velocities, trends, sep, dash)
	}

	// Step 5: Summary table
	if len(summaryLines) > 1 {
		printSummaryTable(summaryLines, dash, sep)
	}

	// Step 6: Dead catalog
	fmt.Print("\nЗагрузка мёртвого каталога...")
	dead, err := repo.LoadDeadCatalog(ctx, cfg.Analysis.PeriodDays)
	if err != nil {
		log.Printf(" ERR: %v", err)
	} else {
		fmt.Printf(" %d категорий без продаж\n", len(dead))
		printDeadCatalog(dead, sep, dash)
		if results != nil {
			if err := results.SaveDeadCatalog(ctx, toDeadCatalogRows(dead)); err != nil {
				log.Printf("  ERR save dead catalog: %v", err)
			}
		}
	}

	// Step 7: CSV export
	if results != nil && *csvPath != "" {
		fmt.Printf("\nЭкспорт CSV: %s...", *csvPath)
		if err := results.ExportCSV(ctx, *csvPath); err != nil {
			log.Printf(" ERR: %v", err)
		} else {
			fmt.Println(" ok")
		}
	}

	// Step 8: XLSX export
	if results != nil && *xlsxPath != "" {
		fmt.Printf("\nЭкспорт XLSX: %s...", *xlsxPath)
		if err := results.ExportXLSX(ctx, *xlsxPath); err != nil {
			log.Printf(" ERR: %v", err)
		} else {
			fmt.Println(" ok")
		}
	}

	fmt.Printf("\nГотово за %s\n", time.Since(start).Round(time.Second))
}

// --- Computation functions ---

func computePareto(skus []SKURow, pcfg ParetoCfg) []SKURanking {
	if len(skus) == 0 {
		return nil
	}

	// Sort by revenue DESC (should already be sorted from query, but ensure)
	sort.Slice(skus, func(i, j int) bool {
		return skus[i].Revenue > skus[j].Revenue
	})

	var totalRev float64
	for _, s := range skus {
		totalRev += s.Revenue
	}
	if totalRev == 0 {
		return nil
	}

	rankings := make([]SKURanking, len(skus))
	var cumPct float64
	for i, s := range skus {
		pct := s.Revenue / totalRev * 100
		cumPct += pct

		tier := "D"
		if cumPct <= pcfg.A {
			tier = "A"
		} else if cumPct <= pcfg.B {
			tier = "B"
		} else if cumPct <= pcfg.C {
			tier = "C"
		}

		rankings[i] = SKURanking{
			NmID:       s.NmID,
			VendorCode: s.VendorCode,
			Revenue:    s.Revenue,
			Units:      s.Units,
			AvgPrice:   s.AvgPrice,
			CumPct:     cumPct,
			ParetoTier: tier,
		}
	}
	return rankings
}

func computeDistribution(skus []SKURow) DistributionRow {
	if len(skus) == 0 {
		return DistributionRow{}
	}

	revenues := make([]float64, len(skus))
	var totalRev, totalUnits float64
	var deadCount int
	for i, s := range skus {
		revenues[i] = s.Revenue
		totalRev += s.Revenue
		totalUnits += float64(s.Units)
		if s.Revenue == 0 {
			deadCount++
		}
	}

	sort.Float64s(revenues)

	return DistributionRow{
		SKUCount:     len(skus),
		TotalRev:     totalRev,
		TotalUnits:   int(totalUnits),
		Gini:         gini(revenues),
		Top10Share:   topShare(skus, 0.10),
		Top20Share:   topShare(skus, 0.20),
		MedianRev:    median(revenues),
		MeanRev:      mean(revenues),
		DeadSKUCount: deadCount,
	}
}

func computeVelocity(skus []SKURow, periodDays int, hot, warm float64) []VelocityRow {
	result := make([]VelocityRow, len(skus))
	for i, s := range skus {
		upd := float64(s.Units) / float64(periodDays)
		class := "dead"
		if upd >= hot {
			class = "hot"
		} else if upd >= warm {
			class = "warm"
		} else if upd > 0 {
			class = "cold"
		}
		result[i] = VelocityRow{
			NmID:        s.NmID,
			VendorCode:  s.VendorCode,
			Units:       s.Units,
			UnitsPerDay: upd,
			Class:       class,
		}
	}
	return result
}

func computeTrend(curr []SKURow, prev []PeriodRow, threshold float64) []TrendRow {
	prevMap := make(map[int]PeriodRow, len(prev))
	for _, p := range prev {
		prevMap[p.NmID] = p
	}

	result := make([]TrendRow, len(curr))
	for i, s := range curr {
		var trend string
		var changePct float64
		prevRev := prevMap[s.NmID].Revenue

		if prevRev == 0 && s.Revenue > 0 {
			trend = "new"
		} else if prevRev > 0 {
			changePct = (s.Revenue - prevRev) / prevRev * 100
			if changePct > threshold {
				trend = "growing"
			} else if changePct < -threshold {
				trend = "declining"
			} else {
				trend = "stable"
			}
		} else {
			trend = "dead"
		}

		result[i] = TrendRow{
			NmID:      s.NmID,
			VendorCode: s.VendorCode,
			CurrRev:   s.Revenue,
			PrevRev:   prevRev,
			ChangePct: changePct,
			Trend:     trend,
		}
	}
	return result
}

func computePriceBrackets(skus []SKURow, bracketBounds []int) []PriceBracketRow {
	if len(bracketBounds) == 0 {
		return nil
	}

	// Build bracket labels
	type bracket struct {
		label string
		min   float64
		max   float64
	}
	brackets := make([]bracket, len(bracketBounds))
	for i := 0; i < len(bracketBounds); i++ {
		b := bracketBounds[i]
		if i+1 < len(bracketBounds) {
			brackets[i] = bracket{
				label: fmt.Sprintf("%d-%d₽", b, bracketBounds[i+1]),
				min:   float64(b),
				max:   float64(bracketBounds[i+1]),
			}
		} else {
			brackets[i] = bracket{
				label: fmt.Sprintf("%d₽+", b),
				min:   float64(b),
				max:   math.MaxFloat64,
			}
		}
	}

	// Accumulate
	type acc struct {
		count   int
		revenue float64
		units   int
	}
	accs := make([]acc, len(brackets))
	var totalRev float64
	for _, s := range skus {
		totalRev += s.Revenue
		for j, b := range brackets {
			if s.AvgPrice >= b.min && s.AvgPrice < b.max {
				accs[j].count++
				accs[j].revenue += s.Revenue
				accs[j].units += s.Units
				break
			}
		}
	}

	result := make([]PriceBracketRow, len(brackets))
	for j, b := range brackets {
		var pct float64
		if totalRev > 0 {
			pct = accs[j].revenue / totalRev * 100
		}
		result[j] = PriceBracketRow{
			Bracket:    b.label,
			SKUCount:   accs[j].count,
			Revenue:    accs[j].revenue,
			Units:      accs[j].units,
			RevenuePct: pct,
		}
	}
	return result
}

// --- Math helpers ---

func gini(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	var sum, weightedSum float64
	for i, v := range values {
		sum += v
		weightedSum += float64(i+1) * v
	}
	if sum == 0 {
		return 0
	}
	return (2*weightedSum)/(float64(n)*sum) - (float64(n)+1)/float64(n)
}

func topShare(skus []SKURow, fraction float64) float64 {
	n := len(skus)
	if n == 0 {
		return 0
	}

	// Sort copy by revenue DESC (defensive — caller may not guarantee order)
	sorted := make([]SKURow, n)
	copy(sorted, skus)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Revenue > sorted[j].Revenue })

	cut := int(math.Ceil(float64(n) * fraction))
	if cut > n {
		cut = n
	}

	var totalRev, topRev float64
	for _, s := range sorted {
		totalRev += s.Revenue
	}
	for i := 0; i < cut; i++ {
		topRev += sorted[i].Revenue
	}
	if totalRev == 0 {
		return 0
	}
	return topRev / totalRev * 100
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// --- Print helpers ---

func formatSummaryLine(cat string, d DistributionRow, rankings []SKURanking) string {
	tierCounts := make(map[string]int)
	for _, r := range rankings {
		tierCounts[r.ParetoTier]++
	}
	return fmt.Sprintf("%s\t%d\t%.0f\t%d\t%d\t%d\t%d\t%.2f\t%d",
		cat, d.SKUCount, d.TotalRev, tierCounts["A"], tierCounts["B"],
		tierCounts["C"], tierCounts["D"], d.Gini, d.DeadSKUCount)
}

func printSummaryTable(lines []string, dash, sep string) {
	fmt.Println("\n" + sep)
	fmt.Println("     СВОДНАЯ ТАБЛИЦА")
	fmt.Println(sep)
	fmt.Printf("  %-18s %5s %12s %4s %4s %4s %4s %6s %5s\n",
		"Категория", "SKU", "Выручка", "A", "B", "C", "D", "Джини", "Dead")
	fmt.Println(dash)
	for _, l := range lines {
		parts := strings.Split(l, "\t")
		if len(parts) != 9 {
			continue
		}
		fmt.Printf("  %-18s %5s %12s %4s %4s %4s %4s %6s %5s\n",
			parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6], parts[7], parts[8])
	}
	fmt.Println(sep)
}

func printCategoryDetail(cat string, d DistributionRow, rankings []SKURanking,
	brackets []PriceBracketRow, velocities []VelocityRow, trends []TrendRow,
	sep, dash string) {

	fmt.Println(sep)
	fmt.Printf("     %s (%d SKU, %s руб.)\n", cat, d.SKUCount, formatNum(d.TotalRev))
	fmt.Println(sep)

	// Distribution
	fmt.Println("\n  РАСПРЕДЕЛЕНИЕ")
	fmt.Println(dash)
	fmt.Printf("    Джини:          %.2f\n", d.Gini)
	fmt.Printf("    Топ-10%% SKU:    %.1f%% выручки\n", d.Top10Share)
	fmt.Printf("    Топ-20%% SKU:    %.1f%% выручки\n", d.Top20Share)
	fmt.Printf("    Медиана:        %s руб.\n", formatNum(d.MedianRev))
	fmt.Printf("    Среднее:        %s руб.\n", formatNum(d.MeanRev))
	fmt.Printf("    Dead SKU:       %d (%.0f%%)\n", d.DeadSKUCount, pctOf(d.DeadSKUCount, d.SKUCount))

	// Pareto tiers
	fmt.Println("\n  ПАРЕТО-КЛАССИФИКАЦИЯ")
	fmt.Println(dash)
	tierCounts := make(map[string]int)
	for _, r := range rankings {
		tierCounts[r.ParetoTier]++
	}
	for _, tier := range []string{"A", "B", "C", "D"} {
		c := tierCounts[tier]
		fmt.Printf("    %s  %4d SKU  (%5.1f%%)\n", tier, c, pctOf(c, d.SKUCount))
	}

	// Top-10
	fmt.Println("\n  ТОП-10 SKU")
	fmt.Println(dash)
	top := 10
	if len(rankings) < top {
		top = len(rankings)
	}
	for i := 0; i < top; i++ {
		r := rankings[i]
		fmt.Printf("    #%2d  %8s  %-12s  %10s руб.  %5.1f%%  %s\n",
			i+1, r.VendorCode, fmt.Sprintf("nm%d", r.NmID),
			formatNum(r.Revenue), r.CumPct, r.ParetoTier)
	}

	// Price brackets
	if len(brackets) > 0 {
		fmt.Println("\n  ЦЕНОВЫЕ СЕГМЕНТЫ")
		fmt.Println(dash)
		fmt.Printf("    %-14s %5s %12s %8s %6s\n", "Брекет", "SKU", "Выручка", "Штуки", "Доля")
		for _, b := range brackets {
			if b.SKUCount == 0 {
				continue
			}
			fmt.Printf("    %-14s %5d %12s %8d %5.1f%%\n",
				b.Bracket, b.SKUCount, formatNum(b.Revenue), b.Units, b.RevenuePct)
		}
	}

	// Velocity
	fmt.Println("\n  СКОРОСТЬ ПРОДАЖ")
	fmt.Println(dash)
	vClasses := make(map[string]int)
	for _, v := range velocities {
		vClasses[v.Class]++
	}
	for _, c := range []struct {
		name, desc string
	}{
		{"hot", "≥2/день"},
		{"warm", "≥0.5/день"},
		{"cold", ">0"},
		{"dead", "0 продаж"},
	} {
		n := vClasses[c.name]
		fmt.Printf("    %-12s %4d  (%5.1f%%)  %s\n", c.name, n, pctOf(n, len(velocities)), c.desc)
	}

	// Trend
	fmt.Println("\n  ТРЕНД (текущий vs предыдущий период)")
	fmt.Println(dash)
	trendCounts := make(map[string]int)
	for _, t := range trends {
		trendCounts[t.Trend]++
	}
	for _, t := range []struct {
		name string
		icon string
	}{
		{"growing", "↑"},
		{"stable", "→"},
		{"declining", "↓"},
		{"new", "+"},
		{"dead", "○"},
	} {
		n := trendCounts[t.name]
		fmt.Printf("    %-12s %s %4d  (%5.1f%%)\n", t.name, t.icon, n, pctOf(n, len(trends)))
	}

	fmt.Println(sep + "\n")
}

func printDeadCatalog(dead []DeadCategory, sep, dash string) {
	if len(dead) == 0 {
		fmt.Println("\n  Мёртвых категорий нет!")
		return
	}

	var totalSKU int
	fmt.Println("\n" + sep)
	fmt.Printf("     МЁРТВЫЙ КАТАЛОГ (%d категорий без продаж)\n", len(dead))
	fmt.Println(sep)
	fmt.Printf("  %-30s %5s\n", "Предмет", "SKU")
	fmt.Println(dash)
	for _, d := range dead {
		fmt.Printf("  %-30s %5d\n", d.SubjectName, d.SKUCount)
		totalSKU += d.SKUCount
	}
	fmt.Println(dash)
	fmt.Printf("  Итого: %d категорий, %d SKU без продаж\n", len(dead), totalSKU)
	fmt.Println(sep)
}

func toDeadCatalogRows(dead []DeadCategory) []DeadCatalogRow {
	rows := make([]DeadCatalogRow, len(dead))
	for i, d := range dead {
		rows[i] = DeadCatalogRow{SubjectName: d.SubjectName, SKUCount: d.SKUCount}
	}
	return rows
}

func formatNum(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := math.Abs(v)
	switch {
	case abs >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func pctOf(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
