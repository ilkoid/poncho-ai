package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	configPath := flag.String("config", "config.yaml", "YAML config file path")
	importXLS := flag.String("import-xls", "", "Import XLS file into onec_dimensions table")
	stage := flag.Bool("stage", false, "Stage: join dimensions with cards, write to staging table")
	diff := flag.Bool("diff", false, "Show before/after for staged cards")
	apply := flag.Bool("apply", false, "Apply staged changes via WB API")
	dryRun := flag.Bool("dry-run", false, "Show payloads without sending (use with --apply)")
	check := flag.Bool("check", false, "Query WB error list for recent card validation errors")
	compare := flag.Bool("compare", false, "Compare WB vs 1C dimensions, show discrepancies")
	force := flag.Bool("force", false, "Stage cards even if dimensions already set (overwrite from 1C)")
	dbPath := flag.String("db", "", "Override db_path from config")
	flag.Parse()

	if *importXLS == "" && !*stage && !*diff && !*apply && !*check && !*compare {
		fmt.Println("fix-card-dimensions — update L/W/H/weight on WB cards from 1C WMS data")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  fix-card-dimensions --import-xls file.xlsx --db /var/db/wb-sales.db")
		fmt.Println("  fix-card-dimensions --stage --config config.yaml")
		fmt.Println("  fix-card-dimensions --diff --config config.yaml")
		fmt.Println("  fix-card-dimensions --check --config config.yaml")
		fmt.Println("  fix-card-dimensions --apply --dry-run --config config.yaml")
		fmt.Println("  fix-card-dimensions --compare --config config.yaml")
		fmt.Println("  fix-card-dimensions --apply --config config.yaml  (⚠ production)")
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch {
	case *importXLS != "":
		count, err := importXLSFile(ctx, db, *importXLS)
		if err != nil {
			log.Fatalf("import: %v", err)
		}
		fmt.Printf("\nDone: imported %d rows into onec_dimensions\n", count)

	case *stage:
		dbConn, err := openDB(db)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		defer dbConn.Close()

		count, err := runStage(ctx, dbConn, &cfg.Filters, *force)
		if err != nil {
			log.Fatalf("stage: %v", err)
		}
		fmt.Printf("\nNext steps:\n")
		fmt.Printf("  Review:  sqlite3 %s \"SELECT * FROM fix_card_dimensions_staging LIMIT 20\"\n", db)
		fmt.Printf("  Diff:    fix-card-dimensions --diff --config config.yaml\n")
		fmt.Printf("  Apply:   fix-card-dimensions --apply --dry-run --config config.yaml\n")
		_ = count

	case *check:
		apiKey := resolveAPIKey()
		client := wb.New(apiKey)
		client.SetRateLimit("cards_content",
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
			cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
		if err := runCheck(ctx, client, cfg.WBUpdate); err != nil {
			log.Fatalf("check: %v", err)
		}

	case *compare:
		dbConn, err := openDB(db)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		defer dbConn.Close()

		if err := runCompare(ctx, dbConn, &cfg.Filters, cfg.Compare); err != nil {
			log.Fatalf("compare: %v", err)
		}

	case *diff:
		dbConn, err := openDB(db)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		defer dbConn.Close()

		if err := showDiff(ctx, dbConn); err != nil {
			log.Fatalf("diff: %v", err)
		}

	case *apply:
		dbConn, err := openDB(db)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		defer dbConn.Close()

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

// openDBAsRepo opens a sqlite repo wrapper (needed only for --import-xls batch methods).
func openDBAsRepo(dbPath string) (*sqlite.SQLiteSalesRepository, error) {
	return sqlite.NewSQLiteSalesRepository(dbPath)
}
