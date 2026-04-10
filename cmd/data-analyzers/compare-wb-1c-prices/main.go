// Package main provides a utility to compare 1C retail prices with WB marketplace prices.
//
// Reads product and price data from SQLite (downloaded by data-downloaders),
// joins 1C and WB data via vendor_code mapping chain, calculates price differences,
// and stores results in a BI knowledge database.
//
// Extended for finance director requirements:
//   - Розничная цена СР (retail stores)
//   - Спец цена для акции (loyalty program flag)
//   - Average WB SPP from sales data (3-day window)
//
// Usage:
//
//	go run .                          # latest snapshot from each system
//	go run . --date 2026-04-08        # specific date
//	go run . --force                  # rebuild even if results exist
//	go run . --csv                    # also export to CSV
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config represents the YAML configuration.
type Config struct {
	Source     SourceConfig     `yaml:"source"`
	Results    ResultsConfig    `yaml:"results"`
	Comparison ComparisonConfig `yaml:"comparison"`
}

type SourceConfig struct {
	DBPath string `yaml:"db_path"`
}

type ResultsConfig struct {
	DBPath string `yaml:"db_path"`
}

type ComparisonConfig struct {
	OneCBasePriceType      string  `yaml:"onec_base_price_type"`
	SPP25Percent           float64 `yaml:"spp25_percent"`
	SPPUserPercent         float64 `yaml:"spp_user_percent"`
	MatchThreshold         float64 `yaml:"match_threshold"`
	WarningThreshold       float64 `yaml:"warning_threshold"`
	OneCSRPriceType        string  `yaml:"onec_sr_price_type"`
	OneCSpecialPriceType   string  `yaml:"onec_special_price_type"`
	SPPDaysBack            int     `yaml:"spp_days_back"`
	WBWalletPercent        float64 `yaml:"wb_wallet_percent"`
	LoyaltyDiscountPercent float64 `yaml:"loyalty_discount_percent"`
}

