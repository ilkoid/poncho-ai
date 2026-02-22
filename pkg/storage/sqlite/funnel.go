// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

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
			open_count, cart_count, order_count, buyout_count, cancel_count, add_to_wishlist,
			order_sum, buyout_sum, cancel_sum, avg_price,
			conversion_add_to_cart, conversion_cart_to_order, conversion_buyout,
			wb_club_order_count, wb_club_buyout_count, wb_club_buyout_percent,
			time_to_ready_days, time_to_ready_hours, time_to_ready_mins,
			localization_percent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			row.CancelCount,
			row.AddToWishlist,
			row.OrderSum,
			row.BuyoutSum,
			row.CancelSum,
			row.AvgPrice,
			row.ConversionAddToCart,
			row.ConversionCartToOrder,
			row.ConversionBuyout,
			row.WBClubOrderCount,
			row.WBClubBuyoutCount,
			row.WBClubBuyoutPercent,
			row.TimeToReadyDays,
			row.TimeToReadyHours,
			row.TimeToReadyMins,
			row.LocalizationPercent,
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

// GetLastFunnelDate returns the most recent metric_date in funnel_metrics_daily.
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
