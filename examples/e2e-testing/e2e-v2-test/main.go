// Package main provides E2E V2 Tools Test
//
// Автономная утилита для тестирования WB V2 tools на данных из SQLite.
// Верифицирует корректность transformation layer между JSON и SQLite.
//
// Usage:
//
//	cd examples/e2e-v2-test
//	go run main.go                                     # Использует ../e2e-twodays.db
//	go run main.go --db ../e2e-twodays.db             # Указать базу данных
//	go run main.go --test funnel                      # Запустить конкретный тест
//	go run main.go --verbose                          # Подробный вывод
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// TestResult represents the result of a single test.
type TestResult struct {
	Name     string
	Passed   bool
	Message  string
	Duration int64 // milliseconds
	Details  map[string]interface{}
}

// TestSuite represents a collection of tests for an endpoint.
type TestSuite struct {
	Name  string
	Tests []TestCase
}

// TestCase represents a single test case.
type TestCase struct {
	Name       string
	Query      string
	Validator  func(*sql.Rows) (bool, string, error)
	SkipIfEmpty bool
}

func main() {
	// Parse command-line flags
	dbPath := flag.String("db", "../e2e-twodays.db", "Path to SQLite database")
	testName := flag.String("test", "all", "Test to run (all, funnel, fullstats, feedbacks, questions)")
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	flag.Parse()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║              E2E V2 Tools Verification Test                ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Open database
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer db.Close()

	// Verify database exists and has data
	if err := verifyDatabase(db); err != nil {
		log.Fatalf("❌ Database verification failed: %v", err)
	}

	fmt.Printf("💾 Database: %s\n", *dbPath)
	fmt.Printf("🧪 Test: %s\n\n", *testName)

	// Define test suites
	suites := map[string]TestSuite{
		"funnel":         createFunnelTestSuite(),
		"funnel_history": createFunnelHistoryTestSuite(),
		"search_positions": createSearchPositionsTestSuite(),
		"fullstats":       createFullstatsTestSuite(),
		"feedbacks":       createFeedbacksTestSuite(),
		"questions":       createQuestionsTestSuite(),
		"attribution":     createAttributionTestSuite(),
	}

	// Run tests
	allResults := make([]TestResult, 0)

	if *testName == "all" {
		// Run all test suites
		for name, suite := range suites {
			fmt.Printf("\n📋 Testing: %s\n", name)
			fmt.Println(strings.Repeat("-", 50))
			results := runTestSuite(db, suite, *verbose)
			allResults = append(allResults, results...)
		}
	} else {
		// Run specific test suite
		suite, ok := suites[*testName]
		if !ok {
			log.Fatalf("❌ Unknown test: %s", *testName)
		}
		fmt.Printf("\n📋 Testing: %s\n", *testName)
		fmt.Println(strings.Repeat("-", 50))
		results := runTestSuite(db, suite, *verbose)
		allResults = append(allResults, results...)
	}

	// Print summary
	printSummary(allResults)
}

// verifyDatabase checks that the database has data.
func verifyDatabase(db *sql.DB) error {
	// Check if any table exists
	var count int
	row := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err := row.Scan(&count); err != nil {
		return fmt.Errorf("check tables: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("database has no tables - run e2e-mock-collector first")
	}

	return nil
}

// runTestSuite runs all tests in a suite.
func runTestSuite(db *sql.DB, suite TestSuite, verbose bool) []TestResult {
	results := make([]TestResult, 0, len(suite.Tests))

	for _, test := range suite.Tests {
		result := runTest(db, test, verbose)
		results = append(results, result)

		// Print result
		status := "✅ PASS"
		if !result.Passed {
			status = "❌ FAIL"
		}
		fmt.Printf("  %s %s: %s (%dms)\n", status, test.Name, result.Message, result.Duration)
	}

	return results
}

// runTest executes a single test.
func runTest(db *sql.DB, test TestCase, verbose bool) TestResult {
	result := TestResult{
		Name:    test.Name,
		Details: make(map[string]interface{}),
	}

	// Check if table has data (for SkipIfEmpty tests)
	if test.SkipIfEmpty {
		tableName := extractTableName(test.Query)
		if tableName != "" {
			var count int
			row := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName))
			if err := row.Scan(&count); err != nil || count == 0 {
				result.Passed = true
				result.Message = "Skipped (no data)"
				return result
			}
		}
	}

	// Execute query
	rows, err := db.Query(test.Query)
	if err != nil {
		result.Passed = false
		result.Message = fmt.Sprintf("Query error: %v", err)
		return result
	}
	defer rows.Close()

	// Validate results
	passed, message, err := test.Validator(rows)
	if err != nil {
		result.Passed = false
		result.Message = fmt.Sprintf("Validation error: %v", err)
		return result
	}

	result.Passed = passed
	result.Message = message

	return result
}