func main() {
	// CLI flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	sourcePath := flag.String("source", "", "Override source DB path (wb-sales.db)")
	outputPath := flag.String("output", "", "Override results DB path (bi.db)")
	dateFlag := flag.String("date", "", "Snapshot date YYYY-MM-DD (default: latest)")
	force := flag.Bool("force", false, "Rebuild even if results exist for this date")
	csvExport := flag.Bool("csv", false, "Export results to CSV")
	flag.Parse()

	// Load config
	var cfg Config
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		log.Fatalf("Load config: %v", err)
	}
	applyDefaults(&cfg)

	// Apply CLI overrides
	if *sourcePath != "" {
		cfg.Source.DBPath = *sourcePath
	}
	if *outputPath != "" {
		cfg.Results.DBPath = *outputPath
	}

	// Context with graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Open source DB (read-only)
	source, err := NewSourceRepo(cfg.Source.DBPath)
	if err != nil {
		log.Fatalf("Open source: %v", err)
	}
	defer source.Close()

	// Resolve dates
	wbDate, onecDate := *dateFlag, *dateFlag
	if wbDate == "" {
		wbDate, onecDate, err = source.GetLatestDates(ctx)
		if err != nil {
			log.Fatalf("Get dates: %v", err)
		}
	}

	fmt.Println("================================================================================")
	fmt.Println("1C vs WB Price Comparison (extended)")
	fmt.Println("================================================================================")
	fmt.Printf("WB date:     %s\n", wbDate)
	fmt.Printf("1C date:     %s\n", onecDate)
	fmt.Printf("Source DB:   %s\n", cfg.Source.DBPath)
	fmt.Printf("Results DB:  %s\n", cfg.Results.DBPath)
	fmt.Printf("SPP window:  %d days\n", cfg.Comparison.SPPDaysBack)

		// Warn about snapshot date mismatch
		if wbTime, err1 := time.Parse("2006-01-02", wbDate); err1 == nil {
			if onecTime, err2 := time.Parse("2006-01-02", onecDate); err2 == nil {
				diff := onecTime.Sub(wbTime)
				if diff < 0 {
					diff = -diff
				}
				if diff > 24*time.Hour {
					log.Printf("WARNING: snapshot dates differ by %v — results may be inaccurate", diff)
				}
			}
		}
	fmt.Println("================================================================================")

	// Check existing results
	results, err := NewResultsRepo(cfg.Results.DBPath)
	if err != nil {
		log.Fatalf("Open results: %v", err)
	}
	defer results.Close()

	if !*force {
		exists, err := results.HasResultsForDate(ctx, wbDate, onecDate)
		if err != nil {
			log.Fatalf("Check existing: %v", err)
		}
		if exists {
			fmt.Println("Results already exist for these dates. Use --force to rebuild.")
			return
		}
	}

	// Run comparison query
	params := CompareParams{
		WBDate:           wbDate,
		OneCDate:         onecDate,
		BasePriceType:    cfg.Comparison.OneCBasePriceType,
		SRPriceType:      cfg.Comparison.OneCSRPriceType,
		SpecialPriceType: cfg.Comparison.OneCSpecialPriceType,
		SPPDaysBack:      cfg.Comparison.SPPDaysBack,
	}
	sourceData, err := source.ComparePrices(ctx, params)
	if err != nil {
		log.Fatalf("Compare prices: %v", err)
	}
	fmt.Printf("Matched products: %d\n", len(sourceData))

	if len(sourceData) == 0 {
		fmt.Println("No matching products found. Check that source DB has data for the specified dates.")
		return
	}

	// Get unmatched counts
	unmatched, err := source.CountUnmatched(ctx, wbDate, onecDate, cfg.Comparison.OneCBasePriceType)
	if err != nil {
		log.Printf("Warning: could not count unmatched: %v", err)
	}

	// Calculate differences and statuses
	comparisonResults := make([]ComparisonResult, len(sourceData))
	for i, s := range sourceData {
		comparisonResults[i] = calculateComparison(s, wbDate, onecDate, cfg.Comparison)
	}

	// Save results
	if err := results.SaveResults(ctx, comparisonResults); err != nil {
		log.Fatalf("Save results: %v", err)
	}
	fmt.Printf("Saved %d rows to %s\n", len(comparisonResults), cfg.Results.DBPath)

	// Defragment after DROP+INSERT cycle
	if _, err := results.db.ExecContext(ctx, "VACUUM"); err != nil {
		log.Printf("Warning: VACUUM failed: %v", err)
	} else {
		fmt.Println("Database defragmented (VACUUM)")
	}

	// Print summary
	printSummary(ctx, results, wbDate, onecDate, unmatched, len(sourceData))

	// Optional CSV export
	if *csvExport {
		csvPath := fmt.Sprintf("price-comparison-%s.csv", wbDate)
		count, err := results.ExportCSV(ctx, wbDate, onecDate, csvPath)
		if err != nil {
			log.Printf("Warning: CSV export failed: %v", err)
		} else {
			fmt.Printf("Exported %d rows to %s\n", count, csvPath)
		}
	}

	fmt.Println("================================================================================")
}

func applyDefaults(cfg *Config) {
	if cfg.Comparison.OneCBasePriceType == "" {
		cfg.Comparison.OneCBasePriceType = "Розничная цена ОЭК"
	}
	if cfg.Comparison.SPP25Percent == 0 {
		cfg.Comparison.SPP25Percent = 25
	}
	if cfg.Comparison.SPPUserPercent == 0 {
		cfg.Comparison.SPPUserPercent = cfg.Comparison.SPP25Percent
	}
	if cfg.Comparison.MatchThreshold == 0 {
		cfg.Comparison.MatchThreshold = 50
	}
	if cfg.Comparison.WarningThreshold == 0 {
		cfg.Comparison.WarningThreshold = 500
	}
	if cfg.Comparison.OneCSRPriceType == "" {
		cfg.Comparison.OneCSRPriceType = "Розничная цена СР"
	}
	if cfg.Comparison.OneCSpecialPriceType == "" {
		cfg.Comparison.OneCSpecialPriceType = "Спец цена для акции"
	}
	if cfg.Comparison.SPPDaysBack == 0 {
		cfg.Comparison.SPPDaysBack = 3
	}
	if cfg.Comparison.WBWalletPercent == 0 {
		cfg.Comparison.WBWalletPercent = 3
	}
	if cfg.Comparison.LoyaltyDiscountPercent == 0 {
		cfg.Comparison.LoyaltyDiscountPercent = 10
	}
}

