package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	stage := flag.Bool("stage", false, "stage 1: collect data from 1C + cards, write to staging table")
	fixType := flag.Bool("fix-type", false, "fix cert/decl type mismatch (same number, wrong char_id)")
	reconcile := flag.Bool("reconcile", false, "find and correct any cert/decl discrepancies between WB and 1C")
	diff := flag.Bool("diff", false, "show before→after diff for staged cards")
	apply := flag.Bool("apply", false, "stage 2: send updates to WB API")
	dryRun := flag.Bool("dry-run", false, "show payloads without sending (use with --apply)")
	check := flag.Bool("check", false, "query WB error list for recent card validation errors")
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	dbPath := flag.String("db", "", "path to SQLite database (overrides config)")
	refDate := flag.String("date", "", "reference date for expiry check (DD.MM.YYYY or YYYY-MM-DD, default: today). Expired certificates are skipped.")
	flag.Parse()

	if !*stage && !*fixType && !*reconcile && !*diff && !*apply && !*check {
		fmt.Println("fix-certificates — fill certificate fields on WB cards from 1C data")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  fix-certificates --stage --config config.yaml       # collect & map data")
		fmt.Println("  fix-certificates --diff --config config.yaml         # review changes")
		fmt.Println("  fix-certificates --apply --dry-run --config config   # preview payloads")
		fmt.Println("  fix-certificates --apply --config config.yaml         # send to WB (⚠ production)")
		fmt.Println()
		fmt.Println("  --date DD.MM.YYYY   reference date (default: today). Expired certs are skipped.")
		fmt.Println()
		fmt.Println("Modes:")
		fmt.Println("  --stage       fill empty cert/decl fields from 1C data")
		fmt.Println("  --fix-type    fix cert/decl type when number matches but char_id is wrong")
		fmt.Println("  --reconcile   fix any discrepancy between WB and 1C (wrong number, type swap, both)")
		fmt.Println("  --check       query WB error list for recent card validation errors")
		flag.Usage()
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// CLI flags override config.
	db := cfg.DBPath
	if *dbPath != "" {
		db = *dbPath
	}
	refTime := parseRefDate(*refDate)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	dbConn, err := openDB(db)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer dbConn.Close()

	switch {
	case *stage:
		if err := runStage(ctx, dbConn, &cfg.Filters, refTime); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *fixType:
		if err := runFixTypeStage(ctx, dbConn, &cfg.Filters); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *reconcile:
		if err := runReconcileStage(ctx, dbConn, &cfg.Filters, refTime); err != nil {
			log.Fatalf("reconcile: %v", err)
		}
	case *check:
		apiKey := resolveAPIKey()
		client := wb.New(apiKey)
		client.SetRateLimit("cards_content",
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
			cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
		if err := runCheck(ctx, client, cfg.WBUpdate); err != nil {
			log.Fatalf("check: %v", err)
		}
	case *diff:
		if err := runDiff(ctx, dbConn); err != nil {
			log.Fatalf("diff: %v", err)
		}
	case *apply:
		apiKey := resolveAPIKey()
		client := wb.New(apiKey)
		client.SetRateLimit("cards_content",
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
			cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
		if err := runApply(ctx, dbConn, client, cfg.WBUpdate, *dryRun); err != nil {
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

func resolveAPIKey() string {
	if k := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); k != "" {
		return k
	}
	return os.Getenv("WB_API_KEY")
}

// parseRefDate parses the --date flag. Accepts DD.MM.YYYY or YYYY-MM-DD.
// Empty string → today.
func parseRefDate(s string) time.Time {
	if s == "" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	for _, layout := range []string{"02.01.2006", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	log.Fatalf("invalid --date %q (use DD.MM.YYYY or YYYY-MM-DD)", s)
	return time.Time{}
}

// isExpired checks whether a 1C ISO date (e.g. "2023-02-07T00:00:00") is before refTime.
func isExpired(isoDate string, refTime time.Time) bool {
	dateStr := isoDate
	if idx := strings.Index(isoDate, "T"); idx >= 0 {
		dateStr = isoDate[:idx]
	}
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return false
	}
	return t.Before(refTime)
}
