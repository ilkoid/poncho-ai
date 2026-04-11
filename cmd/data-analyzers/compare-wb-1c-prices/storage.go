package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// priceComparisonSchema defines the bi.db table for price comparison results.
const priceComparisonSchema = `
DROP TABLE IF EXISTS price_comparison;
CREATE TABLE price_comparison (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    wb_snapshot_date    TEXT    NOT NULL,
    onec_snapshot_date  TEXT    NOT NULL,

    -- ===== ИДЕНТИФИКАЦИЯ (grain = vendor_code) =====
    vendor_code         TEXT    NOT NULL,
    nm_id               INTEGER DEFAULT 0,

    -- ===== ТОВАРНАЯ ИЕРАРХИЯ =====
    onec_type           TEXT    DEFAULT '',
    onec_category       TEXT    DEFAULT '',
    onec_category_l1    TEXT    DEFAULT '',
    onec_category_l2    TEXT    DEFAULT '',
    wb_subject_name     TEXT    DEFAULT '',

    -- ===== СЕЗОННОСТЬ И КОЛЛЕКЦИЯ =====
    season              TEXT    DEFAULT '',
    year_collection     INTEGER DEFAULT 0,
    collection          TEXT    DEFAULT '',
    minicollection      TEXT    DEFAULT '',
    naznacenie          TEXT    DEFAULT '',

    -- ===== ДЕМОГРАФИЯ =====
    sex                 TEXT    DEFAULT '',
    age_category        TEXT    DEFAULT '',

    -- ===== ТОВАРНЫЕ АТРИБУТЫ =====
    onec_brand          TEXT    DEFAULT '',
    wb_brand            TEXT    DEFAULT '',
    onec_name           TEXT    DEFAULT '',
    wb_title            TEXT    DEFAULT '',
    color               TEXT    DEFAULT '',
    country_of_origin   TEXT    DEFAULT '',
    brand_country       TEXT    DEFAULT '',

    -- ===== ЖИЗНЕННЫЙ ЦИКЛ =====
    is_sale             INTEGER DEFAULT 0,
    is_new              INTEGER DEFAULT 0,
    model_status        TEXT    DEFAULT '',
    pim_enabled         INTEGER DEFAULT 1,

    -- ===== СЫРЫЕ ЦЕНЫ 1C =====
    onec_base_price     REAL    DEFAULT 0,
    onec_spp25_price    REAL    DEFAULT 0,

    -- ===== СЫРЫЕ ЦЕНЫ WB =====
    wb_price            INTEGER DEFAULT 0,
    wb_discounted_price REAL    DEFAULT 0,
    wb_discount_pct     INTEGER DEFAULT 0,
    wb_club_price       REAL    DEFAULT 0,
    wb_club_discount    INTEGER DEFAULT 0,

    -- ===== СКЛАД =====
    stock_wb            INTEGER DEFAULT 0,
    stock_mp            INTEGER DEFAULT 0,

    -- ===== РЕЙТИНГ =====
    product_rating      REAL    DEFAULT 0,

    -- ===== РАЗНИЦЫ ЦЕН (WB - 1C) =====
    diff_base           REAL    DEFAULT 0,
    diff_discounted     REAL    DEFAULT 0,
    diff_base_pct       REAL    DEFAULT 0,
    diff_discounted_pct REAL    DEFAULT 0,

    -- ===== СТАТУС =====
    base_status         TEXT    DEFAULT '',
    disc_status         TEXT    DEFAULT '',

    -- ===== ДОПОЛНИТЕЛЬНЫЕ ЦЕНЫ 1C (финдиректор) =====
    onec_sr_price       REAL    DEFAULT 0,
    onec_special_price  REAL    DEFAULT 0,
    is_special_price    INTEGER DEFAULT 0,

    -- ===== СПП ИЗ ПРОДАЖ (фактические данные) =====
    avg_wb_spp_3d       REAL    DEFAULT 0,
    spp_source          TEXT    DEFAULT '',
    avg_wb_spp_assortment REAL  DEFAULT 0,

    -- ===== ЭФФЕКТИВНЫЙ СПП (исправленные расчёты) =====
    effective_spp        REAL    DEFAULT 0,
    spp_type             TEXT    DEFAULT '',
    onec_price_with_spp  REAL    DEFAULT 0,
    wb_price_with_spp    REAL    DEFAULT 0,

    -- ===== ПРЯМОЕ СРАВНЕНИЕ КЛИЕНТСКИХ ЦЕН (WB discounted vs 1C retail) =====
    diff_customer        REAL    DEFAULT 0,   -- wb_discounted_price - onec_base_price
    diff_customer_pct    REAL    DEFAULT 0,   -- diff_customer / onec_base_price * 100
    customer_status      TEXT    DEFAULT '',  -- match/warning/overpriced/underpriced

    -- ===== ФИНДИРЕКТОР: РЦ WB С КОШЕЛЬКОМ =====
    wb_price_with_spp_and_wallet REAL DEFAULT 0,   -- [Цена WB со скидкой]*(1-СПП)*(1-кошелёк%)
    sr_price_with_loyalty        REAL DEFAULT 0,   -- Цена СР со скидкой лояльности

    -- ===== ФИНДИРЕКТОР: ОТКЛОНЕНИЯ СР =====
    diff_sr_vs_wb_wallet        REAL DEFAULT 0,   -- СР_лояльность - РЦ_WB_кошелёк
    diff_sr_vs_wb_wallet_pct    REAL DEFAULT 0,   -- / СР_лояльность * 100
    diff_sr_vs_onec_spp         REAL DEFAULT 0,   -- СР_лояльность - ОЭК_с_СПП
    diff_sr_vs_onec_spp_pct     REAL DEFAULT 0,   -- / СР_лояльность * 100

    -- ===== ОСТАТКИ =====
    has_stock            INTEGER DEFAULT 0,   -- 1 if stock_wb + stock_mp > 0

    compared_at         TEXT    DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(vendor_code, nm_id, wb_snapshot_date, onec_snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_pc_vendor_code ON price_comparison(vendor_code);
CREATE INDEX IF NOT EXISTS idx_pc_base_status ON price_comparison(base_status);
CREATE INDEX IF NOT EXISTS idx_pc_disc_status ON price_comparison(disc_status);
CREATE INDEX IF NOT EXISTS idx_pc_brand ON price_comparison(onec_brand);
CREATE INDEX IF NOT EXISTS idx_pc_season ON price_comparison(season);
CREATE INDEX IF NOT EXISTS idx_pc_type ON price_comparison(onec_type);
CREATE INDEX IF NOT EXISTS idx_pc_category ON price_comparison(onec_category);
CREATE INDEX IF NOT EXISTS idx_pc_year ON price_comparison(year_collection);
CREATE INDEX IF NOT EXISTS idx_pc_is_sale ON price_comparison(is_sale);
CREATE INDEX IF NOT EXISTS idx_pc_dates ON price_comparison(wb_snapshot_date, onec_snapshot_date);
CREATE INDEX IF NOT EXISTS idx_pc_is_special ON price_comparison(is_special_price);
CREATE INDEX IF NOT EXISTS idx_pc_spp_source ON price_comparison(spp_source);
`

