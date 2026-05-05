package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// BuildFilteredQuery создаёт QueryBuilder с базовыми условиями фильтрации.
func BuildFilteredQuery(baseSelect string, f dashboard.FilterParams) *dashboard.QueryBuilder {
	qb := dashboard.NewQuery(baseSelect + " FROM ma_sku_daily WHERE 1=1")
	qb.Where("AND snapshot_date = ?", f.Date)
	qb.Where("AND brand = ?", f.Brand)
	qb.Where("AND category_level1 = ?", f.Category)
	qb.Where("AND region_name = ?", f.Region)
	if f.RiskOnly {
		qb.Where("AND (critical=1 OR risk=1 OR out_of_stock=1)", true)
	}
	return qb
}

// --- KPI ---

func queryKPI(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT COUNT(*) AS total, "+
			"SUM(CASE WHEN critical=1 THEN 1 ELSE 0 END) AS critical, "+
			"SUM(CASE WHEN risk=1 THEN 1 ELSE 0 END) AS at_risk, "+
			"SUM(CASE WHEN out_of_stock=1 AND ma_7>0 THEN 1 ELSE 0 END) AS oos, "+
			"SUM(CASE WHEN broken_grid=1 THEN 1 ELSE 0 END) AS broken_grid",
		f,
	).Build()
}

// queryKPIPrev возвращает KPI за предыдущую дату (для дельты).
func queryKPIPrev(f dashboard.FilterParams) (string, []any) {
	if f.Date == "" {
		return "", nil
	}
	prevDate := fmt.Sprintf("(SELECT MAX(snapshot_date) FROM ma_sku_daily WHERE snapshot_date < '%s')", f.Date)
	qb := dashboard.NewQuery(
		"SELECT COUNT(*) AS total, " +
			"SUM(CASE WHEN critical=1 THEN 1 ELSE 0 END) AS critical, " +
			"SUM(CASE WHEN risk=1 THEN 1 ELSE 0 END) AS at_risk, " +
			"SUM(CASE WHEN out_of_stock=1 AND ma_7>0 THEN 1 ELSE 0 END) AS oos, " +
			"SUM(CASE WHEN broken_grid=1 THEN 1 ELSE 0 END) AS broken_grid " +
			"FROM ma_sku_daily WHERE snapshot_date = " + prevDate)
	qb.Where("AND brand = ?", f.Brand)
	qb.Where("AND category_level1 = ?", f.Category)
	qb.Where("AND region_name = ?", f.Region)
	if f.RiskOnly {
		qb.Where("AND (critical=1 OR risk=1 OR out_of_stock=1)", true)
	}
	return qb.Build()
}

// --- Risk Trends (Line Chart) ---

type riskTrendRow struct {
	Date     string
	Critical int
	Risk     int
	OOS      int
}

func queryRiskTrends(f dashboard.FilterParams) (string, []any) {
	qb := dashboard.NewQuery(
		"SELECT snapshot_date, "+
			"SUM(CASE WHEN critical=1 THEN 1 ELSE 0 END) AS critical, "+
			"SUM(CASE WHEN risk=1 THEN 1 ELSE 0 END) AS risk, "+
			"SUM(CASE WHEN out_of_stock=1 AND ma_7>0 THEN 1 ELSE 0 END) AS oos "+
			"FROM ma_sku_daily WHERE 1=1")
	qb.Where("AND brand = ?", f.Brand)
	qb.Where("AND category_level1 = ?", f.Category)
	qb.Where("AND region_name = ?", f.Region)
	qb.Where("GROUP BY snapshot_date ORDER BY snapshot_date", nil)
	return qb.Build()
}

// --- Treemap: Brand x Category ---

type treemapRow struct {
	Brand    string
	Category string
	Count    int
}

func queryTreemap(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT brand, category_level1 AS category, "+
			"SUM(CASE WHEN critical=1 OR risk=1 THEN 1 ELSE 0 END) AS cnt",
		f,
	).Where("AND (critical=1 OR risk=1)", true).
		Where("GROUP BY brand, category_level1", nil).Build()
}

// --- Heatmap: Region x Category ---

type regionCategoryRow struct {
	Region   string
	Category string
	Count    int
}

