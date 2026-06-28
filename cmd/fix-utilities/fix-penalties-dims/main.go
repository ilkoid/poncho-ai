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

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	configPath := flag.String("config", "config.yaml", "YAML config file path")
	dbPath := flag.String("db", "", "SQLite database path (overrides config storage.db_path)")
	stage := flag.Bool("stage", false, "Stage: latest confirmed penalties → staging table")
	diff := flag.Bool("diff", false, "Show staged before→after for review")
	apply := flag.Bool("apply", false, "Apply staged pending changes via WB API")
	dryRun := flag.Bool("dry-run", false, "With --apply/--auto: print payloads, do not send")
	yes := flag.Bool("yes", false, "Confirm a REAL WB write (--apply/--auto without --dry-run). Required; without it real writes are refused")
	auto := flag.Bool("auto", false, "Stage + apply in one run (for cron)")
	check := flag.Bool("check", false, "Query WB error list for recent validation errors")
	flag.Parse()

	if !*stage && !*diff && !*apply && !*auto && !*check {
		printUsage()
		os.Exit(0)
	}

	// Fail-closed guard: a REAL WB write (--apply/--auto without --dry-run) requires
	// explicit opt-in via --yes or PENALTIES_DIMS_ALLOW_WRITE=1. Without it, REFUSE and
	// exit non-zero. A dropped/mistyped --dry-run in a wrapper script (ilkoid.sh, cron)
	// must never silently overwrite cards — it once did. See confirmRealWrite + plan.
	if *apply || *auto {
		if err := confirmRealWrite(*dryRun, *yes, os.Getenv("PENALTIES_DIMS_ALLOW_WRITE")); err != nil {
			log.Fatalf("⛔ %v", err)
		}
		if !*dryRun {
			fmt.Fprintln(os.Stderr, "⛔ REAL WB WRITE — cards WILL be overwritten (--yes confirmed)")
		}
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *dbPath != "" {
		cfg.Storage.DBPath = *dbPath
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	dllog.PrintHeader("WB Penalties-Dims Fixer",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Mode", Value: modeString(stage, diff, apply, auto, check, dryRun)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	needDB := *stage || *diff || *apply || *auto
	needWB := (*apply && !*dryRun) || (*auto && !*dryRun) || *check

	var db *sql.DB
	if needDB {
		db, err = openSQLite(ctx, cfg.Storage.DBPath)
		if err != nil {
			log.Fatalf("open sqlite: %v", err)
		}
		defer db.Close()
		// Ensure this fixer's staging table + the read tables (cards,
		// measurement_penalties) exist. On a populated fixer.db these are no-ops
		// (IF NOT EXISTS); on a fresh DB they prevent 'no such table' crashes so
		// standalone --stage/--diff degrade gracefully instead of erroring.
		if err := initStagingSchema(ctx, db); err != nil {
			log.Fatalf("staging schema: %v", err)
		}
		if err := ensureReadSchemas(ctx, db); err != nil {
			log.Fatalf("read schema: %v", err)
		}
	}

	var client *wb.Client
	if needWB {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			env := cfg.WB.APIKeyEnv
			if env == "" {
				env = "WB_API_ANALYTICS_AND_PROMO_KEY"
			}
			log.Fatalf("WB API key not set: provide wb.api_key or env %s", env)
		}
		client = wb.New(apiKey)
		// One call covers UpdateCards AND GetCardErrorsList (same ToolID "cards_content").
		client.SetRateLimit("cards_content",
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
			cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
	}

	audit, err := NewAuditor(cfg.Audit.LogDir)
	if err != nil {
		log.Fatalf("audit: %v", err)
	}
	defer audit.Close()

	switch {
	case *check:
		if err := runCheck(ctx, client, cfg); err != nil {
			log.Fatalf("check: %v", err)
		}
	case *stage:
		if _, _, err := runStage(ctx, db, cfg, audit); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *diff:
		if err := showDiff(ctx, db, cfg.Filter); err != nil {
			log.Fatalf("diff: %v", err)
		}
	case *apply:
		if err := runApply(ctx, db, client, cfg, audit, *dryRun); err != nil {
			log.Fatalf("apply: %v", err)
		}
	case *auto:
		if _, _, err := runStage(ctx, db, cfg, audit); err != nil {
			log.Fatalf("stage: %v", err)
		}
		if err := runApply(ctx, db, client, cfg, audit, *dryRun); err != nil {
			log.Fatalf("apply: %v", err)
		}
	}
}

// openSQLite opens the fixer's isolated SQLite database. SetMaxOpenConns(1) serializes
// access through a single connection (SQLite-safe; avoids "database is locked").
// busy_timeout makes a writer wait briefly rather than failing on a transient lock.
func openSQLite(ctx context.Context, dbPath string) (*sql.DB, error) {
	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_busy_timeout=5000"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", dbPath, err)
	}
	return db, nil
}

