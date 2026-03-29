// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

// SQLiteSalesRepository implements SalesRepository for SQLite database.
type SQLiteSalesRepository struct {
	db *sql.DB
}

// NewSQLiteSalesRepository creates a new SQLite repository.
// Opens database at given path and initializes schema.
func NewSQLiteSalesRepository(dbPath string) (*SQLiteSalesRepository, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Enable foreign keys and WAL mode for better concurrency
	_, err = db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;")
	if err != nil {
		db.Close()
		return nil, err
	}

	// Performance PRAGMAs for bulk-loaded databases
	pragmas := []string{
		"PRAGMA page_size = 8192",        // 2x pages (only effective for new DBs)
		"PRAGMA cache_size = -65536",     // 64MB page cache (vs default 2MB)
		"PRAGMA mmap_size = 268435456",   // 256MB memory-mapped I/O
		"PRAGMA synchronous = NORMAL",    // Safe with WAL, faster than FULL
		"PRAGMA busy_timeout = 10000",    // 10s wait on locked DB
		"PRAGMA wal_autocheckpoint = 5000", // Less frequent checkpoints
		"PRAGMA temp_store = MEMORY",     // Temp tables in RAM
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


		// Create stocks warehouse snapshots table (WB Analytics API - stocks-report/wb-warehouses)
		_, err = r.db.Exec(GetStocksWarehouseSchemaSQL())
		if err != nil {
			return err
		}
	return nil
}

// Close closes the database connection.
func (r *SQLiteSalesRepository) Close() error {
	return r.db.Close()
}
