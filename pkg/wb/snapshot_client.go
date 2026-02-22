// Package wb provides snapshot client for E2E testing.
//
// SnapshotDBClient reads data from SQLite database instead of WB API.
// This enables fast, deterministic E2E tests without rate limits.
package wb

import (
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// SnapshotDBClient implements Service interfaces backed by SQLite.
// It provides instant access to pre-collected data for testing.
type SnapshotDBClient struct {
	mu sync.RWMutex
	db *sql.DB

	sales       *snapshotSalesService
	advertising *snapshotAdvertisingService
	feedbacks   *snapshotFeedbacksService
}

// NewSnapshotDBClient creates a new snapshot client from SQLite database.
// The database should contain tables: sales, funnel_metrics_daily, products,
// campaigns, campaign_stats_daily.
func NewSnapshotDBClient(dbPath string) (*SnapshotDBClient, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Verify database has required tables
	if err := verifySnapshotTables(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("verify tables: %w", err)
	}

	client := &SnapshotDBClient{
		db: db,
	}

	// Initialize services
	client.sales = &snapshotSalesService{db: db}
	client.advertising = &snapshotAdvertisingService{db: db}
	client.feedbacks = &snapshotFeedbacksService{db: db}

	return client, nil
}

// Close closes the database connection.
func (c *SnapshotDBClient) Close() error {
	return c.db.Close()
}

// Sales returns the SalesService for funnel and sales data.
func (c *SnapshotDBClient) Sales() SalesService {
	return c.sales
}

// Advertising returns the AdvertisingService for campaign data.
func (c *SnapshotDBClient) Advertising() AdvertisingService {
	return c.advertising
}

// Feedbacks returns the FeedbackService for feedbacks and questions data.
func (c *SnapshotDBClient) Feedbacks() FeedbackService {
	return c.feedbacks
}

// IsDemoKey returns true (snapshot always behaves like demo mode).
func (c *SnapshotDBClient) IsDemoKey() bool {
	return true
}

// GetDB returns the underlying database connection for advanced queries.
func (c *SnapshotDBClient) GetDB() *sql.DB {
	return c.db
}

// verifySnapshotTables checks that required tables exist.
func verifySnapshotTables(db *sql.DB) error {
	requiredTables := []string{
		"sales",
		"funnel_metrics_daily",
		"products",
		"campaigns",
		"campaign_stats_daily",
		"feedbacks_items",
		"questions_items",
	}

	for _, table := range requiredTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&name)
		if err == sql.ErrNoRows {
			return fmt.Errorf("missing required table: %s", table)
		}
		if err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
	}

	return nil
}

// GetStats returns statistics about the snapshot data.
func (c *SnapshotDBClient) GetStats() (map[string]int64, error) {
	stats := make(map[string]int64)

	queries := map[string]string{
		"sales":               "SELECT COUNT(*) FROM sales",
		"funnel_metrics":      "SELECT COUNT(*) FROM funnel_metrics_daily",
		"products":            "SELECT COUNT(*) FROM products",
		"campaigns":           "SELECT COUNT(*) FROM campaigns",
		"campaign_stats":      "SELECT COUNT(*) FROM campaign_stats_daily",
		"unique_nm_ids":       "SELECT COUNT(DISTINCT nm_id) FROM sales WHERE nm_id > 0",
		"unique_advert_ids":   "SELECT COUNT(DISTINCT advert_id) FROM campaign_stats_daily",
		"feedbacks":           "SELECT COUNT(*) FROM feedbacks_items",
		"questions":           "SELECT COUNT(*) FROM questions_items",
	}

	for name, query := range queries {
		var count int64
		if err := c.db.QueryRow(query).Scan(&count); err != nil {
			return nil, fmt.Errorf("get %s count: %w", name, err)
		}
		stats[name] = count
	}

	return stats, nil
}
