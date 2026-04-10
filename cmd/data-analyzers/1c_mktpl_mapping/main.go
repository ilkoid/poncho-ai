// Package main provides a utility to map products between 1C, PIM, and marketplace systems.
//
// Builds three mapping tables in bi.db:
//   - barcode_mapping: 1C SKU barcode → WB nm_id, vendor_code
//   - pim_product_mapping: PIM article → 1C + WB identifiers
//   - nm_product_mapping: WB nm_id → 1C article, PIM article, vendor_code
//
// Usage:
//
//	go run .                          # build all three mappings
//	go run . --csv                    # export to CSV after build
//	go run . --source /path/to/db     # override source DB path
//	go run . --output /path/to/db     # override results DB path
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config represents the YAML configuration.
type Config struct {
	Source  SourceConfig  `yaml:"source"`
	Results ResultsConfig `yaml:"results"`
	Mapping MappingConfig `yaml:"mapping"`
}

type SourceConfig struct {
	DBPath string `yaml:"db_path"`
}

type ResultsConfig struct {
	DBPath string `yaml:"db_path"`
}

type MappingConfig struct {
	BatchSize int `yaml:"batch_size"`
}

func main() {
	startTime := time.Now()

	configPath := flag.String("config", "config.yaml", "Path to config file")
	sourcePath := flag.String("source", "", "Override source DB path (wb-sales.db)")
	outputPath := flag.String("output", "", "Override results DB path (bi.db)")
	csvExport := flag.Bool("csv", false, "Export to CSV after build")
	verbose := flag.Bool("verbose", false, "Show detailed progress")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	var cfg Config
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		log.Fatalf("❌ Load config: %v", err)
	}
	applyDefaults(&cfg)

	if *sourcePath != "" {
		cfg.Source.DBPath = *sourcePath
	}
	if *outputPath != "" {
		cfg.Results.DBPath = *outputPath
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted!")
		cancel()
	}()

	fmt.Println("================================================================================")
	fmt.Println("1C ↔ Marketplace Mapping Utility")
	fmt.Println("================================================================================")
	fmt.Printf("Source DB:  %s\n", cfg.Source.DBPath)
	fmt.Printf("Results DB: %s\n", cfg.Results.DBPath)
	fmt.Println("================================================================================")
	fmt.Println()

	// Open source DB (read-only)
	source, err := NewSourceRepo(cfg.Source.DBPath)
	if err != nil {
		log.Fatalf("❌ Open source: %v", err)
	}
	defer source.Close()

	// Open results DB (drops and recreates tables)
	if *verbose {
		fmt.Println("💾 Opening results database...")
	}
	results, err := NewResultsRepo(cfg.Results.DBPath)
	if err != nil {
		log.Fatalf("❌ Open results: %v", err)
	}
	defer results.Close()

	// --- Step 1: Barcode mapping ---
	fmt.Println("📦 Step 1: Barcode mapping")
	fmt.Println("─────────────────────────────")

	barcodes, err := source.GetBarcodeMappings(ctx)
	if err != nil {
		log.Fatalf("❌ Build barcode mapping: %v", err)
	}
	fmt.Printf("  %d barcodes mapped\n", len(barcodes))

	if *verbose {
		fmt.Println("  💾 Saving barcode mapping...")
	}
	if err := results.SaveBarcodeMappings(ctx, barcodes, cfg.Mapping.BatchSize); err != nil {
		log.Fatalf("❌ Save barcode mapping: %v", err)
	}

	bcStats, _ := results.GetBarcodeStats(ctx)
	if bcStats != nil {
		fmt.Printf("  Mapped to nm_id: %d/%d (%.1f%%)\n",
			bcStats.MappedToNmID, bcStats.TotalBarcodes, pct(bcStats.MappedToNmID, bcStats.TotalBarcodes))
		fmt.Printf("  Has WB sales:    %d/%d (%.1f%%)\n",
			bcStats.HasWBSales, bcStats.TotalBarcodes, pct(bcStats.HasWBSales, bcStats.TotalBarcodes))
	}

	// --- Step 2: PIM product mapping ---
	fmt.Println()
	fmt.Println("🏷️  Step 2: PIM product mapping")
	fmt.Println("─────────────────────────────")

	pimProducts, err := source.GetPimProductMappings(ctx)
	if err != nil {
		log.Fatalf("❌ Build PIM product mapping: %v", err)
	}
	fmt.Printf("  %d products mapped\n", len(pimProducts))

	if *verbose {
		fmt.Println("  💾 Saving PIM product mapping...")
	}
	if err := results.SavePimProductMappings(ctx, pimProducts, cfg.Mapping.BatchSize); err != nil {
		log.Fatalf("❌ Save PIM product mapping: %v", err)
	}

	pimStats, _ := results.GetPimStats(ctx)
	if pimStats != nil {
		fmt.Printf("  Matched to 1C: %d/%d (%.1f%%)\n",
			pimStats.MatchedTo1C, pimStats.TotalProducts, pct(pimStats.MatchedTo1C, pimStats.TotalProducts))
		fmt.Printf("  Matched to WB: %d/%d (%.1f%%)\n",
			pimStats.MatchedToWB, pimStats.TotalProducts, pct(pimStats.MatchedToWB, pimStats.TotalProducts))
		fmt.Printf("  Has WB sales:  %d/%d (%.1f%%)\n",
			pimStats.HasWBSales, pimStats.TotalProducts, pct(pimStats.HasWBSales, pimStats.TotalProducts))
		}

	// --- Step 3: NM → 1C/PIM mapping ---
	fmt.Println()
	fmt.Println("🏷️  Step 3: NM → 1C/PIM mapping")
	fmt.Println("─────────────────────────────")

	nmProducts, err := source.GetNmProductMappings(ctx)
	if err != nil {
		log.Fatalf("❌ Build NM product mapping: %v", err)
	}
	fmt.Printf("  %d products mapped\n", len(nmProducts))

	if *verbose {
		fmt.Println("  💾 Saving NM product mapping...")
	}
	if err := results.SaveNmProductMappings(ctx, nmProducts, cfg.Mapping.BatchSize); err != nil {
		log.Fatalf("❌ Save NM product mapping: %v", err)
	}

	nmStats, _ := results.GetNmStats(ctx)
	if nmStats != nil {
		fmt.Printf("  Mapped to 1C:  %d/%d (%.1f%%)\n",
			nmStats.MatchedTo1C, nmStats.TotalProducts, pct(nmStats.MatchedTo1C, nmStats.TotalProducts))
		fmt.Printf("  Mapped to PIM: %d/%d (%.1f%%)\n",
			nmStats.MatchedToPIM, nmStats.TotalProducts, pct(nmStats.MatchedToPIM, nmStats.TotalProducts))
	}

	// --- Summary ---
	elapsed := time.Since(startTime)

	fmt.Println()
	fmt.Println("================================================================================")
	fmt.Println("Summary")
	fmt.Println("================================================================================")
	if bcStats != nil {
		fmt.Printf("Barcodes:       %d total, %d mapped (%.1f%%), %d with sales (%.1f%%)\n",
			bcStats.TotalBarcodes, bcStats.MappedToNmID,
			pct(bcStats.MappedToNmID, bcStats.TotalBarcodes),
			bcStats.HasWBSales, pct(bcStats.HasWBSales, bcStats.TotalBarcodes))
	}
	if pimStats != nil {
		fmt.Printf("PIM products:   %d total, %d→1C (%.1f%%), %d→WB (%.1f%%), %d with sales (%.1f%%)\n",
			pimStats.TotalProducts,
			pimStats.MatchedTo1C, pct(pimStats.MatchedTo1C, pimStats.TotalProducts),
			pimStats.MatchedToWB, pct(pimStats.MatchedToWB, pimStats.TotalProducts),
			pimStats.HasWBSales, pct(pimStats.HasWBSales, pimStats.TotalProducts))
	}
	if nmStats != nil {
		fmt.Printf("NM products:    %d total, %d→1C (%.1f%%), %d→PIM (%.1f%%)\n",
			nmStats.TotalProducts,
			nmStats.MatchedTo1C, pct(nmStats.MatchedTo1C, nmStats.TotalProducts),
			nmStats.MatchedToPIM, pct(nmStats.MatchedToPIM, nmStats.TotalProducts))
	}
	fmt.Printf("Time elapsed:   %s\n", elapsed.Round(time.Millisecond))
	fmt.Println("================================================================================")

	// Optional CSV export
	if *csvExport {
		ts := time.Now().Format("20060102-150405")

		bcCSV := fmt.Sprintf("barcode-mapping-%s.csv", ts)
		bcHeader := []string{"barcode", "nm_id", "vendor_code", "article", "guid", "pim_nm_id", "has_wb_sales"}
		bcQuery := `SELECT barcode, nm_id, vendor_code, article, guid, pim_nm_id, has_wb_sales FROM barcode_mapping ORDER BY article`
		count, err := results.ExportCSV(ctx, bcCSV, bcQuery, bcHeader, scanBarcodeCSV)
		if err != nil {
			log.Printf("⚠️  Barcode CSV export failed: %v", err)
		} else {
			fmt.Printf("\n📄 Exported %d barcodes to %s\n", count, bcCSV)
		}

		pimCSV := fmt.Sprintf("pim-product-mapping-%s.csv", ts)
		pimHeader := []string{"pim_article", "article", "guid", "nm_id", "vendor_code", "pim_nm_id", "enabled", "barcode_count", "has_wb_sales"}
		pimQuery := `SELECT pim_article, article, guid, nm_id, vendor_code, pim_nm_id, enabled, barcode_count, has_wb_sales FROM pim_product_mapping ORDER BY pim_article`
		count, err = results.ExportCSV(ctx, pimCSV, pimQuery, pimHeader, scanPimCSV)
		if err != nil {
			log.Printf("⚠️  PIM CSV export failed: %v", err)
		} else {
			fmt.Printf("📄 Exported %d products to %s\n", count, pimCSV)
		}

		nmCSV := fmt.Sprintf("nm-product-mapping-%s.csv", ts)
		nmHeader := []string{"nm_id", "article", "pim_article", "vendor_code", "enabled"}
		nmQuery := `SELECT nm_id, article, pim_article, vendor_code, enabled FROM nm_product_mapping ORDER BY nm_id`
		count, err = results.ExportCSV(ctx, nmCSV, nmQuery, nmHeader, scanNmCSV)
		if err != nil {
			log.Printf("⚠️  NM CSV export failed: %v", err)
		} else {
			fmt.Printf("📄 Exported %d NM products to %s\n", count, nmCSV)
		}
	}

	fmt.Println("\n✅ Done!")
}

