// pg-maintenance performs targeted PostgreSQL maintenance (VACUUM / ANALYZE / REINDEX).
//
// In the download-all.sh cycle each downloader is preceded by a per-group pass
// (--group) that reclaims the dead tuples left by the previous run's upserts and
// refreshes planner stats for the load's own DELETE/upsert statements. Tables
// whose nightly load is mostly fresh-date INSERTs (onec_prices,
// stocks_daily_warehouses, warehouse_remains, stock_products) are maintained
// with ANALYZE only (depth "analyze") — a full VACUUM scan there costs minutes
// and reclaims almost nothing.
//
// Usage:
//
//	PG_PWD=x go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --group cards   # per-utility pre-load pass
//	PG_PWD=x go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --analyze-only # final light pass over all tables
//	PG_PWD=x go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml                 # all tables, VACUUM (ANALYZE)
//	PG_PWD=x go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --reindex-concurrently # weekly deep pass
//	PG_PWD=x go run ./cmd/data-maintenance/pg-maintenance --config .../pg-maintenance-PG.yaml --dry-run
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
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
}

// AppendOnlyTables have write patterns that don't benefit from REINDEX.
// sales is written as DELETE-by-date-window + INSERT (sales_repo.go), not UPDATE
// churn — dead index entries are reclaimed by plain VACUUM along with the heap,
// so it stays out of the weekly REINDEX set but still gets VACUUM every run.
//
// NOTE: wbscraper fact tables (search_positions, vitrine_ads, competitor_cards,
// competitor_card_*) are intentionally NOT here. That pipeline's schema lives only
// in the test DB (wb_data_test) per AGENTS.md — it is never created in prod, so
// listing them here made the prod maintenance run fail on SQLSTATE 42P01.
var AppendOnlyTables = []string{
	"sales",
	"service_records",
	"products", // dimension table, updated rarely
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

// GroupConfig is a config-defined maintenance group: the tables one downloader
// writes plus the depth to maintain them at. Defined in the "groups" section of
// pg-maintenance-PG.yaml; --group <name> selects one. Depth "vacuum" (default)
// runs VACUUM (ANALYZE) — for upsert/delete-churn tables; "analyze" runs ANALYZE
// only — for snapshot giants whose nightly load is new-date INSERTs.
type GroupConfig struct {
	Depth  string   `yaml:"depth"` // "vacuum" (default) | "analyze"
	Tables []string `yaml:"tables"`
}

func main() {
	configPath := flag.String("config", "", "Path to YAML config (storage + optional groups section)")
	database := flag.String("database", "", "Override database name from config")
	dryRun := flag.Bool("dry-run", false, "Print what would be done without executing")
	reindex := flag.Bool("reindex", false, "Include REINDEX TABLE for heavy-update tables")
	reindexConcurrently := flag.Bool("reindex-concurrently", false, "REINDEX TABLE CONCURRENTLY for heavy-update tables (no long exclusive locks; weekly deep pass)")
	full := flag.Bool("full", false, "Use VACUUM FULL (rewrites table file, returns space to OS, ACCESS EXCLUSIVE lock)")
	analyzeOnlyFlag := flag.Bool("analyze-only", false, "ANALYZE only — light tier, no dead-tuple scan. Overrides the group's depth")
	groupFlag := flag.String("group", "", "Maintain only this config-defined group's tables (per-utility pre-load pass)")
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
		Storage config.V2StorageConfig  `yaml:"storage"`
		Groups  map[string]GroupConfig `yaml:"groups"`
	}
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "❌ load config: %v\n", err)
		os.Exit(1)
	}

	// Resolve the table set and depth: --group selects a config-defined group
	// (per-utility pre-load pass); without it the hardcoded phase lists apply.
	// resolveGroup is pure config work — no DB needed yet, so flag misuse fails
	// before we connect anywhere.
	analyzeOnly := *analyzeOnlyFlag
	groupTables, err := resolveGroup(*groupFlag, cfg.Groups, &analyzeOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if analyzeOnly && *full {
		fmt.Fprintln(os.Stderr, "❌ --analyze-only and --full are mutually exclusive")
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

	groupLabel := *groupFlag
	if groupLabel == "" {
		groupLabel = "(all)"
	}
	reindexLabel := "off"
	switch {
	case *reindexConcurrently:
		reindexLabel = "concurrent"
	case *reindex:
		reindexLabel = "on"
	}
	modeLabel := maintenanceMode(analyzeOnly, *full)

	dllog.PrintHeader("PG Maintenance Utility",
		dllog.HeaderField{Key: "Database", Value: cfg.Storage.PgDatabase},
		dllog.HeaderField{Key: "Group", Value: groupLabel},
		dllog.HeaderField{Key: "Mode", Value: modeLabel},
		dllog.HeaderField{Key: "Reindex", Value: reindexLabel},
		dllog.HeaderField{Key: "Dry-run", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Full", Value: fmt.Sprintf("%v", *full)},
	)

	// VACUUM FULL takes an ACCESS EXCLUSIVE lock per table — the table is fully
	// unavailable for the duration. Intended for rare manual runs in a maintenance
	// window, not the nightly cycle. Print a loud warning so the operator notices
	// it on the console/log even if --full was set accidentally.
	if *full && !*dryRun {
		dllog.Error("⚠ VACUUM FULL: each table takes ACCESS EXCLUSIVE lock and is unavailable until done")
	}

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

	// Snapshot the set of tables that actually exist in this database's public
	// schema. The maintenance lists are authored against the *designed* schema, but
	// not every target DB has every table — e.g. wbscraper fact tables live only
	// in the test DB (wb_data_test), and a freshly-cloned prod may be missing a
	// table a not-yet-deployed loader creates. Without this filter, ANALYZE hits
	// SQLSTATE 42P01 on any missing relation and the whole run exits non-zero.
	// One round-trip via pg_tables (same catalog the stats helpers below rely on).
	existingTables, err := loadExistingTables(ctx, conn)
	if err != nil {
		dllog.Error("load table catalog: %v", err)
		os.Exit(1)
	}

	// Select the table set: the --group's config tables, or the hardcoded phase
	// lists in canonical order (whole-DB default).
	var allTables []string
	if groupTables != nil {
		allTables = groupTables
	} else {
		allTables = make([]string, 0, len(HeavyUpdateTables)+len(AppendOnlyTables)+len(PromotionTables))
		allTables = append(allTables, HeavyUpdateTables...)
		allTables = append(allTables, AppendOnlyTables...)
		allTables = append(allTables, PromotionTables...)
	}

	// Optional --tables filter: keep only requested names, warn about typos.
	// Lets you point VACUUM at one heavy table (e.g. stock_products) between full runs
	// instead of waiting for the whole cycle.
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

	// Drop tables that don't exist in THIS database. A missing table is logged as
	// info (not an error): the catalog is the source of truth, and "we don't have
	// that table here" is normal for test-only schemas (wbscraper) or partially-
	// migrated prod. This keeps the nightly cron green instead of failing the run.
	kept, missing := filterExistingTables(allTables, existingTables)
	for _, m := range missing {
		dllog.Log("skip %q: not present in database %q", m, cfg.Storage.PgDatabase)
	}
	allTables = kept

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
			opLabel := maintenanceMode(analyzeOnly, *full)
			if (*reindex || *reindexConcurrently) && isHeavyUpdate(table) && !*full {
				opLabel += " + REINDEX"
			}
			dllog.Progress(i+1, total, table, opLabel, start)
			continue
		}

		// Snapshot dead-tuple count before VACUUM for observability.
		// pg_stat_user_tables is updated lazily by PG, so reclaimed may read 0 even
		// when VACUUM reclaimed tuples on a prior run — this is a PG stats quirk,
		// not a bug in the utility.
		deadBefore := readDeadTuples(ctx, conn, table)
		sizeBefore := int64(-1)
		if *full {
			sizeBefore = readTableSize(ctx, conn, table)
		}

		// Single statement — one table scan. VACUUM (ANALYZE) collects planner
		// stats in the same pass that reclaims dead tuples (plain VACUUM marks
		// them reusable without shrinking the file); the FULL variant also folds
		// the post-rewrite ANALYZE in, where rows physically move.
		stmt := maintenanceStmt(analyzeOnly, *full, table)
		opLabel := maintenanceMode(analyzeOnly, *full)
		if _, err := conn.Exec(ctx, stmt); err != nil {
			dllog.Error("%s: %s failed: %v", table, opLabel, err)
			errors++
			continue
		}

		extra := reclaimSuffix(deadBefore, readDeadTuples(ctx, conn, table))
		if *full {
			extra += sizeSuffix(sizeBefore, readTableSize(ctx, conn, table))
		}

		// REINDEX — only for heavy-update tables when requested AND we did NOT run
		// VACUUM FULL (FULL already rebuilds every index of the table, so a
		// separate REINDEX would just redo work).
		if (*reindex || *reindexConcurrently) && isHeavyUpdate(table) && !*full {
			reindexStmt := fmt.Sprintf("REINDEX TABLE %s", table)
			if *reindexConcurrently {
				reindexStmt = fmt.Sprintf("REINDEX TABLE CONCURRENTLY %s", table)
			}
			if _, err := conn.Exec(ctx, reindexStmt); err != nil {
				dllog.Error("%s: REINDEX failed: %v", table, err)
				if *reindexConcurrently {
					// A failed concurrent reindex aborts after creating the
					// transient _ccnew index; it stays behind as INVALID and
					// keeps consuming disk until dropped manually.
					dllog.Error("%s: check pg_indexes for leftover *_ccnew entries and drop invalid ones", table)
				}
				errors++
				continue
			}
			extra += " + REINDEX"
		}

		dllog.Progress(i+1, total, table, opLabel+extra, start)
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

// maintenanceMode returns the per-table maintenance label. Precedence:
// --analyze-only (or group depth "analyze") → ANALYZE; --full → the rewriting
// VACUUM; default → plain VACUUM with stats collected in the same scan.
func maintenanceMode(analyzeOnly, full bool) string {
	switch {
	case analyzeOnly:
		return "ANALYZE"
	case full:
		return "VACUUM (FULL, ANALYZE)"
	default:
		return "VACUUM (ANALYZE)"
	}
}

// maintenanceStmt returns the single maintenance statement for one table.
// One statement = one table scan: ANALYZE and VACUUM never run as separate
// passes in any combination here.
func maintenanceStmt(analyzeOnly, full bool, table string) string {
	switch {
	case analyzeOnly:
		return "ANALYZE " + table
	case full:
		return "VACUUM (FULL, ANALYZE) " + table
	default:
		return "VACUUM (ANALYZE) " + table
	}
}

// resolveGroup resolves --group against the config's groups section. It returns
// the group's tables (nil when no --group was given — caller falls back to the
// hardcoded lists) and may lift analyzeOnly when the group's depth is "analyze"
// (an explicit --analyze-only flag already sets it, so flag and depth compose).
// Unknown group names and bad depth values fail with an actionable error.
func resolveGroup(name string, groups map[string]GroupConfig, analyzeOnly *bool) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	g, ok := groups[name]
	if !ok {
		return nil, fmt.Errorf("--group %q is not defined in the config. Available groups: %s",
			name, strings.Join(sortedGroupNames(groups), ", "))
	}
	switch g.Depth {
	case "", "vacuum":
		// default depth — VACUUM (ANALYZE)
	case "analyze":
		*analyzeOnly = true
	default:
		return nil, fmt.Errorf("group %q: unknown depth %q (want \"vacuum\" or \"analyze\")", name, g.Depth)
	}
	if len(g.Tables) == 0 {
		return nil, fmt.Errorf("group %q: empty tables list", name)
	}
	return g.Tables, nil
}

// sortedGroupNames returns the config's group names sorted, for error messages.
func sortedGroupNames(m map[string]GroupConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// loadExistingTables returns the set of table names present in the public schema
// of the connected database. Used to skip maintenance entries whose backing
// relation isn't there (test-only schemas like wbscraper, not-yet-migrated prod).
// One round-trip; returns an empty (non-nil) map on a database with no tables.
func loadExistingTables(ctx context.Context, conn *pgxpool.Conn) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname = 'public'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// filterExistingTables returns the subset of want that exists in have, preserving
// the canonical order of want, plus the names that were missing (also in want
// order). Pure function: the DB lookup happens in loadExistingTables, so this is
// unit-testable without a live connection.
func filterExistingTables(want []string, have map[string]struct{}) (kept, missing []string) {
	kept = make([]string, 0, len(want))
	missing = make([]string, 0)
	for _, t := range want {
		if _, ok := have[t]; ok {
			kept = append(kept, t)
		} else {
			missing = append(missing, t)
		}
	}
	return kept, missing
}

// readTableSize returns pg_total_relation_size (table + indexes + TOAST) in bytes,
// or -1 if unavailable. Only meaningful for VACUUM FULL, since plain VACUUM does
// not shrink the on-disk file. Uses $1::regclass — safe because table comes from
// a hardcoded list (--tables names are validated against that same list).
func readTableSize(ctx context.Context, conn *pgxpool.Conn, table string) int64 {
	var size *int64
	if err := conn.QueryRow(ctx, "SELECT pg_total_relation_size($1::regclass)", table).Scan(&size); err != nil || size == nil {
		return -1
	}
	return *size
}

// sizeSuffix formats the on-disk size delta for the VACUUM FULL progress line.
// Returns "" when the before-size was unavailable.
func sizeSuffix(sizeBefore, sizeAfter int64) string {
	if sizeBefore < 0 {
		return ""
	}
	freed := sizeBefore
	if sizeAfter >= 0 {
		freed = sizeBefore - sizeAfter
	}
	return fmt.Sprintf(", freed %s", humanBytes(freed))
}

// humanBytes renders a byte count as a compact human-readable string (B/KB/MB/GB).
func humanBytes(b int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
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