// ComparisonResult is one row in the price_comparison table.
type ComparisonResult struct {
	WBSnapshotDate    string
	OneCSnapshotDate  string
	VendorCode        string
	NmID              int
	OneCType          string
	OneCCategory      string
	OneCCategoryL1    string
	OneCCategoryL2    string
	WBSubjectName     string
	Season            string
	YearCollection    int
	Collection        string
	Minicollection    string
	Naznacenie        string
	Sex               string
	AgeCategory       string
	OneCBrand         string
	WBBrand           string
	OneCName          string
	WBTitle           string
	Color             string
	CountryOfOrigin   string
	BrandCountry      string
	IsSale            int
	IsNew             int
	ModelStatus       string
	PIMEnabled        int
	OneCBasePrice     float64
	OneCSPP25Price    float64
	WBPrice           int
	WBDiscountedPrice float64
	WBDiscountPct     int
	WBClubPrice       float64
	WBClubDiscount    int
	StockWB           int
	StockMP           int
	ProductRating     float64
	DiffBase          float64
	DiffDiscounted    float64
	DiffBasePct       float64
	DiffDiscountedPct float64
	BaseStatus        string
	DiscStatus        string
	// New fields for extended requirements
	OneCSRPrice        float64
	OneCSpecialPrice   float64
	IsSpecialPrice     int
	AvgWBSPP3d         float64
	SPPSource          string
	AvgWBSPPAssortment float64
	// Effective SPP fields (fixed calculations)
	EffectiveSPP     float64
	SPPType          string
	OneCPriceWithSPP float64
	WBPriceWithSPP   float64
	// Direct customer price comparison
	DiffCustomer     float64
	DiffCustomerPct  float64
	CustomerStatus   string
	// Finance director: WB wallet and SR loyalty
	WBPriceWithSPPAndWallet float64
	SRPriceWithLoyalty      float64
	DiffSRVsWBWallet        float64
	DiffSRVsWBWalletPct     float64
	DiffSRVsOneCSPP         float64
	DiffSRVsOneCSPPPct      float64
	// Stock flag
	HasStock         int
}

