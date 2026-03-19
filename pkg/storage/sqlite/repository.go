// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
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

	return nil
}

// Close closes the database connection.
func (r *SQLiteSalesRepository) Close() error {
	return r.db.Close()
}
