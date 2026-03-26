// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SaveProduct saves or updates product metadata.
// Uses INSERT OR REPLACE for upsert (nm_id is PRIMARY KEY).
func (r *SQLiteSalesRepository) SaveProduct(ctx context.Context, product wb.FunnelProductMeta) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO products (
			nm_id, vendor_code, title, brand_name,
			subject_id, subject_name,
			product_rating, feedback_rating,
			stock_wb, stock_mp, stock_balance_sum,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`,
		product.NmID,
		product.VendorCode,
		product.Title,
		product.BrandName,
		product.SubjectID,
		product.SubjectName,
		product.ProductRating,
		product.FeedbackRating,
		product.StockWB,
		product.StockMP,
		product.StockBalance,
	)
	if err != nil {
		return fmt.Errorf("upsert product nm_id=%d: %w", product.NmID, err)
	}
	return nil
}

// SaveFunnelHistory saves batch of daily funnel metrics.
// Uses INSERT OR REPLACE for upsert by (nm_id, metric_date) UNIQUE constraint.
// Also saves product metadata from the first row if provided.
func (r *SQLiteSalesRepository) SaveFunnelHistory(ctx context.Context, product wb.FunnelProductMeta, rows []wb.FunnelHistoryRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Upsert product metadata
	if product.NmID > 0 {
		_, err = tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO products (
				nm_id, vendor_code, title, brand_name,
				subject_id, subject_name,
				product_rating, feedback_rating,
				stock_wb, stock_mp, stock_balance_sum,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`,
			product.NmID,
			product.VendorCode,
			product.Title,
			product.BrandName,
			product.SubjectID,
			product.SubjectName,
			product.ProductRating,
			product.FeedbackRating,
			product.StockWB,
			product.StockMP,
			product.StockBalance,
		)
		if err != nil {
			return fmt.Errorf("upsert product nm_id=%d: %w", product.NmID, err)
		}
	}

	// Prepare statement for funnel metrics
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO funnel_metrics_daily (
			nm_id, metric_date,
			open_count, cart_count, order_count, buyout_count, add_to_wishlist,
			order_sum, buyout_sum,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.NmID,
			row.MetricDate,
			row.OpenCount,
			row.CartCount,
			row.OrderCount,
			row.BuyoutCount,
			row.AddToWishlist,
			row.OrderSum,
			row.BuyoutSum,
			row.ConversionAddToCart,
			row.ConversionCartToOrder,
			row.ConversionBuyout,
		)
		if err != nil {
			return fmt.Errorf("insert funnel row nm_id=%d date=%s: %w", row.NmID, row.MetricDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetDistinctNmIDs returns list of distinct nm_id from sales table.
// Used to determine which products need funnel data loaded.
func (r *SQLiteSalesRepository) GetDistinctNmIDs(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT nm_id FROM sales ORDER BY nm_id")
	if err != nil {
		return nil, fmt.Errorf("query distinct nm_id: %w", err)
	}
	defer rows.Close()

	var nmIDs []int
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		nmIDs = append(nmIDs, nmID)
	}

	return nmIDs, rows.Err()
}

// GetTrendingProducts returns products with trend analysis.
// Compares last 7 days vs previous 7 days.
// Returns products sorted by order growth percent (descending).
func (r *SQLiteSalesRepository) GetTrendingProducts(ctx context.Context, limit int) ([]wb.TrendingProduct, error) {
	query := `
	WITH recent_7d AS (
		SELECT
			nm_id,
			SUM(order_count) AS orders,
			SUM(buyout_sum) AS revenue,
			AVG(conversion_buyout) AS avg_conversion
		FROM funnel_metrics_daily
		WHERE metric_date >= DATE('now', '-7 days')
		GROUP BY nm_id
	),
	previous_7d AS (
		SELECT
			nm_id,
			SUM(order_count) AS orders,
			SUM(buyout_sum) AS revenue
		FROM funnel_metrics_daily
		WHERE metric_date >= DATE('now', '-14 days')
		  AND metric_date < DATE('now', '-7 days')
		GROUP BY nm_id
	)
	SELECT
		COALESCE(r.nm_id, prev.nm_id) AS nm_id,
		COALESCE(pr.title, '') AS title,
		COALESCE(pr.brand_name, '') AS brand_name,
		COALESCE(r.orders, 0) AS orders_7d,
		COALESCE(prev.orders, 0) AS orders_prev_7d,
		CASE
			WHEN COALESCE(prev.orders, 0) > 0
			THEN ROUND(100.0 * (COALESCE(r.orders, 0) - prev.orders) / prev.orders, 1)
			ELSE NULL
		END AS order_growth_percent,
		COALESCE(r.revenue, 0) AS revenue_7d,
		CASE
			WHEN COALESCE(prev.revenue, 0) > 0
			THEN ROUND(100.0 * (COALESCE(r.revenue, 0) - prev.revenue) / prev.revenue, 1)
			ELSE NULL
		END AS revenue_growth,
		r.avg_conversion,
		CASE
			WHEN COALESCE(prev.orders, 0) = 0 THEN 'NEW'
			WHEN 100.0 * (COALESCE(r.orders, 0) - prev.orders) / prev.orders >= 20 THEN 'TRENDING_UP'
			WHEN 100.0 * (COALESCE(r.orders, 0) - prev.orders) / prev.orders <= -20 THEN 'TRENDING_DOWN'
			ELSE 'STABLE'
		END AS trend_status
	FROM recent_7d r
	FULL OUTER JOIN previous_7d prev ON r.nm_id = prev.nm_id
	LEFT JOIN products pr ON COALESCE(r.nm_id, prev.nm_id) = pr.nm_id
	WHERE COALESCE(r.orders, 0) > 0
	ORDER BY order_growth_percent DESC NULLS LAST
	LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query trending products: %w", err)
	}
	defer rows.Close()

	var results []wb.TrendingProduct
	for rows.Next() {
		var tp wb.TrendingProduct
		var orderGrowth, revenueGrowth, avgConversion sql.NullFloat64

		err := rows.Scan(
			&tp.NmID,
			&tp.Title,
			&tp.BrandName,
			&tp.Orders7d,
			&tp.OrdersPrev7d,
			&orderGrowth,
			&tp.Revenue7d,
			&revenueGrowth,
			&avgConversion,
			&tp.TrendStatus,
		)
		if err != nil {
			return nil, fmt.Errorf("scan trending product: %w", err)
		}

		if orderGrowth.Valid {
			tp.OrderGrowthPercent = orderGrowth.Float64
		}
		if revenueGrowth.Valid {
			tp.RevenueGrowth = revenueGrowth.Float64
		}
		if avgConversion.Valid {
			tp.AvgConversion = avgConversion.Float64
		}

		results = append(results, tp)
	}

	return results, rows.Err()
}

// CountFunnelMetrics returns total number of funnel metrics records.
func (r *SQLiteSalesRepository) CountFunnelMetrics(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM funnel_metrics_daily").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count funnel_metrics_daily: %w", err)
	}
	return count, nil
}

// CountProducts returns total number of products in products table.
func (r *SQLiteSalesRepository) CountProducts(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count products: %w", err)
	}
	return count, nil
}

// RefreshStats is a local alias for storage.RefreshStats.
type RefreshStats = storage.RefreshStats
// Returns empty string if table is empty.
func (r *SQLiteSalesRepository) GetLastFunnelDate(ctx context.Context) (string, error) {
	var lastDate sql.NullString
	err := r.db.QueryRowContext(ctx, "SELECT MAX(metric_date) FROM funnel_metrics_daily").Scan(&lastDate)
	if err != nil {
		return "", fmt.Errorf("get last funnel date: %w", err)
	}
	if !lastDate.Valid {
		return "", nil
	}
	return lastDate.String, nil
}

// RefreshWithinWindow deletes funnel metrics within the refresh window.
//
// Returns statistics about deleted records. This is called before loading new data
// to report what will be refreshed.
//
// The window is calculated as: (today - refreshDays) to today.
// Records within this window will be replaced with fresh data from WB API.
// Records outside the window are preserved (frozen historical data).
func (r *SQLiteSalesRepository) RefreshWithinWindow(ctx context.Context, nmIDs []int, refreshDays int) (*RefreshStats, error) {
	if len(nmIDs) == 0 || refreshDays <= 0 {
		return &RefreshStats{}, nil
	}

	now := time.Now()
	windowStart := now.AddDate(0, 0, -refreshDays)

	// Build placeholders for IN clause
	placeholders := ""
	args := make([]any, 0, len(nmIDs)+2)
	for i, nmID := range nmIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, nmID)
	}
	args = append(args, windowStart.Format("2006-01-02"), now.Format("2006-01-02"))

	// Count records that will be deleted (for reporting)
	var countBefore int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM funnel_metrics_daily WHERE nm_id IN (%s) AND metric_date >= ? AND metric_date <= ?", placeholders)
	err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&countBefore)
	if err != nil {
		return nil, fmt.Errorf("count records within window: %w", err)
	}

	// Delete records within window
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	deleteQuery := fmt.Sprintf("DELETE FROM funnel_metrics_daily WHERE nm_id IN (%s) AND metric_date >= ? AND metric_date <= ?", placeholders)
	result, err := tx.ExecContext(ctx, deleteQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("delete within window: %w", err)
	}

	deleted, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &RefreshStats{
		Deleted:     int(deleted),
		Inserted:    0,
		WindowStart: windowStart,
		WindowEnd:   now,
	}, nil
}

// SaveFunnelHistoryWithWindow saves funnel metrics with refresh window logic.
//
// Behaves differently based on record date:
//   - Within window (today - refreshDays): INSERT OR REPLACE (update existing)
//   - Outside window: INSERT OR IGNORE (preserve historical data)
//
// This handles WB retroactive updates where recent data may change but
// historical data should be frozen.
func (r *SQLiteSalesRepository) SaveFunnelHistoryWithWindow(
	ctx context.Context,
	product wb.FunnelProductMeta,
	rows []wb.FunnelHistoryRow,
	refreshDays int,
) error {
	if len(rows) == 0 {
		return nil
	}

	// If refreshDays is 0 or negative, use original behavior (always REPLACE)
	if refreshDays <= 0 {
		return r.SaveFunnelHistory(ctx, product, rows)
	}

	cutoffDate := time.Now().AddDate(0, 0, -refreshDays)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Upsert product metadata (always)
	if product.NmID > 0 {
		_, err = tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO products (
				nm_id, vendor_code, title, brand_name,
				subject_id, subject_name,
				product_rating, feedback_rating,
				stock_wb, stock_mp, stock_balance_sum,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`,
			product.NmID,
			product.VendorCode,
			product.Title,
			product.BrandName,
			product.SubjectID,
			product.SubjectName,
			product.ProductRating,
			product.FeedbackRating,
			product.StockWB,
			product.StockMP,
			product.StockBalance,
		)
		if err != nil {
			return fmt.Errorf("upsert product nm_id=%d: %w", product.NmID, err)
		}
	}

	// Prepare statements for REPLACE and IGNORE
	stmtReplace, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO funnel_metrics_daily (
			nm_id, metric_date,
			open_count, cart_count, order_count, buyout_count, add_to_wishlist,
			order_sum, buyout_sum,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare replace statement: %w", err)
	}
	defer stmtReplace.Close()

	stmtIgnore, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO funnel_metrics_daily (
			nm_id, metric_date,
			open_count, cart_count, order_count, buyout_count, add_to_wishlist,
			order_sum, buyout_sum,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare ignore statement: %w", err)
	}
	defer stmtIgnore.Close()

	// Save each row with appropriate strategy
	for _, row := range rows {
		metricDate, err := time.Parse("2006-01-02", row.MetricDate)
		if err != nil {
			return fmt.Errorf("parse date %s: %w", row.MetricDate, err)
		}

		var stmt *sql.Stmt
		if metricDate.After(cutoffDate) || metricDate.Equal(cutoffDate) {
			// Within window → REPLACE (update)
			stmt = stmtReplace
		} else {
			// Outside window → IGNORE (preserve)
			stmt = stmtIgnore
		}

		_, err = stmt.ExecContext(ctx,
			row.NmID,
			row.MetricDate,
			row.OpenCount,
			row.CartCount,
			row.OrderCount,
			row.BuyoutCount,
			row.AddToWishlist,
			row.OrderSum,
			row.BuyoutSum,
			row.ConversionAddToCart,
			row.ConversionCartToOrder,
			row.ConversionBuyout,
		)
		if err != nil {
			return fmt.Errorf("save row nm_id=%d date=%s: %w", row.NmID, row.MetricDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