// ResultsRepo manages the bi.db — stores price comparison results.
type ResultsRepo struct {
	db *sql.DB
}

// NewResultsRepo opens/creates the bi.db with WAL mode and creates schema.
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

	if _, err := db.Exec(priceComparisonSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &ResultsRepo{db: db}, nil
}

// HasResultsForDate checks if comparison results already exist for given dates.
func (r *ResultsRepo) HasResultsForDate(ctx context.Context, wbDate, onecDate string) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM price_comparison
		 WHERE wb_snapshot_date = ? AND onec_snapshot_date = ?)`,
		wbDate, onecDate,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check existing results: %w", err)
	}
	return exists == 1, nil
}

const insertSQL = `
INSERT OR REPLACE INTO price_comparison (
    wb_snapshot_date, onec_snapshot_date,
    vendor_code, nm_id,
    onec_type, onec_category, onec_category_l1, onec_category_l2, wb_subject_name,
    season, year_collection, collection, minicollection, naznacenie,
    sex, age_category,
    onec_brand, wb_brand, onec_name, wb_title, color, country_of_origin, brand_country,
    is_sale, is_new, model_status, pim_enabled,
    onec_base_price, onec_spp25_price,
    wb_price, wb_discounted_price, wb_discount_pct, wb_club_price, wb_club_discount,
    stock_wb, stock_mp, product_rating,
    diff_base, diff_discounted, diff_base_pct, diff_discounted_pct,
    base_status, disc_status,
    onec_sr_price, onec_special_price, is_special_price,
    avg_wb_spp_3d, spp_source, avg_wb_spp_assortment,
    effective_spp, spp_type, onec_price_with_spp, wb_price_with_spp,
    diff_customer, diff_customer_pct, customer_status,
    wb_price_with_spp_and_wallet, sr_price_with_loyalty,
    diff_sr_vs_wb_wallet, diff_sr_vs_wb_wallet_pct,
    diff_sr_vs_onec_spp, diff_sr_vs_onec_spp_pct,
    has_stock,
    compared_at
) VALUES (
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?
)`

// SaveResults saves comparison results in batches.
func (r *ResultsRepo) SaveResults(ctx context.Context, results []ComparisonResult) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	batchSize := 500
	for i, res := range results {
		_, err := stmt.ExecContext(ctx,
			res.WBSnapshotDate, res.OneCSnapshotDate,
			res.VendorCode, res.NmID,
			res.OneCType, res.OneCCategory, res.OneCCategoryL1, res.OneCCategoryL2, res.WBSubjectName,
			res.Season, res.YearCollection, res.Collection, res.Minicollection, res.Naznacenie,
			res.Sex, res.AgeCategory,
			res.OneCBrand, res.WBBrand, res.OneCName, res.WBTitle, res.Color, res.CountryOfOrigin, res.BrandCountry,
			res.IsSale, res.IsNew, res.ModelStatus, res.PIMEnabled,
			res.OneCBasePrice, res.OneCSPP25Price,
			res.WBPrice, res.WBDiscountedPrice, res.WBDiscountPct, res.WBClubPrice, res.WBClubDiscount,
			res.StockWB, res.StockMP, res.ProductRating,
			res.DiffBase, res.DiffDiscounted, res.DiffBasePct, res.DiffDiscountedPct,
			res.BaseStatus, res.DiscStatus,
			res.OneCSRPrice, res.OneCSpecialPrice, res.IsSpecialPrice,
			res.AvgWBSPP3d, res.SPPSource, res.AvgWBSPPAssortment,
			res.EffectiveSPP, res.SPPType, res.OneCPriceWithSPP, res.WBPriceWithSPP,
			res.DiffCustomer, res.DiffCustomerPct, res.CustomerStatus,
			res.WBPriceWithSPPAndWallet, res.SRPriceWithLoyalty,
			res.DiffSRVsWBWallet, res.DiffSRVsWBWalletPct,
			res.DiffSRVsOneCSPP, res.DiffSRVsOneCSPPPct,
			res.HasStock,
			now,
		)
		if err != nil {
			return fmt.Errorf("insert row vendor_code=%s nm_id=%d: %w", res.VendorCode, res.NmID, err)
		}

		if (i+1)%batchSize == 0 {
			stmt.Close()
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit batch at %d: %w", i+1, err)
			}
			tx, err = r.db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin tx after batch: %w", err)
			}
			stmt, err = tx.PrepareContext(ctx, insertSQL)
			if err != nil {
				return fmt.Errorf("prepare insert after batch: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit final: %w", err)
	}
	return nil
}

// StatusCount holds count of products by status.
type StatusCount struct {
	Status string
	Count  int
}

// GetStatusCounts returns counts grouped by base_status or disc_status.
func (r *ResultsRepo) GetStatusCounts(ctx context.Context, wbDate, onecDate, statusCol string) ([]StatusCount, error) {
	col := "base_status"
	if statusCol == "disc" {
		col = "disc_status"
	}

	query := fmt.Sprintf(
		`SELECT %s, COUNT(*) FROM price_comparison
		 WHERE wb_snapshot_date = ? AND onec_snapshot_date = ?
		 GROUP BY %s ORDER BY COUNT(*) DESC`, col, col)

	rows, err := r.db.QueryContext(ctx, query, wbDate, onecDate)
	if err != nil {
		return nil, fmt.Errorf("query status counts: %w", err)
	}
	defer rows.Close()

	var result []StatusCount
	for rows.Next() {
		var sc StatusCount
		if err := rows.Scan(&sc.Status, &sc.Count); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		result = append(result, sc)
	}
	return result, nil
}

// TopDiffRow holds a row for the top-diff summary.
type TopDiffRow struct {
	VendorCode     string
	OneCName       string
	OneCBasePrice  float64
	WBPrice        int
	DiffBase       float64
	DiffBasePct    float64
	DiffDiscounted float64
}

// GetTopDiff returns the top N products by absolute price difference.
func (r *ResultsRepo) GetTopDiff(ctx context.Context, wbDate, onecDate, direction string, limit int) ([]TopDiffRow, error) {
	orderBy := "diff_base DESC"
	if direction == "underpriced" {
		orderBy = "diff_base ASC"
	}

	query := fmt.Sprintf(`SELECT vendor_code, onec_name, onec_base_price, wb_price,
	                              diff_base, diff_base_pct, diff_discounted
	                       FROM price_comparison
	                       WHERE wb_snapshot_date = ? AND onec_snapshot_date = ?
	                       ORDER BY %s LIMIT ?`, orderBy)

	rows, err := r.db.QueryContext(ctx, query, wbDate, onecDate, limit)
	if err != nil {
		return nil, fmt.Errorf("query top diff: %w", err)
	}
	defer rows.Close()

	var result []TopDiffRow
	for rows.Next() {
		var r TopDiffRow
		if err := rows.Scan(&r.VendorCode, &r.OneCName, &r.OneCBasePrice,
			&r.WBPrice, &r.DiffBase, &r.DiffBasePct, &r.DiffDiscounted); err != nil {
			return nil, fmt.Errorf("scan top diff: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}

// SPPCoverage holds counts for SPP source breakdown.
type SPPCoverage struct {
	Source string
	Count  int
}

// GetSPPCoverage returns counts by spp_source.
func (r *ResultsRepo) GetSPPCoverage(ctx context.Context, wbDate, onecDate string) ([]SPPCoverage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT spp_source, COUNT(*) FROM price_comparison
		 WHERE wb_snapshot_date = ? AND onec_snapshot_date = ?
		 GROUP BY spp_source ORDER BY COUNT(*) DESC`, wbDate, onecDate)
	if err != nil {
		return nil, fmt.Errorf("query spp coverage: %w", err)
	}
	defer rows.Close()

	var result []SPPCoverage
	for rows.Next() {
		var sc SPPCoverage
		if err := rows.Scan(&sc.Source, &sc.Count); err != nil {
			return nil, fmt.Errorf("scan spp coverage: %w", err)
		}
		result = append(result, sc)
	}
	return result, nil
}