func queryHeatmap(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT region_name, category_level1 AS category, "+
			"SUM(CASE WHEN critical=1 OR risk=1 THEN 1 ELSE 0 END) AS cnt",
		f,
	).Where("AND (critical=1 OR risk=1)", true).
		Where("GROUP BY region_name, category_level1 ORDER BY cnt DESC LIMIT 30", nil).Build()
}

// --- Stacked Bar: Risk by Category ---

type riskByCategoryRow struct {
	Category string
	Critical int
	AtRisk   int
	OOS      int
	Grid     int
}

func queryRiskByCategory(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT category_level1 AS category, "+
			"SUM(CASE WHEN critical=1 THEN 1 ELSE 0 END) AS critical, "+
			"SUM(CASE WHEN risk=1 AND critical=0 THEN 1 ELSE 0 END) AS at_risk, "+
			"SUM(CASE WHEN out_of_stock=1 AND ma_7>0 THEN 1 ELSE 0 END) AS oos, "+
			"SUM(CASE WHEN broken_grid=1 THEN 1 ELSE 0 END) AS broken_grid",
		f,
	).Where("GROUP BY category_level1", nil).
		Where("HAVING critical+at_risk+oos+broken_grid > 0", nil).
		Where("ORDER BY (critical+at_risk+oos) DESC LIMIT 10", nil).Build()
}

// --- Stacked Bar: Risk by Region ---

type riskByRegionRow struct {
	Region   string
	Critical int
	AtRisk   int
	OOS      int
	Grid     int
}

func queryRiskByRegion(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT region_name, "+
			"SUM(CASE WHEN critical=1 THEN 1 ELSE 0 END) AS critical, "+
			"SUM(CASE WHEN risk=1 AND critical=0 THEN 1 ELSE 0 END) AS at_risk, "+
			"SUM(CASE WHEN out_of_stock=1 AND ma_7>0 THEN 1 ELSE 0 END) AS oos, "+
			"SUM(CASE WHEN broken_grid=1 THEN 1 ELSE 0 END) AS broken_grid",
		f,
	).Where("GROUP BY region_name", nil).
		Where("HAVING critical+at_risk+oos+broken_grid > 0", nil).
		Where("ORDER BY (critical+at_risk+oos) DESC LIMIT 10", nil).Build()
}

// --- Scatter: Stock vs Demand ---

type scatterRow struct {
	Name    string
	Stock   int
	MA7     float64
	Status  string
	NmID    int
}

func queryScatter(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT name, stock_qty, ROUND(ma_7,2), "+
			"CASE WHEN critical=1 THEN 'critical' "+
			"WHEN risk=1 THEN 'risk' "+
			"WHEN out_of_stock=1 THEN 'oos' "+
			"ELSE 'ok' END AS status, "+
			"nm_id",
		f,
	).Where("AND ma_7 > 0", true).
		Where("ORDER BY RANDOM() LIMIT 500", nil).Build()
}

// --- Donut: Dead Stock ---

type deadStockRow struct {
	Category string
	Count    int
}

func queryDeadStock(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT category_level1 AS category, COUNT(*) AS cnt",
		f,
	).Where("AND stock_qty >= 30", true).
		Where("AND (ma_28 IS NULL OR ma_28 < 0.1)", true).
		Where("GROUP BY category_level1 ORDER BY cnt DESC LIMIT 10", nil).Build()
}

// --- Table: Ideal Storm ---

func queryIdealStorm(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		fmt.Sprintf(
			"SELECT name || ' (' || tech_size || ')' AS item, brand, category_level1 AS category, "+
				"region_name, stock_qty, ROUND(ma_7,2), ROUND(trend_pct,0), ROUND(sdr_days,2), "+
				"COALESCE(supply_incoming,0)"),
		f,
	).Where("AND critical = 1", true).
		Where("AND (supply_incoming IS NULL OR supply_incoming = 0)", true).
		Where("AND (trend_pct IS NULL OR trend_pct > 0)", true).
		Where("ORDER BY sdr_days ASC LIMIT 30", nil).Build()
}

// --- Table: Lost Revenue ---

func queryLostRevenue(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT name || ' (' || tech_size || ')' AS item, brand, category_level1 AS category, "+
			"region_name, ROUND(ma_7,2), ROUND(ma_7*14,0), COALESCE(supply_incoming,0), season",
		f,
	).Where("AND out_of_stock = 1", true).
		Where("AND ma_7 > 0", true).
		Where("ORDER BY ma_7 DESC LIMIT 30", nil).Build()
}
