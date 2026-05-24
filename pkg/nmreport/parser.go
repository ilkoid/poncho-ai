package nmreport

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseDetailCSV parses DETAIL_HISTORY_REPORT CSV into FunnelDetailRow slice.
// Expected columns: nmID, dt, openCardCount, addToCartCount, ordersCount, ordersSumRub,
// buyoutsCount, buyoutsSumRub, cancelCount, cancelSumRub, addToCartConversion,
// cartToOrderConversion, buyoutPercent, addToWishlist, currency
func ParseDetailCSV(r io.Reader) ([]FunnelDetailRow, error) {
	reader := csv.NewReader(r)
	reader.Comma = ','
	reader.LazyQuotes = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := buildIndexMap(header)

	var rows []FunnelDetailRow
	lineNum := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line %d: %w", lineNum, err)
		}
		lineNum++

		dt := strings.TrimSpace(lookup(record, col, "dt"))
		if dt == "" {
			continue
		}

		row := FunnelDetailRow{
			NmID:                  parseInt(record, col, "nmID"),
			MetricDate:            dt,
			OpenCardCount:         parseInt(record, col, "openCardCount"),
			AddToCartCount:        parseInt(record, col, "addToCartCount"),
			OrdersCount:           parseInt(record, col, "ordersCount"),
			OrdersSumRub:          parseInt(record, col, "ordersSumRub"),
			BuyoutsCount:          parseInt(record, col, "buyoutsCount"),
			BuyoutsSumRub:         parseInt(record, col, "buyoutsSumRub"),
			CancelCount:           parseInt(record, col, "cancelCount"),
			CancelSumRub:          parseInt(record, col, "cancelSumRub"),
			AddToCartConversion:   parseFloat(record, col, "addToCartConversion"),
			CartToOrderConversion: parseFloat(record, col, "cartToOrderConversion"),
			BuyoutPercent:         parseFloat(record, col, "buyoutPercent"),
			AddToWishlist:         parseInt(record, col, "addToWishlist"),
			Currency:              strings.TrimSpace(lookup(record, col, "currency")),
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// ParseGroupedCSV parses GROUPED_HISTORY_REPORT CSV into FunnelGroupedRow slice.
// Expected columns: dt, openCardCount, addToCartCount, ordersCount, ordersSumRub,
// buyoutsCount, buyoutsSumRub, cancelCount, cancelSumRub, addToCartConversion,
// cartToOrderConversion, buyoutPercent, addToWishlist, currency
func ParseGroupedCSV(r io.Reader) ([]FunnelGroupedRow, error) {
	reader := csv.NewReader(r)
	reader.Comma = ','
	reader.LazyQuotes = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := buildIndexMap(header)

	var rows []FunnelGroupedRow
	lineNum := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line %d: %w", lineNum, err)
		}
		lineNum++

		dt := strings.TrimSpace(lookup(record, col, "dt"))
		if dt == "" {
			continue
		}

		row := FunnelGroupedRow{
			MetricDate:            dt,
			OpenCardCount:         parseInt(record, col, "openCardCount"),
			AddToCartCount:        parseInt(record, col, "addToCartCount"),
			OrdersCount:           parseInt(record, col, "ordersCount"),
			OrdersSumRub:          parseInt(record, col, "ordersSumRub"),
			BuyoutsCount:          parseInt(record, col, "buyoutsCount"),
			BuyoutsSumRub:         parseInt(record, col, "buyoutsSumRub"),
			CancelCount:           parseInt(record, col, "cancelCount"),
			CancelSumRub:          parseInt(record, col, "cancelSumRub"),
			AddToCartConversion:   parseFloat(record, col, "addToCartConversion"),
			CartToOrderConversion: parseFloat(record, col, "cartToOrderConversion"),
			BuyoutPercent:         parseFloat(record, col, "buyoutPercent"),
			AddToWishlist:         parseInt(record, col, "addToWishlist"),
			Currency:              strings.TrimSpace(lookup(record, col, "currency")),
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// buildIndexMap creates column name → index map from CSV header.
func buildIndexMap(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(h)] = i
	}
	return m
}

// lookup returns trimmed value at column, or "" if not found.
func lookup(record []string, col map[string]int, name string) string {
	idx, ok := col[name]
	if !ok || idx >= len(record) {
		return ""
	}
	return record[idx]
}

func parseInt(record []string, col map[string]int, name string) int {
	v := strings.TrimSpace(lookup(record, col, name))
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

func parseFloat(record []string, col map[string]int, name string) float64 {
	v := strings.TrimSpace(lookup(record, col, name))
	if v == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	return f
}
