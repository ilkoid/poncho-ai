package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	configPath := flag.String("config", "config.yaml", "YAML config file path")
	backend := flag.String("backend", "", "Storage backend override (this fixer is PostgreSQL-only)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database override (e.g. wb_data_test)")
	stage := flag.Bool("stage", false, "Stage: latest confirmed penalties → staging table")
	diff := flag.Bool("diff", false, "Show staged before→after for review")
	apply := flag.Bool("apply", false, "Apply staged pending changes via WB API")
	dryRun := flag.Bool("dry-run", false, "With --apply/--auto: print payloads, do not send")
	auto := flag.Bool("auto", false, "Stage + apply in one run (for cron)")
	check := flag.Bool("check", false, "Query WB error list for recent validation errors")
	flag.Parse()

	if !*stage && !*diff && !*apply && !*auto && !*check {
		printUsage()
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *backend != "" {
		cfg.Storage.Backend = *backend
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}
	if cfg.Storage.Backend != "postgres" && cfg.Storage.Backend != "postgresql" {
		log.Fatalf("this fixer is PostgreSQL-only (got backend=%q)", cfg.Storage.Backend)
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

	var pool *pgxpool.Pool
	if needDB {
		dsn, err := cfg.Storage.GetEffectiveDSN()
		if err != nil {
			log.Fatalf("storage DSN: %v", err)
		}
		p, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			log.Fatalf("postgres pool: %v", err)
		}
		defer p.Close()
		pool = p.DB()
		// Ensure staging table exists for every DB mode (idempotent).
		if err := initStagingSchema(ctx, pool); err != nil {
			log.Fatalf("staging schema: %v", err)
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
		if _, _, err := runStage(ctx, pool, cfg, audit); err != nil {
			log.Fatalf("stage: %v", err)
		}
	case *diff:
		if err := showDiff(ctx, pool); err != nil {
			log.Fatalf("diff: %v", err)
		}
	case *apply:
		if err := runApply(ctx, pool, client, cfg, audit, *dryRun); err != nil {
			log.Fatalf("apply: %v", err)
		}
	case *auto:
		if _, _, err := runStage(ctx, pool, cfg, audit); err != nil {
			log.Fatalf("stage: %v", err)
		}
		if err := runApply(ctx, pool, client, cfg, audit, *dryRun); err != nil {
			log.Fatalf("apply: %v", err)
		}
	}
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
	fmt.Println("PostgreSQL only. Reuses pkg/cardupdate for safe full-card overwrites.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  fix-penalties-dims --stage  --config config.yaml --pg-database wb_data_test")
	fmt.Println("  fix-penalties-dims --diff   --config config.yaml --pg-database wb_data_test")
	fmt.Println("  fix-penalties-dims --apply --dry-run --config config.yaml --pg-database wb_data_test")
	fmt.Println("  fix-penalties-dims --check  --config config.yaml")
	fmt.Println("  fix-penalties-dims --auto   --config config.yaml                  (⚠ production, cron)")
	fmt.Println()
	fmt.Println("⚠ --apply / --auto without --dry-run performs a REAL WB write — run by user only.")
}
