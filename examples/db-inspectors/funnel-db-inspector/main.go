// Package main provides a database inspection tool for WB funnel analytics data.
// READ-ONLY utility for diagnosing data quality and completeness.
//
// Usage:
//
//	go run main.go --db /path/to/sales.db [--from 2026-01-01] [--to 2026-01-31] [--compare] [-v]
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
	dbPath   = flag.String("db", "", "Path to SQLite database (required)")
	fromDate = flag.String("from", "", "Start date (YYYY-MM-DD)")
	toDate   = flag.String("to", "", "End date (YYYY-MM-DD)")
	compare  = flag.Bool("compare", false, "Compare with WB API swagger schema")
	verbose  = flag.Bool("v", false, "Verbose output with sample data")
)

func main() {
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --db flag is required")
		flag.Usage()
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
	fmt.Println("     FUNNEL DATABASE INSPECTION TOOL (READ-ONLY MODE)")
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

	// Inspect funnel metadata
	inspectFunnelMetadata(ctx, db)

	// Inspect daily metrics
	inspectFunnelDailyMetrics(ctx, db)

	// Check data quality
	inspectFunnelDataQuality(ctx, db)

	// Compare with swagger schema (optional)
	if *compare {
		compareWithSwaggerSchema(ctx, db)
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

// buildDateFilter builds SQL date filter clause for metric_date column
// Uses direct string comparison for performance (avoids DATE() function on every row)
// metric_date is stored as TEXT in format "2026-01-02"
func buildDateFilter() string {
	var conditions []string

	if *fromDate != "" {
		conditions = append(conditions, fmt.Sprintf("metric_date >= '%s'", *fromDate))
	}
	if *toDate != "" {
		conditions = append(conditions, fmt.Sprintf("metric_date <= '%s'", *toDate))
	}

	if len(conditions) == 0 {
		return ""
	}
	return "AND " + strings.Join(conditions, " AND ")
}

// inspectFunnelMetadata analyzes funnel_metrics_daily and products tables
func inspectFunnelMetadata(ctx context.Context, db *sql.DB) {
	// Check if funnel_metrics_daily table exists
	var funnelExists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funnel_metrics_daily'",
	).Scan(&funnelExists)
	if err != nil || funnelExists == 0 {
		fmt.Println("=== FUNNEL METADATA ===")
		fmt.Println("   Table funnel_metrics_daily not found")
		fmt.Println()
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM funnel_metrics_daily").Scan(&total)

	// Get unique products count (OPTIMIZED: use subquery instead of COUNT(DISTINCT))
	var uniqueProducts int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT DISTINCT nm_id FROM funnel_metrics_daily)").Scan(&uniqueProducts)

	// Get date range
	var firstDate, lastDate sql.NullString
	db.QueryRowContext(ctx, "SELECT MIN(metric_date) FROM funnel_metrics_daily").Scan(&firstDate)
	db.QueryRowContext(ctx, "SELECT MAX(metric_date) FROM funnel_metrics_daily").Scan(&lastDate)

	fmt.Println("=== FUNNEL METADATA ===")
	fmt.Printf("   Total records: %d\n", total)
	fmt.Printf("   Unique products: %d\n", uniqueProducts)

	if firstDate.Valid && lastDate.Valid {
		fmt.Printf("   Date range: %s ... %s\n", firstDate.String, lastDate.String)

		// Calculate days covered
		start, err1 := time.Parse("2006-01-02", firstDate.String)
		end, err2 := time.Parse("2006-01-02", lastDate.String)
		if err1 == nil && err2 == nil {
			days := int(end.Sub(start).Hours()/24) + 1
			fmt.Printf("   Days covered: %d\n", days)
		}
	}
	fmt.Println()

	if total == 0 {
		fmt.Println("   No data to display")
		fmt.Println()
		return
	}

	// Check products table
	var productsExists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='products'",
	).Scan(&productsExists)
	if err == nil && productsExists > 0 {
		var productsCount int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products").Scan(&productsCount)
		fmt.Printf("   Products metadata: %d records\n", productsCount)
	}
	fmt.Println()
}

// DailyFunnelStats holds daily funnel statistics
type DailyFunnelStats struct {
	Date     string
	Records  int
	Views    int64
	Cart     int64
	Orders   int64
	Buyouts  int64
	AvgConv  float64
}

