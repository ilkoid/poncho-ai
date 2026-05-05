package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// BuildSalesSections строит все секции таба "Продажи".
func BuildSalesSections(db *sql.DB, filter dashboard.FilterParams) []dashboard.Section {
	var sections []dashboard.Section

	// Check if analytics DB is attached
	var attached int
	db.QueryRow("SELECT count(*) FROM pragma_database_list WHERE name = 'analytics'").Scan(&attached)
	if attached == 0 {
		log.Printf("[sales] analytics DB not attached, skipping")
		return nil
	}

	if s, err := buildSalesKPI(db, filter); err != nil {
		log.Printf("[sales] KPI error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildRevenueTrend(db, filter); err != nil {
		log.Printf("[sales] revenue-trend error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildRevenueByCategorySection(db, filter); err != nil {
		log.Printf("[sales] rev-by-category error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildRevenueByBrandSection(db, filter); err != nil {
		log.Printf("[sales] rev-by-brand error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildTopProductsSection(db, filter); err != nil {
		log.Printf("[sales] top-products error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildSalesRiskSection(db, filter); err != nil {
		log.Printf("[sales] sales-risk error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	return sections
}

func buildSalesKPI(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := querySalesKPI(f)
	dashboard.LogQuery(q, args)

	var kpi salesKPI
	err := db.QueryRow(q, args...).Scan(&kpi.Orders, &kpi.Units, &kpi.Revenue, &kpi.Payout, &kpi.AvgPrice)
	if err != nil {
		return dashboard.Section{}, fmt.Errorf("sales kpi: %w", err)
	}

	return dashboard.Section{
		ID:    "sales-kpi",
		Title: "Продажи за 30 дней",
		Type:  dashboard.SectionTypeKPI,
		Width: "w-full",
		KPIs: []dashboard.KPICard{
			{Label: "Выручка", Value: formatMoney(kpi.Revenue), Badge: "green"},
			{Label: "Выплата", Value: formatMoney(kpi.Payout)},
			{Label: "Заказов", Value: fmt.Sprintf("%d", kpi.Orders)},
			{Label: "Штук", Value: fmt.Sprintf("%d", kpi.Units)},
			{Label: "Средний чек", Value: formatMoney(kpi.AvgPrice)},
		},
	}, nil
}

func buildRevenueTrend(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryDailyRevenue(f)
	dashboard.LogQuery(q, args)

	var rows []dailyRevenueRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r dailyRevenueRow
			if err := rs.Scan(&r.Day, &r.Revenue, &r.Units); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(rows) == 0 {
		return dashboard.Section{}, nil
	}

	dates := make([]string, len(rows))
	revenue := make([]float64, len(rows))
	units := make([]int, len(rows))
	for i, r := range rows {
		dates[i] = r.Day
		revenue[i] = r.Revenue
		units[i] = r.Units
	}

	_ = units // TODO: add dual axis for units
	snippet := buildRevenueLine(dates, revenue, units)
	return dashboard.Section{
		ID:       "revenue-trend",
		Title:    "Тренд выручки за 90 дней",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildRevenueByCategorySection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryRevenueByCategory(f)
	dashboard.LogQuery(q, args)

	var labels []string
	var values []float64

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r revenueByCategoryRow
			if err := rs.Scan(&r.Category, &r.Revenue, &r.Units); err != nil {
				return err
			}
			labels = append(labels, r.Category)
			values = append(values, r.Revenue)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(labels) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildRevenueBar(labels, values, "Категория")
	return dashboard.Section{
		ID:       "revenue-by-category",
		Title:    "Выручка по категориям (30 дней)",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildRevenueByBrandSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryRevenueByBrand(f)
	dashboard.LogQuery(q, args)

	var labels []string
	var values []float64

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r revenueByBrandRow
			if err := rs.Scan(&r.Brand, &r.Revenue, &r.Units); err != nil {
				return err
			}
			labels = append(labels, r.Brand)
			values = append(values, r.Revenue)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(labels) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildRevenueBar(labels, values, "Бренд")
	return dashboard.Section{
		ID:       "revenue-by-brand",
		Title:    "Выручка по брендам (30 дней)",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildTopProductsSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryTopProducts(f)
	dashboard.LogQuery(q, args)

	td := &dashboard.TableData{
		Headers: []string{"Товар", "Бренд", "Категория", "Штук", "Выручка", "WB"},
	}

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r topProductRow
			if err := rs.Scan(&r.NmID, &r.Title, &r.Brand, &r.Category, &r.Units, &r.Revenue); err != nil {
				return err
			}
			wbLink := fmt.Sprintf("https://www.wildberries.ru/catalog/%d/detail.aspx", r.NmID)
			td.Rows = append(td.Rows, []dashboard.TableCell{
				{Text: truncate(r.Title, 50)},
				{Text: r.Brand},
				{Text: r.Category},
				{Text: strconv.Itoa(r.Units)},
				{Text: formatMoney(r.Revenue)},
				{Text: "открыть", Link: wbLink},
			})
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	return dashboard.Section{
		ID:    "top-products",
		Title: "Топ-30 товаров по выручке (30 дней)",
		Type:  dashboard.SectionTypeTable,
		Width: "w-full",
		Table: td,
	}, nil
}

func buildSalesRiskSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := querySalesRiskCorrelation(f)
	dashboard.LogQuery(q, args)

	var rows []salesRiskRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r salesRiskRow
			if err := rs.Scan(&r.NmID, &r.Brand, &r.Category, &r.StockQty, &r.MA7, &r.Revenue7d); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(rows) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildSalesRiskScatter(rows)
	return dashboard.Section{
		ID:       "sales-risk-correlation",
		Title:    "Продажи vs Риски: выручка за 7 дней vs MA-7",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

// formatMoney форматирует число как деньги с пробелами.
func formatMoney(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM₽", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.0fK₽", v/1_000)
	}
	return fmt.Sprintf("%.0f₽", v)
}

// truncate обрезает строку до maxLen символов.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
