// Package main provides a database inspection tool for WB sales data.
// READ-ONLY utility for diagnosing download issues.
//
// Usage:
//
//	go run main.go --db /path/to/sales.db [--from 2026-01-01] [--to 2026-01-31]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Flags
var (
	dbPath    = flag.String("db", "", "Path to SQLite database (required)")
	tableType = flag.String("table", "all", "Table type: sales, service, funnel, feedbacks, quality, all")
	fromDate  = flag.String("from", "", "Start date (YYYY-MM-DD)")
	toDate    = flag.String("to", "", "End date (YYYY-MM-DD)")
)

func main() {
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --db flag is required")
		flag.Usage()
		os.Exit(1)
	}

	// Validate table type
	validTables := map[string]bool{"sales": true, "service": true, "funnel": true, "feedbacks": true, "quality": true, "all": true}
	if !validTables[*tableType] {
		fmt.Fprintf(os.Stderr, "Error: invalid table type '%s'. Use: sales, service, funnel, feedbacks, quality, or all\n", *tableType)
		os.Exit(1)
	}

	// Check file exists and get size
	info, err := os.Stat(*dbPath)
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: database file not found: %s\n", *dbPath)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access database: %v\n", err)
		os.Exit(1)
	}

	// Print warning banner
	fmt.Println("============================================================")
	fmt.Println("     DATABASE INSPECTION TOOL (READ-ONLY MODE)")
	fmt.Println("============================================================")
	fmt.Printf("   File: %s\n", *dbPath)
	fmt.Printf("   Size: %s\n", formatBytes(info.Size()))
	fmt.Printf("   Modified: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
	fmt.Println("============================================================")
	fmt.Println()

	// Open database in READ-ONLY mode
	connStr := fmt.Sprintf("file:%s?mode=ro", *dbPath)
	db, err := sql.Open("sqlite3", connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	// Run integrity check
	runIntegrityCheck(ctx, db)

	// Print period info
	printPeriodInfo(ctx, db)

	// Inspect tables based on selection
	if *tableType == "all" || *tableType == "sales" {
		inspectSalesTable(ctx, db)
	}
	if *tableType == "all" || *tableType == "service" {
		inspectServiceTable(ctx, db)
	}
	if *tableType == "all" || *tableType == "funnel" {
		inspectFunnelTable(ctx, db)
	}
	if *tableType == "all" || *tableType == "feedbacks" {
		inspectFeedbacksTable(ctx, db)
	}
	if *tableType == "all" || *tableType == "quality" {
		inspectQualityTable(ctx, db)
	}
}

// formatBytes converts bytes to human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// runIntegrityCheck runs SQLite integrity check
func runIntegrityCheck(ctx context.Context, db *sql.DB) {
	var result string
	err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result)
	if err != nil {
		fmt.Printf("❌ Integrity check failed: %v\n\n", err)
		return
	}
	if result == "ok" {
		fmt.Println("✅ Database integrity: OK")
	} else {
		fmt.Printf("⚠️  Database integrity: %s\n", result)
	}
	fmt.Println()
}

// printPeriodInfo shows date range of data
func printPeriodInfo(ctx context.Context, db *sql.DB) {
	// Check if sales table exists
	var salesExists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sales'",
	).Scan(&salesExists)
	if err != nil || salesExists == 0 {
		return
	}

	var firstDT, lastDT sql.NullString
	db.QueryRowContext(ctx, "SELECT MIN(rr_dt) FROM sales").Scan(&firstDT)
	db.QueryRowContext(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)

	fmt.Println("📅 Data period (by rr_dt):")
	if firstDT.Valid && firstDT.String != "" {
		fmt.Printf("   First record: %s\n", firstDT.String)
	}
	if lastDT.Valid && lastDT.String != "" {
		fmt.Printf("   Last record:  %s\n", lastDT.String)
	}
	fmt.Println()
}

// DailyStats holds daily statistics for sales
type DailyStats struct {
	Date           string
	Total          int
	Sales          int
	Returns        int
	UniqueProducts int
}

