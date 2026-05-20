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

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const defaultDBPath = "/var/db/wb-sales.db"

func main() {
	stage := flag.Bool("stage", false, "Stage 1: collect data from DB, map composition, write to staging table")
	apply := flag.Bool("apply", false, "Stage 2: read staging table and update cards via WB API")
	dbPath := flag.String("db", defaultDBPath, "path to wb-sales.db")
	dryRun := flag.Bool("dry-run", false, "show what would be sent without calling WB API")
	flag.Parse()

	if !*stage && !*apply {
		fmt.Println("Usage: fix-fill-material-upper --stage | --apply [--dry-run] [--db PATH]")
		fmt.Println()
		fmt.Println("  --stage     Step 1: collect sneakers without 'Материал верха', map from 1C data")
		fmt.Println("  --apply     Step 2: send staged mappings to WB Content API")
		fmt.Println("  --dry-run   (with --apply) print payloads without sending")
		fmt.Println("  --db PATH   database path (default: /var/db/wb-sales.db)")
		os.Exit(0)
	}

	ctx, cancel := signalCtx()
	defer cancel()

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	switch {
	case *stage:
		if err := runStage(ctx, db); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *apply:
		apiKey := resolveAPIKey()
		if apiKey == "" && !*dryRun {
			log.Fatal("WB_API_KEY or WB_API_ANALYTICS_AND_PROMO_KEY not set")
		}
		client := wb.New(apiKey)
		client.SetRateLimit("cards_content", 8, 2, 4, 1)
		if err := runApply(ctx, db, client, *dryRun); err != nil {
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
