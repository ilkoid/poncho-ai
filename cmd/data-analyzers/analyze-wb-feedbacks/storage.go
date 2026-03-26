package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ResultsRepo manages the quality_reports.db — stores analysis results.
type ResultsRepo struct {
	db *sql.DB
}

// ProductSummary represents a stored quality analysis result.
type ProductSummary struct {
	ProductNmID    int
	SupplierArticle string
	ProductName    string
	AvgRating      float64
	FeedbackCount  int
	QualitySummary string
	RequestFrom    string
	RequestTo      string
	AnalyzedFrom   string
	AnalyzedTo     string
	AnalyzedAt     string
	ModelUsed      string
}

// NewResultsRepo opens/creates the results database.
func NewResultsRepo(dbPath string) (*ResultsRepo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open results db: %w", err)
	}

	pragmas := []struct{ key, val string }{
		{"journal_mode", "WAL"},
		{"synchronous", "NORMAL"},
		{"cache_size", "-64000"},
		{"temp_store", "MEMORY"},
	}
	for _, p := range pragmas {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA %s = %s", p.key, p.val)); err != nil {
			db.Close()
			return nil, fmt.Errorf("PRAGMA %s: %w", p.key, err)
		}
	}

	repo := &ResultsRepo{db: db}
	if err := repo.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return repo, nil
}

const createSummaryTable = `
CREATE TABLE IF NOT EXISTS product_quality_summary (
    product_nm_id    INTEGER PRIMARY KEY,
    supplier_article  TEXT,
    product_name      TEXT,
    avg_rating        REAL,
    feedback_count    INTEGER,
    quality_summary   TEXT,
    request_from      TEXT,
    request_to        TEXT,
    analyzed_from     TEXT,
    analyzed_to       TEXT,
    analyzed_at       TEXT,
    model_used        TEXT
);`

func (r *ResultsRepo) initSchema() error {
	if _, err := r.db.Exec(createSummaryTable); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

// GetSummary retrieves a stored summary for a product. Returns nil if not found.
func (r *ResultsRepo) GetSummary(ctx context.Context, productNmID int) (*ProductSummary, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT product_nm_id, supplier_article, product_name, avg_rating, feedback_count,
		        quality_summary, request_from, request_to, analyzed_from, analyzed_to,
		        analyzed_at, model_used
		 FROM product_quality_summary WHERE product_nm_id = ?`, productNmID)

	var s ProductSummary
	err := row.Scan(
		&s.ProductNmID, &s.SupplierArticle, &s.ProductName,
		&s.AvgRating, &s.FeedbackCount, &s.QualitySummary,
		&s.RequestFrom, &s.RequestTo, &s.AnalyzedFrom, &s.AnalyzedTo,
		&s.AnalyzedAt, &s.ModelUsed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get summary nm_id=%d: %w", productNmID, err)
	}
	return &s, nil
}

// SaveSummary inserts or replaces a product quality summary.
func (r *ResultsRepo) SaveSummary(ctx context.Context, s ProductSummary) error {
	if s.AnalyzedAt == "" {
		s.AnalyzedAt = time.Now().Format(time.RFC3339)
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO product_quality_summary
		 (product_nm_id, supplier_article, product_name, avg_rating, feedback_count,
		  quality_summary, request_from, request_to, analyzed_from, analyzed_to,
		  analyzed_at, model_used)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ProductNmID, s.SupplierArticle, s.ProductName,
		s.AvgRating, s.FeedbackCount, s.QualitySummary,
		s.RequestFrom, s.RequestTo, s.AnalyzedFrom, s.AnalyzedTo,
		s.AnalyzedAt, s.ModelUsed,
	)
	if err != nil {
		return fmt.Errorf("save summary nm_id=%d: %w", s.ProductNmID, err)
	}
	return nil
}

// GetAllSummaries returns all stored summaries ordered by avg_rating ASC.
func (r *ResultsRepo) GetAllSummaries(ctx context.Context) ([]ProductSummary, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT product_nm_id, supplier_article, product_name, avg_rating, feedback_count,
		        quality_summary, request_from, request_to, analyzed_from, analyzed_to,
		        analyzed_at, model_used
		 FROM product_quality_summary ORDER BY avg_rating ASC`)
	if err != nil {
		return nil, fmt.Errorf("list summaries: %w", err)
	}
	defer rows.Close()

	var results []ProductSummary
	for rows.Next() {
		var s ProductSummary
		if err := rows.Scan(
			&s.ProductNmID, &s.SupplierArticle, &s.ProductName,
			&s.AvgRating, &s.FeedbackCount, &s.QualitySummary,
			&s.RequestFrom, &s.RequestTo, &s.AnalyzedFrom, &s.AnalyzedTo,
			&s.AnalyzedAt, &s.ModelUsed,
		); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// Close closes the database connection.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}
