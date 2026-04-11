package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// dailySalesSQL queries daily sales per nm_id for 29 days back from the reference date.
// This covers the maximum MA window (28) plus 1 day for sold_prev.
// sale_dt is stored as ISO timestamp (2026-03-27T00:00:00Z), so we use date() for comparison.
const dailySalesSQL = `
SELECT nm_id, date(sale_dt) AS d,
       SUM(CASE WHEN doc_type_name = 'Продажа' THEN quantity ELSE 0 END) AS sold
FROM sales
WHERE date(sale_dt) >= date(?, '-29 days')
  AND date(sale_dt) <= ?
  AND is_cancel = 0
GROUP BY nm_id, date(sale_dt)
`

// productAttrsSQL enriches nm_id with vendor_code, 1C, and PIM attributes via a single JOIN.
// products.vendor_code = onec_goods.article = pim_goods.identifier
const productAttrsSQL = `
SELECT
    p.nm_id,
    COALESCE(o.article, '')           AS article,
    COALESCE(pm.identifier, o.article, '') AS identifier,
    COALESCE(p.vendor_code, '')       AS vendor_code,
    COALESCE(o.name, '')              AS name,
    COALESCE(o.name_im, '')           AS name_im,
    COALESCE(o.brand, '')             AS brand,
    COALESCE(o.type, '')              AS type,
    COALESCE(o.category, '')          AS category,
    COALESCE(o.category_level1, '')   AS category_level1,
    COALESCE(o.category_level2, '')   AS category_level2,
    COALESCE(o.sex, '')               AS sex,
    COALESCE(o.season, '')            AS season,
    COALESCE(o.color, '')             AS color,
    COALESCE(o.collection, '')        AS collection
FROM products p
LEFT JOIN onec_goods o  ON o.article = p.vendor_code
LEFT JOIN pim_goods pm  ON pm.identifier = p.vendor_code
WHERE p.nm_id IN (%s)
`

// SourceRepo provides read-only access to wb-sales.db.
type SourceRepo struct {
	db *sql.DB
}

// NewSourceRepo opens the source database in read-only mode.
func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// QueryDailySales returns a map of nm_id → date → sold count for the 29-day window.
func (r *SourceRepo) QueryDailySales(ctx context.Context, refDate string) (map[int]map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, dailySalesSQL, refDate, refDate)
	if err != nil {
		return nil, fmt.Errorf("query daily sales: %w", err)
	}
	defer rows.Close()

	result := make(map[int]map[string]int)
	for rows.Next() {
		var nmID int
		var d string
		var sold int
		if err := rows.Scan(&nmID, &d, &sold); err != nil {
			return nil, fmt.Errorf("scan daily sales: %w", err)
		}
		if result[nmID] == nil {
			result[nmID] = make(map[string]int)
		}
		result[nmID][d] = sold
	}
	return result, rows.Err()
}

// QueryProductAttrs returns product attributes for the given nm_ids as MARow values.
// SCAN from source DB goes directly into MARow fields — no intermediate struct needed.
func (r *SourceRepo) QueryProductAttrs(ctx context.Context, nmIDs []int) (map[int]*MARow, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(productAttrsSQL, strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query product attrs: %w", err)
	}
	defer rows.Close()

	result := make(map[int]*MARow, len(nmIDs))
	for rows.Next() {
		var a MARow
		if err := rows.Scan(
			&a.NmID, &a.Article, &a.Identifier, &a.VendorCode,
			&a.Name, &a.NameIM, &a.Brand, &a.Type, &a.Category, &a.CategoryLevel1, &a.CategoryLevel2,
			&a.Sex, &a.Season, &a.Color, &a.Collection,
		); err != nil {
			return nil, fmt.Errorf("scan product attrs: %w", err)
		}
		result[a.NmID] = &a
	}
	return result, rows.Err()
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}
