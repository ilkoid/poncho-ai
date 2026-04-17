// Package main provides a utility to build daily SKU-level moving average snapshots
// for stock analysis at regional warehouses.
//
// Computes MA-3, MA-7, MA-14, MA-28 per size (chrt_id) from sales data in wb-sales.db,
// joins with stock data from stocks_daily_warehouses, enriches with product attributes,
// and calculates risk flags (SDR, trend, broken grid).
// Results stored in flat ma_sku_daily table in bi.db for PowerBI consumption.
//
// Usage:
//
//	go run .                              # snapshot for yesterday
//	go run . --date 2026-04-15            # specific date
//	go run . --dry-run                    # summary to console, no DB write
//	go run . --force                      # rebuild even if snapshot exists
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// CLI Config represents the YAML configuration.
type CLIConfig struct {
	Source  SourceConfig         `yaml:"source"`
	Results ResultsConfig        `yaml:"results"`
	MA      MAConfig             `yaml:"ma"`
	Alerts  config.AlertsConfig  `yaml:"alerts"`
	Filter  config.YearFilterConfig `yaml:"filter"`
	Force   bool                 `yaml:"force"`
}

type SourceConfig struct {
	DBPath string `yaml:"db_path"`
}

type ResultsConfig struct {
	DBPath string `yaml:"db_path"`
}