// createFunnelTestSuite creates tests for funnel endpoint.
func createFunnelTestSuite() TestSuite {
	return TestSuite{
		Name: "funnel",
		Tests: []TestCase{
			{
				Name:  "funnel_fetches_exist",
				Query: "SELECT id, fetched_at, record_count FROM funnel_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No funnel fetch records found", nil
					}
					var id, recordCount int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt, &recordCount); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d with %d records", id, recordCount), nil
				},
			},
			{
				Name:  "funnel_products_exist",
				Query: "SELECT COUNT(*) FROM funnel_products",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No products found", nil
					}
					var count int
					if err := rows.Scan(&count); err != nil {
						return false, "", err
					}
					if count == 0 {
						return false, "No products in database", nil
					}
					return true, fmt.Sprintf("%d products found", count), nil
				},
			},
			{
				Name:  "funnel_products_have_metrics",
				Query: "SELECT nm_id, open_count, cart_count, order_count FROM funnel_products WHERE open_count > 0 LIMIT 5",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					count := 0
					for rows.Next() {
						count++
						var nmID, openCount, cartCount, orderCount int
						if err := rows.Scan(&nmID, &openCount, &cartCount, &orderCount); err != nil {
							return false, "", err
						}
					}
					if count == 0 {
						return false, "No products with funnel metrics", nil
					}
					return true, fmt.Sprintf("%d products have funnel metrics", count), nil
				},
			},
			{
				Name:  "funnel_conversions_valid",
				Query: "SELECT nm_id, add_to_cart_percent, cart_to_order_percent, buyout_percent FROM funnel_products WHERE add_to_cart_percent > 0",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					count := 0
					invalidCount := 0
					for rows.Next() {
						count++
						var nmID int
						var addToCart, cartToOrder, buyout float64
						if err := rows.Scan(&nmID, &addToCart, &cartToOrder, &buyout); err != nil {
							return false, "", err
						}
						// Check conversion rates are reasonable (0-100%)
						if addToCart < 0 || addToCart > 100 || cartToOrder < 0 || cartToOrder > 100 || buyout < 0 || buyout > 100 {
							invalidCount++
						}
					}
					if count == 0 {
						return false, "No products with conversion metrics", nil
					}
					if invalidCount > 0 {
						return false, fmt.Sprintf("%d products have invalid conversion rates", invalidCount), nil
					}
					return true, fmt.Sprintf("All %d products have valid conversions", count), nil
				},
			},
		},
	}
}

// createFunnelHistoryTestSuite creates tests for funnel_history endpoint.
func createFunnelHistoryTestSuite() TestSuite {
	return TestSuite{
		Name: "funnel_history",
		Tests: []TestCase{
			{
				Name:  "funnel_history_fetches_exist",
				Query: "SELECT id, fetched_at FROM funnel_history_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No funnel_history fetch records", nil
					}
					var id int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d", id), nil
				},
				SkipIfEmpty: true,
			},
		},
	}
}

// createSearchPositionsTestSuite creates tests for search_positions endpoint.
func createSearchPositionsTestSuite() TestSuite {
	return TestSuite{
		Name: "search_positions",
		Tests: []TestCase{
			{
				Name:  "search_positions_fetches_exist",
				Query: "SELECT id, fetched_at FROM search_positions_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No search_positions fetch records", nil
					}
					var id int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d", id), nil
				},
				SkipIfEmpty: true,
			},
		},
	}
}

// createFullstatsTestSuite creates tests for fullstats endpoint.
func createFullstatsTestSuite() TestSuite {
	return TestSuite{
		Name: "fullstats",
		Tests: []TestCase{
			{
				Name:  "fullstats_fetches_exist",
				Query: "SELECT id, fetched_at, record_count FROM fullstats_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No fullstats fetch records found", nil
					}
					var id, recordCount int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt, &recordCount); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d with %d campaigns", id, recordCount), nil
				},
			},
			{
				Name:  "fullstats_campaigns_exist",
				Query: "SELECT COUNT(*) FROM fullstats_campaigns",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No campaigns found", nil
					}
					var count int
					if err := rows.Scan(&count); err != nil {
						return false, "", err
					}
					if count == 0 {
						return false, "No campaigns in database", nil
					}
					return true, fmt.Sprintf("%d campaigns found", count), nil
				},
			},
			{
				Name:  "fullstats_campaigns_have_metrics",
				Query: "SELECT advert_id, views, clicks, orders, sum FROM fullstats_campaigns WHERE views > 0 LIMIT 5",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					count := 0
					for rows.Next() {
						count++
						var advertID, views, clicks, orders int
						var sum float64
						if err := rows.Scan(&advertID, &views, &clicks, &orders, &sum); err != nil {
							return false, "", err
						}
					}
					if count == 0 {
						return false, "No campaigns with metrics", nil
					}
					return true, fmt.Sprintf("%d campaigns have metrics", count), nil
				},
			},
			{
				Name:  "fullstats_daily_exists",
				Query: "SELECT COUNT(*) FROM fullstats_daily",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No daily stats found", nil
					}
					var count int
					if err := rows.Scan(&count); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("%d daily records found", count), nil
				},
			},
		},
	}
}