// inspectSalesTable analyzes sales table
func inspectSalesTable(ctx context.Context, db *sql.DB) {
	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sales'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		fmt.Println("=== SALES ===")
		fmt.Println("   Table not found")
		fmt.Println()
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sales").Scan(&total)

	// Get unique products count
	var uniqueProducts int
	db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT nm_id) FROM sales").Scan(&uniqueProducts)

	fmt.Println("=== SALES ===")
	fmt.Printf("   Total records: %d\n", total)
	fmt.Printf("   Unique products: %d\n", uniqueProducts)
	fmt.Println()

	if total == 0 {
		fmt.Println("   No data to display")
		fmt.Println()
		return
	}

	// Build date filter
	dateFilter := buildDateFilter("sale_dt")

	// Query daily stats
	// Use SUBSTR instead of DATE() for performance (avoids function call on every row)
	// sale_dt format: "2026-01-02T00:00:00+03:00" -> SUBSTR 1-10 = "2026-01-02"
	query := fmt.Sprintf(`
		SELECT
			SUBSTR(sale_dt, 1, 10) as day,
			COUNT(*) as total,
			SUM(CASE WHEN doc_type_name = 'Продажа' THEN 1 ELSE 0 END) as sales,
			SUM(CASE WHEN doc_type_name = 'Возврат' THEN 1 ELSE 0 END) as returns,
			COUNT(DISTINCT nm_id) as unique_products
		FROM sales
		WHERE sale_dt IS NOT NULL AND sale_dt != ''
		%s
		GROUP BY SUBSTR(sale_dt, 1, 10)
		ORDER BY day
	`, dateFilter)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Printf("   Query error: %v\n\n", err)
		return
	}
	defer rows.Close()

	var stats []DailyStats
	for rows.Next() {
		var s DailyStats
		if err := rows.Scan(&s.Date, &s.Total, &s.Sales, &s.Returns, &s.UniqueProducts); err != nil {
			fmt.Printf("   Scan error: %v\n", err)
			return
		}
		stats = append(stats, s)
	}

	if len(stats) == 0 {
		fmt.Println("   No records with valid sale_dt found")
		fmt.Println()

		// Check if data exists with rr_dt instead
		var rrDtCount int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sales WHERE rr_dt IS NOT NULL AND rr_dt != ''").Scan(&rrDtCount)
		if rrDtCount > 0 {
			fmt.Printf("   ⚠️  Found %d records with rr_dt (report date) instead of sale_dt\n", rrDtCount)
			fmt.Println("   This may indicate data was loaded but sale_dt is empty")
		}
		fmt.Println()
		return
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "   Date\t\tTotal\tSales\tReturns\tProducts")
	fmt.Fprintln(w, "   ----\t\t-----\t-----\t-------\t--------")

	var grandTotal, grandSales, grandReturns int
	for _, s := range stats {
		fmt.Fprintf(w, "   %s\t%d\t%d\t%d\t%d\n",
			s.Date, s.Total, s.Sales, s.Returns, s.UniqueProducts)
		grandTotal += s.Total
		grandSales += s.Sales
		grandReturns += s.Returns
	}
	w.Flush()

	fmt.Println()
	fmt.Printf("   TOTAL: %d records (%d sales, %d returns, %d unique products)\n",
		grandTotal, grandSales, grandReturns, uniqueProducts)
	fmt.Println()

	// Detect gaps
	detectGaps(stats, "sales")
}

// ServiceDailyStats holds daily stats for service records
type ServiceDailyStats struct {
	Date  string
	Total int
	Types map[string]int
}