type MAConfig struct {
	Windows []int `yaml:"windows"`
	MinDays int   `yaml:"min_days"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	sourcePath := flag.String("source", "", "Override source DB path (wb-sales.db)")
	outputPath := flag.String("db", "", "Override results DB path (bi.db)")
	dateFlag := flag.String("date", "", "Snapshot date YYYY-MM-DD (default: yesterday)")
	dryRun := flag.Bool("dry-run", false, "Compute and show summary, do not write to DB")
	force := flag.Bool("force", false, "Rebuild even if snapshot exists for this date")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// Load config
	var cfg CLIConfig
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
	if *force {
		cfg.Force = true
	}

	// Resolve reference date
	refDate := *dateFlag
	if refDate == "" {
		refDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
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

	// Header
	fmt.Println("========================================================================")
	fmt.Println("MA SKU Snapshots Builder")
	fmt.Println("========================================================================")
	fmt.Printf("Snapshot date: %s\n", refDate)
	fmt.Printf("Source DB:     %s\n", cfg.Source.DBPath)
	fmt.Printf("Results DB:    %s\n", cfg.Results.DBPath)
	fmt.Printf("Windows:       %v\n", cfg.MA.Windows)
	fmt.Printf("Min days:      %d\n", cfg.MA.MinDays)
	fmt.Printf("Alerts:        threshold=%d, reorder=%dd, critical=%dd\n",
		cfg.Alerts.ZeroStockThreshold, cfg.Alerts.ReorderWindow, cfg.Alerts.CriticalDays)
	fmt.Printf("Year filter:   %v\n", cfg.Filter.AllowedYears)
	fmt.Printf("Dry-run:       %v\n", *dryRun)
	fmt.Println("========================================================================")

	// Open source DB (read-only)
	source, err := NewSourceRepo(cfg.Source.DBPath)
	if err != nil {
		log.Fatalf("Open source: %v", err)
	}
	defer source.Close()

	// Step 0: Year filter — get allowed nm_ids
	var filteredNmIDs []int // empty = no filter
	if len(cfg.Filter.AllowedYears) > 0 {
		fmt.Print("Filtering by year... ")
		// Get all vendor codes first, then filter
		entries, err := source.QueryVendorCodes(ctx, nil)
		if err != nil {
			log.Fatalf("Query vendor codes: %v", err)
		}
		filteredNmIDs = config.FilterNmIDsByYear(entries, cfg.Filter.AllowedYears)
		fmt.Printf("%d products match years %v\n", len(filteredNmIDs), cfg.Filter.AllowedYears)
	}

	// Step 1: Build warehouse → FO mapping (from address parsing)
	fmt.Print("Querying warehouse FO map... ")
	whByID, whByName, err := source.QueryWarehouseFOMaps(ctx, refDate)
	if err != nil {
		log.Fatalf("Query warehouse FO map: %v", err)
	}
	fmt.Printf("%d by ID, %d by name\n", len(whByID), len(whByName))

	// Step 2: Query stock positions (grouped by FO)
	fmt.Print("Querying stock positions... ")
	stocks, err := source.QueryStockPositions(ctx, refDate, filteredNmIDs, whByID)
	if err != nil {
		log.Fatalf("Query stocks: %v", err)
	}
	fmt.Printf("%d positions\n", len(stocks))

	if len(stocks) == 0 {
		fmt.Println("No stock data found for this date. Check source DB and date.")
		return
	}

	// Collect unique nm_ids from stock data
	stockNmIDs := collectNmIDs(stocks)

	// Step 3: Size info (total sizes per nm_id, sizes in stock per FO)
	fmt.Print("Querying size info... ")
	totalSizes, err := source.QueryTotalSizes(ctx, filteredNmIDs)
	if err != nil {
		log.Fatalf("Query total sizes: %v", err)
	}
	sizesInStock, err := source.QuerySizesInStock(ctx, refDate, cfg.Alerts.ZeroStockThreshold, filteredNmIDs, whByID)
	if err != nil {
		log.Fatalf("Query sizes in stock: %v", err)
	}
	fmt.Printf("%d products, %d FO groups\n", len(totalSizes), len(sizesInStock))

	// Combine into SizeInfo map
	sizeInfoMap := make(map[SizeRegionKey]SizeInfo)
	for sk, count := range sizesInStock {
		sizeInfoMap[sk] = SizeInfo{
			TotalSizes:   totalSizes[sk.NmID],
			SizesInStock: count,
		}
	}
	// Fill missing regions (products with no sizes above threshold)
	// Build nm_id → set of regions from stocks for O(N+M) lookup.
	nmRegions := make(map[int]map[string]bool)
	for key := range stocks {
		if nmRegions[key.NmID] == nil {
			nmRegions[key.NmID] = make(map[string]bool)
		}
		nmRegions[key.NmID][key.RegionName] = true
	}
	for nmID, total := range totalSizes {
		for region := range nmRegions[nmID] {
			sk := SizeRegionKey{NmID: nmID, RegionName: region}
			if _, ok := sizeInfoMap[sk]; !ok {
				sizeInfoMap[sk] = SizeInfo{TotalSizes: total, SizesInStock: 0}
			}
		}
	}

	// Step 3: Card sizes mapping (barcode → chrt_id)
	fmt.Print("Querying card sizes... ")
	cardSizes, err := source.QueryCardSizes(ctx)
	if err != nil {
		log.Fatalf("Query card sizes: %v", err)
	}

	// Build maps: chrt_id → CardSizeEntry, barcode → chrt_id (all barcodes per entry)
	chrtToEntry := make(map[int64]CardSizeEntry, len(cardSizes))
	barcodeToChrt := make(map[string]int64, len(cardSizes))
	for _, cs := range cardSizes {
		chrtToEntry[cs.ChrtID] = cs
		for _, bc := range cs.Barcodes {
			barcodeToChrt[bc] = cs.ChrtID
		}
	}
	fmt.Printf("%d mappings (barcode → chrt_id)\n", len(barcodeToChrt))

	// Step 5: Daily sales by FO for MA
	fmt.Print("Querying daily sales by region... ")
	maData, err := source.QueryDailySalesByRegion(ctx, refDate, stockNmIDs, whByName, barcodeToChrt)
	if err != nil {
		log.Fatalf("Query sales: %v", err)
	}
	fmt.Printf("%d products with regional sales data\n", len(maData))

	// Step 6: Compute SKU snapshots (regional MA, no fallback)
	fmt.Print("Computing SKU snapshots... ")
	alerts := AlertsParams{
		ZeroStockThreshold: cfg.Alerts.ZeroStockThreshold,
		ReorderWindow:      cfg.Alerts.ReorderWindow,
		CriticalDays:       cfg.Alerts.CriticalDays,
	}
	rows := ComputeSKUSnapshots(stocks, maData, sizeInfoMap, refDate, cfg.MA.Windows, cfg.MA.MinDays, alerts)
	fmt.Printf("%d rows\n", len(rows))

	if len(rows) == 0 {
		fmt.Println("No rows computed.")
		return
	}

	// Step 6: Enrich with tech_size from card_sizes
	for i := range rows {
		if entry, ok := chrtToEntry[rows[i].ChrtID]; ok {
			rows[i].TechSize = entry.TechSize
		}
	}

	// Step 6b: Enrich with incoming supply data
	fmt.Print("Querying supply incoming... ")
	supplyIncoming, err := source.QuerySupplyIncoming(ctx)
	if err != nil {
		log.Fatalf("Query supply incoming: %v", err)
	}
	// Build chrt_id → incoming map via barcode lookup
	chrtIncoming := make(map[int64]int64)
	for barcode, incoming := range supplyIncoming {
		if chrtID, ok := barcodeToChrt[barcode]; ok {
			chrtIncoming[chrtID] += incoming
		}
	}
	// Apply to rows
	var totalIncoming int64
	for i := range rows {
		if inc, ok := chrtIncoming[rows[i].ChrtID]; ok {
			rows[i].SupplyIncoming = inc
			totalIncoming += inc
		}
	}
	fmt.Printf("%d barcodes, %d chrt_ids, %d total incoming units\n",
		len(supplyIncoming), len(chrtIncoming), totalIncoming)

	// Step 7: Enrich with product attributes
	fmt.Print("Querying product attributes... ")
	attrs, err := source.QueryProductAttrs(ctx, stockNmIDs)
	if err != nil {
		log.Fatalf("Query attrs: %v", err)
	}
	fmt.Printf("%d products matched\n", len(attrs))

	for i := range rows {
		if a, ok := attrs[rows[i].NmID]; ok {
			rows[i].Article = a.Article
			rows[i].Identifier = a.Identifier
			rows[i].VendorCode = a.VendorCode
			rows[i].Name = a.Name
			rows[i].Brand = a.Brand
			rows[i].Type = a.Type
			rows[i].Category = a.Category
			rows[i].CategoryLevel1 = a.CategoryLevel1
			rows[i].CategoryLevel2 = a.CategoryLevel2
			rows[i].Sex = a.Sex
			rows[i].Season = a.Season
			rows[i].Color = a.Color
			rows[i].Collection = a.Collection
		}
	}

	// Step 8: Save or dry-run
	if *dryRun {
			printDryRunSummary(rows)
		return
	}

	// Open results DB (read-write)
	results, err := NewResultsRepo(cfg.Results.DBPath)
	if err != nil {
		log.Fatalf("Open results: %v", err)
	}
	defer results.Close()

	// Check existing
	if !cfg.Force {
		exists, err := results.HasSnapshot(ctx, refDate)
		if err != nil {
			log.Fatalf("Check existing: %v", err)
		}
		if exists {
			fmt.Println("Snapshot already exists for this date. Use --force to rebuild.")
			return
		}
	} else {
		// Delete existing data for this date
		if err := results.DeleteSnapshot(ctx, refDate); err != nil {
			log.Fatalf("Delete existing: %v", err)
		}
	}

	// Drop indexes for fast bulk insert, then recreate after
	fmt.Print("Dropping indexes for fast insert... ")
	if err := results.DropIndexes(ctx); err != nil {
		log.Fatalf("Drop indexes: %v", err)
	}
	fmt.Println("done")

	fmt.Print("Saving SKU snapshots... ")
	saved, err := results.SaveSKUSnapshots(ctx, rows)
	if err != nil {
		log.Fatalf("Save snapshots: %v", err)
	}
	fmt.Printf("%d rows\n", saved)

	fmt.Print("Creating indexes... ")
	if err := results.CreateIndexes(ctx); err != nil {
		log.Fatalf("Create indexes: %v", err)
	}
	fmt.Println("done")

	// Summary
	fmt.Println("========================================================================")
	printSummary(rows)
	fmt.Println("========================================================================")
}

func applyDefaults(cfg *CLIConfig) {
	if len(cfg.MA.Windows) == 0 {
		cfg.MA.Windows = []int{3, 7, 14, 28}
	}
	if cfg.MA.MinDays == 0 {
		cfg.MA.MinDays = 1
	}
	cfg.Alerts = cfg.Alerts.GetDefaults()
}

// collectNmIDs extracts unique nm_ids from stock data.
func collectNmIDs(stocks map[StockKey]StockInfo) []int {
	seen := make(map[int]bool)
	var result []int
	for k := range stocks {
		if !seen[k.NmID] {
			seen[k.NmID] = true
			result = append(result, k.NmID)
		}
	}
	return result
}

// printDryRunSummary outputs risk-ordered summary to console without writing to DB.
func printDryRunSummary(rows []SKURow) {
	// Sort: critical first, then out_of_stock, then risk, then broken_grid, then by SDR asc
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rowPriority(rows[i]), rowPriority(rows[j])
		if ri != rj {
			return ri < rj
		}
		// Within same priority, sort by SDR ascending
		sdrI, sdrJ := safeSDR(rows[i].SDRDays), safeSDR(rows[j].SDRDays)
		return sdrI < sdrJ
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "nm_id\tchrt_id\tarticle\tbrand\tsize\tregion\tstock\tsupply\tMA-7\tSDR\tstatus\n")
	fmt.Fprintf(w, "─────\t───────\t───────\t─────\t────\t──────\t─────\t───────\t────\t───\t──────\n")

	printed := 0
	const maxRows = 100

	for _, r := range rows {
		status := rowStatus(r)
		if status == "" {
			continue // skip non-risky rows
		}

		sdr := "-"
		if r.SDRDays != nil {
			sdr = fmt.Sprintf("%.1f", *r.SDRDays)
		}
		ma7 := "-"
		if r.MA7 != nil {
			ma7 = fmt.Sprintf("%.1f", *r.MA7)
		}

		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			r.NmID, r.ChrtID, r.Article, r.Brand, r.TechSize,
			truncate(r.RegionName, 20), r.StockQty, r.SupplyIncoming, ma7, sdr, status)
		printed++
		if printed >= maxRows {
			break
		}
	}
	w.Flush()

	if printed >= maxRows {
		fmt.Printf("... (showing top %d of %d total flagged rows)\n", maxRows, countFlagged(rows))
	}

	fmt.Println()
	printSummary(rows)
}

// rowPriority returns sort priority (lower = more important).
func rowPriority(r SKURow) int {
	if r.Critical {
		return 0
	}
	if r.OutOfStock {
		return 1
	}
	if r.Risk {
		return 2
	}
	if r.BrokenGrid {
		return 3
	}
	return 4
}

// rowStatus returns a human-readable risk status.
func rowStatus(r SKURow) string {
	if r.Critical {
		return "CRITICAL"
	}
	if r.OutOfStock {
		return "OUT_OF_STOCK"
	}
	if r.Risk {
		return "RISK"
	}
	if r.BrokenGrid {
		return "BROKEN_GRID"
	}
	return ""
}

func safeSDR(sdr *float64) float64 {
	if sdr == nil {
		return 9999
	}
	return *sdr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "~"
}

func countFlagged(rows []SKURow) int {
	count := 0
	for _, r := range rows {
		if r.Critical || r.OutOfStock || r.Risk || r.BrokenGrid {
			count++
		}
	}
	return count
}

func printSummary(rows []SKURow) {
	var withMA7, critical, risk, outOfStock, brokenGrid int
	var totalStock, totalIncoming int64

	for _, r := range rows {
		totalStock += r.StockQty
		totalIncoming += r.SupplyIncoming
		if r.MA7 != nil {
			withMA7++
		}
		if r.Critical {
			critical++
		}
		if r.Risk {
			risk++
		}
		if r.OutOfStock {
			outOfStock++
		}
		if r.BrokenGrid {
			brokenGrid++
		}
	}

	fmt.Printf("Total rows:     %d\n", len(rows))
	fmt.Printf("Total stock:    %d\n", totalStock)
	fmt.Printf("Supply incoming:%d\n", totalIncoming)
	fmt.Printf("With MA-7:      %d\n", withMA7)
	fmt.Printf("Critical:       %d\n", critical)
	fmt.Printf("Out of stock:   %d\n", outOfStock)
	fmt.Printf("Risk:           %d\n", risk)
	fmt.Printf("Broken grid:    %d\n", brokenGrid)

	// Top 5 critical items by SDR
	criticalRows := make([]SKURow, 0)
	for _, r := range rows {
		if r.Critical && r.SDRDays != nil {
			criticalRows = append(criticalRows, r)
		}
	}
	if len(criticalRows) > 0 {
		sort.Slice(criticalRows, func(i, j int) bool {
			return *criticalRows[i].SDRDays < *criticalRows[j].SDRDays
		})

		fmt.Println()
		fmt.Println("Top critical items (lowest SDR):")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "  nm_id\tarticle\tbrand\tsize\tregion\tstock\tMA-7\tSDR\n")
		for i, r := range criticalRows {
			if i >= 5 {
				break
			}
			fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\t%d\t%.1f\t%.1f\n",
				r.NmID, r.Article, r.Brand, r.TechSize,
				truncate(r.RegionName, 20), r.StockQty, *r.MA7, *r.SDRDays)
		}
		w.Flush()
	}
}

func printHelp() {
	fmt.Println("MA SKU Snapshots Builder — stock analysis with moving averages per size")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run . [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --config PATH    Path to config file (default: config.yaml)")
	fmt.Println("  --source PATH    Override source DB path (wb-sales.db)")
	fmt.Println("  --db PATH        Override results DB path (bi.db)")
	fmt.Println("  --date DATE      Snapshot date YYYY-MM-DD (default: yesterday)")
	fmt.Println("  --dry-run        Compute and show summary, do not write to DB")
	fmt.Println("  --force          Rebuild even if snapshot exists for this date")
	fmt.Println("  --help, -h       Show this help")
}
