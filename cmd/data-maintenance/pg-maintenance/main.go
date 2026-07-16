// pg-maintenance performs post-load maintenance on PostgreSQL databases.
//
// After a download-all.sh cycle, tables accumulate dead tuples from heavy
// upserts (ON CONFLICT DO UPDATE). Autovacuum may not keep up with the rate.
// This utility runs ANALYZE + VACUUM on all tables, with optional REINDEX
// for heavily-updated tables.
//
// Usage:
//
//	PG_PWD=password go run ./cmd/data-maintenance/pg-maintenance --config cmd/.configs/download-all/pg-maintenance-PG.yaml
//	PG_PWD=password go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --dry-run
//	PG_PWD=password go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --reindex
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HeavyUpdateTables are candidates for REINDEX — they receive frequent
// ON CONFLICT DO UPDATE which causes index bloat over time.
// Order matches the download-all.sh phase structure.
var HeavyUpdateTables = []string{
	// Phase 1: Catalog
	"cards", "card_photos", "card_sizes", "card_characteristics", "card_tags",
	"product_prices",
	"onec_goods", "onec_goods_sku", "onec_prices", "onec_rests", "onec_dimensions", "pim_goods",

	// Phase 2: Feedbacks
	"feedbacks", "questions",

	// Phase 3: Sales & Revenue
	"orders", "operational_sales",
	"region_sales",

	// Phase 4: Stock & Logistics
	"stocks_daily_warehouses", "warehouse_remains",
	"stock_products", // 33-col ON CONFLICT DO UPDATE per snapshot_date+nm_id (4 secondary indexes → bloat)
	"stock_history_reports", "stock_history_daily", "stock_history_metrics",
	"supplies", "supply_goods", "supply_packages",
	"wb_warehouses", "wb_transit_tariffs",

	// Phase 5: Advertising
	"campaigns", "campaign_stats_daily", "campaign_stats_nm", "campaign_stats_app", "campaign_products",
	"campaign_booster_stats",

	// Phase 6: Analytics
	"funnel_metrics_daily", "funnel_metrics_aggregated",
	"funnel_metrics_grouped_daily",
	"search_positions_daily", "search_queries_daily",
	"nm_report_downloads",
	"measurement_penalties",

	// Phase 7: WB Scraper snapshots
	"search_queries", // DIMENSION upserted via ON CONFLICT (query) DO UPDATE on every /snapshot push
}

// AppendOnlyTables rarely get UPDATE — only INSERT, or DELETE+INSERT (snapshot replacement).
// VACUUM ANALYZE is sufficient; REINDEX is never needed because the write pattern
// generates dead tuples but not the per-row UPDATE churn that bloats indexes.
var AppendOnlyTables = []string{
	"sales",
	"service_records",
	"products", // dimension table, updated rarely

	// wbscraper fact tables: written by ReplaceSnapshot (DELETE WHERE snapshot_ts + INSERT per push).
	// DELETE+INSERT creates dead tuples (VACUUM reclaims them) but not index bloat from UPDATEs.
	"search_positions",
	"vitrine_ads",
	"competitor_cards",
	"competitor_card_prices",
	"competitor_card_details",
	"competitor_card_stocks",
	"competitor_card_meta",
	"competitor_card_options",
	"competitor_card_compositions",
	"competitor_card_sizes",
	"competitor_card_colors",
}

// PromotionTables are promotion/normquery reference tables.
// Moderate update rate — REINDEX only when explicitly requested.
var PromotionTables = []string{
	"bid_recommendations", "bid_recommendations_nq",
	"campaign_bids", "campaign_budget",
	"min_bids", "normquery_bids", "normquery_clusters",
	"normquery_minus", "normquery_stats",
	"promotion_balance", "promotion_balance_cashbacks",
	"promotion_expenses", "promotion_payments",
	"wb_calendar_promotions", "wb_calendar_promotion_details",
	"wb_calendar_promotion_nomenclatures", "wb_calendar_promotion_advantages",
	"wb_calendar_promotion_ranging",
}

