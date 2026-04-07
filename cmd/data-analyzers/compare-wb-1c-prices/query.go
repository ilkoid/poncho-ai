package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// SourceData is one row from the cross-join query against wb-sales.db.
type SourceData struct {
	NmID            int
	VendorCode      string
	WBTitle         string
	WBBrand         string
	WBSubjectName   string
	OneCName        string
	OneCBrand       string
	OneCType        string
	OneCCategory    string
	OneCCategoryL1  string
	OneCCategoryL2  string
	Sex             string
	Season          string
	Collection      string
	Color           string
	CountryOfOrigin string
	IsSale          int
	IsNew           int
	ModelStatus     string
	YearCollection  int
	Minicollection  string
	Naznacenie      string
	AgeCategory     string
	BrandCountry    string
	PIMEnabled      int
	OneCBasePrice   float64
	OneCSPP25Price  float64
	WBPrice         int
	WBDiscountPrice float64
	WBDiscountPct   int
	WBClubPrice     float64
	WBClubDiscount  int
	StockWB         int
	StockMP         int
	ProductRating   float64
	// New fields for extended requirements
	OneCSRPrice        float64
	OneCSpecialPrice   float64
	AvgWBSPP3d         float64
	SPPSource          string
	AvgWBSPPAssortment float64
}

// UnmatchedCounts holds diagnostic counts for products without cross-system matches.
type UnmatchedCounts struct {
	OneCWithoutWB int // 1C goods with prices but no matching WB card
	WBWithoutOneC int // WB cards with prices but no matching 1C goods
}

// CompareParams holds all parameters for the cross-join query.
type CompareParams struct {
	WBDate           string
	OneCDate         string
	BasePriceType    string
	SRPriceType      string
	SpecialPriceType string
	SPPDaysBack      int
}