func calculateComparison(s SourceData, wbDate, onecDate string, cfg ComparisonConfig) ComparisonResult {
	diffBase := float64(s.WBPrice) - s.OneCBasePrice

	// 1. onec_spp25_price — legacy, computed from config (backward compatibility)
	onecSPP25Price := s.OneCBasePrice * (1 - cfg.SPP25Percent/100)

	// 2. Effective SPP for WB: WB sales API → user fallback
	var effectiveSPP float64
	var sppType string
	if s.AvgWBSPP3d > 0 {
		effectiveSPP = s.AvgWBSPP3d
		sppType = "wb_api"
	} else {
		effectiveSPP = cfg.SPPUserPercent
		sppType = "user_fallback"
	}

	// 3. ОЭК с СПП — ALWAYS uses user SPP (14-40%), never WB API SPP
	onecPriceWithSPP := s.OneCBasePrice * (1 - cfg.SPPUserPercent/100)

	// 4. РЦ WB с СПП — uses discounted price (NOT base), WB API SPP or user fallback
	wbPriceWithSPP := s.WBDiscountPrice * (1 - effectiveSPP/100)

	// 5. РЦ WB с СПП и кошельком = [РЦ WB с СПП] × (1 − [WB кошелёк %])
	wbPriceWithSPPAndWallet := wbPriceWithSPP * (1 - cfg.WBWalletPercent/100)

	// 6. Цена СР со скидкой лояльности
	var srPriceWithLoyalty float64
	if s.OneCSpecialPrice == 1 {
		// Спец. цена применяется — без скидки лояльности
		srPriceWithLoyalty = s.OneCSRPrice
	} else {
		// Спец. цена НЕ применяется — со скидкой "Яркие вместе"
		srPriceWithLoyalty = s.OneCSRPrice * (1 - cfg.LoyaltyDiscountPercent/100)
	}

	// 7. Fixed deviations: diff_discounted through onecPriceWithSPP
	diffDiscounted := s.WBDiscountPrice - onecPriceWithSPP

	var diffBasePct, diffDiscPct float64
	if s.OneCBasePrice > 0 {
		diffBasePct = (diffBase / s.OneCBasePrice) * 100
	}
	if onecPriceWithSPP > 0 {
		diffDiscPct = (diffDiscounted / onecPriceWithSPP) * 100
	}

	isSpecial := 0
	if s.OneCSpecialPrice == 1 {
		isSpecial = 1
	}

	// 8. Direct customer price comparison: what client pays on WB vs own store
	diffCustomer := s.WBDiscountPrice - s.OneCBasePrice
	var diffCustomerPct float64
	if s.OneCBasePrice > 0 {
		diffCustomerPct = (diffCustomer / s.OneCBasePrice) * 100
	}
	customerStatus := classifyDiff(diffCustomer, s.OneCBasePrice, cfg.MatchThreshold, cfg.WarningThreshold)

	// 9. Отклонение СР от РЦ WB = [Цена СР со скидкой лояльности] - [РЦ WB с СПП и кошельком]
	diffSRVsWBWallet := srPriceWithLoyalty - wbPriceWithSPPAndWallet
	var diffSRVsWBWalletPct float64
	if srPriceWithLoyalty > 0 {
		diffSRVsWBWalletPct = (diffSRVsWBWallet / srPriceWithLoyalty) * 100
	}

	// 10. Отклонение СР от ОЭК с СПП = [Цена СР со скидкой лояльности] - [Цена ОЭК с СПП]
	diffSRVsOneCSPP := srPriceWithLoyalty - onecPriceWithSPP
	var diffSRVsOneCSPPPct float64
	if srPriceWithLoyalty > 0 {
		diffSRVsOneCSPPPct = (diffSRVsOneCSPP / srPriceWithLoyalty) * 100
	}

	// 11. Stock flag
	hasStock := 0
	if s.StockWB+s.StockMP > 0 {
		hasStock = 1
	}

	return ComparisonResult{
		WBSnapshotDate:    wbDate,
		OneCSnapshotDate:  onecDate,
		VendorCode:        s.VendorCode,
		NmID:              s.NmID,
		OneCType:          s.OneCType,
		OneCCategory:      s.OneCCategory,
		OneCCategoryL1:    s.OneCCategoryL1,
		OneCCategoryL2:    s.OneCCategoryL2,
		WBSubjectName:     s.WBSubjectName,
		Season:            s.Season,
		YearCollection:    s.YearCollection,
		Collection:        s.Collection,
		Minicollection:    s.Minicollection,
		Naznacenie:        s.Naznacenie,
		Sex:               s.Sex,
		AgeCategory:       s.AgeCategory,
		OneCBrand:         s.OneCBrand,
		WBBrand:           s.WBBrand,
		OneCName:          s.OneCName,
		WBTitle:           s.WBTitle,
		Color:             s.Color,
		CountryOfOrigin:   s.CountryOfOrigin,
		BrandCountry:      s.BrandCountry,
		IsSale:            s.IsSale,
		IsNew:             s.IsNew,
		ModelStatus:       s.ModelStatus,
		PIMEnabled:        s.PIMEnabled,
		OneCBasePrice:     s.OneCBasePrice,
		OneCSPP25Price:    onecSPP25Price,
		WBPrice:           s.WBPrice,
		WBDiscountedPrice: s.WBDiscountPrice,
		WBDiscountPct:     s.WBDiscountPct,
		WBClubPrice:       s.WBClubPrice,
		WBClubDiscount:    s.WBClubDiscount,
		StockWB:           s.StockWB,
		StockMP:           s.StockMP,
		ProductRating:     s.ProductRating,
		DiffBase:          diffBase,
		DiffDiscounted:    diffDiscounted,
		DiffBasePct:       diffBasePct,
		DiffDiscountedPct: diffDiscPct,
		BaseStatus:        classifyDiff(diffBase, s.OneCBasePrice, cfg.MatchThreshold, cfg.WarningThreshold),
		DiscStatus:        classifyDiff(diffDiscounted, onecPriceWithSPP, cfg.MatchThreshold, cfg.WarningThreshold),
		// Extended fields
		OneCSRPrice:        s.OneCSRPrice,
		OneCSpecialPrice:   s.OneCSpecialPrice,
		IsSpecialPrice:     isSpecial,
		AvgWBSPP3d:         s.AvgWBSPP3d,
		SPPSource:          s.SPPSource,
		AvgWBSPPAssortment: s.AvgWBSPPAssortment,
		// Effective SPP fields
		EffectiveSPP:     effectiveSPP,
		SPPType:          sppType,
		OneCPriceWithSPP: onecPriceWithSPP,
		WBPriceWithSPP:   wbPriceWithSPP,
		// Direct customer price comparison
		DiffCustomer:    diffCustomer,
		DiffCustomerPct: diffCustomerPct,
		CustomerStatus:  customerStatus,
		// New finance director fields
		WBPriceWithSPPAndWallet: wbPriceWithSPPAndWallet,
		SRPriceWithLoyalty:      srPriceWithLoyalty,
		DiffSRVsWBWallet:        diffSRVsWBWallet,
		DiffSRVsWBWalletPct:     diffSRVsWBWalletPct,
		DiffSRVsOneCSPP:         diffSRVsOneCSPP,
		DiffSRVsOneCSPPPct:      diffSRVsOneCSPPPct,
		// Stock flag
		HasStock: hasStock,
	}
}

