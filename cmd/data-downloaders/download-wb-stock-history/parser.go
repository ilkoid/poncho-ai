// Package main provides CSV parsing logic for WB Stock History data.
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ============================================================================
// Fixed column definitions
// ============================================================================

// Fixed columns for STOCK_HISTORY_REPORT_CSV (metrics)
var metricsFixedColumns = []string{
	"VendorCode", "Name", "NmID", "SubjectName", "BrandName", "SizeName", "ChrtID",
	"RegionName", "OfficeName", "Availability",
	"OrdersCount", "OrdersSum", "BuyoutCount", "BuyoutSum", "BuyoutPercent",
	"AvgOrders",
	"StockCount", "StockSum",
	"SaleRate", "AvgStockTurnover",
	"ToClientCount", "FromClientCount",
	"Price",
	"OfficeMissingTime",
	"LostOrdersCount", "LostOrdersSum", "LostBuyoutsCount", "LostBuyoutsSum",
	"Currency",
}

// Fixed columns for STOCK_HISTORY_DAILY_CSV
var dailyFixedColumns = []string{
	"VendorCode", "Name", "NmID", "SubjectName", "BrandName", "SizeName", "ChrtID",
	"OfficeName",
}

// ============================================================================
// Parsed row types
// ============================================================================

// MetricRow represents a parsed row from STOCK_HISTORY_REPORT_CSV.
type MetricRow struct {
	// Fixed fields
	VendorCode       string
	Name             string
	NmID             int64
	SubjectName      string
	BrandName        string
	SizeName         string
	ChrtID           int64
	RegionName       string
	OfficeName       string
	Availability     string
	OrdersCount      *int
	OrdersSum        *int
	BuyoutCount      *int
	BuyoutSum        *int
	BuyoutPercent    *int
	AvgOrders        *float64
	StockCount       *int
	StockSum         *int
	SaleRate         *int
	AvgStockTurnover *int
	ToClientCount    *int
	FromClientCount  *int
	Price            *int
	OfficeMissingTime *int
	LostOrdersCount  *float64
	LostOrdersSum    *float64
	LostBuyoutsCount *float64
	LostBuyoutsSum   *float64
	Currency         string

	// Dynamic columns (AvgOrdersByMonth_MM.YYYY)
	MonthlyData map[string]float64 // {"02.2024": 10.5, "03.2024": 15.2}
}

// DailyRow represents a parsed row from STOCK_HISTORY_DAILY_CSV.
type DailyRow struct {
	// Fixed fields
	VendorCode string
	Name       string
	NmID       int64
	SubjectName string
	BrandName  string
	SizeName   string
	ChrtID     int64
	OfficeName string

	// Dynamic columns (DD.MM.YYYY — остаток на 23:59)
	DailyData map[string]int64 // {"10.02.2024": 100, "11.02.2024": 95}
}

// ============================================================================
// CSV Parsing Functions
// ============================================================================

