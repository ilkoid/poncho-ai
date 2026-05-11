// Package main provides a utility to build daily SKU-level moving average snapshots
// for stock analysis at regional warehouses.
//
// Computes MA-3, MA-7, MA-14, MA-28 per size (chrt_id) from sales data in wb-sales.db,
// joins with stock data from stocks_daily_warehouses, enriches with product attributes,
// and calculates risk flags (SDR, trend, broken grid).
// Results stored in flat ma_sku_daily + ma_article_daily tables in bi.db for PowerBI consumption.
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

// CLIConfig represents the YAML configuration.
type CLIConfig struct {
	Source  SourceConfig            `yaml:"source"`
	Results ResultsConfig           `yaml:"results"`
	MA      MAConfig                `yaml:"ma"`
	Alerts  config.AlertsConfig     `yaml:"alerts"`
	Filter  config.YearFilterConfig `yaml:"filter"`
	Force   bool                    `yaml:"force"`
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

	// Step 4: Card sizes mapping (barcode → chrt_id)
	fmt.Print("Querying card sizes... ")
	cardSizes, err := source.QueryCardSizes(ctx)
	if err != nil {
		log.Fatalf("Query card sizes: %v", err)
	}

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

	// Step 6a: Enrich with tech_size from card_sizes
	for i := range rows {
		if entry, ok := chrtToEntry[rows[i].ChrtID]; ok {
			rows[i].TechSize = entry.TechSize
		}
	}

	// Step 6b: Enrich with incoming supply data (REGIONAL via warehouse_id → FO)
	fmt.Print("Querying supply incoming (regional)... ")
	supplyIncoming, err := source.QuerySupplyIncoming(ctx)
	if err != nil {
		log.Fatalf("Query supply incoming: %v", err)
	}
	// Build (chrt_id, region) → incoming map via barcode + warehouse_id → FO
	type chrtRegion struct {
		ChrtID     int64
		RegionName string
	}
	chrtRegionIncoming := make(map[chrtRegion]int64)
	var totalBarcodes int
	for barcode, whMap := range supplyIncoming {
		totalBarcodes++
		chrtID, ok := barcodeToChrt[barcode]
		if !ok {
			continue
		}
		for whID, incoming := range whMap {
			fo, foOK := whByID[whID]
			if !foOK {
				continue
			}
			chrtRegionIncoming[chrtRegion{ChrtID: chrtID, RegionName: fo}] += incoming
		}
	}
	// Apply to rows
	var totalIncoming int64
	for i := range rows {
		key := chrtRegion{ChrtID: rows[i].ChrtID, RegionName: rows[i].RegionName}
		if inc, ok := chrtRegionIncoming[key]; ok {
			rows[i].SupplyIncoming = inc
			totalIncoming += inc
		}
	}
	fmt.Printf("%d barcodes, %d (chrt,region) groups, %d total incoming units\n",
		totalBarcodes, len(chrtRegionIncoming), totalIncoming)

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

	// Step 9: Build article-level sales (collapse chrt_id from maData)
	fmt.Print("Building article-level sales... ")
	articleSales := make(map[int]map[string]map[string]int)
	for nmID, chrtMap := range maData {
		for _, regionMap := range chrtMap {
			for region, dayMap := range regionMap {
				if articleSales[nmID] == nil {
					articleSales[nmID] = make(map[string]map[string]int)
				}
				if articleSales[nmID][region] == nil {
					articleSales[nmID][region] = make(map[string]int)
				}
				for d, sold := range dayMap {
					articleSales[nmID][region][d] += sold
				}
			}
		}
	}
	fmt.Printf("%d products, %d region groups\n", len(articleSales), countArticleRegions(articleSales))

	// Step 10: Compute article snapshots
	fmt.Print("Computing article snapshots... ")
	articleRows := ComputeArticleSnapshots(rows, articleSales, refDate, cfg.MA.Windows, cfg.MA.MinDays, alerts)
	fmt.Printf("%d rows\n", len(articleRows))

	// Step 11: Enrich with ratings + feedback counts
	fmt.Print("Enriching articles with ratings... ")
	ratings, err := source.QueryProductRatings(ctx, stockNmIDs)
	if err != nil {
		log.Printf("WARN: query product ratings: %v (skipping)", err)
		ratings = nil
	}
	feedbackCounts, err := source.QueryFeedbackCounts(ctx)
	if err != nil {
		log.Printf("WARN: query feedback counts: %v (skipping)", err)
		feedbackCounts = nil
	}
	var ratedCount int
	for i := range articleRows {
		nmID := articleRows[i].NmID
		if r, ok := ratings[nmID]; ok {
			articleRows[i].ProductRating = r.ProductRating
			articleRows[i].FeedbackRating = r.FeedbackRating
			ratedCount++
		}
		if cnt, ok := feedbackCounts[nmID]; ok {
			articleRows[i].FeedbackCount = cnt
		}
	}
	fmt.Printf("%d rated\n", ratedCount)

	// Step 12: Enrich with prices
	fmt.Print("Enriching articles with prices... ")
	prices, err := source.QueryLatestPrices(ctx, refDate, stockNmIDs)
	if err != nil {
		log.Printf("WARN: query latest prices: %v (skipping)", err)
		prices = nil
	}
	var pricedCount int
	for i := range articleRows {
		if p, ok := prices[articleRows[i].NmID]; ok {
			articleRows[i].Price = p.Price
			articleRows[i].DiscountedPrice = p.DiscountedPrice
			articleRows[i].Discount = p.Discount
			pricedCount++
		}
	}
	fmt.Printf("%d priced\n", pricedCount)

	// Step 13: Enrich with funnel metrics
	fmt.Print("Enriching articles with funnel... ")
	funnel, err := source.QueryLatestFunnel(ctx, stockNmIDs)
	if err != nil {
		log.Printf("WARN: query latest funnel: %v (skipping)", err)
		funnel = nil
	}
	var funnelCount int
	for i := range articleRows {
		if f, ok := funnel[articleRows[i].NmID]; ok {
			articleRows[i].OpenCount = f.OpenCount
			articleRows[i].CartCount = f.CartCount
			articleRows[i].OrderCount = f.OrderCount
			articleRows[i].BuyoutCount = f.BuyoutCount
			articleRows[i].ConversionBuyout = f.ConversionBuyout
			funnelCount++
		}
	}
	fmt.Printf("%d with funnel data\n", funnelCount)

	// Step 14: Enrich with search visibility (table may be empty)
	fmt.Print("Enriching articles with search visibility... ")
	visibility, err := source.QuerySearchVisibility(ctx, refDate, stockNmIDs)
	if err != nil {
		log.Printf("WARN: query search visibility: %v (skipping)", err)
		visibility = nil
	}
	var visCount int
	for i := range articleRows {
		if v, ok := visibility[articleRows[i].NmID]; ok {
			articleRows[i].AvgPosition = v.AvgPosition
			articleRows[i].Visibility = v.Visibility
			visCount++
		}
	}
	fmt.Printf("%d with visibility data\n", visCount)

	// Step 8: Save or dry-run
	if *dryRun {
		printDryRunSummary(rows)
		fmt.Println()
		fmt.Println("------------------------------------------------------------------------")
		printArticleSummary(articleRows)
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
		if err := results.DeleteSnapshot(ctx, refDate); err != nil {
			log.Fatalf("Delete existing: %v", err)
		}
		if err := results.DeleteArticleSnapshot(ctx, refDate); err != nil {
			log.Fatalf("Delete existing article: %v", err)
		}
	}

	// Drop indexes for fast bulk insert
	fmt.Print("Dropping indexes for fast insert... ")
	if err := results.DropIndexes(ctx); err != nil {
		log.Fatalf("Drop indexes: %v", err)
	}
	if err := results.DropArticleIndexes(ctx); err != nil {
		log.Fatalf("Drop article indexes: %v", err)
	}
	fmt.Println("done")

	// Save SKU snapshots
	fmt.Print("Saving SKU snapshots... ")
	saved, err := results.SaveSKUSnapshots(ctx, rows)
	if err != nil {
		log.Fatalf("Save snapshots: %v", err)
	}
	fmt.Printf("%d rows\n", saved)

	fmt.Print("Creating SKU indexes... ")
	if err := results.CreateIndexes(ctx); err != nil {
		log.Fatalf("Create indexes: %v", err)
	}
	fmt.Println("done")

	// Step 15: Save article snapshots
	fmt.Print("Saving article snapshots... ")
	articleSaved, err := results.SaveArticleSnapshots(ctx, articleRows)
	if err != nil {
		log.Fatalf("Save article snapshots: %v", err)
	}
	fmt.Printf("%d rows\n", articleSaved)

	fmt.Print("Creating article indexes... ")
	if err := results.CreateArticleIndexes(ctx); err != nil {
		log.Fatalf("Create article indexes: %v", err)
	}
	fmt.Println("done")

	// Summary
	fmt.Println("========================================================================")
	printSummary(rows)
	fmt.Println("------------------------------------------------------------------------")
	printArticleSummary(articleRows)
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

// countArticleRegions counts total region groups across all products in article sales data.
func countArticleRegions(articleSales map[int]map[string]map[string]int) int {
	var total int
	for _, regions := range articleSales {
		total += len(regions)
	}
	return total
}

// printDryRunSummary outputs risk-ordered summary to console without writing to DB.
func printDryRunSummary(rows []SKURow) {
	// Sort: critical first, then out_of_stock, then risk, then broken_grid, then by SDR asc
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rowPriority(rows[i]), rowPriority(rows[j])
		if ri != rj {
			return ri < rj
		}
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
			continue
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

	fmt.Printf("[SKU] Total rows:     %d\n", len(rows))
	fmt.Printf("[SKU] Total stock:    %d\n", totalStock)
	fmt.Printf("[SKU] Supply incoming:%d\n", totalIncoming)
	fmt.Printf("[SKU] With MA-7:      %d\n", withMA7)
	fmt.Printf("[SKU] Critical:       %d\n", critical)
	fmt.Printf("[SKU] Out of stock:   %d\n", outOfStock)
	fmt.Printf("[SKU] Risk:           %d\n", risk)
	fmt.Printf("[SKU] Broken grid:    %d\n", brokenGrid)

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
		fmt.Println("Top critical SKU items (lowest SDR):")
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

// printArticleSummary outputs article-level aggregation statistics.
func printArticleSummary(rows []ArticleRow) {
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

	fmt.Printf("[Article] Total rows:     %d\n", len(rows))
	fmt.Printf("[Article] Total stock:    %d\n", totalStock)
	fmt.Printf("[Article] Supply incoming:%d\n", totalIncoming)
	fmt.Printf("[Article] With MA-7:      %d\n", withMA7)
	fmt.Printf("[Article] Critical:       %d\n", critical)
	fmt.Printf("[Article] Out of stock:   %d\n", outOfStock)
	fmt.Printf("[Article] Risk:           %d\n", risk)
	fmt.Printf("[Article] Broken grid:    %d\n", brokenGrid)

	// Top 5 critical articles by SDR
	criticalRows := make([]ArticleRow, 0)
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
		fmt.Println("Top critical articles (lowest SDR):")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "  nm_id\tarticle\tbrand\tregion\tstock\tMA-7\tSDR\n")
		for i, r := range criticalRows {
			if i >= 5 {
				break
			}
			fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%d\t%.1f\t%.1f\n",
				r.NmID, r.Article, r.Brand,
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
