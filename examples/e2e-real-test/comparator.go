// Package main provides JSON vs SQLite comparison functionality.
//
// Comparator reads JSON responses and compares them with SQLite data
// to verify data integrity after storage.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Comparator compares JSON data with SQLite records.
type Comparator struct {
	db *sql.DB
}

// NewComparator creates a new Comparator instance.
func NewComparator(db *sql.DB) *Comparator {
	return &Comparator{db: db}
}

// Difference represents a single difference between JSON and SQLite.
type Difference struct {
	Table     string
	RecordID  interface{}
	Field     string
	JSONValue interface{}
	DBValue   interface{}
	Type      string // "mismatch", "missing_in_db", "missing_in_json", "type_mismatch"
}

// ComparisonResult represents the result of comparing an endpoint.
type ComparisonResult struct {
	Endpoint       string
	RecordsInJSON  int
	RecordsInDB    int
	Differences    []Difference
	Passed         bool
	Summary        string
}

// CompareAll compares all endpoints.
func (c *Comparator) CompareAll(jsonData map[string][]byte) map[string]ComparisonResult {
	results := make(map[string]ComparisonResult)

	for endpoint, data := range jsonData {
		fmt.Printf("\n🔍 Comparing %s...\n", endpoint)
		results[endpoint] = c.CompareEndpoint(endpoint, data)
	}

	return results
}

// CompareEndpoint compares JSON data with SQLite for a specific endpoint.
func (c *Comparator) CompareEndpoint(endpoint string, jsonData []byte) ComparisonResult {
	result := ComparisonResult{
		Endpoint:    endpoint,
		Differences: make([]Difference, 0),
	}

	var data map[string]interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		result.Passed = false
		result.Summary = fmt.Sprintf("Failed to parse JSON: %v", err)
		return result
	}

	switch endpoint {
	case "funnel":
		return c.compareFunnel(data, result)
	case "fullstats":
		return c.compareFullstats(data, result)
	case "feedbacks":
		return c.compareFeedbacks(data, result)
	case "questions":
		return c.compareQuestions(data, result)
	default:
		return c.compareGeneric(endpoint, data, result)
	}
}

