package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

// SQL queries for source DB (wb-sales.db, read-only).

// Stock positions aggregated by (nm_id, chrt_id, region_name).
// Includes in_way_from_client (returns from customers) as available stock.
const stockPositionsSQL = `
SELECT nm_id, chrt_id, region_name,
       SUM(quantity + COALESCE(in_way_from_client, 0)) AS stock_qty
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND nm_id IN (%s)
GROUP BY nm_id, chrt_id, region_name
`

// All stock positions without year filter (allowed_years empty).
// Includes in_way_from_client (returns from customers) as available stock.
const stockPositionsAllSQL = `
SELECT nm_id, chrt_id, region_name,
       SUM(quantity + COALESCE(in_way_from_client, 0)) AS stock_qty
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
GROUP BY nm_id, chrt_id, region_name
`

// Total unique sizes per nm_id (globally, not per region).
const totalSizesSQL = `
SELECT nm_id, COUNT(DISTINCT chrt_id) AS total_sizes
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND nm_id IN (%s)
GROUP BY nm_id
`

const totalSizesAllSQL = `
SELECT nm_id, COUNT(DISTINCT chrt_id) AS total_sizes
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
GROUP BY nm_id
`

// Sizes with stock > threshold per (nm_id, region).
// Includes in_way_from_client (returns from customers) as available stock.
const sizesInStockSQL = `
SELECT nm_id, region_name, COUNT(DISTINCT chrt_id) AS sizes_in_stock
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND (quantity + COALESCE(in_way_from_client, 0)) > ?
  AND nm_id IN (%s)
GROUP BY nm_id, region_name
`

// Sizes with stock > threshold without year filter.
// Includes in_way_from_client (returns from customers) as available stock.
const sizesInStockAllSQL = `
SELECT nm_id, region_name, COUNT(DISTINCT chrt_id) AS sizes_in_stock
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND (quantity + COALESCE(in_way_from_client, 0)) > ?
GROUP BY nm_id, region_name
`

// Daily sales per (nm_id, barcode) for MA computation.
// MA is global (no region filter) — sales are counted across all warehouses.
const dailySalesByBarcodeSQL = `
SELECT nm_id, barcode, date(sale_dt) AS d,
       SUM(CASE WHEN doc_type_name = 'Продажа' THEN quantity ELSE 0 END) AS sold
FROM sales
WHERE date(sale_dt) >= date(?, '-29 days')
  AND date(sale_dt) <= ?
  AND is_cancel = 0
  AND nm_id IN (%s)
GROUP BY nm_id, barcode, date(sale_dt)
`

const dailySalesByBarcodeAllSQL = `
SELECT nm_id, barcode, date(sale_dt) AS d,
       SUM(CASE WHEN doc_type_name = 'Продажа' THEN quantity ELSE 0 END) AS sold
FROM sales
WHERE date(sale_dt) >= date(?, '-29 days')
  AND date(sale_dt) <= ?
  AND is_cancel = 0
GROUP BY nm_id, barcode, date(sale_dt)
`

// Card sizes mapping: chrt_id → (nm_id, tech_size, barcode from skus_json).
const cardSizesSQL = `
SELECT chrt_id, nm_id, tech_size, skus_json
FROM card_sizes
`

// Product attributes via 3-table JOIN.
// Uses cards table (populated by download-wb-cards) instead of products (populated by funnel downloaders).
const productAttrsSQL = `
SELECT
    p.nm_id,
    COALESCE(o.article, '')           AS article,
    COALESCE(pm.identifier, o.article, '') AS identifier,
    COALESCE(p.vendor_code, '')       AS vendor_code,
    COALESCE(o.name, '')              AS name,
    COALESCE(o.brand, '')             AS brand,
    COALESCE(o.type, '')              AS type,
    COALESCE(o.category, '')          AS category,
    COALESCE(o.category_level1, '')   AS category_level1,
    COALESCE(o.category_level2, '')   AS category_level2,
    COALESCE(o.sex, '')               AS sex,
    COALESCE(o.season, '')            AS season,
    COALESCE(o.color, '')             AS color,
    COALESCE(o.collection, '')        AS collection
FROM cards p
LEFT JOIN onec_goods o  ON o.article = p.vendor_code
LEFT JOIN pim_goods pm  ON pm.identifier = p.vendor_code
WHERE p.nm_id IN (%s)
`

