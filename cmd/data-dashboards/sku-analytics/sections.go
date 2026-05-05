package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// BuildRiskSections строит все секции таба "Риски SKU".
func BuildRiskSections(db *sql.DB, filter dashboard.FilterParams) []dashboard.Section {
	var sections []dashboard.Section

	if s, err := buildKPICards(db, filter); err != nil {
		log.Printf("[sections] KPI error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildRiskTrendSection(db, filter); err != nil {
		log.Printf("[sections] risk-trend error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildTreemapSection(db, filter); err != nil {
		log.Printf("[sections] treemap error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildHeatmapSection(db, filter); err != nil {
		log.Printf("[sections] heatmap error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildRiskByCategorySection(db, filter); err != nil {
		log.Printf("[sections] risk-by-category error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if filter.Region == "" {
		if s, err := buildRiskByRegionSection(db, filter); err != nil {
			log.Printf("[sections] risk-by-region error: %v", err)
		} else if s.ID != "" {
			sections = append(sections, s)
		}
	}

	if s, err := buildScatterSection(db, filter); err != nil {
		log.Printf("[sections] scatter error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildDeadStockSection(db, filter); err != nil {
		log.Printf("[sections] dead-stock error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildIdealStormSection(db, filter); err != nil {
		log.Printf("[sections] ideal-storm error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildLostRevenueSection(db, filter); err != nil {
		log.Printf("[sections] lost-revenue error: %v", err)
	} else {
		sections = append(sections, s)
	}

	return sections
}

// --- Section Builders ---

func buildKPICards(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryKPI(f)
	dashboard.LogQuery(q, args)

	var total, critical, atRisk, oos, brokenGrid int
	err := db.QueryRow(q, args...).Scan(&total, &critical, &atRisk, &oos, &brokenGrid)
	if err != nil {
		return dashboard.Section{}, fmt.Errorf("kpi query: %w", err)
	}

	cards := []dashboard.KPICard{
		{Label: "Всего SKU", Value: fmt.Sprintf("%d", total)},
		{Label: "Критичные", Value: fmt.Sprintf("%d", critical), Badge: "red"},
		{Label: "В зоне риска", Value: fmt.Sprintf("%d", atRisk), Badge: "yellow"},
		{Label: "OOS со спросом", Value: fmt.Sprintf("%d", oos), Badge: "red"},
		{Label: "Выбитая сетка", Value: fmt.Sprintf("%d", brokenGrid), Badge: "yellow"},
	}

	// Try to get yesterday's values for delta
	qPrev, argsPrev := queryKPIPrev(f)
	if qPrev != "" {
		var prevTotal, prevCritical, prevAtRisk, prevOOS, prevBrokenGrid int
		if err := db.QueryRow(qPrev, argsPrev...).Scan(&prevTotal, &prevCritical, &prevAtRisk, &prevOOS, &prevBrokenGrid); err == nil {
			cards[0] = addDelta(cards[0], total, prevTotal)
			cards[1] = addDelta(cards[1], critical, prevCritical)
			cards[2] = addDelta(cards[2], atRisk, prevAtRisk)
			cards[3] = addDelta(cards[3], oos, prevOOS)
			cards[4] = addDelta(cards[4], brokenGrid, prevBrokenGrid)
		}
	}

	return dashboard.Section{
		ID:   "kpi-overview",
		Title: "Обзор рисков",
		Type:  dashboard.SectionTypeKPI,
		Width: "w-full",
		KPIs:  cards,
	}, nil
}

func buildHeatmapSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryHeatmap(f)
	dashboard.LogQuery(q, args)

	var rows []regionCategoryRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r regionCategoryRow
			if err := rs.Scan(&r.Region, &r.Category, &r.Count); err != nil {
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

	regionSet := make(map[string]bool)
	catSet := make(map[string]bool)
	for _, r := range rows {
		regionSet[r.Region] = true
		catSet[r.Category] = true
	}
	var regions, categories []string
	for r := range regionSet {
		regions = append(regions, r)
	}
	for c := range catSet {
		categories = append(categories, c)
	}

	snippet := buildHeatmap(rows, regions, categories)
	return dashboard.Section{
		ID:       "heatmap-brand-category",
		Title:    "Тепловая карта рисков",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildRiskByCategorySection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryRiskByCategory(f)
	dashboard.LogQuery(q, args)

	var labels []string
	var critical, atRisk, oos, grid []int

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r riskByCategoryRow
			if err := rs.Scan(&r.Category, &r.Critical, &r.AtRisk, &r.OOS, &r.Grid); err != nil {
				return err
			}
			labels = append(labels, r.Category)
			critical = append(critical, r.Critical)
			atRisk = append(atRisk, r.AtRisk)
			oos = append(oos, r.OOS)
			grid = append(grid, r.Grid)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	if len(labels) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildStackedBar("category", labels, critical, atRisk, oos, grid)
	return dashboard.Section{
		ID:       "risk-by-category",
		Title:    "Риски по категориям",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildRiskByRegionSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryRiskByRegion(f)
	dashboard.LogQuery(q, args)

	var labels []string
	var critical, atRisk, oos, grid []int

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r riskByRegionRow
			if err := rs.Scan(&r.Region, &r.Critical, &r.AtRisk, &r.OOS, &r.Grid); err != nil {
				return err
			}
			labels = append(labels, r.Region)
			critical = append(critical, r.Critical)
			atRisk = append(atRisk, r.AtRisk)
			oos = append(oos, r.OOS)
			grid = append(grid, r.Grid)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	if len(labels) == 0 {
		return dashboard.Section{}, nil
	}
	snippet := buildStackedBar("region", labels, critical, atRisk, oos, grid)
	return dashboard.Section{
		ID:       "risk-by-region",
		Title:    "Риски по регионам",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildDeadStockSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryDeadStock(f)
	dashboard.LogQuery(q, args)

	var rows []deadStockRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r deadStockRow
			if err := rs.Scan(&r.Category, &r.Count); err != nil {
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

	snippet := buildDonut(rows)
	return dashboard.Section{
		ID:       "dead-stock",
		Title:    "Неликвид",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildIdealStormSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryIdealStorm(f)
	dashboard.LogQuery(q, args)

	td := &dashboard.TableData{
		Headers: []string{"Товар (размер)", "Бренд", "Категория", "ФО", "Остаток", "MA-7", "Тренд%", "SDR дней", "В пути"},
	}

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var item, brand, category, region string
			var stock int
			var ma7, trend, sdr sql.NullFloat64
			var incoming int
			if err := rs.Scan(&item, &brand, &category, &region, &stock, &ma7, &trend, &sdr, &incoming); err != nil {
				return err
			}
			td.Rows = append(td.Rows, dashboard.SimpleRow(
				item, brand, category, region,
				strconv.Itoa(stock),
				formatFloat(ma7),
				formatFloat(trend),
				formatFloat(sdr),
				strconv.Itoa(incoming),
			))
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	return dashboard.Section{
		ID:    "ideal-storm",
		Title: "Идеальный шторм: товар кончается, спрос растёт, поставок нет",
		Type:  dashboard.SectionTypeTable,
		Width: "w-full",
		Table: td,
	}, nil
}

func buildLostRevenueSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryLostRevenue(f)
	dashboard.LogQuery(q, args)

	td := &dashboard.TableData{
		Headers: []string{"Товар (размер)", "Бренд", "Категория", "ФО", "Спрос/день", "Потеря за 2нед", "В пути", "Сезон"},
	}

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var item, brand, category, region, season string
			var ma7 sql.NullFloat64
			var loss sql.NullFloat64
			var incoming int
			if err := rs.Scan(&item, &brand, &category, &region, &ma7, &loss, &incoming, &season); err != nil {
				return err
			}
			td.Rows = append(td.Rows, dashboard.SimpleRow(
				item, brand, category, region,
				formatFloat(ma7),
				formatFloat(loss),
				strconv.Itoa(incoming),
				season,
			))
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	return dashboard.Section{
		ID:    "lost-revenue",
		Title: "Упущенная выручка: товар закончился, а спрос остался",
		Type:  dashboard.SectionTypeTable,
		Width: "w-full",
		Table: td,
	}, nil
}

// formatFloat форматирует sql.NullFloat64 для отображения.
func formatFloat(v sql.NullFloat64) string {
	if !v.Valid {
		return "—"
	}
	return strconv.FormatFloat(v.Float64, 'f', 2, 64)
}

// addDelta добавляет дельту к KPI-карточке.
func addDelta(card dashboard.KPICard, current, prev int) dashboard.KPICard {
	if prev == 0 {
		return card
	}
	diff := current - prev
	pct := float64(diff) / float64(prev) * 100
	sign := "+"
	if diff < 0 {
		sign = ""
	}
	card.Delta = fmt.Sprintf("%s%d (%+.0f%% vs вчера)", sign, diff, pct)
	up := diff <= 0 // для рисков уменьшение = хорошо
	card.Up = &up
	return card
}

// --- New Sections: Trend, Treemap, Scatter ---

func buildRiskTrendSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryRiskTrends(f)
	dashboard.LogQuery(q, args)

	var rows []riskTrendRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r riskTrendRow
			if err := rs.Scan(&r.Date, &r.Critical, &r.Risk, &r.OOS); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(rows) < 2 {
		return dashboard.Section{}, nil
	}

	dates := make([]string, len(rows))
	critical := make([]int, len(rows))
	risk := make([]int, len(rows))
	oos := make([]int, len(rows))
	for i, r := range rows {
		dates[i] = r.Date
		critical[i] = r.Critical
		risk[i] = r.Risk
		oos[i] = r.OOS
	}

	snippet := buildLineChart(dates, map[string][]int{
		"Критичные": critical,
		"Риск":      risk,
		"OOS":       oos,
	}, map[string]string{
		"Критичные": "#ee6666",
		"Риск":      "#fac858",
		"OOS":       "#fc8452",
	})

	return dashboard.Section{
		ID:       "risk-trend",
		Title:    "Тренды рисков",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildTreemapSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryTreemap(f)
	dashboard.LogQuery(q, args)

	var rows []treemapRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r treemapRow
			if err := rs.Scan(&r.Brand, &r.Category, &r.Count); err != nil {
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

	snippet := buildTreemap(rows)
	return dashboard.Section{
		ID:       "risk-treemap",
		Title:    "Риски: Бренд → Категория",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildScatterSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryScatter(f)
	dashboard.LogQuery(q, args)

	var rows []scatterRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r scatterRow
			if err := rs.Scan(&r.Name, &r.Stock, &r.MA7, &r.Status, &r.NmID); err != nil {
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

	snippet := buildScatter(rows)
	return dashboard.Section{
		ID:       "stock-demand-scatter",
		Title:    "Остаток vs Спрос (клик → карточка WB)",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}
