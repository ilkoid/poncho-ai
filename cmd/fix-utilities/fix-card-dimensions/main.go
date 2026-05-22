package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

func main() {
	configPath := flag.String("config", "config.yaml", "YAML config file path")
	importXLS := flag.String("import-xls", "", "Import XLS file into onec_dimensions table")
	stage := flag.Bool("stage", false, "Stage: join dimensions with cards, write to staging table")
	diff := flag.Bool("diff", false, "Show before/after for staged cards")
	apply := flag.Bool("apply", false, "Apply staged changes via WB API")
	dryRun := flag.Bool("dry-run", false, "Show payloads without sending (use with --apply)")
	dbPath := flag.String("db", "", "Override db_path from config")
	flag.Parse()

	if *configPath == "" && *importXLS == "" && !*stage && !*diff && !*apply {
		fmt.Println("Usage: fix-card-dimensions --config config.yaml [--import-xls file.xlsx] [--stage] [--diff] [--apply] [--dry-run]")
		fmt.Println("       fix-card-dimensions --import-xls file.xlsx --db /tmp/test.db")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := &Config{}
	if _, err := os.Stat(*configPath); err == nil {
		cfg, err = loadConfig(*configPath)
		if err != nil {
			log.Fatalf("Config error: %v", err)
		}
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	if *importXLS != "" {
		if cfg.DBPath == "" {
			log.Fatal("--db is required for --import-xls")
		}
		count, err := importXLSFile(ctx, cfg.DBPath, *importXLS)
		if err != nil {
			log.Fatalf("Import failed: %v", err)
		}
		dllog.Done(0, "imported %d rows into onec_dimensions", count)
		return
	}

	if *stage {
		if cfg.DBPath == "" {
			log.Fatal("--db is required for --stage")
		}
		count, err := runStage(ctx, cfg)
		if err != nil {
			log.Fatalf("Stage failed: %v", err)
		}
		dllog.Done(0, "staged %d cards for dimension update", count)
		return
	}

	if *diff {
		if cfg.DBPath == "" {
			log.Fatal("--db is required for --diff")
		}
		if err := showDiff(ctx, cfg.DBPath); err != nil {
			log.Fatalf("Diff failed: %v", err)
		}
		return
	}

	if *apply {
		apiKey := getWBApiKey(cfg.WBUpdate.APIKey)
		if apiKey == "" {
			log.Fatal("WB_API_KEY (or WB_API_ANALYTICS_AND_PROMO_KEY) not set")
		}
		if cfg.DBPath == "" {
			log.Fatal("--db is required for --apply")
		}
		if *dryRun {
			if err := runDryRun(ctx, cfg, apiKey); err != nil {
				log.Fatalf("Dry-run failed: %v", err)
			}
			return
		}
		count, err := runApply(ctx, cfg, apiKey)
		if err != nil {
			log.Fatalf("Apply failed: %v", err)
		}
		dllog.Done(0, "updated %d cards", count)
		return
	}

	fmt.Println("No action specified. Use --import-xls, --stage, --diff, or --apply")
}

func openDB(dbPath string) (*sqlite.SQLiteSalesRepository, error) {
	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", dbPath, err)
	}
	return repo, nil
}
