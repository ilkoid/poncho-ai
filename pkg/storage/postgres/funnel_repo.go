package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/funnel"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgFunnelRepo implements funnel.FunnelWriter.
var _ funnel.FunnelWriter = (*PgFunnelRepo)(nil)

// PgFunnelRepo implements funnel.FunnelWriter for PostgreSQL.
// Focused repository (ISP) — only funnel persistence methods.
//
// Read methods (GetDistinctNmIDs, GetSupplierArticlesByNmIDs, FilterActiveNmIDs)
// query the sales table which is created by PgSalesRepo. This is a pipeline dependency:
// sales must be loaded before funnel.
type PgFunnelRepo struct {
	pool *pgxpool.Pool
}

// NewPgFunnelRepo creates a new PostgreSQL funnel repository.
func NewPgFunnelRepo(pool *pgxpool.Pool) *PgFunnelRepo {
	return &PgFunnelRepo{pool: pool}
}

// InitSchema creates products and funnel_metrics_daily tables if they don't exist.
func (r *PgFunnelRepo) InitSchema(ctx context.Context) error {
	return initFunnelSchema(ctx, r.pool)
}

// GetDistinctNmIDs returns list of distinct nm_id from sales table.
// Requires: sales table (created by PgSalesRepo).
func (r *PgFunnelRepo) GetDistinctNmIDs(ctx context.Context) ([]int, error) {
	rows, err := r.pool.Query(ctx, "SELECT DISTINCT nm_id FROM sales ORDER BY nm_id")
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

// GetSupplierArticlesByNmIDs returns a map of nm_id to supplier_article.
// Used for filtering products by article properties (length, year digits).
// Requires: sales table (created by PgSalesRepo).
func (r *PgFunnelRepo) GetSupplierArticlesByNmIDs(ctx context.Context, nmIDs []int) (map[int]string, error) {
	if len(nmIDs) == 0 {
		return make(map[int]string), nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT nm_id, supplier_article FROM sales
		 WHERE nm_id = ANY($1::int[]) AND supplier_article IS NOT NULL AND supplier_article != ''`,
		nmIDs)
	if err != nil {
		return nil, fmt.Errorf("query supplier articles: %w", err)
	}
	defer rows.Close()

	result := make(map[int]string)
	for rows.Next() {
		var nmID int
		var article string
		if err := rows.Scan(&nmID, &article); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		result[nmID] = article
	}
	return result, rows.Err()
}

// GetRecentlyLoadedNmIDs returns nm_ids that have funnel data loaded within the last N hours.
// Used for incremental loading: skip recently-loaded products to avoid redundant API calls.
func (r *PgFunnelRepo) GetRecentlyLoadedNmIDs(ctx context.Context, hours int) (map[int]bool, error) {
	if hours <= 0 {
		return make(map[int]bool), nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT nm_id FROM funnel_metrics_daily
		 WHERE created_at >= TO_CHAR(NOW() AT TIME ZONE 'UTC' - make_interval(hours => $1),
		                             'YYYY-MM-DD HH24:MI:SS')`,
		hours)
	if err != nil {
		return nil, fmt.Errorf("query recently loaded nm_ids: %w", err)
	}
	defer rows.Close()

	result := make(map[int]bool)
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		result[nmID] = true
	}
	return result, rows.Err()
}

