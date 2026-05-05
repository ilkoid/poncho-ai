package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// --- Sales KPI ---

type salesKPI struct {
	Orders   int
	Units    int
	Revenue  float64
	Payout   float64
	AvgPrice float64
}

func querySalesKPI(f dashboard.FilterParams) (string, []any) {
	q := "SELECT COUNT(*), COALESCE(SUM(quantity),0), COALESCE(ROUND(SUM(retail_amount),0),0), " +
		"COALESCE(ROUND(SUM(ppvz_for_pay),0),0), COALESCE(ROUND(AVG(retail_price),0),0) " +
		"FROM analytics.sales WHERE sale_dt >= date(?, '-30 days') AND is_cancel = 0"
	return q, []any{f.Date}
}

// --- Daily Revenue Trend ---

type dailyRevenueRow struct {
	Day     string
	Revenue float64
	Units   int
}

func queryDailyRevenue(f dashboard.FilterParams) (string, []any) {
	return "SELECT date(sale_dt) AS day, COALESCE(ROUND(SUM(retail_amount),0),0), COALESCE(SUM(quantity),0) " +
		"FROM analytics.sales WHERE sale_dt >= date(?, '-90 days') AND is_cancel = 0 " +
		"GROUP BY day ORDER BY day", []any{f.Date}
}

// --- Revenue by Category ---

type revenueByCategoryRow struct {
	Category string
	Revenue  float64
	Units    int
}

func queryRevenueByCategory(f dashboard.FilterParams) (string, []any) {
	return "SELECT subject_name, COALESCE(ROUND(SUM(retail_amount),0),0), COALESCE(SUM(quantity),0) " +
		"FROM analytics.sales WHERE sale_dt >= date(?, '-30 days') AND is_cancel = 0 " +
		"GROUP BY subject_name ORDER BY SUM(retail_amount) DESC LIMIT 15", []any{f.Date}
}

// --- Revenue by Brand ---

type revenueByBrandRow struct {
	Brand   string
	Revenue float64
	Units   int
}

func queryRevenueByBrand(f dashboard.FilterParams) (string, []any) {
	return "SELECT brand_name, COALESCE(ROUND(SUM(retail_amount),0),0), COALESCE(SUM(quantity),0) " +
		"FROM analytics.sales WHERE sale_dt >= date(?, '-30 days') AND is_cancel = 0 " +
		"GROUP BY brand_name ORDER BY SUM(retail_amount) DESC LIMIT 10", []any{f.Date}
}

// --- Top Products ---

type topProductRow struct {
	NmID     int
	Title    string
	Brand    string
	Category string
	Units    int
	Revenue  float64
}

func queryTopProducts(f dashboard.FilterParams) (string, []any) {
	return "SELECT s.nm_id, COALESCE(c.title, ''), COALESCE(s.brand_name, ''), COALESCE(c.subject_name, ''), " +
		"COALESCE(SUM(s.quantity),0), COALESCE(ROUND(SUM(s.retail_amount),0),0) " +
		"FROM analytics.sales s LEFT JOIN analytics.cards c ON s.nm_id = c.nm_id " +
		"WHERE s.sale_dt >= date(?, '-30 days') AND s.is_cancel = 0 " +
		"GROUP BY s.nm_id ORDER BY SUM(s.retail_amount) DESC LIMIT 30", []any{f.Date}
}

// --- Sales vs Risk Correlation ---

type salesRiskRow struct {
	NmID       int
	Brand      string
	Category   string
	StockQty   int
	MA7        float64
	Revenue7d  float64
}

func querySalesRiskCorrelation(f dashboard.FilterParams) (string, []any) {
	q := fmt.Sprintf(
		"SELECT m.nm_id, m.brand, m.category_level1, m.stock_qty, ROUND(m.ma_7,2), "+
			"COALESCE(s.rev_7, 0) "+
			"FROM ma_sku_daily m "+
			"LEFT JOIN (SELECT nm_id, ROUND(SUM(retail_amount),0) AS rev_7 FROM analytics.sales "+
			"WHERE sale_dt >= date('%s', '-7 days') AND is_cancel = 0 GROUP BY nm_id) s ON m.nm_id = s.nm_id "+
			"WHERE m.snapshot_date = '%s' AND m.ma_7 > 0 "+
			"ORDER BY RANDOM() LIMIT 500", f.Date, f.Date)
	return q, nil
}