// ensureReadSchemas idempotently creates the read tables the fixer queries
// (cards + child tables, measurement_penalties) by reusing the canonical DDL from
// pkg/storage/sqlite (Rule 0). On a populated fixer.db this is a no-op (IF NOT EXISTS);
// on a fresh DB it prevents 'no such table' crashes so standalone --stage/--diff degrade
// gracefully. The downloaders create the same tables when they populate fixer.db.
func ensureReadSchemas(ctx context.Context, db *sql.DB) error {
	for _, ddl := range []string{sqlite.CardsSchemaSQL, sqlite.PenaltiesSchemaSQL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("ensure read schema: %w", err)
		}
	}
	return nil
}

// resolveAPIKey resolves the WB API key: direct config value > named env var.
func resolveAPIKey(cfg *Config) string {
	if cfg.WB.APIKey != "" {
		return cfg.WB.APIKey
	}
	if cfg.WB.APIKeyEnv != "" {
		return os.Getenv(cfg.WB.APIKeyEnv)
	}
	return ""
}

// confirmRealWrite is the fail-closed gate for a real WB card write. Returns nil when
// the write is explicitly confirmed (or not happening at all); returns an error when a
// real write would occur without opt-in — the caller then refuses and exits non-zero.
// dryRun short-circuits to nil (no write). This guard is independent of how the binary
// is invoked: even if a wrapper script drops --dry-run, the fixer itself refuses.
func confirmRealWrite(dryRun, yes bool, allowEnv string) error {
	if dryRun {
		return nil
	}
	if yes || allowEnv == "1" {
		return nil
	}
	return fmt.Errorf("real WB write refused: --apply/--auto without --dry-run requires explicit opt-in\n" +
		"  → re-run with --dry-run to preview payloads, or add --yes (or set PENALTIES_DIMS_ALLOW_WRITE=1) to write for real")
}

func modeString(stage, diff, apply, auto, check, dryRun *bool) string {
	switch {
	case *check:
		return "check"
	case *auto:
		if *dryRun {
			return "auto (dry-run)"
		}
		return "auto"
	case *apply:
		if *dryRun {
			return "apply (dry-run)"
		}
		return "apply"
	case *stage:
		return "stage"
	case *diff:
		return "diff"
	}
	return "?"
}

func printUsage() {
	fmt.Println("fix-penalties-dims — rewrite card L/W/H from WB measurement penalties (МГХ)")
	fmt.Println()
	fmt.Println("Autonomous SQLite robot. Run via run-penalties-dims-fixer.sh (loads fixer.db first),")
	fmt.Println("or standalone for inspection. Reuses pkg/cardupdate for safe full-card overwrites.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  fix-penalties-dims --stage  --config config.yaml --db /tmp/test.db")
	fmt.Println("  fix-penalties-dims --diff   --config config.yaml --db /tmp/test.db")
	fmt.Println("  fix-penalties-dims --apply --dry-run --config config.yaml --db /tmp/test.db")
	fmt.Println("  fix-penalties-dims --check  --config config.yaml")
	fmt.Println("  fix-penalties-dims --auto   --config config.yaml                  (⚠ production, cron)")
	fmt.Println()
	fmt.Println("⚠ --apply / --auto without --dry-run REFUSES to write unless --yes is given (fail-closed guard).")
}
