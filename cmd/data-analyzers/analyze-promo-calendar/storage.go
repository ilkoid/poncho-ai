// analyze-promo-calendar — календарь рекламы по артикулам WB.
//
// Показывает по каким дням артикул был в рекламе, с указанием расхода и ДРР.
// Читает из wb-sales.db (campaign_stats_nm), пишет xlsx.
//
// Usage:
//
//	go run ./cmd/data-analyzers/analyze-promo-calendar/ [options]
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// PromoDay holds aggregated promo stats per article per date.
type PromoDay struct {
	VendorCode   string
	NMID         int64
	Title        string  // cards.title — название товара
	Category1C   string  // onec_goods.category — категория 1С
	StatsDate    string  // YYYY-MM-DD
	TotalSpend   float64 // SUM(sum) — расход на рекламу
	TotalRevenue float64 // SUM(sum_price) — выручка с рекламы
}

// DRR computes the Доля Рекламных Расходов as percentage.
// Returns -1 to indicate "infinity" (spend > 0 but no revenue).
func (p PromoDay) DRR() float64 {
	if p.TotalRevenue > 0 {
		return p.TotalSpend / p.TotalRevenue * 100
	}
	if p.TotalSpend > 0 {
		return -1 // infinity marker
	}
	return 0
}

// SourceRepo provides read-only access to wb-sales.db for promo calendar.
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

// LoadPromoDays loads aggregated promo stats per article per date.
//   - articles: if non-empty, only those vendor_codes are included
//   - categories: if non-empty, only those 1C categories are included
//   - includeZeroSpend: if false, only articles with at least one day of spend > 0
func (r *SourceRepo) LoadPromoDays(ctx context.Context, from, to string, articles, categories []string, includeZeroSpend bool) ([]PromoDay, error) {
	query := `
		SELECT cs.nm_id, c.vendor_code,
		       COALESCE(c.title, ''), COALESCE(og.category, ''),
		       cs.stats_date,
		       SUM(cs.sum) as total_spend,
		       SUM(cs.sum_price) as total_revenue
		FROM campaign_stats_nm cs
		JOIN cards c ON cs.nm_id = c.nm_id
		LEFT JOIN onec_goods og ON c.vendor_code = og.article
		WHERE cs.stats_date BETWEEN ? AND ?`
	args := []any{from, to}

	// Default: only articles that had at least one day with spend > 0
	if !includeZeroSpend {
		query += `
			AND c.vendor_code IN (
				SELECT c2.vendor_code
				FROM campaign_stats_nm cs2
				JOIN cards c2 ON cs2.nm_id = c2.nm_id
				WHERE cs2.stats_date BETWEEN ? AND ?
				GROUP BY c2.vendor_code
				HAVING SUM(cs2.sum) > 0
			)`
		args = append(args, from, to)
	}

	if len(articles) > 0 {
		placeholders := make([]string, len(articles))
		for i, a := range articles {
			placeholders[i] = "?"
			args = append(args, strings.TrimSpace(a))
		}
		query += fmt.Sprintf(" AND c.vendor_code IN (%s)", strings.Join(placeholders, ","))
	}

	if len(categories) > 0 {
		placeholders := make([]string, len(categories))
		for i, cat := range categories {
			placeholders[i] = "?"
			args = append(args, strings.TrimSpace(cat))
		}
		query += fmt.Sprintf(" AND og.category IN (%s)", strings.Join(placeholders, ","))
	}

	query += `
		GROUP BY cs.nm_id, c.vendor_code, cs.stats_date
		ORDER BY c.vendor_code, cs.stats_date`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load promo days: %w", err)
	}
	defer rows.Close()

	var result []PromoDay
	for rows.Next() {
		var p PromoDay
		if err := rows.Scan(&p.NMID, &p.VendorCode, &p.Title, &p.Category1C, &p.StatsDate, &p.TotalSpend, &p.TotalRevenue); err != nil {
			return nil, fmt.Errorf("scan promo day: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// ListDates returns distinct stats_dates in the range, sorted ascending.
func (r *SourceRepo) ListDates(ctx context.Context, from, to string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT stats_date
		FROM campaign_stats_nm
		WHERE stats_date BETWEEN ? AND ?
		ORDER BY stats_date`,
		from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("list dates: %w", err)
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan date: %w", err)
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}
