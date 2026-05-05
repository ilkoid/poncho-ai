package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// BuildWarehouseSections строит все секции таба "Склады".
func BuildWarehouseSections(db *sql.DB, filter dashboard.FilterParams) []dashboard.Section {
	var sections []dashboard.Section

	// Check if analytics DB is attached
	var attached int
	db.QueryRow("SELECT count(*) FROM pragma_database_list WHERE name = 'analytics'").Scan(&attached)
	if attached == 0 {
		log.Printf("[warehouse] analytics DB not attached, skipping")
		return nil
	}

	if s, err := buildWarehouseKPISection(db, filter); err != nil {
		log.Printf("[warehouse] KPI error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildStockByWarehouseSection(db, filter); err != nil {
		log.Printf("[warehouse] stock-by-wh error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildStockTrendSection(db); err != nil {
		log.Printf("[warehouse] stock-trend error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	if s, err := buildActiveSuppliesSection(db); err != nil {
		log.Printf("[warehouse] active-supplies error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildRecentSuppliesSection(db); err != nil {
		log.Printf("[warehouse] recent-supplies error: %v", err)
	} else {
		sections = append(sections, s)
	}

	if s, err := buildFillPctSection(db, filter); err != nil {
		log.Printf("[warehouse] fill-pct error: %v", err)
	} else if s.ID != "" {
		sections = append(sections, s)
	}

	return sections
}

func buildWarehouseKPISection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryWarehouseKPI(f)
	dashboard.LogQuery(q, args)

	var kpi warehouseKPI
	err := db.QueryRow(q, args...).Scan(&kpi.TotalStock, &kpi.Warehouses, &kpi.InTransit, &kpi.UniqueProducts)
	if err != nil {
		return dashboard.Section{}, fmt.Errorf("warehouse kpi: %w", err)
	}

	return dashboard.Section{
		ID:    "warehouse-kpi",
		Title: "Складские остатки",
		Type:  dashboard.SectionTypeKPI,
		Width: "w-full",
		KPIs: []dashboard.KPICard{
			{Label: "Всего на складе", Value: fmt.Sprintf("%d шт", kpi.TotalStock), Badge: "green"},
			{Label: "Складов", Value: fmt.Sprintf("%d", kpi.Warehouses)},
			{Label: "В пути к клиенту", Value: fmt.Sprintf("%d шт", kpi.InTransit)},
			{Label: "Уникальных товаров", Value: fmt.Sprintf("%d", kpi.UniqueProducts)},
		},
	}, nil
}

func buildStockByWarehouseSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryStockByWarehouse(f)
	dashboard.LogQuery(q, args)

	var labels []string
	var stock, products []int

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r stockByWarehouseRow
			if err := rs.Scan(&r.Warehouse, &r.Stock, &r.Products); err != nil {
				return err
			}
			labels = append(labels, r.Warehouse)
			stock = append(stock, r.Stock)
			products = append(products, r.Products)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(labels) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildWarehouseBar(labels, stock, products)
	return dashboard.Section{
		ID:       "stock-by-warehouse",
		Title:    "Остатки по складам",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildStockTrendSection(db *sql.DB) (dashboard.Section, error) {
	q, args := queryStockTrend()
	dashboard.LogQuery(q, args)

	var rows []stockTrendRow
	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r stockTrendRow
			if err := rs.Scan(&r.Date, &r.Stock, &r.InTransit); err != nil {
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
	stock := make([]int, len(rows))
	transit := make([]int, len(rows))
	for i, r := range rows {
		dates[i] = r.Date
		stock[i] = r.Stock
		transit[i] = r.InTransit
	}

	snippet := buildStockTrendLine(dates, stock, transit)
	return dashboard.Section{
		ID:       "stock-trend",
		Title:    "Тренд остатков",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}

func buildActiveSuppliesSection(db *sql.DB) (dashboard.Section, error) {
	q, args := queryActiveSupplies()
	dashboard.LogQuery(q, args)

	td := &dashboard.TableData{
		Headers: []string{"ID поставки", "Склад", "Дата", "Кол-во", "Принято", "Готово", "Статус"},
	}

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r activeSupplyRow
			if err := rs.Scan(&r.SupplyID, &r.Warehouse, &r.SupplyDate, &r.FactDate,
				&r.Quantity, &r.Accepted, &r.ReadyForSale, &r.StatusID); err != nil {
				return err
			}
			status := supplyStatusName(r.StatusID)
			statusClass := ""
			if r.StatusID == 5 {
				statusClass = "text-green-600 font-semibold"
			} else if r.StatusID == 6 {
				statusClass = "text-red-600 font-semibold"
			}

			td.Rows = append(td.Rows, []dashboard.TableCell{
				{Text: r.SupplyID},
				{Text: r.Warehouse},
				{Text: r.SupplyDate},
				{Text: strconv.Itoa(r.Quantity)},
				{Text: strconv.Itoa(r.Accepted)},
				{Text: strconv.Itoa(r.ReadyForSale)},
				{Text: status, Class: statusClass},
			})
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	return dashboard.Section{
		ID:    "active-supplies",
		Title: "Поставки",
		Type:  dashboard.SectionTypeTable,
		Width: "w-full",
		Table: td,
	}, nil
}

func buildRecentSuppliesSection(db *sql.DB) (dashboard.Section, error) {
	q, args := queryRecentSupplies()
	dashboard.LogQuery(q, args)

	td := &dashboard.TableData{
		Headers: []string{"ID поставки", "Склад", "Дата поставки", "Дата приёмки", "Принято", "Готово к продаже"},
	}

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var id, wh, supplyDate, factDate string
			var accepted, ready int
			if err := rs.Scan(&id, &wh, &supplyDate, &factDate, &accepted, &ready); err != nil {
				return err
			}
			td.Rows = append(td.Rows, dashboard.SimpleRow(id, wh, supplyDate, factDate,
				strconv.Itoa(accepted), strconv.Itoa(ready)))
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}

	return dashboard.Section{
		ID:    "recent-supplies",
		Title: "Последние принятые поставки",
		Type:  dashboard.SectionTypeTable,
		Width: "w-full",
		Table: td,
	}, nil
}

func buildFillPctSection(db *sql.DB, f dashboard.FilterParams) (dashboard.Section, error) {
	q, args := queryFillPct(f)
	dashboard.LogQuery(q, args)

	var categories []string
	var values []float64

	err := dashboard.QueryRows(db, q, args, func(rs *sql.Rows) error {
		for rs.Next() {
			var r fillPctRow
			if err := rs.Scan(&r.Category, &r.AvgFill); err != nil {
				return err
			}
			categories = append(categories, r.Category)
			values = append(values, r.AvgFill)
		}
		return nil
	})
	if err != nil {
		return dashboard.Section{}, err
	}
	if len(categories) == 0 {
		return dashboard.Section{}, nil
	}

	snippet := buildFillPctBar(categories, values)
	return dashboard.Section{
		ID:       "fill-pct",
		Title:    "Заполненность размерной сетки (худшие)",
		Type:     dashboard.SectionTypeChart,
		Width:    "w-full",
		Snippets: []dashboard.ChartSnippet{snippet},
	}, nil
}
