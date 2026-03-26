package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// SourceRepo provides read-only access to the feedbacks database.
type SourceRepo struct {
	db *sql.DB
}

// ProductStats represents aggregated stats for a product from feedbacks DB.
type ProductStats struct {
	ProductNmID    int
	SupplierArticle string
	ProductName    string
	AvgRating      float64
	FeedbackCount  int
}

// Feedback represents a single feedback for LLM analysis.
type Feedback struct {
	Text             string
	Pros             string
	Cons             string
	ProductValuation int
	CreatedDate      string
}

// NewSourceRepo opens the feedbacks database in read-only mode.
func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	dsn := dbPath + "?mode=ro"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// QueryParams holds optional filters for product listing.
type QueryParams struct {
	DateFrom      string
	DateTo        string
	Subject       string // optional: filter by subject_name
	NmIDs         []int  // optional: filter by product_nm_id list
	MinArticleLen int    // optional: min LENGTH(supplier_article) to skip short SKUs
	Seasons       []int  // optional: include only these seasons (1st char of article)
	Years         []int  // optional: include only these years (chars 2-3 of article)
}

// ListProducts returns products with feedbacks, sorted by avg_rating ASC.
func (r *SourceRepo) ListProducts(ctx context.Context, p QueryParams) ([]ProductStats, error) {
	query := `SELECT product_nm_id, supplier_article, product_name,
	                 AVG(product_valuation) as avg_rating, COUNT(*) as feedback_count
	          FROM feedbacks
	          WHERE created_date BETWEEN ? AND ?`
	args := []any{p.DateFrom, p.DateTo}

	if p.Subject != "" {
		query += " AND subject_name = ?"
		args = append(args, p.Subject)
	}
	if p.MinArticleLen > 0 {
		query += " AND LENGTH(supplier_article) >= ?"
		args = append(args, p.MinArticleLen)
	}
	if len(p.NmIDs) > 0 {
		placeholders := make([]string, len(p.NmIDs))
		for i := range p.NmIDs {
			placeholders[i] = "?"
			args = append(args, p.NmIDs[i])
		}
		query += fmt.Sprintf(" AND product_nm_id IN (%s)", strings.Join(placeholders, ","))
	}
	if len(p.Seasons) > 0 {
		placeholders := make([]string, len(p.Seasons))
		for i := range p.Seasons {
			placeholders[i] = "?"
			args = append(args, p.Seasons[i])
		}
		query += fmt.Sprintf(" AND SUBSTR(supplier_article, 1, 1) IN (%s)", strings.Join(placeholders, ","))
	}
	if len(p.Years) > 0 {
		placeholders := make([]string, len(p.Years))
		for i := range p.Years {
			placeholders[i] = "?"
			args = append(args, p.Years[i])
		}
		query += fmt.Sprintf(" AND CAST(SUBSTR(supplier_article, 2, 2) AS INTEGER) IN (%s)", strings.Join(placeholders, ","))
	}

	query += ` GROUP BY product_nm_id
	           HAVING COUNT(*) >= 1
	           ORDER BY avg_rating ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()

	var products []ProductStats
	for rows.Next() {
		var ps ProductStats
		if err := rows.Scan(&ps.ProductNmID, &ps.SupplierArticle, &ps.ProductName,
			&ps.AvgRating, &ps.FeedbackCount); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, ps)
	}
	return products, rows.Err()
}

// GetFeedbacks returns feedbacks for a product within date range, limited by maxFeedbacks.
// Returns feedbacks sorted by created_date DESC (newest first).
func (r *SourceRepo) GetFeedbacks(ctx context.Context, productNmID int, dateFrom, dateTo string, maxFeedbacks int) ([]Feedback, error) {
	query := `SELECT text, pros, cons, product_valuation, created_date
	          FROM feedbacks
	          WHERE product_nm_id = ? AND created_date BETWEEN ? AND ?
	          ORDER BY created_date DESC`
	args := []any{productNmID, dateFrom, dateTo}

	if maxFeedbacks > 0 {
		query += fmt.Sprintf(" LIMIT %d", maxFeedbacks)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get feedbacks nm_id=%d: %w", productNmID, err)
	}
	defer rows.Close()

	var feedbacks []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.Text, &f.Pros, &f.Cons, &f.ProductValuation, &f.CreatedDate); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		feedbacks = append(feedbacks, f)
	}
	return feedbacks, rows.Err()
}

// GetFeedbackDateRange returns the actual min/max created_date for a product's feedbacks.
// Used to determine analyzed_from/analyzed_to for incremental logic.
func (r *SourceRepo) GetFeedbackDateRange(ctx context.Context, productNmID int, dateFrom, dateTo string) (string, string, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT MIN(created_date), MAX(created_date)
		 FROM feedbacks
		 WHERE product_nm_id = ? AND created_date BETWEEN ? AND ?`,
		productNmID, dateFrom, dateTo)

	var minDate, maxDate sql.NullString
	if err := row.Scan(&minDate, &maxDate); err != nil {
		return "", "", fmt.Errorf("get date range nm_id=%d: %w", productNmID, err)
	}
	return minDate.String, maxDate.String, nil
}

// Close closes the database connection.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}
