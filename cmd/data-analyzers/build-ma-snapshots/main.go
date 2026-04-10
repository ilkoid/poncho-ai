// Package main provides a utility to build daily moving average snapshots for product sales.
//
// Computes MA-3, MA-7, MA-14, MA-28 per product (nm_id) from sales data in wb-sales.db,
// enriches with 1C/PIM attributes, and stores results in bi.db for PowerBI consumption.
//
// Usage:
//
//	go run .                              # snapshot for yesterday
//	go run . --date 2026-03-27            # specific date
//	go run . --force                      # rebuild even if snapshot exists
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config represents the YAML configuration.
type Config struct {
	Source  SourceConfig `yaml:"source"`
	Results ResultsConfig `yaml:"results"`
	MA      MAConfig     `yaml:"ma"`
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
	force := flag.Bool("force", false, "Rebuild even if snapshot exists for this date")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

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
	fmt.Println("MA Snapshots Builder")
	fmt.Println("========================================================================")
	fmt.Printf("Snapshot date: %s\n", refDate)
	fmt.Printf("Source DB:     %s\n", cfg.Source.DBPath)
	fmt.Printf("Results DB:    %s\n", cfg.Results.DBPath)
	fmt.Printf("Windows:       %v\n", cfg.MA.Windows)
	fmt.Printf("Min days:      %d\n", cfg.MA.MinDays)
	fmt.Println("========================================================================")

	// Open source DB (read-only)
	source, err := NewSourceRepo(cfg.Source.DBPath)
	if err != nil {
		log.Fatalf("Open source: %v", err)
	}
	defer source.Close()

	// Open results DB (read-write, creates if not exists)
	results, err := NewResultsRepo(cfg.Results.DBPath)
	if err != nil {
		log.Fatalf("Open results: %v", err)
	}
	defer results.Close()

	// Check existing
	if !*force {
		exists, err := results.HasSnapshot(ctx, refDate)
		if err != nil {
			log.Fatalf("Check existing: %v", err)
		}
		if exists {
			fmt.Println("Snapshot already exists for this date. Use --force to rebuild.")
			return
		}
	}

	// Step 1: Query daily sales
	fmt.Print("Querying sales data... ")
	daily, err := source.QueryDailySales(ctx, refDate)
	if err != nil {
		log.Fatalf("Query sales: %v", err)
	}
	fmt.Printf("%d products with sales\n", len(daily))

	if len(daily) == 0 {
		fmt.Println("No sales data found for the period. Check source DB and date.")
		return
	}

	// Step 2: Compute MA snapshots
	fmt.Print("Computing MA snapshots... ")
	rows := ComputeMASnapshots(daily, refDate, cfg.MA.Windows, cfg.MA.MinDays)
	fmt.Printf("%d rows\n", len(rows))

	if len(rows) == 0 {
		fmt.Println("No rows to save (all products had zero sales on reference date).")
		return
	}

	// Step 3: Enrich with product attributes
	fmt.Print("Querying product attributes... ")
	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}
	attrs, err := source.QueryProductAttrs(ctx, nmIDs)
	if err != nil {
		log.Fatalf("Query attrs: %v", err)
	}
	fmt.Printf("%d products matched\n", len(attrs))

	// Attach attributes to rows and collect unique attrs for saving
	attrSet := make(map[int]*ProductAttrs)
	for i := range rows {
		if a, ok := attrs[rows[i].NmID]; ok {
			rows[i].Article = a.Article
			rows[i].Identifier = a.Identifier
			rows[i].VendorCode = a.VendorCode
			attrSet[rows[i].NmID] = a
		}
	}

	// Step 4: Save product attributes
	uniqueAttrs := make([]ProductAttrs, 0, len(attrSet))
	for _, a := range attrSet {
		uniqueAttrs = append(uniqueAttrs, *a)
	}
	fmt.Print("Saving product attributes... ")
	savedAttrs, err := results.SaveProductAttrs(ctx, uniqueAttrs)
	if err != nil {
		log.Fatalf("Save attrs: %v", err)
	}
	fmt.Printf("%d rows\n", savedAttrs)

	// Step 5: Save MA snapshots
	fmt.Print("Saving MA snapshots... ")
	saved, err := results.SaveMASnapshots(ctx, rows)
	if err != nil {
		log.Fatalf("Save snapshots: %v", err)
	}
	fmt.Printf("%d rows\n", saved)

	// Summary
	fmt.Println("========================================================================")
	printSummary(rows)
	fmt.Println("========================================================================")
}

func applyDefaults(cfg *Config) {
	if len(cfg.MA.Windows) == 0 {
		cfg.MA.Windows = []int{3, 7, 14, 28}
	}
	if cfg.MA.MinDays == 0 {
		cfg.MA.MinDays = 2
	}
}

func printSummary(rows []MARow) {
	// Count products with data per MA window
	var withMA3, withMA7, withMA14, withMA28 int
	var totalSold int
	topByDelta := make(map[int]*MARow) // window → top positive delta

	for i := range rows {
		totalSold += rows[i].Sold
		if rows[i].MA3 != nil {
			withMA3++
		}
		if rows[i].MA7 != nil {
			withMA7++
		}
		if rows[i].MA14 != nil {
			withMA14++
		}
		if rows[i].MA28 != nil {
			withMA28++
		}

		// Track largest positive delta vs MA-7
		if rows[i].DeltaMA7Pct != nil {
			if t, ok := topByDelta[7]; !ok || *rows[i].DeltaMA7Pct > *t.DeltaMA7Pct {
				topByDelta[7] = &rows[i]
			}
		}
	}

	fmt.Printf("Products:       %d\n", len(rows))
	fmt.Printf("Total sold:     %d\n", totalSold)
	fmt.Printf("With MA-3:      %d\n", withMA3)
	fmt.Printf("With MA-7:      %d\n", withMA7)
	fmt.Printf("With MA-14:     %d\n", withMA14)
	fmt.Printf("With MA-28:     %d\n", withMA28)

	// Top movers vs MA-7
	if top, ok := topByDelta[7]; ok && top.DeltaMA7Pct != nil {
		fmt.Println()
		fmt.Println("Top mover vs MA-7:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "  nm_id:\t%d\n", top.NmID)
		fmt.Fprintf(w, "  article:\t%s\n", top.Article)
		fmt.Fprintf(w, "  sold:\t%d\n", top.Sold)
		if top.MA7 != nil {
			fmt.Fprintf(w, "  MA-7:\t%.1f\n", *top.MA7)
		}
		fmt.Fprintf(w, "  delta:\t%+.1f%%\n", *top.DeltaMA7Pct)
		w.Flush()
	}
}

func printHelp() {
	fmt.Println("MA Snapshots Builder — computes daily moving average snapshots for product sales")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run . [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --config PATH    Path to config file (default: config.yaml)")
	fmt.Println("  --source PATH    Override source DB path (wb-sales.db)")
	fmt.Println("  --db PATH        Override results DB path (bi.db)")
	fmt.Println("  --date DATE      Snapshot date YYYY-MM-DD (default: yesterday)")
	fmt.Println("  --force          Rebuild even if snapshot exists for this date")
	fmt.Println("  --help, -h       Show this help")
}