// inspectServiceTable analyzes service_records table
func inspectServiceTable(ctx context.Context, db *sql.DB) {
	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='service_records'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		fmt.Println("=== SERVICE RECORDS ===")
		fmt.Println("   Table not found")
		fmt.Println()
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM service_records").Scan(&total)

	fmt.Println("=== SERVICE RECORDS ===")
	fmt.Printf("   Total records: %d\n", total)
	fmt.Println()

	if total == 0 {
		fmt.Println("   No data to display")
		fmt.Println()
		return
	}

	// Get breakdown by operation type
	typeRows, err := db.QueryContext(ctx, `
		SELECT COALESCE(supplier_oper_name, '(empty)'), COUNT(*)
		FROM service_records
		GROUP BY supplier_oper_name
		ORDER BY COUNT(*) DESC
		LIMIT 10
	`)
	if err != nil {
		fmt.Printf("   Query error: %v\n\n", err)
		return
	}
	defer typeRows.Close()

	fmt.Println("   Top operation types:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for typeRows.Next() {
		var opType string
		var count int
		if err := typeRows.Scan(&opType, &count); err != nil {
			continue
		}
		fmt.Fprintf(w, "   - %s\t%d\n", truncate(opType, 50), count)
	}
	w.Flush()
	fmt.Println()

	// Build date filter
	dateFilter := buildDateFilter("rr_dt")

	// Query daily stats (use SUBSTR for performance)
	query := fmt.Sprintf(`
		SELECT
			SUBSTR(rr_dt, 1, 10) as day,
			COUNT(*) as total
		FROM service_records
		WHERE rr_dt IS NOT NULL AND rr_dt != ''
		%s
		GROUP BY SUBSTR(rr_dt, 1, 10)
		ORDER BY day
	`, dateFilter)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Printf("   Query error: %v\n\n", err)
		return
	}
	defer rows.Close()

	var stats []struct {
		Date  string
		Total int
	}
	for rows.Next() {
		var s struct {
			Date  string
			Total int
		}
		if err := rows.Scan(&s.Date, &s.Total); err != nil {
			continue
		}
		stats = append(stats, s)
	}

	if len(stats) > 0 {
		fmt.Println("   Daily counts:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "   Date\t\tCount")
		fmt.Fprintln(w, "   ----\t\t-----")
		for _, s := range stats {
			fmt.Fprintf(w, "   %s\t%d\n", s.Date, s.Total)
		}
		w.Flush()
		fmt.Println()
	}
}

// inspectFunnelTable analyzes funnel_metrics_daily table
func inspectFunnelTable(ctx context.Context, db *sql.DB) {
	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funnel_metrics_daily'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		fmt.Println("=== FUNNEL METRICS ===")
		fmt.Println("   Table not found")
		fmt.Println()
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM funnel_metrics_daily").Scan(&total)

	// Get unique products count
	var uniqueProducts int
	db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT nm_id) FROM funnel_metrics_daily").Scan(&uniqueProducts)

	fmt.Println("=== FUNNEL METRICS ===")
	fmt.Printf("   Total records: %d\n", total)
	fmt.Printf("   Unique products: %d\n", uniqueProducts)
	fmt.Println()

	if total == 0 {
		fmt.Println("   No data to display")
		fmt.Println()
		return
	}

	// Build date filter
	dateFilter := buildDateFilter("metric_date")

	// Query daily stats
	query := fmt.Sprintf(`
		SELECT
			metric_date as day,
			COUNT(*) as records,
			COUNT(DISTINCT nm_id) as products
		FROM funnel_metrics_daily
		WHERE 1=1
		%s
		GROUP BY metric_date
		ORDER BY day
	`, dateFilter)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Printf("   Query error: %v\n\n", err)
		return
	}
	defer rows.Close()

	var stats []struct {
		Date     string
		Records  int
		Products int
	}
	for rows.Next() {
		var s struct {
			Date     string
			Records  int
			Products int
		}
		if err := rows.Scan(&s.Date, &s.Records, &s.Products); err != nil {
			continue
		}
		stats = append(stats, s)
	}

	if len(stats) == 0 {
		fmt.Println("   No records found in date range")
		fmt.Println()
		return
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "   Date\t\tRecords\tProducts")
	fmt.Fprintln(w, "   ----\t\t-------\t--------")

	for _, s := range stats {
		fmt.Fprintf(w, "   %s\t%d\t%d\n", s.Date, s.Records, s.Products)
	}
	w.Flush()
	fmt.Println()
}