// compareFunnel compares funnel data.
func (c *Comparator) compareFunnel(data map[string]interface{}, result ComparisonResult) ComparisonResult {
	// Extract products from JSON
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No data.data in JSON"
		return result
	}

	products, ok := dataWrapper["products"].([]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No products in JSON"
		return result
	}

	result.RecordsInJSON = len(products)

	// Get records from SQLite
	rows, err := c.db.Query(`
		SELECT nm_id, vendor_code, open_count, cart_count, order_count,
		       buyout_count, cancel_count, add_to_cart_percent, buyout_percent
		FROM funnel_products ORDER BY nm_id`)
	if err != nil {
		result.Passed = false
		result.Summary = fmt.Sprintf("DB query error: %v", err)
		return result
	}
	defer rows.Close()

	// Build map from DB records
	dbRecords := make(map[int]map[string]interface{})
	for rows.Next() {
		var nmID int
		var vendorCode string
		var openCount, cartCount, orderCount, buyoutCount, cancelCount int
		var addToCartPercent, buyoutPercent float64

		if err := rows.Scan(&nmID, &vendorCode, &openCount, &cartCount, &orderCount,
			&buyoutCount, &cancelCount, &addToCartPercent, &buyoutPercent); err != nil {
			continue
		}

		dbRecords[nmID] = map[string]interface{}{
			"nmId":             nmID,
			"vendorCode":       vendorCode,
			"openCount":        openCount,
			"cartCount":        cartCount,
			"orderCount":       orderCount,
			"buyoutCount":      buyoutCount,
			"cancelCount":      cancelCount,
			"addToCartPercent": addToCartPercent,
			"buyoutPercent":    buyoutPercent,
		}
	}

	result.RecordsInDB = len(dbRecords)

	// Compare each product
	for _, p := range products {
		product, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		productInfo, _ := product["product"].(map[string]interface{})
		statistics, _ := product["statistic"].(map[string]interface{})
		selected, _ := statistics["selected"].(map[string]interface{})
		conversions, _ := selected["conversions"].(map[string]interface{})

		nmID := getInt(productInfo, "nmId")
		jsonRecord := map[string]interface{}{
			"nmId":             nmID,
			"vendorCode":       getString(productInfo, "vendorCode"),
			"openCount":        getInt(selected, "openCount"),
			"cartCount":        getInt(selected, "cartCount"),
			"orderCount":       getInt(selected, "orderCount"),
			"buyoutCount":      getInt(selected, "buyoutCount"),
			"cancelCount":      getInt(selected, "cancelCount"),
			"addToCartPercent": getFloat(conversions, "addToCartPercent"),
			"buyoutPercent":    getFloat(conversions, "buyoutPercent"),
		}

		dbRecord, exists := dbRecords[nmID]
		if !exists {
			result.Differences = append(result.Differences, Difference{
				Table:     "funnel_products",
				RecordID:  nmID,
				Type:      "missing_in_db",
				JSONValue: jsonRecord,
				DBValue:   nil,
			})
			continue
		}

		// Compare fields
		for field, jsonVal := range jsonRecord {
			dbVal := dbRecord[field]
			if !compareValues(jsonVal, dbVal) {
				result.Differences = append(result.Differences, Difference{
					Table:     "funnel_products",
					RecordID:  nmID,
					Field:     field,
					Type:      "mismatch",
					JSONValue: jsonVal,
					DBValue:   dbVal,
				})
			}
		}
	}

	// Check for records in DB but not in JSON
	for nmID := range dbRecords {
		found := false
		for _, p := range products {
			product, _ := p.(map[string]interface{})
			productInfo, _ := product["product"].(map[string]interface{})
			if getInt(productInfo, "nmId") == nmID {
				found = true
				break
			}
		}
		if !found {
			result.Differences = append(result.Differences, Difference{
				Table:     "funnel_products",
				RecordID:  nmID,
				Type:      "missing_in_json",
				JSONValue: nil,
				DBValue:   dbRecords[nmID],
			})
		}
	}

	result.Passed = len(result.Differences) == 0
	result.Summary = fmt.Sprintf("JSON: %d records, DB: %d records, %d differences",
		result.RecordsInJSON, result.RecordsInDB, len(result.Differences))

	return result
}

// compareFullstats compares campaign fullstats data.
func (c *Comparator) compareFullstats(data map[string]interface{}, result ComparisonResult) ComparisonResult {
	// Fullstats returns array directly or in data field
	var campaigns []interface{}
	if arr, ok := data["data"].([]interface{}); ok {
		campaigns = arr
	} else if arr, ok := data[""].([]interface{}); ok {
		campaigns = arr
	}

	result.RecordsInJSON = len(campaigns)

	// Get records from SQLite
	rows, err := c.db.Query(`
		SELECT advert_id, views, clicks, orders, sum
		FROM fullstats_campaigns ORDER BY advert_id`)
	if err != nil {
		result.Passed = false
		result.Summary = fmt.Sprintf("DB query error: %v", err)
		return result
	}
	defer rows.Close()

	// Build map from DB records
	dbRecords := make(map[int]map[string]interface{})
	for rows.Next() {
		var advertID, views, clicks, orders int
		var sum float64

		if err := rows.Scan(&advertID, &views, &clicks, &orders, &sum); err != nil {
			continue
		}

		dbRecords[advertID] = map[string]interface{}{
			"advertId": advertID,
			"views":    views,
			"clicks":   clicks,
			"orders":   orders,
			"sum":      sum,
		}
	}

	result.RecordsInDB = len(dbRecords)

	// Compare each campaign
	for _, c := range campaigns {
		campaign, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		advertID := getInt(campaign, "advertId")
		jsonRecord := map[string]interface{}{
			"advertId": advertID,
			"views":    getInt(campaign, "views"),
			"clicks":   getInt(campaign, "clicks"),
			"orders":   getInt(campaign, "orders"),
			"sum":      getFloat(campaign, "sum"),
		}

		dbRecord, exists := dbRecords[advertID]
		if !exists {
			result.Differences = append(result.Differences, Difference{
				Table:     "fullstats_campaigns",
				RecordID:  advertID,
				Type:      "missing_in_db",
				JSONValue: jsonRecord,
				DBValue:   nil,
			})
			continue
		}

		// Compare fields
		for field, jsonVal := range jsonRecord {
			dbVal := dbRecord[field]
			if !compareValues(jsonVal, dbVal) {
				result.Differences = append(result.Differences, Difference{
					Table:     "fullstats_campaigns",
					RecordID:  advertID,
					Field:     field,
					Type:      "mismatch",
					JSONValue: jsonVal,
					DBValue:   dbVal,
				})
			}
		}
	}

	result.Passed = len(result.Differences) == 0
	result.Summary = fmt.Sprintf("JSON: %d campaigns, DB: %d campaigns, %d differences",
		result.RecordsInJSON, result.RecordsInDB, len(result.Differences))

	return result
}