// comparePricesSQL is the main cross-join query with CTE for SPP and additional price types.
// Joins: onec_goods → onec_prices (ОЭК) → [onec_prices СР] → [onec_prices спец]
//
//	→ [pim_goods] → cards → product_prices → [products] → [product_spp]
const comparePricesSQL = `
WITH product_spp AS (
    SELECT
        nm_id,
        ROUND(AVG(ppvz_spp_prc), 2) AS avg_spp_3d,
        COUNT(*) AS spp_order_count
    FROM sales
    WHERE doc_type_name = 'Продажа'
      AND is_cancel = 0
      AND sale_dt >= date(?, '-3 days')
      AND ppvz_spp_prc > 0
    GROUP BY nm_id
),
overall_spp AS (
    SELECT ROUND(AVG(ppvz_spp_prc), 2) AS avg_spp_assortment
    FROM sales
    WHERE doc_type_name = 'Продажа'
      AND is_cancel = 0
      AND sale_dt >= date(?, '-3 days')
      AND ppvz_spp_prc > 0
)
SELECT
    c.nm_id,
    c.vendor_code,
    c.title         AS wb_title,
    c.brand         AS wb_brand,
    c.subject_name  AS wb_subject_name,

    g.name          AS onec_name,
    g.brand         AS onec_brand,
    g.type          AS onec_type,
    g.category      AS onec_category,
    g.category_level1,
    g.category_level2,
    g.sex,
    g.season,
    g.collection,
    g.color,
    g.country_of_origin,
    g.is_sale,
    g.is_new,
    g.model_status,

    COALESCE(p.year_collection, 0) AS year_collection,
    COALESCE(p.minicollection, '') AS minicollection,
    COALESCE(p.naznacenie, '')     AS naznacenie,
    COALESCE(p.age_category, '')   AS age_category,
    COALESCE(p.brand_country, '')  AS brand_country,
    COALESCE(p.enabled, 1)         AS pim_enabled,

    op.price        AS onec_base_price,
    ROUND(op.price * 0.75, 2) AS onec_spp25_price,

    pp.price        AS wb_price,
    pp.discounted_price AS wb_discounted_price,
    pp.discount     AS wb_discount_pct,
    pp.club_discounted_price AS wb_club_price,
    pp.club_discount AS wb_club_discount,

    COALESCE(pr.stock_wb, 0)  AS stock_wb,
    COALESCE(pr.stock_mp, 0)  AS stock_mp,
    COALESCE(pr.product_rating, 0) AS product_rating,

    COALESCE(op_sr.price, 0)  AS onec_sr_price,
    COALESCE(op_sp.price, 0)  AS onec_special_price,

    COALESCE(spp.avg_spp_3d, oa.avg_spp_assortment) AS avg_wb_spp_3d,
    CASE
        WHEN spp.avg_spp_3d IS NULL THEN 'assortment'
        ELSE 'product'
    END AS spp_source,
    oa.avg_spp_assortment AS avg_wb_spp_assortment

FROM onec_goods g
JOIN onec_prices op
    ON g.guid = op.good_guid
    AND op.type_name = ?
    AND op.snapshot_date = ?
LEFT JOIN onec_prices op_sr
    ON g.guid = op_sr.good_guid
    AND op_sr.type_name = ?
    AND op_sr.snapshot_date = ?
LEFT JOIN onec_prices op_sp
    ON g.guid = op_sp.good_guid
    AND op_sp.type_name = ?
    AND op_sp.snapshot_date = ?
LEFT JOIN pim_goods p
    ON g.article = p.identifier
JOIN cards c
    ON g.article = c.vendor_code
JOIN product_prices pp
    ON c.nm_id = pp.nm_id
    AND pp.snapshot_date = ?
LEFT JOIN products pr
    ON c.nm_id = pr.nm_id
LEFT JOIN product_spp spp
    ON c.nm_id = spp.nm_id
CROSS JOIN overall_spp oa
WHERE g.article != ''
ORDER BY g.article
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

// GetLatestDates returns the latest snapshot dates from each system independently.
func (r *SourceRepo) GetLatestDates(ctx context.Context) (wbDate, onecDate string, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT MAX(snapshot_date) FROM product_prices`).Scan(&wbDate)
	if err != nil {
		return "", "", fmt.Errorf("get latest WB date: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT MAX(snapshot_date) FROM onec_prices`).Scan(&onecDate)
	if err != nil {
		return "", "", fmt.Errorf("get latest 1C date: %w", err)
	}

	return wbDate, onecDate, nil
}

// ComparePrices executes the cross-join query and returns matched products.
func (r *SourceRepo) ComparePrices(ctx context.Context, params CompareParams) ([]SourceData, error) {
	args := []any{
		// CTE: product_spp date and overall_spp date
		params.WBDate, params.WBDate,
		// onec_prices (ОЭК)
		params.BasePriceType, params.OneCDate,
		// onec_prices (СР)
		params.SRPriceType, params.OneCDate,
		// onec_prices (Спец цена)
		params.SpecialPriceType, params.OneCDate,
		// product_prices
		params.WBDate,
	}

	rows, err := r.db.QueryContext(ctx, comparePricesSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("compare prices query: %w", err)
	}
	defer rows.Close()

	var result []SourceData
	for rows.Next() {
		var s SourceData
		err := rows.Scan(
			&s.NmID, &s.VendorCode, &s.WBTitle, &s.WBBrand, &s.WBSubjectName,
			&s.OneCName, &s.OneCBrand, &s.OneCType, &s.OneCCategory, &s.OneCCategoryL1, &s.OneCCategoryL2,
			&s.Sex, &s.Season, &s.Collection, &s.Color, &s.CountryOfOrigin,
			&s.IsSale, &s.IsNew, &s.ModelStatus,
			&s.YearCollection, &s.Minicollection, &s.Naznacenie, &s.AgeCategory, &s.BrandCountry, &s.PIMEnabled,
			&s.OneCBasePrice, &s.OneCSPP25Price,
			&s.WBPrice, &s.WBDiscountPrice, &s.WBDiscountPct, &s.WBClubPrice, &s.WBClubDiscount,
			&s.StockWB, &s.StockMP, &s.ProductRating,
			&s.OneCSRPrice, &s.OneCSpecialPrice,
			&s.AvgWBSPP3d, &s.SPPSource, &s.AvgWBSPPAssortment,
		)
		if err != nil {
			return nil, fmt.Errorf("scan source row: %w", err)
		}
		result = append(result, s)
	}
	return result, nil
}

// CountUnmatched returns diagnostic counts of products without cross-system matches.
func (r *SourceRepo) CountUnmatched(ctx context.Context, wbDate, onecDate, basePriceType string) (*UnmatchedCounts, error) {
	var uc UnmatchedCounts

	// 1C goods with prices but no matching WB card
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM onec_goods g
		JOIN onec_prices op ON g.guid = op.good_guid
			AND op.type_name = ?
			AND op.snapshot_date = ?
		WHERE g.article != ''
		  AND NOT EXISTS (SELECT 1 FROM cards c WHERE c.vendor_code = g.article)`,
		basePriceType, onecDate,
	).Scan(&uc.OneCWithoutWB)
	if err != nil {
		return nil, fmt.Errorf("count 1C without WB: %w", err)
	}

	// WB cards with prices but no matching 1C goods
	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cards c
		JOIN product_prices pp ON c.nm_id = pp.nm_id
			AND pp.snapshot_date = ?
		WHERE c.vendor_code != ''
		  AND NOT EXISTS (SELECT 1 FROM onec_goods g WHERE g.article = c.vendor_code AND g.article != '')`,
		wbDate,
	).Scan(&uc.WBWithoutOneC)
	if err != nil {
		return nil, fmt.Errorf("count WB without 1C: %w", err)
	}

	return &uc, nil
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}
