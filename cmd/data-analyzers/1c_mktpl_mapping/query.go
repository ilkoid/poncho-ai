package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// --- Barcode mapping (barcode → nm_id, vendor_code, article) ---

// BarcodeMapping is one row from the barcode mapping query.
type BarcodeMapping struct {
	Barcode    string
	NmID       sql.NullInt64
	VendorCode sql.NullString
	Article    string
	GUID       string
	PimNmID    sql.NullInt64
	HasWBSales bool
}

// barcodeMappingSQL builds barcode mapping from 1C SKU data.
const barcodeMappingSQL = `
WITH onec_base AS (
    SELECT sku.barcode, g.article, g.guid
    FROM onec_goods_sku sku
    INNER JOIN onec_goods g ON sku.guid = g.guid
    WHERE sku.barcode IS NOT NULL
      AND length(sku.barcode) > 0
),
wb_cards AS (
    SELECT nm_id, vendor_code
    FROM cards
),
pim_links AS (
    SELECT identifier, wb_nm_id
    FROM pim_goods
    WHERE wb_nm_id > 0
),
sales_check AS (
    SELECT DISTINCT barcode
    FROM sales
    WHERE barcode IS NOT NULL
)
SELECT
    o.barcode,
    c.nm_id,
    c.vendor_code,
    o.article,
    o.guid,
    p.wb_nm_id AS pim_nm_id,
    CASE WHEN s.barcode IS NOT NULL THEN 1 ELSE 0 END AS has_wb_sales
FROM onec_base o
LEFT JOIN wb_cards c ON o.article = c.vendor_code
LEFT JOIN pim_links p ON o.article = p.identifier
LEFT JOIN sales_check s ON o.barcode = s.barcode
ORDER BY o.article
`

// --- PIM product mapping (pim_article → nm_id, vendor_code, article) ---

// PimProductMapping is one row from the PIM product mapping query.
type PimProductMapping struct {
	PimArticle   string
	Article      sql.NullString
	GUID         sql.NullString
	NmID         sql.NullInt64
	VendorCode   sql.NullString
	PimNmID      sql.NullInt64
	Enabled      bool
	BarcodeCount int
	HasWBSales   bool
}

// pimProductMappingSQL builds product mapping from PIM data.
const pimProductMappingSQL = `
WITH pim_base AS (
    SELECT identifier, wb_nm_id, enabled
    FROM pim_goods
),
onec_data AS (
    SELECT
        g.article,
        g.guid,
        COUNT(sku.barcode) AS barcode_count
    FROM onec_goods g
    LEFT JOIN onec_goods_sku sku ON g.guid = sku.guid
        AND sku.barcode IS NOT NULL
        AND length(sku.barcode) > 0
    GROUP BY g.article, g.guid
),
wb_cards AS (
    SELECT nm_id, vendor_code
    FROM cards
),
sales_check AS (
    SELECT DISTINCT sku.guid
    FROM sales s
    INNER JOIN onec_goods_sku sku ON s.barcode = sku.barcode
    WHERE s.barcode IS NOT NULL
)
SELECT
    p.identifier    AS pim_article,
    o.article,
    o.guid,
    c.nm_id,
    c.vendor_code,
    p.wb_nm_id      AS pim_nm_id,
    p.enabled,
    COALESCE(o.barcode_count, 0) AS barcode_count,
    CASE WHEN sc.guid IS NOT NULL THEN 1 ELSE 0 END AS has_wb_sales
FROM pim_base p
LEFT JOIN onec_data o ON p.identifier = o.article
LEFT JOIN wb_cards c ON p.identifier = c.vendor_code
LEFT JOIN sales_check sc ON o.guid = sc.guid
ORDER BY p.identifier
`

// --- NM product mapping (nm_id → article, pim_article, vendor_code, enabled) ---

// NmProductMapping is one row from the NM product mapping query.
type NmProductMapping struct {
	NmID       int64
	Article    sql.NullString
	PimArticle sql.NullString
	VendorCode string
	Enabled    bool
}

// nmProductMappingSQL builds NM-centric mapping from cards.
const nmProductMappingSQL = `
SELECT
    c.nm_id,
    o.article,
    p.identifier AS pim_article,
    c.vendor_code,
    CASE WHEN p.enabled IS NULL THEN 1 ELSE p.enabled END AS enabled
FROM cards c
LEFT JOIN onec_goods o ON c.vendor_code = o.article
LEFT JOIN pim_goods p ON c.vendor_code = p.identifier
ORDER BY c.nm_id
`

// --- Statistics ---

// BarcodeStats holds diagnostic counts for barcode mapping.
type BarcodeStats struct {
	TotalBarcodes int
	MappedToNmID  int
	HasWBSales    int
}

// PimStats holds diagnostic counts for PIM product mapping.
type PimStats struct {
	TotalProducts int
	MatchedTo1C   int
	MatchedToWB   int
	HasWBSales    int
}

// NmStats holds diagnostic counts for NM product mapping.
type NmStats struct {
	TotalProducts int
	MatchedTo1C   int
	MatchedToPIM  int
}

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