// compareFeedbacks compares feedbacks data.
func (c *Comparator) compareFeedbacks(data map[string]interface{}, result ComparisonResult) ComparisonResult {
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No data.data in JSON"
		return result
	}

	feedbacks, ok := dataWrapper["feedbacks"].([]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No feedbacks in JSON"
		return result
	}

	result.RecordsInJSON = len(feedbacks)

	// Get records from SQLite
	rows, err := c.db.Query(`
		SELECT feedback_id, nm_id, product_valuation
		FROM feedbacks_items ORDER BY id`)
	if err != nil {
		result.Passed = false
		result.Summary = fmt.Sprintf("DB query error: %v", err)
		return result
	}
	defer rows.Close()

	// Count DB records
	dbCount := 0
	for rows.Next() {
		dbCount++
	}
	result.RecordsInDB = dbCount

	// For feedbacks, just compare counts (full comparison would need ID matching)
	if result.RecordsInJSON != result.RecordsInDB {
		result.Differences = append(result.Differences, Difference{
			Table:     "feedbacks_items",
			Field:     "count",
			Type:      "mismatch",
			JSONValue: result.RecordsInJSON,
			DBValue:   result.RecordsInDB,
		})
	}

	result.Passed = len(result.Differences) == 0
	result.Summary = fmt.Sprintf("JSON: %d feedbacks, DB: %d feedbacks, %d differences",
		result.RecordsInJSON, result.RecordsInDB, len(result.Differences))

	return result
}

// compareQuestions compares questions data.
func (c *Comparator) compareQuestions(data map[string]interface{}, result ComparisonResult) ComparisonResult {
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No data.data in JSON"
		return result
	}

	questions, ok := dataWrapper["questions"].([]interface{})
	if !ok {
		result.Passed = false
		result.Summary = "No questions in JSON"
		return result
	}

	result.RecordsInJSON = len(questions)

	// Get records from SQLite
	rows, err := c.db.Query(`SELECT COUNT(*) FROM questions_items`)
	if err != nil {
		result.Passed = false
		result.Summary = fmt.Sprintf("DB query error: %v", err)
		return result
	}
	defer rows.Close()

	if rows.Next() {
		rows.Scan(&result.RecordsInDB)
	}

	// Compare counts
	if result.RecordsInJSON != result.RecordsInDB {
		result.Differences = append(result.Differences, Difference{
			Table:     "questions_items",
			Field:     "count",
			Type:      "mismatch",
			JSONValue: result.RecordsInJSON,
			DBValue:   result.RecordsInDB,
		})
	}

	result.Passed = len(result.Differences) == 0
	result.Summary = fmt.Sprintf("JSON: %d questions, DB: %d questions, %d differences",
		result.RecordsInJSON, result.RecordsInDB, len(result.Differences))

	return result
}