// Vendor codes for year filtering.
// Uses cards table (populated by download-wb-cards) instead of products (populated by funnel downloaders).
const vendorCodesSQL = `
SELECT DISTINCT nm_id, vendor_code
FROM cards
WHERE nm_id IN (%s)
`

const vendorCodesAllSQL = `
SELECT DISTINCT nm_id, vendor_code
FROM cards
`

// Incoming supply per barcode from active (non-completed) supplies.
// quantity - ready_for_sale_quantity = units not yet reflected in stock.
// status_id 5 = completed, excluded because those are already in stock.
const supplyIncomingSQL = `
SELECT sg.barcode,
       SUM(sg.quantity) - SUM(sg.ready_for_sale_quantity) AS incoming
FROM supply_goods sg
JOIN supplies s ON s.supply_id = sg.supply_id AND s.preorder_id = sg.preorder_id
WHERE s.status_id NOT IN (5)
GROUP BY sg.barcode
HAVING incoming > 0
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

// QueryStockPositions returns stock data grouped by (nm_id, chrt_id, region).
func (r *SourceRepo) QueryStockPositions(ctx context.Context, date string, nmIDs []int) (map[StockKey]StockInfo, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(stockPositionsSQL, ph)
		args = append([]any{date}, a...)
	} else {
		query = stockPositionsAllSQL
		args = []any{date}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query stock positions: %w", err)
	}
	defer rows.Close()

	result := make(map[StockKey]StockInfo)
	for rows.Next() {
		var key StockKey
		var qty int64
		if err := rows.Scan(&key.NmID, &key.ChrtID, &key.RegionName, &qty); err != nil {
			return nil, fmt.Errorf("scan stock position: %w", err)
		}
		result[key] = StockInfo{StockQty: qty}
	}
	return result, rows.Err()
}

// QueryTotalSizes returns total unique sizes per nm_id.
func (r *SourceRepo) QueryTotalSizes(ctx context.Context, date string, nmIDs []int) (map[int]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(totalSizesSQL, ph)
		args = append([]any{date}, a...)
	} else {
		query = totalSizesAllSQL
		args = []any{date}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query total sizes: %w", err)
	}
	defer rows.Close()

	result := make(map[int]int)
	for rows.Next() {
		var nmID, total int
		if err := rows.Scan(&nmID, &total); err != nil {
			return nil, fmt.Errorf("scan total sizes: %w", err)
		}
		result[nmID] = total
	}
	return result, rows.Err()
}

// QuerySizesInStock returns sizes with stock > threshold per (nm_id, region).
func (r *SourceRepo) QuerySizesInStock(ctx context.Context, date string, threshold int, nmIDs []int) (map[SizeRegionKey]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(sizesInStockSQL, ph)
		args = append([]any{date, threshold}, a...)
	} else {
		query = sizesInStockAllSQL
		args = []any{date, threshold}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sizes in stock: %w", err)
	}
	defer rows.Close()

	result := make(map[SizeRegionKey]int)
	for rows.Next() {
		var key SizeRegionKey
		var count int
		if err := rows.Scan(&key.NmID, &key.RegionName, &count); err != nil {
			return nil, fmt.Errorf("scan sizes in stock: %w", err)
		}
		result[key] = count
	}
	return result, rows.Err()
}

// QueryDailySalesByBarcode returns daily sales per (nm_id, barcode) for 29-day window.
func (r *SourceRepo) QueryDailySalesByBarcode(ctx context.Context, refDate string, nmIDs []int) (map[int]map[string]map[string]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(dailySalesByBarcodeSQL, ph)
		args = append([]any{refDate, refDate}, a...)
	} else {
		query = dailySalesByBarcodeAllSQL
		args = []any{refDate, refDate}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily sales by barcode: %w", err)
	}
	defer rows.Close()

	// nm_id → barcode → date → sold
	result := make(map[int]map[string]map[string]int)
	for rows.Next() {
		var nmID int
		var barcode, d string
		var sold int
		if err := rows.Scan(&nmID, &barcode, &d, &sold); err != nil {
			return nil, fmt.Errorf("scan daily sales: %w", err)
		}
		if result[nmID] == nil {
			result[nmID] = make(map[string]map[string]int)
		}
		if result[nmID][barcode] == nil {
			result[nmID][barcode] = make(map[string]int)
		}
		result[nmID][barcode][d] = sold
	}
	return result, rows.Err()
}

// CardSizeEntry maps chrt_id to nm_id, tech_size, and barcode (from skus_json).
type CardSizeEntry struct {
	ChrtID   int64
	NmID     int
	TechSize string
	Barcode  string // first barcode from skus_json
}

// QueryCardSizes returns all card_sizes entries with parsed barcodes.
func (r *SourceRepo) QueryCardSizes(ctx context.Context) ([]CardSizeEntry, error) {
	rows, err := r.db.QueryContext(ctx, cardSizesSQL)
	if err != nil {
		return nil, fmt.Errorf("query card sizes: %w", err)
	}
	defer rows.Close()

	var result []CardSizeEntry
	for rows.Next() {
		var chrtID int64
		var nmID int
		var techSize, skusJSON string
		if err := rows.Scan(&chrtID, &nmID, &techSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan card sizes: %w", err)
		}

		barcode := parseFirstBarcode(skusJSON)
		if barcode == "" {
			continue // skip entries without barcode
		}

		result = append(result, CardSizeEntry{
			ChrtID:   chrtID,
			NmID:     nmID,
			TechSize: techSize,
			Barcode:  barcode,
		})
	}
	return result, rows.Err()
}

// QueryProductAttrs returns product attributes for the given nm_ids.
func (r *SourceRepo) QueryProductAttrs(ctx context.Context, nmIDs []int) (map[int]*SKURow, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	ph, args := placeholders(nmIDs)
	query := fmt.Sprintf(productAttrsSQL, ph)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query product attrs: %w", err)
	}
	defer rows.Close()

	result := make(map[int]*SKURow, len(nmIDs))
	for rows.Next() {
		var a SKURow
		if err := rows.Scan(
			&a.NmID, &a.Article, &a.Identifier, &a.VendorCode,
			&a.Name, &a.Brand, &a.Type, &a.Category, &a.CategoryLevel1, &a.CategoryLevel2,
			&a.Sex, &a.Season, &a.Color, &a.Collection,
		); err != nil {
			return nil, fmt.Errorf("scan product attrs: %w", err)
		}
		result[a.NmID] = &a
	}
	return result, rows.Err()
}

// QueryVendorCodes returns all (nm_id, vendor_code) pairs.
func (r *SourceRepo) QueryVendorCodes(ctx context.Context, nmIDs []int) ([]config.YearEntry, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(vendorCodesSQL, ph)
		args = a
	} else {
		query = vendorCodesAllSQL
		args = nil
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vendor codes: %w", err)
	}
	defer rows.Close()

	var result []config.YearEntry
	for rows.Next() {
		var e config.YearEntry
		if err := rows.Scan(&e.NmID, &e.VendorCode); err != nil {
			return nil, fmt.Errorf("scan vendor codes: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}

// QuerySupplyIncoming returns incoming stock per barcode from active supplies.
// Returns map of barcode → incoming units (quantity - ready_for_sale_quantity).
func (r *SourceRepo) QuerySupplyIncoming(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, supplyIncomingSQL)
	if err != nil {
		return nil, fmt.Errorf("query supply incoming: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var barcode string
		var incoming int64
		if err := rows.Scan(&barcode, &incoming); err != nil {
			return nil, fmt.Errorf("scan supply incoming: %w", err)
		}
		result[barcode] = incoming
	}
	return result, rows.Err()
}

// parseFirstBarcode extracts the first barcode from skus_json array.
// skus_json format: ["4630047636342"] or ["barcode1","barcode2"]
func parseFirstBarcode(skusJSON string) string {
	if skusJSON == "" || skusJSON == "[]" {
		return ""
	}
	var barcodes []string
	if err := json.Unmarshal([]byte(skusJSON), &barcodes); err != nil {
		return ""
	}
	if len(barcodes) == 0 {
		return ""
	}
	return barcodes[0]
}

// placeholders generates comma-separated "?" placeholders and args for nmIDs.
func placeholders(nmIDs []int) (string, []any) {
	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}