// GetBarcodeMappings executes the barcode mapping query.
func (r *SourceRepo) GetBarcodeMappings(ctx context.Context) ([]BarcodeMapping, error) {
	rows, err := r.db.QueryContext(ctx, barcodeMappingSQL)
	if err != nil {
		return nil, fmt.Errorf("barcode mapping query: %w", err)
	}
	defer rows.Close()

	var result []BarcodeMapping
	for rows.Next() {
		var b BarcodeMapping
		var hasSales int
		err := rows.Scan(&b.Barcode, &b.NmID, &b.VendorCode, &b.Article, &b.GUID, &b.PimNmID, &hasSales)
		if err != nil {
			return nil, fmt.Errorf("scan barcode mapping row: %w", err)
		}
		b.HasWBSales = hasSales == 1
		result = append(result, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate barcode mapping rows: %w", err)
	}
	return result, nil
}

// GetPimProductMappings executes the PIM product mapping query.
func (r *SourceRepo) GetPimProductMappings(ctx context.Context) ([]PimProductMapping, error) {
	rows, err := r.db.QueryContext(ctx, pimProductMappingSQL)
	if err != nil {
		return nil, fmt.Errorf("pim product mapping query: %w", err)
	}
	defer rows.Close()

	var result []PimProductMapping
	for rows.Next() {
		var m PimProductMapping
		var enabled int
		var hasSales int
		err := rows.Scan(
			&m.PimArticle, &m.Article, &m.GUID,
			&m.NmID, &m.VendorCode, &m.PimNmID,
			&enabled, &m.BarcodeCount, &hasSales,
		)
		if err != nil {
			return nil, fmt.Errorf("scan pim product mapping row: %w", err)
		}
		m.Enabled = enabled == 1
		m.HasWBSales = hasSales == 1
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pim product mapping rows: %w", err)
	}
	return result, nil
}

// GetNmProductMappings executes the NM product mapping query.
func (r *SourceRepo) GetNmProductMappings(ctx context.Context) ([]NmProductMapping, error) {
	rows, err := r.db.QueryContext(ctx, nmProductMappingSQL)
	if err != nil {
		return nil, fmt.Errorf("nm product mapping query: %w", err)
	}
	defer rows.Close()

	var result []NmProductMapping
	for rows.Next() {
		var m NmProductMapping
		var enabled int
		err := rows.Scan(&m.NmID, &m.Article, &m.PimArticle, &m.VendorCode, &enabled)
		if err != nil {
			return nil, fmt.Errorf("scan nm product mapping row: %w", err)
		}
		m.Enabled = enabled == 1
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nm product mapping rows: %w", err)
	}
	return result, nil
}

// GetBarcodeStats collects diagnostic counts for barcode mapping.
func (r *SourceRepo) GetBarcodeStats(ctx context.Context) (*BarcodeStats, error) {
	var stats BarcodeStats

	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT barcode)
		FROM onec_goods_sku
		WHERE barcode IS NOT NULL AND length(barcode) > 0
	`).Scan(&stats.TotalBarcodes)
	if err != nil {
		return nil, fmt.Errorf("count total barcodes: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT sku.barcode)
		FROM onec_goods_sku sku
		INNER JOIN onec_goods g ON sku.guid = g.guid
		INNER JOIN cards c ON g.article = c.vendor_code
		WHERE sku.barcode IS NOT NULL AND length(sku.barcode) > 0
	`).Scan(&stats.MappedToNmID)
	if err != nil {
		return nil, fmt.Errorf("count mapped barcodes: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT s.barcode)
		FROM sales s
		INNER JOIN onec_goods_sku sku ON s.barcode = sku.barcode
		WHERE s.barcode IS NOT NULL
	`).Scan(&stats.HasWBSales)
	if err != nil {
		return nil, fmt.Errorf("count barcodes with sales: %w", err)
	}

	return &stats, nil
}

// GetPimStats collects diagnostic counts for PIM product mapping.
func (r *SourceRepo) GetPimStats(ctx context.Context) (*PimStats, error) {
	var stats PimStats

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pim_goods`).Scan(&stats.TotalProducts)
	if err != nil {
		return nil, fmt.Errorf("count total products: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pim_goods p
		INNER JOIN onec_goods g ON p.identifier = g.article
	`).Scan(&stats.MatchedTo1C)
	if err != nil {
		return nil, fmt.Errorf("count matched to 1C: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pim_goods p
		INNER JOIN cards c ON p.identifier = c.vendor_code
	`).Scan(&stats.MatchedToWB)
	if err != nil {
		return nil, fmt.Errorf("count matched to WB: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT p.identifier)
		FROM pim_goods p
		INNER JOIN onec_goods g ON p.identifier = g.article
		INNER JOIN onec_goods_sku sku ON g.guid = sku.guid
		INNER JOIN sales s ON sku.barcode = s.barcode
		WHERE sku.barcode IS NOT NULL
	`).Scan(&stats.HasWBSales)
	if err != nil {
		return nil, fmt.Errorf("count products with sales: %w", err)
	}

	return &stats, nil
}

// GetNmStats collects diagnostic counts for NM product mapping.
func (r *SourceRepo) GetNmStats(ctx context.Context) (*NmStats, error) {
	var stats NmStats

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cards`).Scan(&stats.TotalProducts)
	if err != nil {
		return nil, fmt.Errorf("count total nm products: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cards c
		INNER JOIN onec_goods g ON c.vendor_code = g.article
	`).Scan(&stats.MatchedTo1C)
	if err != nil {
		return nil, fmt.Errorf("count matched to 1C: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cards c
		INNER JOIN pim_goods p ON c.vendor_code = p.identifier
	`).Scan(&stats.MatchedToPIM)
	if err != nil {
		return nil, fmt.Errorf("count matched to PIM: %w", err)
	}

	return &stats, nil
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}