// compareGeneric compares generic endpoint data.
func (c *Comparator) compareGeneric(endpoint string, data map[string]interface{}, result ComparisonResult) ComparisonResult {
	// Count records in JSON
	jsonCount := countRecords(data)
	result.RecordsInJSON = jsonCount

	// Count records in DB
	tableName := endpoint + "_fetches"
	rows, err := c.db.Query(fmt.Sprintf("SELECT SUM(record_count) FROM %s", tableName))
	if err == nil {
		defer rows.Close()
		if rows.Next() {
			rows.Scan(&result.RecordsInDB)
		}
	}

	result.Passed = result.RecordsInJSON == result.RecordsInDB
	result.Summary = fmt.Sprintf("JSON: %d records, DB: %d records",
		result.RecordsInJSON, result.RecordsInDB)

	return result
}

// GenerateReport generates a comparison report.
func (c *Comparator) GenerateReport(results map[string]ComparisonResult) string {
	var sb strings.Builder

	sb.WriteString("\n")
	sb.WriteString("╔════════════════════════════════════════════════════════════╗\n")
	sb.WriteString("║              JSON vs SQLite Comparison Report              ║\n")
	sb.WriteString("╚════════════════════════════════════════════════════════════╝\n")
	sb.WriteString("\n")

	// Summary table
	sb.WriteString("┌──────────────────┬─────────┬─────────┬────────────┬────────┐\n")
	sb.WriteString("│ Endpoint         │ JSON    │ SQLite  │ Differences│ Status │\n")
	sb.WriteString("├──────────────────┼─────────┼─────────┼────────────┼────────┤\n")

	for endpoint, result := range results {
		status := "✅"
		if !result.Passed {
			status = "❌"
		}
		sb.WriteString(fmt.Sprintf("│ %-16s │ %7d │ %7d │ %10d │ %s     │\n",
			endpoint, result.RecordsInJSON, result.RecordsInDB, len(result.Differences), status))
	}

	sb.WriteString("└──────────────────┴─────────┴─────────┴────────────┴────────┘\n")

	// Detailed differences
	hasDifferences := false
	for endpoint, result := range results {
		if len(result.Differences) > 0 {
			hasDifferences = true
			sb.WriteString(fmt.Sprintf("\n📋 %s - %d differences:\n", endpoint, len(result.Differences)))

			for i, diff := range result.Differences {
				if i >= 10 {
					sb.WriteString(fmt.Sprintf("   ... and %d more differences\n", len(result.Differences)-10))
					break
				}

				switch diff.Type {
				case "mismatch":
					sb.WriteString(fmt.Sprintf("   ❌ Record %v, field '%s': JSON=%v, DB=%v\n",
						diff.RecordID, diff.Field, diff.JSONValue, diff.DBValue))
				case "missing_in_db":
					sb.WriteString(fmt.Sprintf("   ⚠️  Record %v: exists in JSON but not in DB\n", diff.RecordID))
				case "missing_in_json":
					sb.WriteString(fmt.Sprintf("   ⚠️  Record %v: exists in DB but not in JSON\n", diff.RecordID))
				}
			}
		}
	}

	// Overall summary
	sb.WriteString("\n")
	totalDifferences := 0
	passedCount := 0
	for _, result := range results {
		totalDifferences += len(result.Differences)
		if result.Passed {
			passedCount++
		}
	}

	sb.WriteString(fmt.Sprintf("📊 Summary: %d/%d endpoints passed, %d total differences\n",
		passedCount, len(results), totalDifferences))

	if !hasDifferences {
		sb.WriteString("\n✅ All data matches perfectly between JSON and SQLite!\n")
	}

	return sb.String()
}

// Helper functions

func countRecords(data map[string]interface{}) int {
	count := 0
	for _, v := range data {
		switch val := v.(type) {
		case []interface{}:
			count += len(val)
		case map[string]interface{}:
			count += countRecords(val)
		}
	}
	return count
}

func compareValues(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Convert to comparable types
	aFloat, aIsFloat := toFloat(a)
	bFloat, bIsFloat := toFloat(b)

	if aIsFloat && bIsFloat {
		// Allow small floating point differences
		diff := aFloat - bFloat
		if diff < 0 {
			diff = -diff
		}
		return diff < 0.01
	}

	return reflect.DeepEqual(a, b)
}

func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	default:
		return 0, false
	}
}

func getInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func getString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