// FilterActiveNmIDs filters nm_ids to only those with sales activity in the last N days.
// Used to skip dead products (no recent sales) to reduce API call volume.
// Requires: sales table (created by PgSalesRepo).
func (r *PgFunnelRepo) FilterActiveNmIDs(ctx context.Context, nmIDs []int, activeDays int) ([]int, error) {
	if activeDays <= 0 || len(nmIDs) == 0 {
		return nmIDs, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT nm_id FROM sales
		 WHERE nm_id = ANY($1::int[])
		   AND sale_dt >= TO_CHAR(CURRENT_DATE - make_interval(days => $2), 'YYYY-MM-DD')`,
		nmIDs, activeDays)
	if err != nil {
		return nil, fmt.Errorf("filter active nm_ids: %w", err)
	}
	defer rows.Close()

	var result []int
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		result = append(result, nmID)
	}
	return result, rows.Err()
}

// SaveFunnelHistoryWithWindow saves funnel metrics with refresh window logic.
//
// Behaves differently based on record date (mirrors SQLite logic):
//   - Within window (today - refreshDays): ON CONFLICT DO UPDATE (refresh)
//   - Outside window: ON CONFLICT DO NOTHING (preserve historical data)
//
// This handles WB retroactive updates where recent data may change but
// historical data should be frozen.
func (r *PgFunnelRepo) SaveFunnelHistoryWithWindow(
	ctx context.Context,
	product wb.FunnelProductMeta,
	rows []wb.FunnelHistoryRow,
	refreshDays int,
) error {
	if len(rows) == 0 {
		return nil
	}

	// If refreshDays is 0 or negative, use simple upsert (always update)
	if refreshDays <= 0 {
		return r.saveFunnelHistory(ctx, product, rows)
	}

	cutoffDate := time.Now().AddDate(0, 0, -refreshDays)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Upsert product metadata (always)
	if err := r.upsertProduct(ctx, tx, product); err != nil {
		return err
	}

	// Save each row with appropriate strategy
	for _, row := range rows {
		metricDate, err := time.Parse("2006-01-02", row.MetricDate)
		if err != nil {
			return fmt.Errorf("parse date %s: %w", row.MetricDate, err)
		}

		sql := pgUpsertFunnelMetricSQL // ON CONFLICT DO UPDATE
		if metricDate.Before(cutoffDate) {
			sql = pgInsertFunnelMetricIgnoreSQL // ON CONFLICT DO NOTHING
		}

		_, err = tx.Exec(ctx, sql,
			row.NmID, row.MetricDate,
			row.OpenCount, row.CartCount, row.OrderCount, row.BuyoutCount, row.AddToWishlist,
			row.OrderSum, row.BuyoutSum,
			row.ConversionAddToCart, row.ConversionCartToOrder, row.ConversionBuyout,
		)
		if err != nil {
			return fmt.Errorf("save row nm_id=%d date=%s: %w", row.NmID, row.MetricDate, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// saveFunnelHistory saves all rows with upsert (no window logic — always update).
func (r *PgFunnelRepo) saveFunnelHistory(ctx context.Context, product wb.FunnelProductMeta, rows []wb.FunnelHistoryRow) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := r.upsertProduct(ctx, tx, product); err != nil {
		return err
	}

	for _, row := range rows {
		_, err := tx.Exec(ctx, pgUpsertFunnelMetricSQL,
			row.NmID, row.MetricDate,
			row.OpenCount, row.CartCount, row.OrderCount, row.BuyoutCount, row.AddToWishlist,
			row.OrderSum, row.BuyoutSum,
			row.ConversionAddToCart, row.ConversionCartToOrder, row.ConversionBuyout,
		)
		if err != nil {
			return fmt.Errorf("save row nm_id=%d date=%s: %w", row.NmID, row.MetricDate, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// upsertProduct saves or updates product metadata within an existing transaction.
// $1-$11 map to nm_id through stock_balance_sum (11 fields).
// updated_at uses TO_CHAR SQL function — not a placeholder.
func (r *PgFunnelRepo) upsertProduct(ctx context.Context, tx pgx.Tx, product wb.FunnelProductMeta) error {
	if product.NmID <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, pgUpsertProductSQL,
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

var (
	// pgUpsertProductSQL upserts product metadata.
	// 11 placeholders ($1-$11) + 1 SQL function (TO_CHAR for updated_at) = 12 VALUES.
	// Column count: nm_id + 10 fields + updated_at = 12. ✓
	pgUpsertProductSQL = `
INSERT INTO products (
    nm_id, vendor_code, title, brand_name,
    subject_id, subject_name,
    product_rating, feedback_rating,
    stock_wb, stock_mp, stock_balance_sum,
    updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
    TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
ON CONFLICT (nm_id) DO UPDATE SET
    vendor_code       = EXCLUDED.vendor_code,
    title             = EXCLUDED.title,
    brand_name        = EXCLUDED.brand_name,
    subject_id        = EXCLUDED.subject_id,
    subject_name      = EXCLUDED.subject_name,
    product_rating    = EXCLUDED.product_rating,
    feedback_rating   = EXCLUDED.feedback_rating,
    stock_wb          = EXCLUDED.stock_wb,
    stock_mp          = EXCLUDED.stock_mp,
    stock_balance_sum = EXCLUDED.stock_balance_sum,
    updated_at        = EXCLUDED.updated_at`

	// pgUpsertFunnelMetricSQL upserts funnel metrics (within refresh window).
	// 12 placeholders ($1-$12) = 12 columns. ✓
	pgUpsertFunnelMetricSQL = `
INSERT INTO funnel_metrics_daily (
    nm_id, metric_date,
    open_count, cart_count, order_count, buyout_count, add_to_wishlist,
    order_sum, buyout_sum,
    conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (nm_id, metric_date) DO UPDATE SET
    open_count            = EXCLUDED.open_count,
    cart_count            = EXCLUDED.cart_count,
    order_count           = EXCLUDED.order_count,
    buyout_count          = EXCLUDED.buyout_count,
    add_to_wishlist       = EXCLUDED.add_to_wishlist,
    order_sum             = EXCLUDED.order_sum,
    buyout_sum            = EXCLUDED.buyout_sum,
    conversion_add_to_cart   = EXCLUDED.conversion_add_to_cart,
    conversion_cart_to_order = EXCLUDED.conversion_cart_to_order,
    conversion_buyout        = EXCLUDED.conversion_buyout`

	// pgInsertFunnelMetricIgnoreSQL preserves historical data (outside refresh window).
	// 12 placeholders ($1-$12) = 12 columns. ✓
	pgInsertFunnelMetricIgnoreSQL = `
INSERT INTO funnel_metrics_daily (
    nm_id, metric_date,
    open_count, cart_count, order_count, buyout_count, add_to_wishlist,
    order_sum, buyout_sum,
    conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (nm_id, metric_date) DO NOTHING`
)