func main() {
	configPath := flag.String("config", "", "Path to YAML config (storage section)")
	database := flag.String("database", "", "Override database name from config")
	dryRun := flag.Bool("dry-run", false, "Print what would be done without executing")
	reindex := flag.Bool("reindex", false, "Include REINDEX TABLE for heavy-update tables")
	tablesFlag := flag.String("tables", "", "Comma-separated table names to maintain (default: all). Unknown names are warned, not fatal.")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "❌ --config flag is required")
		flag.Usage()
		os.Exit(1)
	}

	// Graceful shutdown on SIGINT/SIGTERM. Note: PG cannot interrupt a running
	// VACUUM/REINDEX mid-statement on a cancelled Go context — the in-flight
	// statement runs to completion, then ctx.Err() between iterations exits the
	// loop. Partial completion still reports the per-table error count.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	start := time.Now()

	// Load config
	var cfg struct {
		Storage config.V2StorageConfig `yaml:"storage"`
	}
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "❌ load config: %v\n", err)
		os.Exit(1)
	}

	// Override database if flag provided
	if *database != "" {
		cfg.Storage.PgDatabase = *database
	}

	cfg.Storage.Backend = "postgres"
	cfg.Storage = cfg.Storage.GetDefaults()

	dsn, err := cfg.Storage.GetEffectiveDSN()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ DSN: %v\n", err)
		os.Exit(1)
	}

	dllog.PrintHeader("PG Maintenance Utility",
		dllog.HeaderField{Key: "Database", Value: cfg.Storage.PgDatabase},
		dllog.HeaderField{Key: "Dry-run", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Reindex", Value: fmt.Sprintf("%v", *reindex)},
	)

	// Connect. Dry-run also connects: it's a full rehearsal (PG reachable, creds
	// valid, statement_timeout lifted) minus the VACUUM/ANALYZE/REINDEX execution —
	// a cron pre-flight that catches "PG down" before the real run.
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		dllog.Error("connect: %v", err)
		os.Exit(1)
	}
	defer pool.Close()

	db := pool.DB()

	// Maintenance ops (VACUUM/REINDEX) routinely exceed the 5min statement_timeout
	// that NewPool applies as a bulk-INSERT safety net for downloaders. Hold one
	// pooled connection for the whole run and lift the timeout so these ops can
	// run to completion.
	//
	// Why a held connection (not db.Exec on the pool): pgxpool's (*Pool).Exec may
	// serve each call from a different backend, so a SET + VACUUM issued as two
	// separate db.Exec calls is not guaranteed to land on the same connection.
	// VACUUM also can't run inside a transaction block → use SET (session scope),
	// not SET LOCAL (which lives only inside a transaction).
	conn, err := db.Acquire(ctx)
	if err != nil {
		dllog.Error("acquire connection: %v", err)
		os.Exit(1)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SET statement_timeout = 0"); err != nil {
		dllog.Error("disable statement_timeout: %v", err)
		os.Exit(1)
	}
	dllog.Log("statement_timeout lifted for maintenance run (VACUUM/REINDEX may run long)")

	// All tables in maintenance order
	allTables := make([]string, 0, len(HeavyUpdateTables)+len(AppendOnlyTables)+len(PromotionTables))
	allTables = append(allTables, HeavyUpdateTables...)
	allTables = append(allTables, AppendOnlyTables...)
	allTables = append(allTables, PromotionTables...)

	// Optional --tables filter: keep only requested names, warn about typos.
	// Lets you point VACUUM at one heavy table (e.g. stock_products) between full runs
	// instead of waiting for the whole 74-table cycle.
	if *tablesFlag != "" {
		want := make(map[string]struct{})
		for _, t := range strings.Split(*tablesFlag, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				want[t] = struct{}{}
			}
		}
		known := make(map[string]struct{}, len(allTables))
		for _, t := range allTables {
			known[t] = struct{}{}
		}
		filtered := make([]string, 0, len(want))
		for t := range want {
			if _, ok := known[t]; ok {
				filtered = append(filtered, t)
			} else {
				dllog.Error("--tables: %q is not a known maintenance table (skipped)", t)
			}
		}
		// Preserve canonical phase order, not the comma-list order.
		ordered := make([]string, 0, len(filtered))
		for _, t := range allTables {
			if _, ok := want[t]; ok {
				ordered = append(ordered, t)
			}
		}
		allTables = ordered
		if len(allTables) == 0 {
			dllog.Error("--tables: no matching tables after filter")
			os.Exit(1)
		}
	}
	total := len(allTables)

	dllog.Log("Maintaining %d tables...", total)

	var errors int
	for i, table := range allTables {
		// Honor SIGINT/SIGTERM between tables (PG can't interrupt an in-flight VACUUM).
		if err := ctx.Err(); err != nil {
			dllog.Error("interrupted before %s: %v", table, err)
			break
		}

		if *dryRun {
			dllog.Progress(i+1, total, table, "ANALYZE + VACUUM"+reindexSuffix(*reindex, table), start)
			continue
		}

		// Snapshot dead-tuple count before VACUUM for observability.
		// pg_stat_user_tables is updated lazily by PG, so reclaimed may read 0 even
		// when VACUUM reclaimed tuples on a prior run — this is a PG stats quirk,
		// not a bug in the utility.
		deadBefore := readDeadTuples(ctx, conn, table)

		// ANALYZE — update planner statistics
		if _, err := conn.Exec(ctx, fmt.Sprintf("ANALYZE %s", table)); err != nil {
			dllog.Error("%s: ANALYZE failed: %v", table, err)
			errors++
			continue
		}

		// VACUUM — reclaim dead tuples (non-FULL, non-blocking)
		if _, err := conn.Exec(ctx, fmt.Sprintf("VACUUM %s", table)); err != nil {
			dllog.Error("%s: VACUUM failed: %v", table, err)
			errors++
			continue
		}

		extra := reclaimSuffix(deadBefore, readDeadTuples(ctx, conn, table))

		// REINDEX — only for heavy-update tables when flag is set
		if *reindex && isHeavyUpdate(table) {
			if _, err := conn.Exec(ctx, fmt.Sprintf("REINDEX TABLE %s", table)); err != nil {
				dllog.Error("%s: REINDEX failed: %v", table, err)
				errors++
				continue
			}
			extra += " + REINDEX"
		}

		dllog.Progress(i+1, total, table, "ANALYZE + VACUUM"+extra, start)
	}

	if ctx.Err() != nil {
		dllog.Error("interrupted: %d/%d tables maintained, %d errors", len(allTables), total, errors)
		os.Exit(1)
	}
	if errors > 0 {
		dllog.Error("%d tables had errors", errors)
		os.Exit(1)
	}

	dllog.Done(time.Since(start), "%d tables maintained", total)
}

