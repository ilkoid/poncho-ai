// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/sales"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SalesRepository defines storage interface for sales data (Port).
// Interface defined in consumer package following Rule 6 (Go idiom).
//
// Design follows Interface Segregation Principle - essential methods only.
type SalesRepository interface {
	// Save saves batch of sales rows to storage.
	// Uses INSERT OR IGNORE to skip duplicates (rrd_id is UNIQUE).
	Save(ctx context.Context, rows []wb.RealizationReportRow) error

	// SaveServiceRecords saves batch of service records (logistics, deductions).
	// These are records with nm_id = 0 from WB API.
	SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error

	// Exists checks if row with given rrdID already exists.
	// Used for resume functionality after interruption.
	Exists(ctx context.Context, rrdID int) (bool, error)

	// Count returns total number of sales in database.
	// Used for resume mode status display.
	Count(ctx context.Context) (int, error)

	// GetFBWOnly returns only FBW sales (filtered by delivery_method).
	// Returns empty slice if no FBW sales found.
	GetFBWOnly(ctx context.Context) ([]wb.RealizationReportRow, error)

	// Close closes the database connection.
	Close() error

	// GetLastSaleDT returns timestamp of the last sale in database.
	// For smart resume: start loading from this moment + 1 second.
	// Returns zero time if database is empty.
	GetLastSaleDT(ctx context.Context) (time.Time, error)

	// GetFirstSaleDT returns timestamp of the earliest sale in database.
	// For resume mode: detects if requested period is before existing data.
	// Returns zero time if database is empty.
	GetFirstSaleDT(ctx context.Context) (time.Time, error)
}

// Ensure implementation satisfies interface.
var _ SalesRepository = (*SQLiteSalesRepository)(nil)

// Ensure implementation satisfies sales.SalesWriter (narrow persistence port).
var _ sales.SalesWriter = (*SQLiteSalesRepository)(nil)

// SQLiteSalesRepository implements SalesRepository for SQLite database.
type SQLiteSalesRepository struct {
	db *sql.DB
}

// NewSQLiteSalesRepository creates a new SQLite repository.
// Opens database at given path and initializes schema.
func NewSQLiteSalesRepository(dbPath string) (*SQLiteSalesRepository, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("db_path is required — set it in config or --db flag")
	}

	// DSN parameters set PRAGMAs on every connection (persist across reconnects).
	// Supported by go-sqlite3: _journal_mode, _cache_size, _busy_timeout, _foreign_keys, _synchronous.
	// _journal_mode=WAL also auto-sets synchronous=NORMAL.
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_cache_size=-65536&_busy_timeout=10000&_foreign_keys=1", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	// Single connection: prevents pool from creating unconfigured connections.
	// Safe for SQLite (single-writer, WAL allows concurrent reads on separate connections,
	// but this repo is used sequentially by downloaders).
	db.SetMaxOpenConns(1)

	// PRAGMAs not supported in DSN — set via Exec (persist as long as connection lives)
	pragmas := []string{
		"PRAGMA mmap_size = 268435456",      // 256MB memory-mapped I/O
		"PRAGMA wal_autocheckpoint = 5000",   // Less frequent checkpoints
		"PRAGMA temp_store = MEMORY",         // Temp tables in RAM
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	repo := &SQLiteSalesRepository{db: db}

	// Initialize schema
	if err := repo.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	return repo, nil
}

