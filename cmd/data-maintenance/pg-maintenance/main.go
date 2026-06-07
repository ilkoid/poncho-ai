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
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
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

// AppendOnlyTables rarely get UPDATE — only INSERT.
// VACUUM ANALYZE is sufficient; REINDEX is never needed.
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

func main() {
	configPath := flag.String("config", "", "Path to YAML config (storage section)")
	database := flag.String("database", "", "Override database name from config")
	dryRun := flag.Bool("dry-run", false, "Print what would be done without executing")
	reindex := flag.Bool("reindex", false, "Include REINDEX TABLE for heavy-update tables")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "❌ --config flag is required")
		flag.Usage()
		os.Exit(1)
	}

	ctx := context.Background()
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

	// Connect
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		dllog.Error("connect: %v", err)
		os.Exit(1)
	}
	defer pool.Close()

	db := pool.DB()

	// All tables in maintenance order
	allTables := make([]string, 0, len(HeavyUpdateTables)+len(AppendOnlyTables)+len(PromotionTables))
	allTables = append(allTables, HeavyUpdateTables...)
	allTables = append(allTables, AppendOnlyTables...)
	allTables = append(allTables, PromotionTables...)
	total := len(allTables)

	dllog.Log("Maintaining %d tables...", total)

	var errors int
	for i, table := range allTables {
		if *dryRun {
			dllog.Progress(i+1, total, table, "ANALYZE + VACUUM"+reindexSuffix(*reindex, table), start)
			continue
		}

		// ANALYZE — update planner statistics
		if _, err := db.Exec(ctx, fmt.Sprintf("ANALYZE %s", table)); err != nil {
			dllog.Error("%s: ANALYZE failed: %v", table, err)
			errors++
			continue
		}

		// VACUUM — reclaim dead tuples (non-FULL, non-blocking)
		if _, err := db.Exec(ctx, fmt.Sprintf("VACUUM %s", table)); err != nil {
			dllog.Error("%s: VACUUM failed: %v", table, err)
			errors++
			continue
		}

		extra := ""
		// REINDEX — only for heavy-update tables when flag is set
		if *reindex && isHeavyUpdate(table) {
			if _, err := db.Exec(ctx, fmt.Sprintf("REINDEX TABLE %s", table)); err != nil {
				dllog.Error("%s: REINDEX failed: %v", table, err)
				errors++
				continue
			}
			extra = " + REINDEX"
		}

		dllog.Progress(i+1, total, table, "ANALYZE + VACUUM"+extra, start)
	}

	if errors > 0 {
		dllog.Error("%d tables had errors", errors)
		os.Exit(1)
	}

	dllog.Done(time.Since(start), "%d tables maintained", total)
}

// isHeavyUpdate returns true if the table is in the HeavyUpdate list.
func isHeavyUpdate(table string) bool {
	for _, t := range HeavyUpdateTables {
		if t == table {
			return true
		}
	}
	return false
}

// reindexSuffix returns a display suffix for dry-run mode.
func reindexSuffix(doReindex bool, table string) string {
	if doReindex && isHeavyUpdate(table) {
		return " + REINDEX"
	}
	return ""
}