// readDeadTuples returns the current n_dead_tup for a table, or -1 if unavailable
// (stats not collected / NULL on a freshly-created table / query error).
// VACUUM VERBOSE would write to the server log, not the client, so we read
// pg_stat_user_tables directly — the only client-visible source of this metric.
func readDeadTuples(ctx context.Context, conn *pgxpool.Conn, table string) int64 {
	var dead *int64
	if err := conn.QueryRow(ctx,
		"SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = $1", table,
	).Scan(&dead); err != nil || dead == nil {
		return -1
	}
	return *dead
}

// reclaimSuffix formats the dead-tuple delta for the progress line.
// Returns "" when pre-stats were unavailable (→ no honest number to show).
func reclaimSuffix(deadBefore, deadAfter int64) string {
	if deadBefore < 0 {
		return ""
	}
	reclaimed := deadBefore
	if deadAfter >= 0 {
		reclaimed = deadBefore - deadAfter
	}
	return fmt.Sprintf(" (reclaimed %d dead tuples)", reclaimed)
}

// heavyUpdateSet is the lookup backing isHeavyUpdate. Built once at package init
// from HeavyUpdateTables so membership checks are O(1) (the old linear scan was a
// stylistic smell rather than a real cost, but the map is the idiomatic form).
var heavyUpdateSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(HeavyUpdateTables))
	for _, t := range HeavyUpdateTables {
		m[t] = struct{}{}
	}
	return m
}()

// isHeavyUpdate returns true if the table is in the HeavyUpdate list.
func isHeavyUpdate(table string) bool {
	_, ok := heavyUpdateSet[table]
	return ok
}

// reindexSuffix returns a display suffix for dry-run mode.
func reindexSuffix(doReindex bool, table string) string {
	if doReindex && isHeavyUpdate(table) {
		return " + REINDEX"
	}
	return ""
}