// ParseMetricsCSV parses STOCK_HISTORY_REPORT_CSV data.
// Confirmed: comma delimiter, UTF-8 encoding (tested 2026-03-29).
func ParseMetricsCSV(r io.Reader) ([]MetricRow, error) {
	reader := csv.NewReader(r)
	reader.Comma = ',' // WB CSV uses comma (confirmed by Python test)

	// 1. Read header
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// 2. Build column index maps
	fixedIdx := make(map[string]int)
	dynamicIdx := make([]int, 0)
	dynamicNames := make([]string, 0)

	for i, h := range headers {
		h = strings.TrimSpace(h)
		if isFixedMetricsColumn(h) {
			fixedIdx[h] = i
		} else if strings.HasPrefix(h, "AvgOrdersByMonth_") {
			dynamicIdx = append(dynamicIdx, i)
			dynamicNames = append(dynamicNames, h)
		}
	}

	// 3. Read data rows
	var rows []MetricRow
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}

		row := MetricRow{
			MonthlyData: make(map[string]float64),
		}

		// 3a. Parse fixed columns
		row.VendorCode = getString(record, fixedIdx, "VendorCode")
		row.Name = getString(record, fixedIdx, "Name")
		row.NmID = getInt64(record, fixedIdx, "NmID")
		row.SubjectName = getString(record, fixedIdx, "SubjectName")
		row.BrandName = getString(record, fixedIdx, "BrandName")
		row.SizeName = getString(record, fixedIdx, "SizeName")
		row.ChrtID = getInt64(record, fixedIdx, "ChrtID")
		row.RegionName = getString(record, fixedIdx, "RegionName")
		row.OfficeName = getString(record, fixedIdx, "OfficeName")
		row.Availability = getString(record, fixedIdx, "Availability")
		row.OrdersCount = getIntAsIntPtr(record, fixedIdx, "OrdersCount")
		row.OrdersSum = getIntAsIntPtr(record, fixedIdx, "OrdersSum")
		row.BuyoutCount = getIntAsIntPtr(record, fixedIdx, "BuyoutCount")
		row.BuyoutSum = getIntAsIntPtr(record, fixedIdx, "BuyoutSum")
		row.BuyoutPercent = getIntPtr(record, fixedIdx, "BuyoutPercent")
		row.AvgOrders = getFloat64Ptr(record, fixedIdx, "AvgOrders")
		row.StockCount = getIntAsIntPtr(record, fixedIdx, "StockCount")
		row.StockSum = getIntAsIntPtr(record, fixedIdx, "StockSum")
		row.SaleRate = getIntPtr(record, fixedIdx, "SaleRate")
		row.AvgStockTurnover = getIntPtr(record, fixedIdx, "AvgStockTurnover")
		row.ToClientCount = getIntAsIntPtr(record, fixedIdx, "ToClientCount")
		row.FromClientCount = getIntAsIntPtr(record, fixedIdx, "FromClientCount")
		row.Price = getIntAsIntPtr(record, fixedIdx, "Price")
		row.OfficeMissingTime = getIntPtr(record, fixedIdx, "OfficeMissingTime")
		row.LostOrdersCount = getFloat64Ptr(record, fixedIdx, "LostOrdersCount")
		row.LostOrdersSum = getFloat64Ptr(record, fixedIdx, "LostOrdersSum")
		row.LostBuyoutsCount = getFloat64Ptr(record, fixedIdx, "LostBuyoutsCount")
		row.LostBuyoutsSum = getFloat64Ptr(record, fixedIdx, "LostBuyoutsSum")
		row.Currency = getString(record, fixedIdx, "Currency")

		// 3b. Parse dynamic columns (AvgOrdersByMonth_MM.YYYY)
		for i, idx := range dynamicIdx {
			if idx >= len(record) {
				continue
			}
			val := strings.TrimSpace(record[idx])
			if val == "" {
				continue
			}
			colName := dynamicNames[i]
			monthKey := strings.TrimPrefix(colName, "AvgOrdersByMonth_")
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				row.MonthlyData[monthKey] = f
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// ParseDailyCSV parses STOCK_HISTORY_DAILY_CSV data.
// Confirmed: comma delimiter, UTF-8 encoding (tested 2026-03-29).
func ParseDailyCSV(r io.Reader) ([]DailyRow, error) {
	reader := csv.NewReader(r)
	reader.Comma = ',' // WB CSV uses comma (confirmed by Python test)

	// 1. Read header
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// 2. Build column index maps
	fixedIdx := make(map[string]int)
	dateColumns := make([]struct {
		Idx  int
		Date string
	}, 0)

	for i, h := range headers {
		h = strings.TrimSpace(h)
		if isFixedDailyColumn(h) {
			fixedIdx[h] = i
		} else if isDateColumn(h) {
			dateColumns = append(dateColumns, struct{ Idx int; Date string }{i, h})
		}
	}

	// 3. Read data rows
	var rows []DailyRow
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}

		row := DailyRow{
			DailyData: make(map[string]int64),
		}

		// Parse fixed columns
		row.VendorCode = getString(record, fixedIdx, "VendorCode")
		row.Name = getString(record, fixedIdx, "Name")
		row.NmID = getInt64(record, fixedIdx, "NmID")
		row.SubjectName = getString(record, fixedIdx, "SubjectName")
		row.BrandName = getString(record, fixedIdx, "BrandName")
		row.SizeName = getString(record, fixedIdx, "SizeName")
		row.ChrtID = getInt64(record, fixedIdx, "ChrtID")
		row.OfficeName = getString(record, fixedIdx, "OfficeName")

		// Parse dynamic date columns (DD.MM.YYYY)
		for _, dc := range dateColumns {
			if dc.Idx >= len(record) {
				continue
			}
			val := strings.TrimSpace(record[dc.Idx])
			if val == "" {
				continue
			}
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				row.DailyData[dc.Date] = n
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// ============================================================================
// Helper functions for parsing
// ============================================================================

func isFixedMetricsColumn(name string) bool {
	for _, c := range metricsFixedColumns {
		if c == name {
			return true
		}
	}
	return false
}

func isFixedDailyColumn(name string) bool {
	for _, c := range dailyFixedColumns {
		if c == name {
			return true
		}
	}
	return false
}

func isDateColumn(name string) bool {
	// Check DD.MM.YYYY format
	parts := strings.Split(name, ".")
	if len(parts) != 3 {
		return false
	}
	// All parts should be numeric
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

func getString(record []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[i])
}

func getInt64(record []string, idx map[string]int, name string) int64 {
	s := getString(record, idx, name)
	if s == "" {
		return 0
	}
	val, _ := strconv.ParseInt(s, 10, 64)
	return val
}

func getInt64Ptr(record []string, idx map[string]int, name string) *int64 {
	s := getString(record, idx, name)
	if s == "" {
		return nil
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &val
}

// getIntAsIntPtr parses int64 but returns *int (for DB compatibility).
// Values that overflow int32 will be nil.
func getIntAsIntPtr(record []string, idx map[string]int, name string) *int {
	s := getString(record, idx, name)
	if s == "" {
		return nil
	}
	val64, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	// Check for overflow (Go int is at least 32-bit)
	if val64 > 2147483647 || val64 < -2147483648 {
		return nil
	}
	val := int(val64)
	return &val
}

func getIntPtr(record []string, idx map[string]int, name string) *int {
	s := getString(record, idx, name)
	if s == "" {
		return nil
	}
	val, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &val
}

func getFloat64Ptr(record []string, idx map[string]int, name string) *float64 {
	s := getString(record, idx, name)
	if s == "" {
		return nil
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &val
}

// ============================================================================
// JSON conversion helpers
// ============================================================================

// MonthlyDataToJSON converts map to JSON string.
func MonthlyDataToJSON(data map[string]float64) *string {
	if len(data) == 0 {
		return nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	s := string(bytes)
	return &s
}

// DailyDataToJSON converts map to JSON string.
func DailyDataToJSON(data map[string]int64) *string {
	if len(data) == 0 {
		return nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	s := string(bytes)
	return &s
}