// inspectFunnelDailyMetrics analyzes daily funnel metrics
// OPTIMIZED for large tables without indexes:
// - Simple aggregation without COUNT(DISTINCT)
// - Direct string comparison for dates
// - Separate query for unique products count
func inspectFunnelDailyMetrics(ctx context.Context, db *sql.DB) {
	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funnel_metrics_daily'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM funnel_metrics_daily").Scan(&total)
	if total == 0 {
		return
	}

	// Build date filter (index-friendly: direct string comparison)
	dateFilter := buildDateFilter()

	// OPTIMIZED: Simple daily aggregation without COUNT(DISTINCT)
	// Products count is approximated as records per day (assuming one record per product per day)
	query := fmt.Sprintf(`
		SELECT
			metric_date as day,
			COUNT(*) as records,
			SUM(open_count) as views,
			SUM(cart_count) as cart,
			SUM(order_count) as orders,
			SUM(buyout_count) as buyouts,
			AVG(conversion_buyout) as avg_conv
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

	var stats []DailyFunnelStats
	for rows.Next() {
		var s DailyFunnelStats
		var avgConv sql.NullFloat64
		if err := rows.Scan(&s.Date, &s.Records, &s.Views, &s.Cart, &s.Orders, &s.Buyouts, &avgConv); err != nil {
			fmt.Printf("   Scan error: %v\n", err)
			return
		}
		if avgConv.Valid {
			s.AvgConv = avgConv.Float64
		}
		stats = append(stats, s)
	}

	if len(stats) == 0 {
		fmt.Println("=== DAILY METRICS ===")
		fmt.Println("   No records found in date range")
		fmt.Println()
		return
	}

	fmt.Println("=== DAILY METRICS ===")

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "   Date\t\tRecords\tViews\tCart\tOrders\tBuyouts\tConv%")
	fmt.Fprintln(w, "   ----\t\t-------\t-----\t----\t------\t-------\t-----")

	var grandViews, grandCart, grandOrders, grandBuyouts int64
	for _, s := range stats {
		convStr := fmt.Sprintf("%.1f", s.AvgConv)
		if s.AvgConv == 0 {
			convStr = "-"
		}
		fmt.Fprintf(w, "   %s\t%d\t%d\t%d\t%d\t%d\t%s\n",
			s.Date, s.Records, s.Views, s.Cart, s.Orders, s.Buyouts, convStr)
		grandViews += s.Views
		grandCart += s.Cart
		grandOrders += s.Orders
		grandBuyouts += s.Buyouts
	}
	w.Flush()

	fmt.Println()
	fmt.Printf("   TOTAL: %d records, %d views, %d cart, %d orders, %d buyouts\n",
		len(stats), grandViews, grandCart, grandOrders, grandBuyouts)
	fmt.Println()
}

// inspectFunnelDataQuality checks data quality issues
// OPTIMIZED: Avoid expensive subqueries, use LIMIT where possible
func inspectFunnelDataQuality(ctx context.Context, db *sql.DB) {
	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funnel_metrics_daily'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		return
	}

	// Get total count
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM funnel_metrics_daily").Scan(&total)
	if total == 0 {
		return
	}

	fmt.Println("=== DATA QUALITY ===")

	// Build date filter
	dateFilter := buildDateFilter()

	// 1. Check for missing dates (gaps)
	detectFunnelGaps(ctx, db, dateFilter)

	// 2. Check for zero metrics (single optimized query)
	var zeroViews, zeroOrders int
	query := fmt.Sprintf(`
		SELECT
			SUM(CASE WHEN open_count = 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN order_count = 0 THEN 1 ELSE 0 END)
		FROM funnel_metrics_daily
		WHERE 1=1 %s
	`, dateFilter)
	db.QueryRowContext(ctx, query).Scan(&zeroViews, &zeroOrders)

	if zeroViews > 0 {
		fmt.Printf("   Zero open_count: %d records (may indicate API issues)\n", zeroViews)
	}
	if zeroOrders > 0 {
		fmt.Printf("   Zero order_count: %d records\n", zeroOrders)
	}

	// 3. Check for anomalies: buyouts > orders (single query)
	var anomalyBuyouts int
	anomalyQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM funnel_metrics_daily
		WHERE buyout_count > order_count AND order_count > 0
		%s
	`, dateFilter)
	db.QueryRowContext(ctx, anomalyQuery).Scan(&anomalyBuyouts)
	if anomalyBuyouts > 0 {
		fmt.Printf("   ⚠️  Anomaly: buyout_count > order_count: %d records\n", anomalyBuyouts)
	}

	// 4. Check conversion anomalies (< 0 or > 100)
	var convAnomalies int
	convQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM funnel_metrics_daily
		WHERE conversion_buyout < 0 OR conversion_buyout > 100
		%s
	`, dateFilter)
	db.QueryRowContext(ctx, convQuery).Scan(&convAnomalies)
	if convAnomalies > 0 {
		fmt.Printf("   ⚠️  Conversion anomalies (<0 or >100): %d records\n", convAnomalies)
	}

	// 5. Check products without metadata (optimized: LIMIT 1 check first)
	var productsExists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='products'",
	).Scan(&productsExists)
	if err == nil && productsExists > 0 {
		var productsWithoutMetadata int
		db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM (SELECT DISTINCT nm_id FROM funnel_metrics_daily) f
			LEFT JOIN products p ON f.nm_id = p.nm_id
			WHERE p.nm_id IS NULL
		`).Scan(&productsWithoutMetadata)
		if productsWithoutMetadata > 0 {
			fmt.Printf("   ⚠️  Products without metadata: %d\n", productsWithoutMetadata)
		} else {
			fmt.Println("   Products metadata: ✅ All products have metadata")
		}
	}

	// 6. Conversion statistics
	var minConv, maxConv, avgConv sql.NullFloat64
	convStatsQuery := fmt.Sprintf(`
		SELECT MIN(conversion_buyout), MAX(conversion_buyout), AVG(conversion_buyout)
		FROM funnel_metrics_daily
		WHERE conversion_buyout IS NOT NULL
		%s
	`, dateFilter)
	db.QueryRowContext(ctx, convStatsQuery).Scan(&minConv, &maxConv, &avgConv)
	if minConv.Valid && maxConv.Valid {
		fmt.Printf("   Conversion range: %.1f%% ... %.1f%% (avg: %.1f%%)\n",
			minConv.Float64, maxConv.Float64, avgConv.Float64)
	}

	// 7. WB Club metrics check
	var wbClubRecords int
	wbClubQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM funnel_metrics_daily
		WHERE wb_club_order_count > 0 OR wb_club_buyout_count > 0
		%s
	`, dateFilter)
	db.QueryRowContext(ctx, wbClubQuery).Scan(&wbClubRecords)
	if wbClubRecords > 0 {
		fmt.Printf("   WB Club data: ✅ %d records with WB Club metrics\n", wbClubRecords)
	} else {
		fmt.Println("   WB Club data: ⚠️ No WB Club metrics found")
	}

	fmt.Println()

	// Verbose: show sample records
	if *verbose {
		showSampleRecords(ctx, db, dateFilter)
	}
}

// detectFunnelGaps finds missing dates in funnel data
func detectFunnelGaps(ctx context.Context, db *sql.DB, dateFilter string) {
	// Get all dates in range
	query := fmt.Sprintf(`
		SELECT DISTINCT metric_date
		FROM funnel_metrics_daily
		WHERE 1=1
		%s
		ORDER BY metric_date
	`, dateFilter)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			continue
		}
		dates = append(dates, date)
	}

	if len(dates) < 2 {
		return
	}

	// Sort dates
	sort.Strings(dates)

	// Find gaps
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
		fmt.Printf("   ⚠️  Missing dates: %d\n", len(gaps))
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
	} else {
		fmt.Println("   Date continuity: ✅ No gaps detected")
	}
}

// showSampleRecords displays sample records in verbose mode
func showSampleRecords(ctx context.Context, db *sql.DB, dateFilter string) {
	fmt.Println("=== SAMPLE RECORDS (verbose) ===")

	query := fmt.Sprintf(`
		SELECT
			f.nm_id,
			f.metric_date,
			f.open_count,
			f.cart_count,
			f.order_count,
			f.buyout_count,
			f.conversion_buyout,
			COALESCE(p.title, '(no metadata)') as title
		FROM funnel_metrics_daily f
		LEFT JOIN products p ON f.nm_id = p.nm_id
		WHERE 1=1
		%s
		ORDER BY f.metric_date DESC, f.nm_id
		LIMIT 10
	`, dateFilter)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Printf("   Query error: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "   NmID\tDate\t\tViews\tCart\tOrders\tBuyouts\tConv%\tTitle")
	fmt.Fprintln(w, "   ----\t----\t\t-----\t----\t------\t-------\t-----\t-----")

	for rows.Next() {
		var nmID int
		var date string
		var views, cart, orders, buyouts int64
		var conv sql.NullFloat64
		var title string
		if err := rows.Scan(&nmID, &date, &views, &cart, &orders, &buyouts, &conv, &title); err != nil {
			continue
		}
		convStr := "-"
		if conv.Valid {
			convStr = fmt.Sprintf("%.1f", conv.Float64)
		}
		fmt.Fprintf(w, "   %d\t%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
			nmID, date, views, cart, orders, buyouts, convStr, truncate(title, 30))
	}
	w.Flush()
	fmt.Println()
}

// SwaggerField represents a field from WB API swagger schema
type SwaggerField struct {
	Name        string
	DBColumn    string
	Description string
}

// compareWithSwaggerSchema compares DB schema with WB API swagger response
func compareWithSwaggerSchema(ctx context.Context, db *sql.DB) {
	fmt.Println("=== SCHEMA COMPARISON ===")

	// Check if table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funnel_metrics_daily'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		fmt.Println("   Table funnel_metrics_daily not found")
		fmt.Println()
		return
	}

	// Swagger fields from API response (based on provided schema)
	swaggerFields := []SwaggerField{
		{Name: "nmId", DBColumn: "nm_id", Description: "Product ID"},
		{Name: "date", DBColumn: "metric_date", Description: "Metric date"},
		{Name: "openCount", DBColumn: "open_count", Description: "Product views"},
		{Name: "cartCount", DBColumn: "cart_count", Description: "Add to cart"},
		{Name: "orderCount", DBColumn: "order_count", Description: "Orders"},
		{Name: "orderSum", DBColumn: "order_sum", Description: "Order sum (RUB)"},
		{Name: "buyoutCount", DBColumn: "buyout_count", Description: "Buyouts"},
		{Name: "buyoutSum", DBColumn: "buyout_sum", Description: "Buyout sum (RUB)"},
		{Name: "buyoutPercent", DBColumn: "conversion_buyout", Description: "Buyout conversion"},
		{Name: "addToCartConversion", DBColumn: "conversion_add_to_cart", Description: "Add to cart conversion"},
		{Name: "cartToOrderConversion", DBColumn: "conversion_cart_to_order", Description: "Cart to order conversion"},
		{Name: "addToWishlistCount", DBColumn: "add_to_wishlist", Description: "Add to wishlist"},
	}

	// Get DB columns
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(funnel_metrics_daily)")
	if err != nil {
		fmt.Printf("   Query error: %v\n", err)
		return
	}
	defer rows.Close()

	dbColumns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		dbColumns[name] = true
	}

	// Compare
	var present, missing []string
	for _, sf := range swaggerFields {
		if dbColumns[sf.DBColumn] {
			present = append(present, sf.Name)
		} else {
			missing = append(missing, sf.Name)
		}
	}

	// Find extra columns (in DB but not in swagger)
	swaggerColumnSet := make(map[string]bool)
	for _, sf := range swaggerFields {
		swaggerColumnSet[sf.DBColumn] = true
	}
	swaggerColumnSet["id"] = true        // surrogate key
	swaggerColumnSet["created_at"] = true // metadata

	var extra []string
	for col := range dbColumns {
		if !swaggerColumnSet[col] {
			extra = append(extra, col)
		}
	}
	sort.Strings(extra)

	// Print results
	fmt.Printf("   Swagger fields present: %d/%d", len(present), len(swaggerFields))
	if len(missing) == 0 {
		fmt.Println(" ✅")
	} else {
		fmt.Println()
		fmt.Printf("   Missing from DB: %s\n", strings.Join(missing, ", "))
	}

	fmt.Printf("   DB columns total: %d\n", len(dbColumns))
	if len(extra) > 0 {
		fmt.Printf("   Extra DB fields (%d): %s\n", len(extra), strings.Join(extra, ", "))
	}

	// Check products table
	var productsExists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='products'",
	).Scan(&productsExists)
	if err == nil && productsExists > 0 {
		fmt.Println("   Products table: ✅ Present")

		// Check product metadata fields
		productSwaggerFields := []string{"nmId", "title", "vendorCode", "brandName", "subjectId", "subjectName"}
		fmt.Printf("   Product metadata fields (swagger): %s\n", strings.Join(productSwaggerFields, ", "))
	}

	fmt.Println()
}

// truncate shortens a string
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