// inspectFeedbacksTable analyzes feedbacks and questions tables.
func inspectFeedbacksTable(ctx context.Context, db *sql.DB) {
	// Check if feedbacks table exists
	var feedbacksExists, questionsExists int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feedbacks'",
	).Scan(&feedbacksExists)
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='questions'",
	).Scan(&questionsExists)

	fmt.Println("=== FEEDBACKS & QUESTIONS ===")

	if feedbacksExists == 0 && questionsExists == 0 {
		fmt.Println("   Tables not found")
		fmt.Println()
		return
	}

	// Feedbacks stats
	if feedbacksExists > 0 {
		var totalFeedbacks, withAnswer, withPhotos, withVideo int
		var avgRating sql.NullFloat64
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM feedbacks").Scan(&totalFeedbacks)
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM feedbacks WHERE answer_text IS NOT NULL AND answer_text != ''").Scan(&withAnswer)
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM feedbacks WHERE photo_links IS NOT NULL AND photo_links != ''").Scan(&withPhotos)
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM feedbacks WHERE video_link IS NOT NULL AND video_link != ''").Scan(&withVideo)
		db.QueryRowContext(ctx, "SELECT AVG(product_valuation) FROM feedbacks WHERE product_valuation IS NOT NULL").Scan(&avgRating)

		var uniqueProducts int
		db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT product_nm_id) FROM feedbacks WHERE product_nm_id IS NOT NULL").Scan(&uniqueProducts)

		fmt.Printf("   Feedbacks total:      %d\n", totalFeedbacks)
		fmt.Printf("   With seller answer:   %d (%.1f%%)\n", withAnswer, pct(withAnswer, totalFeedbacks))
		fmt.Printf("   With photos:          %d\n", withPhotos)
		fmt.Printf("   With video:           %d\n", withVideo)
		if avgRating.Valid {
			fmt.Printf("   Avg product valuation: %.2f\n", avgRating.Float64)
		}
		fmt.Printf("   Unique products:      %d\n", uniqueProducts)

		// Date range
		var firstDate, lastDate sql.NullString
		db.QueryRowContext(ctx, "SELECT MIN(created_date) FROM feedbacks").Scan(&firstDate)
		db.QueryRowContext(ctx, "SELECT MAX(created_date) FROM feedbacks").Scan(&lastDate)
		if firstDate.Valid {
			fmt.Printf("   Period: %s → %s\n", firstDate.String, lastDate.String)
		}
	}

	// Questions stats
	if questionsExists > 0 {
		var totalQuestions, withAnswer, unanswered int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM questions").Scan(&totalQuestions)
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM questions WHERE answer_text IS NOT NULL AND answer_text != ''").Scan(&withAnswer)
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM questions WHERE (answer_text IS NULL OR answer_text = '') AND state != 'deleted'").Scan(&unanswered)

		fmt.Println()
		fmt.Printf("   Questions total:      %d\n", totalQuestions)
		fmt.Printf("   With seller answer:   %d (%.1f%%)\n", withAnswer, pct(withAnswer, totalQuestions))
		fmt.Printf("   Unanswered:           %d\n", unanswered)
	}

	// Top categories by feedback count
	if feedbacksExists > 0 {
		fmt.Println()
		fmt.Println("   Top 10 categories by feedbacks:")
		rows, err := db.QueryContext(ctx, `
			SELECT subject_name, COUNT(*) as cnt
			FROM feedbacks
			WHERE subject_name IS NOT NULL AND subject_name != ''
			GROUP BY subject_name
			ORDER BY cnt DESC
			LIMIT 10
		`)
		if err == nil {
			defer rows.Close()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for rows.Next() {
				var name string
				var count int
				if rows.Scan(&name, &count) == nil {
					fmt.Fprintf(w, "   - %s\t%d\n", truncate(name, 40), count)
				}
			}
			w.Flush()
		}
	}

	fmt.Println()
}