// GetAvgSPPAssortment returns the overall average SPP for a given date pair.
func (r *ResultsRepo) GetAvgSPPAssortment(ctx context.Context, wbDate, onecDate string) (float64, error) {
	var avg float64
	err := r.db.QueryRowContext(ctx,
		`SELECT ROUND(AVG(avg_wb_spp_assortment), 2) FROM price_comparison
		 WHERE wb_snapshot_date = ? AND onec_snapshot_date = ? LIMIT 1`, wbDate, onecDate,
	).Scan(&avg)
	if err != nil {
		return 0, fmt.Errorf("query avg spp assortment: %w", err)
	}
	return avg, nil
}

// ExportCSV writes comparison results to a CSV file.
func (r *ResultsRepo) ExportCSV(ctx context.Context, wbDate, onecDate, csvPath string) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT vendor_code, nm_id, onec_type, onec_category, onec_category_l1, onec_category_l2,
		       wb_subject_name, season, year_collection, collection, minicollection, naznacenie,
		       sex, age_category, onec_brand, wb_brand, onec_name, wb_title,
		       color, country_of_origin, brand_country,
		       is_sale, is_new, model_status, pim_enabled,
		       onec_base_price, onec_spp25_price,
		       wb_price, wb_discounted_price, wb_discount_pct, wb_club_price, wb_club_discount,
		       stock_wb, stock_mp, product_rating,
		       diff_base, diff_discounted, diff_base_pct, diff_discounted_pct,
		       base_status, disc_status,
		       onec_sr_price, onec_special_price, is_special_price,
		       avg_wb_spp_3d, spp_source, avg_wb_spp_assortment,
		       effective_spp, spp_type, onec_price_with_spp, wb_price_with_spp,
		       diff_customer, diff_customer_pct, customer_status,
		       wb_price_with_spp_and_wallet, sr_price_with_loyalty,
		       diff_sr_vs_wb_wallet, diff_sr_vs_wb_wallet_pct,
		       diff_sr_vs_onec_spp, diff_sr_vs_onec_spp_pct,
		       has_stock
		FROM price_comparison
		WHERE wb_snapshot_date = ? AND onec_snapshot_date = ?
		ORDER BY vendor_code`, wbDate, onecDate)
	if err != nil {
		return 0, fmt.Errorf("query for csv: %w", err)
	}
	defer rows.Close()

	f, err := os.Create(csvPath)
	if err != nil {
		return 0, fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"vendor_code", "nm_id", "onec_type", "onec_category", "onec_category_l1", "onec_category_l2",
		"wb_subject_name", "season", "year_collection", "collection", "minicollection", "naznacenie",
		"sex", "age_category", "onec_brand", "wb_brand", "onec_name", "wb_title",
		"color", "country_of_origin", "brand_country",
		"is_sale", "is_new", "model_status", "pim_enabled",
		"onec_base_price", "onec_spp25_price",
		"wb_price", "wb_discounted_price", "wb_discount_pct", "wb_club_price", "wb_club_discount",
		"stock_wb", "stock_mp", "product_rating",
		"diff_base", "diff_discounted", "diff_base_pct", "diff_discounted_pct",
		"base_status", "disc_status",
		"onec_sr_price", "onec_special_price", "is_special_price",
		"avg_wb_spp_3d", "spp_source", "avg_wb_spp_assortment",
		"effective_spp", "spp_type", "onec_price_with_spp", "wb_price_with_spp",
			"diff_customer", "diff_customer_pct", "customer_status",
			"wb_price_with_spp_and_wallet", "sr_price_with_loyalty",
			"diff_sr_vs_wb_wallet", "diff_sr_vs_wb_wallet_pct",
			"diff_sr_vs_onec_spp", "diff_sr_vs_onec_spp_pct",
			"has_stock",
	}
	if err := w.Write(header); err != nil {
		return 0, fmt.Errorf("write csv header: %w", err)
	}

	count := 0
	for rows.Next() {
		var r ComparisonResult
		err := rows.Scan(
			&r.VendorCode, &r.NmID, &r.OneCType, &r.OneCCategory, &r.OneCCategoryL1, &r.OneCCategoryL2,
			&r.WBSubjectName, &r.Season, &r.YearCollection, &r.Collection, &r.Minicollection, &r.Naznacenie,
			&r.Sex, &r.AgeCategory, &r.OneCBrand, &r.WBBrand, &r.OneCName, &r.WBTitle,
			&r.Color, &r.CountryOfOrigin, &r.BrandCountry,
			&r.IsSale, &r.IsNew, &r.ModelStatus, &r.PIMEnabled,
			&r.OneCBasePrice, &r.OneCSPP25Price,
			&r.WBPrice, &r.WBDiscountedPrice, &r.WBDiscountPct, &r.WBClubPrice, &r.WBClubDiscount,
			&r.StockWB, &r.StockMP, &r.ProductRating,
			&r.DiffBase, &r.DiffDiscounted, &r.DiffBasePct, &r.DiffDiscountedPct,
			&r.BaseStatus, &r.DiscStatus,
			&r.OneCSRPrice, &r.OneCSpecialPrice, &r.IsSpecialPrice,
			&r.AvgWBSPP3d, &r.SPPSource, &r.AvgWBSPPAssortment,
				&r.EffectiveSPP, &r.SPPType, &r.OneCPriceWithSPP, &r.WBPriceWithSPP,
				&r.DiffCustomer, &r.DiffCustomerPct, &r.CustomerStatus,
				&r.WBPriceWithSPPAndWallet, &r.SRPriceWithLoyalty,
				&r.DiffSRVsWBWallet, &r.DiffSRVsWBWalletPct,
				&r.DiffSRVsOneCSPP, &r.DiffSRVsOneCSPPPct,
				&r.HasStock,
		)
		if err != nil {
			return count, fmt.Errorf("scan csv row: %w", err)
		}

		record := []string{
			r.VendorCode, fmt.Sprintf("%d", r.NmID), r.OneCType, r.OneCCategory, r.OneCCategoryL1, r.OneCCategoryL2,
			r.WBSubjectName, r.Season, fmt.Sprintf("%d", r.YearCollection), r.Collection, r.Minicollection, r.Naznacenie,
			r.Sex, r.AgeCategory, r.OneCBrand, r.WBBrand, r.OneCName, r.WBTitle,
			r.Color, r.CountryOfOrigin, r.BrandCountry,
			fmt.Sprintf("%d", r.IsSale), fmt.Sprintf("%d", r.IsNew), r.ModelStatus, fmt.Sprintf("%d", r.PIMEnabled),
			fmt.Sprintf("%.2f", r.OneCBasePrice), fmt.Sprintf("%.2f", r.OneCSPP25Price),
			fmt.Sprintf("%d", r.WBPrice), fmt.Sprintf("%.2f", r.WBDiscountedPrice), fmt.Sprintf("%d", r.WBDiscountPct),
			fmt.Sprintf("%.2f", r.WBClubPrice), fmt.Sprintf("%d", r.WBClubDiscount),
			fmt.Sprintf("%d", r.StockWB), fmt.Sprintf("%d", r.StockMP), fmt.Sprintf("%.1f", r.ProductRating),
			fmt.Sprintf("%.2f", r.DiffBase), fmt.Sprintf("%.2f", r.DiffDiscounted),
			fmt.Sprintf("%.1f", r.DiffBasePct), fmt.Sprintf("%.1f", r.DiffDiscountedPct),
			r.BaseStatus, r.DiscStatus,
			fmt.Sprintf("%.2f", r.OneCSRPrice), fmt.Sprintf("%.2f", r.OneCSpecialPrice), fmt.Sprintf("%d", r.IsSpecialPrice),
			fmt.Sprintf("%.2f", r.AvgWBSPP3d), r.SPPSource, fmt.Sprintf("%.2f", r.AvgWBSPPAssortment),
			fmt.Sprintf("%.2f", r.EffectiveSPP), r.SPPType, fmt.Sprintf("%.2f", r.OneCPriceWithSPP), fmt.Sprintf("%.2f", r.WBPriceWithSPP),
				fmt.Sprintf("%.2f", r.DiffCustomer), fmt.Sprintf("%.1f", r.DiffCustomerPct), r.CustomerStatus,
				fmt.Sprintf("%.2f", r.WBPriceWithSPPAndWallet), fmt.Sprintf("%.2f", r.SRPriceWithLoyalty),
				fmt.Sprintf("%.2f", r.DiffSRVsWBWallet), fmt.Sprintf("%.1f", r.DiffSRVsWBWalletPct),
				fmt.Sprintf("%.2f", r.DiffSRVsOneCSPP), fmt.Sprintf("%.1f", r.DiffSRVsOneCSPPPct),
				fmt.Sprintf("%d", r.HasStock),
		}
		if err := w.Write(record); err != nil {
			return count, fmt.Errorf("write csv row: %w", err)
		}
		count++
	}
	return count, nil
}

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}