// createFeedbacksTestSuite creates tests for feedbacks endpoint.
func createFeedbacksTestSuite() TestSuite {
	return TestSuite{
		Name: "feedbacks",
		Tests: []TestCase{
			{
				Name:  "feedbacks_fetches_exist",
				Query: "SELECT id, fetched_at, record_count FROM feedbacks_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No feedbacks fetch records found", nil
					}
					var id, recordCount int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt, &recordCount); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d with %d feedbacks", id, recordCount), nil
				},
			},
			{
				Name:  "feedbacks_items_exist",
				Query: "SELECT COUNT(*) FROM feedbacks_items",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No feedbacks found", nil
					}
					var count int
					if err := rows.Scan(&count); err != nil {
						return false, "", err
					}
					if count == 0 {
						return false, "No feedbacks in database", nil
					}
					return true, fmt.Sprintf("%d feedbacks found", count), nil
				},
			},
			{
				Name:  "feedbacks_have_product_details",
				Query: "SELECT feedback_id, nm_id, product_name, product_valuation FROM feedbacks_items WHERE nm_id > 0 LIMIT 5",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					count := 0
					for rows.Next() {
						count++
						var feedbackID, productName string
						var nmID, valuation int
						if err := rows.Scan(&feedbackID, &nmID, &productName, &valuation); err != nil {
							return false, "", err
						}
					}
					if count == 0 {
						return false, "No feedbacks with product details", nil
					}
					return true, fmt.Sprintf("%d feedbacks have product details", count), nil
				},
			},
		},
	}
}

// createQuestionsTestSuite creates tests for questions endpoint.
func createQuestionsTestSuite() TestSuite {
	return TestSuite{
		Name: "questions",
		Tests: []TestCase{
			{
				Name:  "questions_fetches_exist",
				Query: "SELECT id, fetched_at, record_count FROM questions_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No questions fetch records found", nil
					}
					var id, recordCount int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt, &recordCount); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d with %d questions", id, recordCount), nil
				},
			},
			{
				Name:  "questions_items_exist",
				Query: "SELECT COUNT(*) FROM questions_items",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No questions found", nil
					}
					var count int
					if err := rows.Scan(&count); err != nil {
						return false, "", err
					}
					if count == 0 {
						return false, "No questions in database", nil
					}
					return true, fmt.Sprintf("%d questions found", count), nil
				},
			},
		},
	}
}

// createAttributionTestSuite creates tests for attribution endpoint.
func createAttributionTestSuite() TestSuite {
	return TestSuite{
		Name: "attribution",
		Tests: []TestCase{
			{
				Name:  "attribution_fetches_exist",
				Query: "SELECT id, fetched_at FROM attribution_fetches ORDER BY id DESC LIMIT 1",
				Validator: func(rows *sql.Rows) (bool, string, error) {
					if !rows.Next() {
						return false, "No attribution fetch records", nil
					}
					var id int
					var fetchedAt string
					if err := rows.Scan(&id, &fetchedAt); err != nil {
						return false, "", err
					}
					return true, fmt.Sprintf("Found fetch #%d", id), nil
				},
				SkipIfEmpty: true,
			},
		},
	}
}

// printSummary prints a summary of all test results.
func printSummary(results []TestResult) {
	fmt.Println("\n" + "╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    TEST SUMMARY                            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	passed := 0
	failed := 0
	skipped := 0

	for _, r := range results {
		if r.Message == "Skipped (no data)" {
			skipped++
		} else if r.Passed {
			passed++
		} else {
			failed++
		}
	}

	fmt.Printf("\n  ✅ Passed:  %d\n", passed)
	fmt.Printf("  ❌ Failed:  %d\n", failed)
	fmt.Printf("  ⏭️  Skipped: %d\n", skipped)
	fmt.Printf("  📊 Total:   %d\n", len(results))

	if failed == 0 {
		fmt.Println("\n  🎉 All tests passed!")
	} else {
		fmt.Println("\n  ⚠️  Some tests failed. See details above.")

		// Print failed tests
		fmt.Println("\n  Failed tests:")
		for _, r := range results {
			if !r.Passed && r.Message != "Skipped (no data)" {
				fmt.Printf("    - %s: %s\n", r.Name, r.Message)
			}
		}
	}
}

// extractTableName extracts table name from a SQL query.
func extractTableName(query string) string {
	// Simple extraction - look for FROM clause
	upper := strings.ToUpper(query)
	fromIdx := strings.Index(upper, "FROM")
	if fromIdx == -1 {
		return ""
	}

	// Get text after FROM
	afterFrom := strings.TrimSpace(query[fromIdx+4:])
	// Get first word (table name)
	parts := strings.Fields(afterFrom)
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

// Helper to print JSON
func toJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