func scanBarcodeCSV(w *csv.Writer, rows *sql.Rows) (int, error) {
	count := 0
	for rows.Next() {
		var barcode, article, guid string
		var nmID, pimNmID sql.NullInt64
		var vendorCode sql.NullString
		var hasSales int
		if err := rows.Scan(&barcode, &nmID, &vendorCode, &article, &guid, &pimNmID, &hasSales); err != nil {
			return count, err
		}
		w.Write([]string{
			barcode,
			nullIntStr(nmID),
			nullStringOrEmpty(vendorCode),
			article, guid,
			nullIntStr(pimNmID),
			fmt.Sprintf("%d", hasSales),
		})
		count++
	}
	return count, nil
}

func scanPimCSV(w *csv.Writer, rows *sql.Rows) (int, error) {
	count := 0
	for rows.Next() {
		var pimArticle string
		var article, guid, vendorCode sql.NullString
		var nmID, pimNmID sql.NullInt64
		var enabled, barcodeCount, hasSales int
		if err := rows.Scan(&pimArticle, &article, &guid, &nmID, &vendorCode, &pimNmID, &enabled, &barcodeCount, &hasSales); err != nil {
			return count, err
		}
		w.Write([]string{
			pimArticle,
			nullStringOrEmpty(article),
			nullStringOrEmpty(guid),
			nullIntStr(nmID),
			nullStringOrEmpty(vendorCode),
			nullIntStr(pimNmID),
			fmt.Sprintf("%d", enabled),
			fmt.Sprintf("%d", barcodeCount),
			fmt.Sprintf("%d", hasSales),
		})
		count++
	}
	return count, nil
}