func classifyDiff(diff, onecPrice, matchThreshold, warningThreshold float64) string {
	if onecPrice == 0 {
		return "no_1c"
	}
	absDiff := math.Abs(diff)
	switch {
	case absDiff <= matchThreshold:
		return "match"
	case diff > warningThreshold:
		return "overpriced"
	case diff < -warningThreshold:
		return "underpriced"
	default:
		return "warning"
	}
}

func printSummary(ctx context.Context, results *ResultsRepo, wbDate, onecDate string, unmatched *UnmatchedCounts, total int) {
	fmt.Println()

	if unmatched != nil {
		fmt.Printf("1C products (no WB match): %d\n", unmatched.OneCWithoutWB)
		fmt.Printf("WB products (no 1C match): %d\n", unmatched.WBWithoutOneC)
	}

	fmt.Println()
	fmt.Println("Base price comparison:")
	baseCounts, err := results.GetStatusCounts(ctx, wbDate, onecDate, "base")
	if err == nil {
		printStatusCounts(baseCounts, total)
	}

	fmt.Println()
	fmt.Println("Discounted price comparison:")
	discCounts, err := results.GetStatusCounts(ctx, wbDate, onecDate, "disc")
	if err == nil {
		printStatusCounts(discCounts, total)
	}

		fmt.Println()
		fmt.Println("Customer price comparison (WB discounted vs 1C retail):")
		custCounts, err := results.GetStatusCounts(ctx, wbDate, onecDate, "customer")
		if err == nil {
			printStatusCounts(custCounts, total)
		}

	// SPP coverage
	fmt.Println()
	fmt.Println("SPP coverage:")
	sppCoverage, err := results.GetSPPCoverage(ctx, wbDate, onecDate)
	if err == nil && len(sppCoverage) > 0 {
		for _, sc := range sppCoverage {
			pct := 0.0
			if total > 0 {
				pct = float64(sc.Count) / float64(total) * 100
			}
			fmt.Printf("  %-15s %6d (%5.1f%%)\n", sc.Source, sc.Count, pct)
		}
		avgSPP, err := results.GetAvgSPPAssortment(ctx, wbDate, onecDate)
		if err == nil && avgSPP > 0 {
			fmt.Printf("  Average WB SPP (assortment): %.1f%%\n", avgSPP)
		}
	}

	// Special price breakdown
	fmt.Println()
	var specialCount int
	results.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM price_comparison
		 WHERE wb_snapshot_date = ? AND onec_snapshot_date = ? AND is_special_price = 1`,
		wbDate, onecDate,
	).Scan(&specialCount)
	if total > 0 {
		fmt.Printf("Special price (loyalty): %d / %d (%.1f%%)\n", specialCount, total, float64(specialCount)/float64(total)*100)
	}

	// Top overpriced
	fmt.Println()
	fmt.Println("Top 10 overpriced (base price):")
	topOver, err := results.GetTopDiff(ctx, wbDate, onecDate, "overpriced", 10)
	if err == nil && len(topOver) > 0 {
		printTopDiffs(topOver)
	}

	// Top underpriced
	fmt.Println()
	fmt.Println("Top 10 underpriced (base price):")
	topUnder, err := results.GetTopDiff(ctx, wbDate, onecDate, "underpriced", 10)
	if err == nil && len(topUnder) > 0 {
		printTopDiffs(topUnder)
	}
}

func printStatusCounts(counts []StatusCount, total int) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, c := range counts {
		pct := 0.0
		if total > 0 {
			pct = float64(c.Count) / float64(total) * 100
		}
		fmt.Fprintf(w, "  %-15s\t%6d\t(%5.1f%%)\n", c.Status, c.Count, pct)
	}
	w.Flush()
}

func printTopDiffs(rows []TopDiffRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", "VendorCode", "1C Name", "1C Price", "WB Price", "Diff", "%")
	for _, r := range rows {
		name := r.OneCName
		if len(name) > 30 {
			name = name[:30] + "..."
		}
		fmt.Fprintf(w, "  %s\t%s\t%10.0f\t%9d\t%+8.0f\t%+6.1f%%\n",
			r.VendorCode, name, r.OneCBasePrice, r.WBPrice, r.DiffBase, r.DiffBasePct)
	}
	w.Flush()
}
