package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// --- Warehouse KPI ---

type warehouseKPI struct {
	TotalStock     int
	Warehouses     int
	InTransit      int
	UniqueProducts int
}

func queryWarehouseKPI(f dashboard.FilterParams) (string, []any) {
	return "SELECT COALESCE(SUM(quantity),0), COUNT(DISTINCT warehouse_name), " +
		"COALESCE(SUM(in_way_to_client),0), COUNT(DISTINCT nm_id) " +
		"FROM analytics.stocks_daily_warehouses WHERE snapshot_date = ?", []any{f.Date}
}

// --- Stock by Warehouse ---

type stockByWarehouseRow struct {
	Warehouse string
	Stock     int
	Products  int
}

func queryStockByWarehouse(f dashboard.FilterParams) (string, []any) {
	return "SELECT warehouse_name, COALESCE(SUM(quantity),0), COUNT(DISTINCT nm_id) " +
		"FROM analytics.stocks_daily_warehouses WHERE snapshot_date = ? " +
		"GROUP BY warehouse_name ORDER BY SUM(quantity) DESC LIMIT 15", []any{f.Date}
}

// --- Stock Trend ---

type stockTrendRow struct {
	Date      string
	Stock     int
	InTransit int
}

func queryStockTrend() (string, []any) {
	return "SELECT snapshot_date, COALESCE(SUM(quantity),0), COALESCE(SUM(in_way_to_client),0) " +
		"FROM analytics.stocks_daily_warehouses " +
		"GROUP BY snapshot_date ORDER BY snapshot_date", nil
}

// --- Active Supplies ---

type activeSupplyRow struct {
	SupplyID         string
	Warehouse        string
	SupplyDate       string
	FactDate         string
	Quantity         int
	Accepted         int
	ReadyForSale     int
	StatusID         int
}

func queryActiveSupplies() (string, []any) {
	return "SELECT supply_id, COALESCE(warehouse_name,''), COALESCE(supply_date,''), COALESCE(fact_date,''), " +
		"COALESCE(quantity,0), COALESCE(accepted_quantity,0), COALESCE(ready_for_sale_quantity,0), " +
		"COALESCE(status_id,0) " +
		"FROM analytics.supplies ORDER BY supply_date ASC LIMIT 30", nil
}

// --- Fill Pct by Category ---

type fillPctRow struct {
	Category string
	AvgFill  float64
}

func queryFillPct(f dashboard.FilterParams) (string, []any) {
	return BuildFilteredQuery(
		"SELECT category_level1 AS category, ROUND(AVG(fill_pct),1) AS avg_fill", f,
	).Where("GROUP BY category_level1 ORDER BY avg_fill ASC LIMIT 10", nil).Build()
}

// --- Recent Accepted Supplies ---

func queryRecentSupplies() (string, []any) {
	return "SELECT s.supply_id, COALESCE(s.warehouse_name,''), COALESCE(s.supply_date,''), " +
		"COALESCE(s.fact_date,''), COALESCE(s.accepted_quantity,0), COALESCE(s.ready_for_sale_quantity,0) " +
		"FROM analytics.supplies s WHERE s.fact_date IS NOT NULL AND s.fact_date != '' " +
		"ORDER BY s.fact_date DESC LIMIT 10", nil
}

// supplyStatusName maps status_id to human-readable name.
func supplyStatusName(statusID int) string {
	names := map[int]string{
		1: "Новая",
		2: "Принята на склад",
		3: "Принята частично",
		4: "На складе",
		5: "Завершена",
		6: "Отменена",
	}
	if name, ok := names[statusID]; ok {
		return name
	}
	return fmt.Sprintf("Статус %d", statusID)
}