func scanNmCSV(w *csv.Writer, rows *sql.Rows) (int, error) {
	count := 0
	for rows.Next() {
		var nmID int64
		var article, pimArticle, vendorCode sql.NullString
		var enabled int
		if err := rows.Scan(&nmID, &article, &pimArticle, &vendorCode, &enabled); err != nil {
			return count, err
		}
		w.Write([]string{
			fmt.Sprintf("%d", nmID),
			nullStringOrEmpty(article),
			nullStringOrEmpty(pimArticle),
			nullStringOrEmpty(vendorCode),
			fmt.Sprintf("%d", enabled),
		})
		count++
	}
	return count, nil
}

func nullIntStr(n sql.NullInt64) string {
	if n.Valid {
		return fmt.Sprintf("%d", n.Int64)
	}
	return "NULL"
}

func applyDefaults(cfg *Config) {
	if cfg.Source.DBPath == "" {
		cfg.Source.DBPath = "db/wb-sales.db"
	}
	if cfg.Results.DBPath == "" {
		cfg.Results.DBPath = "db/bi.db"
	}
	if cfg.Mapping.BatchSize == 0 {
		cfg.Mapping.BatchSize = 500
	}
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func printHelp() {
	fmt.Println("1C ↔ Marketplace Mapping Utility")
	fmt.Println()
	fmt.Println("Builds barcode_mapping, pim_product_mapping, and nm_product_mapping tables in bi.db.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run . [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -config string   Path to config file (default \"config.yaml\")")
	fmt.Println("  -source string   Override source DB path (wb-sales.db)")
	fmt.Println("  -output string   Override results DB path (bi.db)")
	fmt.Println("  -csv             Export all tables to CSV after build")
	fmt.Println("  -verbose         Show detailed progress")
	fmt.Println("  -h, -help        Show this help")
}