// inspectQualityTable analyzes product_quality_summary table (LLM analysis results).
func inspectQualityTable(ctx context.Context, db *sql.DB) {
	var exists int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='product_quality_summary'",
	).Scan(&exists)

	fmt.Println("=== QUALITY SUMMARIES (LLM) ===")

	if exists == 0 {
		fmt.Println("   Table not found")
		fmt.Println()
		return
	}

	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM product_quality_summary").Scan(&total)
	fmt.Printf("   Total analyzed products: %d\n", total)

	if total == 0 {
		fmt.Println("   No LLM analysis results yet")
		fmt.Println()
		return
	}

	// Model distribution
	fmt.Println()
	fmt.Println("   Models used:")
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(model_used, '(unknown)'), COUNT(*)
		FROM product_quality_summary
		GROUP BY model_used
		ORDER BY COUNT(*) DESC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var model string
			var count int
			if rows.Scan(&model, &count) == nil {
				fmt.Printf("   - %s\t%d\n", truncate(model, 50), count)
			}
		}
	}

	// Analyzed period range
	var minFrom, maxTo sql.NullString
	db.QueryRowContext(ctx, "SELECT MIN(request_from) FROM product_quality_summary").Scan(&minFrom)
	db.QueryRowContext(ctx, "SELECT MAX(request_to) FROM product_quality_summary").Scan(&maxTo)
	if minFrom.Valid && maxTo.Valid {
		fmt.Println()
		fmt.Printf("   Request period range: %s → %s\n", minFrom.String, maxTo.String)
	}

	// Rating distribution
	fmt.Println()
	fmt.Println("   Rating distribution:")
	ratingRows, err := db.QueryContext(ctx, `
		SELECT
			CASE
				WHEN avg_rating >= 4.5 THEN '4.5-5.0 (excellent)'
				WHEN avg_rating >= 4.0 THEN '4.0-4.4 (good)'
				WHEN avg_rating >= 3.5 THEN '3.5-3.9 (average)'
				WHEN avg_rating >= 3.0 THEN '3.0-3.4 (below avg)'
				ELSE '< 3.0 (poor)'
			END as bucket,
			COUNT(*)
		FROM product_quality_summary
		WHERE avg_rating IS NOT NULL
		GROUP BY bucket
		ORDER BY MIN(avg_rating) DESC
	`)
	if err == nil {
		defer ratingRows.Close()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for ratingRows.Next() {
			var bucket string
			var count int
			if ratingRows.Scan(&bucket, &count) == nil {
				fmt.Fprintf(w, "   %s\t%d\n", bucket, count)
			}
		}
		w.Flush()
	}

	fmt.Println()
}

// pct calculates percentage, avoiding division by zero.
func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// buildDateFilter builds SQL date filter clause
// Uses direct string comparison for performance (avoids DATE() function on every row)
// sale_dt is stored as TEXT in format "2026-01-02T00:00:00+03:00"
func buildDateFilter(dateCol string) string {
	var conditions []string

	if *fromDate != "" {
		// sale_dt >= "2026-01-01T..." matches dates >= 2026-01-01
		conditions = append(conditions, fmt.Sprintf("%s >= '%sT00:00:00'", dateCol, *fromDate))
	}
	if *toDate != "" {
		// sale_dt <= "2026-01-07T23:59:59" matches dates <= 2026-01-07
		conditions = append(conditions, fmt.Sprintf("%s <= '%sT23:59:59'", dateCol, *toDate))
	}

	if len(conditions) == 0 {
		return ""
	}
	return "AND " + strings.Join(conditions, " AND ")
}

// detectGaps finds missing dates in the data
func detectGaps(stats []DailyStats, tableName string) {
	if len(stats) < 2 {
		return
	}

	// Sort dates
	dates := make([]string, len(stats))
	for i, s := range stats {
		dates[i] = s.Date
	}
	sort.Strings(dates)

	// Parse and find gaps
	var gaps []string
	for i := 1; i < len(dates); i++ {
		prev, err := time.Parse("2006-01-02", dates[i-1])
		if err != nil {
			continue
		}
		curr, err := time.Parse("2006-01-02", dates[i])
		if err != nil {
			continue
		}

		diff := int(curr.Sub(prev).Hours() / 24)
		if diff > 1 {
			for d := 1; d < diff; d++ {
				gap := prev.AddDate(0, 0, d).Format("2006-01-02")
				gaps = append(gaps, gap)
			}
		}
	}

	if len(gaps) > 0 {
		fmt.Printf("   ⚠️  Gaps detected in %s (%d missing days):\n", tableName, len(gaps))
		if len(gaps) <= 10 {
			for _, g := range gaps {
				fmt.Printf("      - %s\n", g)
			}
		} else {
			for _, g := range gaps[:5] {
				fmt.Printf("      - %s\n", g)
			}
			fmt.Printf("      ... and %d more\n", len(gaps)-5)
		}
		fmt.Println()
	}
}

// truncate shortens a string
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
