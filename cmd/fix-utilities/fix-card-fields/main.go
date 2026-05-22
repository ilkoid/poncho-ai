package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	stage := flag.Bool("stage", false, "Stage 1: collect cards, match rules, write to staging table")
	apply := flag.Bool("apply", false, "Stage 2: send staged changes to WB API")
	dryRun := flag.Bool("dry-run", false, "with --apply: print payloads without sending")
	showDiff := flag.Bool("diff", false, "Show before/after for staged cards")
	check := flag.Bool("check", false, "Query WB error list for card validation errors")
	listChars := flag.Int("list-chars", 0, "Query characteristic dictionary for subject_id")
	dbPathOverride := flag.String("db", "", "Override db_path from config")
	flag.Parse()

	// --check and --list-chars don't need config/DB
	if *check {
		if err := runCheck(); err != nil {
			log.Fatalf("check: %v", err)
		}
		return
	}

	if *listChars > 0 {
		if err := runListChars(*listChars); err != nil {
			log.Fatalf("list-chars: %v", err)
		}
		return
	}

	if !*stage && !*apply && !*showDiff && !*check {
		fmt.Println("Usage: fix-card-fields --config PATH --stage | --apply | --diff | --check")
		fmt.Println()
		fmt.Println("  --config PATH     YAML config (required)")
		fmt.Println("  --stage           Step 1: collect cards, match rules, write staging")
		fmt.Println("  --apply           Step 2: send staged changes via WB API")
		fmt.Println("  --dry-run         with --apply: print payloads without sending")
		fmt.Println("  --diff            Show before/after for staged cards")
		fmt.Println("  --check           Query WB error list for card validation errors")
		fmt.Println("  --list-chars ID   Query WB characteristic dictionary for subject_id")
		fmt.Println("  --db PATH         Override db_path from config")
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *dbPathOverride != "" {
		cfg.DBPath = *dbPathOverride
	}

	ctx, cancel := signalCtx()
	defer cancel()

	db, err := openDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	switch {
	case *stage:
		if err := runStage(ctx, db, cfg); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *showDiff:
		if err := runDiff(ctx, db); err != nil {
			log.Fatalf("diff: %v", err)
		}
	case *apply:
		apiKey := resolveAPIKey()
		if apiKey == "" && !*dryRun {
			log.Fatal("WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY not set")
		}
		client := wb.New(apiKey)
		client.SetRateLimit("cards_content",
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
			cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
		if err := runApply(ctx, db, client, cfg, *dryRun); err != nil {
			log.Fatalf("apply: %v", err)
		}
	}
}

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_cache_size=-65536&_busy_timeout=10000&_foreign_keys=1", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func signalCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func resolveAPIKey() string {
	if k := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); k != "" {
		return k
	}
	return os.Getenv("WB_API_KEY")
}

// runListChars queries the WB characteristic dictionary and prints results.
func runListChars(subjectID int) error {
	apiKey := resolveAPIKey()
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY not set")
	}

	client := wb.New(apiKey)
	client.SetRateLimit("cards_content", 8, 2, 5, 1)

	ctx := context.Background()
	chars, err := client.GetCharacteristics(ctx, wb.CardsBaseURL, 8, 2, subjectID)
	if err != nil {
		return fmt.Errorf("get characteristics: %w", err)
	}

	fmt.Printf("Characteristics for subject_id=%d (%d entries):\n\n", subjectID, len(chars))
	fmt.Printf("%-10s %-6s %-8s %-10s %s\n", "CharcID", "Req", "Type", "MaxCount", "Name")
	fmt.Println(stringsRepeat("-", 60))
	for _, c := range chars {
		req := ""
		if c.Required {
			req = "yes"
		}
		typeStr := "string"
		if c.CharcType == 4 {
			typeStr = "number"
		}
		fmt.Printf("%-10d %-6s %-8s %-10d %s\n", c.CharcID, req, typeStr, c.MaxCount, c.Name)
	}
	return nil
}

func stringsRepeat(s string, n int) string {
	return strings.Repeat(s, n)
}

// runCheck queries the WB error list and prints all card validation errors.
func runCheck() error {
	apiKey := resolveAPIKey()
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY not set")
	}

	client := wb.New(apiKey)
	client.SetRateLimit("cards_content", 8, 2, 5, 1)

	ctx := context.Background()
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL, 8, 2,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		return fmt.Errorf("get card errors: %w", err)
	}

	if len(items) == 0 {
		fmt.Println("No errors found in WB error list.")
		return nil
	}

	totalErrors := 0
	for i, item := range items {
		fmt.Printf("\n── Batch %d (UUID: %s) ──\n", i+1, item.BatchUUID)
		fmt.Printf("  Vendor codes: %v\n", item.VendorCodes)
		for vc, sub := range item.Subjects {
			fmt.Printf("  Subject: %s → %s (id=%d)\n", vc, sub.Name, sub.ID)
		}
		for vc, errs := range item.Errors {
			for _, e := range errs {
				fmt.Printf("  ERROR vendor_code=%s: %s\n", vc, e)
				totalErrors++
			}
		}
	}
	fmt.Printf("\nTotal: %d error batches, %d individual errors\n", len(items), totalErrors)

	// Save full report
	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	allVCs := make(map[string]bool)
	allErrors := make(map[string][]string)
	for _, item := range items {
		for _, vc := range item.VendorCodes {
			allVCs[vc] = true
		}
		for vc, errs := range item.Errors {
			allErrors[vc] = append(allErrors[vc], errs...)
		}
	}
	report := struct {
		Timestamp   string              `json:"timestamp"`
		VendorCodes []string            `json:"vendor_codes"`
		WBErrors    map[string][]string `json:"wb_errors"`
		RawResponse []wb.CardErrorItem  `json:"raw_response"`
	}{
		Timestamp:   time.Now().Format(time.RFC3339),
		RawResponse: items,
		WBErrors:    allErrors,
	}
	for vc := range allVCs {
		report.VendorCodes = append(report.VendorCodes, vc)
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("  WARN: failed to write report: %v", err)
	} else {
		log.Printf("Full report saved: %s", filename)
	}

	if totalErrors > 0 {
		return fmt.Errorf("WB error list contains %d errors", totalErrors)
	}
	return nil
}