// initSchema creates tables and indexes.
func (r *SQLiteSalesRepository) initSchema() error {
	// Migrate: drop old heavy index if it exists (replaced by idx_service_oper_type)
	_, _ = r.db.Exec("DROP INDEX IF EXISTS idx_service_oper")

	// Create main sales table
	_, err := r.db.Exec(GetSchemaSQL())
	if err != nil {
		return err
	}

	// Create service_records table for logistics/deductions
	_, err = r.db.Exec(GetServiceRecordsSchemaSQL())
	if err != nil {
		return err
	}

	// Create FBW view (non-critical, fail silently)
	_, _ = r.db.Exec(GetCreateViewSQL())

	// Create funnel analytics tables (products, funnel_metrics_daily)
	_, err = r.db.Exec(GetFunnelSchemaSQL())
	if err != nil {
		return err
	}

	// Create promotion analytics tables (campaigns, campaign_stats_daily, campaign_products)
	_, err = r.db.Exec(GetPromotionSchemaSQL())
	if err != nil {
		return err
	}

	// Add tags column to products table if not exists (migration for existing DBs)
	_, err = r.db.Exec(`ALTER TABLE products ADD COLUMN tags TEXT`)
	if err != nil {
		// Column may already exist - ignore error
		// SQLite returns "duplicate column name" error
	}

	// Migration for funnel_metrics_aggregated: create if not exists
	var tableName string
	err = r.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='funnel_metrics_aggregated'`).Scan(&tableName)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check table existence: %w", err)
	}

	if err == sql.ErrNoRows || tableName == "" {
		// Table doesn't exist - create it
		_, err = r.db.Exec(GetFunnelAggregatedSchemaSQL())
		if err != nil {
			return err
		}
	}
	// If table exists, we use INSERT OR REPLACE which handles updates

	// Create campaign fullstats tables (app-level, nm-level, booster stats)
	_, err = r.db.Exec(GetCampaignFullstatsSchemaSQL())
	if err != nil {
		return err
	}

	// Create promotion v2 tables (normquery, bid recommendations, finance, calendar)
	_, err = r.db.Exec(GetPromotionV2SchemaSQL())
	if err != nil {
		return err
	}

	// Migrations for new financial columns (ALTER TABLE ADD COLUMN is idempotent-safe via ignoring errors)
	salesMigrations := []string{
		"ALTER TABLE sales ADD COLUMN ppvz_sales_commission REAL",
		"ALTER TABLE sales ADD COLUMN acquiring_fee REAL",
		"ALTER TABLE sales ADD COLUMN acquiring_percent REAL",
		"ALTER TABLE sales ADD COLUMN retail_price_withdisc_rub REAL",
		"ALTER TABLE sales ADD COLUMN ppvz_spp_prc REAL",
		"ALTER TABLE sales ADD COLUMN ppvz_kvw_prc_base REAL",
		"ALTER TABLE sales ADD COLUMN ppvz_kvw_prc REAL",
		"ALTER TABLE sales ADD COLUMN sup_rating_prc_up REAL",
		"ALTER TABLE sales ADD COLUMN is_kgvp_v2 REAL",
		"ALTER TABLE sales ADD COLUMN product_discount_for_report REAL",
		"ALTER TABLE sales ADD COLUMN supplier_promo REAL",
		"ALTER TABLE sales ADD COLUMN seller_promo_discount REAL",
		"ALTER TABLE sales ADD COLUMN sale_price_promocode_discount_prc REAL",
		"ALTER TABLE sales ADD COLUMN wibes_wb_discount_percent REAL",
		"ALTER TABLE sales ADD COLUMN loyalty_discount REAL",
		"ALTER TABLE sales ADD COLUMN cashback_amount REAL",
		"ALTER TABLE sales ADD COLUMN cashback_discount REAL",
		"ALTER TABLE sales ADD COLUMN cashback_commission_change REAL",
	}
	for _, m := range salesMigrations {
		_, _ = r.db.Exec(m) // Ignore "duplicate column name" for existing DBs
	}

	serviceMigrations := []string{
		"ALTER TABLE service_records ADD COLUMN penalty REAL",
		"ALTER TABLE service_records ADD COLUMN deduction REAL",
		"ALTER TABLE service_records ADD COLUMN storage_fee REAL",
		"ALTER TABLE service_records ADD COLUMN acceptance REAL",
		"ALTER TABLE service_records ADD COLUMN gi_id INTEGER",
	}
	for _, m := range serviceMigrations {
		_, _ = r.db.Exec(m)
	}

	// Migrations for campaign detail columns (from /api/advert/v2/adverts)
	campaignMigrations := []string{
		"ALTER TABLE campaigns ADD COLUMN name TEXT",
		"ALTER TABLE campaigns ADD COLUMN payment_type TEXT",
		"ALTER TABLE campaigns ADD COLUMN bid_type TEXT",
		"ALTER TABLE campaigns ADD COLUMN placement_search INTEGER DEFAULT 0",
		"ALTER TABLE campaigns ADD COLUMN placement_reco INTEGER DEFAULT 0",
		"ALTER TABLE campaigns ADD COLUMN ts_created TEXT",
		"ALTER TABLE campaigns ADD COLUMN ts_started TEXT",
		"ALTER TABLE campaigns ADD COLUMN ts_deleted TEXT",
	}
	for _, m := range campaignMigrations {
		_, _ = r.db.Exec(m)
	}

	// Create feedbacks tables (Feedbacks API: feedbacks + questions)
	_, err = r.db.Exec(GetFeedbacksSchemaSQL())
	if err != nil {
		return err
	}

	// Create quality analysis results table (LLM analyzer output)
	_, err = r.db.Exec(GetQualitySchemaSQL())
	if err != nil {
		return err
	}



		// Create stock history CSV reports tables (WB Analytics API - async CSV generation)
		_, err = r.db.Exec(GetStockHistorySchemaSQL())
		if err != nil {
			return err
		}
		// Create stocks warehouse snapshots table (WB Analytics API - stocks-report/wb-warehouses)
		_, err = r.db.Exec(GetStocksWarehouseSchemaSQL())
		if err != nil {
			return err
		}
		// Create region sales table (WB Seller Analytics API — /api/v1/analytics/region-sale)
		_, err = r.db.Exec(GetRegionSalesSchemaSQL())
		if err != nil {
			return err
		}
		// Create content cards tables (WB Content API - /content/v2/get/cards/list)
		_, err = r.db.Exec(GetCardsSchemaSQL())
		if err != nil {
			return err
		}
		// Create orders table (WB Statistics API — /api/v1/supplier/orders)
		_, err = r.db.Exec(GetOrdersSchemaSQL())
		if err != nil {
			return err
		}
		// Create operational sales table (WB Statistics API — /api/v1/supplier/sales)
		_, err = r.db.Exec(GetOpsalesSchemaSQL())
		if err != nil {
			return err
		}
		// Create product prices table (WB Discounts-Prices API — /api/v2/list/goods/filter)
		_, err = r.db.Exec(GetPricesSchemaSQL())
		if err != nil {
			return err
		}
		// Create 1C/PIM tables (1C Goods, SKUs, Prices + PIM Goods)
		_, err = r.db.Exec(GetOneCSchemaSQL())
		if err != nil {
			return err
		}
		// Migrations for 1C goods new fields (41 columns)
		onecGoodsMigrations := []string{
			"ALTER TABLE onec_goods ADD COLUMN length REAL DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN wideness REAL DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN height REAL DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN weight_sku_g REAL DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN certificate TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN has_certificate INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN certificate_begin TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN certificate_end TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN certificate_number TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN approval_date TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN date_of_production TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN date_of_receipt TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN pps_date TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN collection_season TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN collection_year TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN look_season TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN opt_collection_season TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN opt_collection_year TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN production_season TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN production_year TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN category_level1_name TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN category_level2_name TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN age TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN figure_features TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN licensor TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN main_capture TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN markirovka TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN model_height TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN ratio_heat TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN recommendations TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN size_on_model TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN tag TEXT DEFAULT ''",
			"ALTER TABLE onec_goods ADD COLUMN quantity_bar_code INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_adult INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_article_blocked INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_exclude_from_site INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_exclusive INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_genuine_leather INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_model_cancelled INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_new_collection INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_not_require_ironing INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_pps INTEGER DEFAULT 0",
			"ALTER TABLE onec_goods ADD COLUMN is_ya_price_list_opt INTEGER DEFAULT 0",
		}
		for _, m := range onecGoodsMigrations {
			_, _ = r.db.Exec(m)
		}

		// Migrations for per-SKU dimensions (1C API now returns dims at SKU level)
		onecSKUMigrations := []string{
			"ALTER TABLE onec_goods_sku ADD COLUMN length REAL DEFAULT 0",
			"ALTER TABLE onec_goods_sku ADD COLUMN wideness REAL DEFAULT 0",
			"ALTER TABLE onec_goods_sku ADD COLUMN height REAL DEFAULT 0",
			"ALTER TABLE onec_goods_sku ADD COLUMN weight_sku_g REAL DEFAULT 0",
		}
		for _, m := range onecSKUMigrations {
			_, _ = r.db.Exec(m)
		}

		// New certificate field
		_, _ = r.db.Exec("ALTER TABLE onec_goods ADD COLUMN certificate_type TEXT DEFAULT ''")

		// PIM wildberries dimensions (cm, from PIM Akeneo attributes)
		pimMigrations := []string{
			"ALTER TABLE pim_goods ADD COLUMN wildberries_length REAL DEFAULT 0",
			"ALTER TABLE pim_goods ADD COLUMN wildberries_width REAL DEFAULT 0",
			"ALTER TABLE pim_goods ADD COLUMN wildberries_height REAL DEFAULT 0",
		}
		for _, m := range pimMigrations {
			_, _ = r.db.Exec(m)
		}

		// Create supply tables (warehouses, tariffs, supplies, goods, packages)
		_, err = r.db.Exec(GetSupplySchemaSQL())
		if err != nil {
			return err
		}
		// Create search visibility tables (positions, queries)
		_, err = r.db.Exec(GetSearchVisibilitySchemaSQL())
		if err != nil {
			return err
		}

		// Create nm-report funnel tables (report tracking + grouped metrics)
		_, err = r.db.Exec(GetNmReportSchemaSQL())
		if err != nil {
			return err
		}

		// Migrations for funnel_metrics_daily: cancel columns (from nm-report CSV)
		funnelMigrations := []string{
			"ALTER TABLE funnel_metrics_daily ADD COLUMN cancel_count INTEGER DEFAULT 0",
			"ALTER TABLE funnel_metrics_daily ADD COLUMN cancel_sum_rub INTEGER DEFAULT 0",
		}
		for _, m := range funnelMigrations {
			_, _ = r.db.Exec(m)
		}
	return nil
}

// Close closes the database connection.
func (r *SQLiteSalesRepository) Close() error {
	return r.db.Close()
}

// DB returns the underlying *sql.DB for direct queries needed by cmd utilities
// (staging tables, complex joins not in the repository layer).
func (r *SQLiteSalesRepository) DB() *sql.DB {
	return r.db
}

// Optimize updates query planner statistics via ANALYZE.
// Call after bulk data loads for optimal query plans.
func (r *SQLiteSalesRepository) Optimize(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "ANALYZE"); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}
