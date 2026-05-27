package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// SKURow holds aggregated sales data per SKU (nm_id) for one category.
type SKURow struct {
	NmID       int
	VendorCode string
	Revenue    float64
	Units      int
	AvgPrice   float64
	DaysActive int
}

// PeriodRow holds revenue/units per SKU for a specific time period.
type PeriodRow struct {
	NmID    int
	Revenue float64
	Units   int
}

// DeadCategory holds a category from cards with zero sales.
type DeadCategory struct {
	SubjectName string
	SKUCount    int
}

// SourceRepo provides read-only access to wb-sales.db.
type SourceRepo struct {
	db *sql.DB
}

// NewSourceRepo opens the source database in read-only mode.
func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}

// ListCategories returns all distinct subject_name values with sales in the period.
func (r *SourceRepo) ListCategories(ctx context.Context, periodDays int) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT subject_name
		FROM sales
		WHERE is_cancel = 0
		  AND sale_dt >= date('now', ?||' days')
		  AND subject_name IS NOT NULL
		  AND subject_name != ''
		ORDER BY subject_name`,
		fmt.Sprintf("-%d", periodDays),
	)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var cats []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// LoadCategorySales loads per-SKU aggregated sales for one category (Q1).
func (r *SourceRepo) LoadCategorySales(ctx context.Context, subjectName string, periodDays int) ([]SKURow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.nm_id, COALESCE(c.vendor_code, ''),
		       SUM(CASE WHEN s.is_cancel=0 THEN s.retail_amount ELSE 0 END),
		       SUM(CASE WHEN s.is_cancel=0 THEN s.quantity ELSE 0 END),
		       AVG(CASE WHEN s.is_cancel=0 THEN s.retail_price END),
		       COUNT(DISTINCT CASE WHEN s.is_cancel=0 THEN SUBSTR(s.sale_dt,1,10) END)
		FROM sales s
		LEFT JOIN cards c ON s.nm_id = c.nm_id
		WHERE s.subject_name = ?
		  AND s.sale_dt >= date('now', ?||' days')
		GROUP BY s.nm_id
		ORDER BY SUM(CASE WHEN s.is_cancel=0 THEN s.retail_amount ELSE 0 END) DESC`,
		subjectName, fmt.Sprintf("-%d", periodDays),
	)
	if err != nil {
		return nil, fmt.Errorf("load category sales [%s]: %w", subjectName, err)
	}
	defer rows.Close()

	var result []SKURow
	for rows.Next() {
		var r SKURow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Revenue, &r.Units, &r.AvgPrice, &r.DaysActive); err != nil {
			return nil, fmt.Errorf("scan sku row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadPeriodSales loads per-SKU revenue/units for a shifted period (Q2).
func (r *SourceRepo) LoadPeriodSales(ctx context.Context, subjectName string, fromDays, toDays int) ([]PeriodRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT nm_id,
		       SUM(CASE WHEN is_cancel=0 THEN retail_amount ELSE 0 END),
		       SUM(CASE WHEN is_cancel=0 THEN quantity ELSE 0 END)
		FROM sales
		WHERE subject_name = ?
		  AND sale_dt >= date('now', ?||' days')
		  AND sale_dt <  date('now', ?||' days')
		GROUP BY nm_id`,
		subjectName, fmt.Sprintf("-%d", fromDays), fmt.Sprintf("-%d", toDays),
	)
	if err != nil {
		return nil, fmt.Errorf("load period sales [%s]: %w", subjectName, err)
	}
	defer rows.Close()

	var result []PeriodRow
	for rows.Next() {
		var r PeriodRow
		if err := rows.Scan(&r.NmID, &r.Revenue, &r.Units); err != nil {
			return nil, fmt.Errorf("scan period row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadDeadCatalog loads categories from cards with zero sales in the period (Q4).
func (r *SourceRepo) LoadDeadCatalog(ctx context.Context, periodDays int) ([]DeadCategory, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.subject_name, COUNT(DISTINCT c.nm_id)
		FROM cards c
		WHERE c.subject_name IS NOT NULL
		  AND c.subject_name != ''
		  AND c.subject_name NOT IN (
		      SELECT DISTINCT s.subject_name
		      FROM sales s
		      WHERE s.is_cancel = 0
		        AND s.sale_dt >= date('now', ?||' days')
		        AND s.subject_name IS NOT NULL
		        AND s.subject_name != ''
		  )
		GROUP BY c.subject_name
		ORDER BY COUNT(DISTINCT c.nm_id) DESC`,
		fmt.Sprintf("-%d", periodDays),
	)
	if err != nil {
		return nil, fmt.Errorf("load dead catalog: %w", err)
	}
	defer rows.Close()

	var result []DeadCategory
	for rows.Next() {
		var d DeadCategory
		if err := rows.Scan(&d.SubjectName, &d.SKUCount); err != nil {
			return nil, fmt.Errorf("scan dead category: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
