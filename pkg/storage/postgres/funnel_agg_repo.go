package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/funnelagg"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgFunnelAggRepo implements funnelagg.Writer.
var _ funnelagg.Writer = (*PgFunnelAggRepo)(nil)

// PgFunnelAggRepo implements funnelagg.Writer for PostgreSQL.
// Focused repository (ISP) — only aggregated funnel persistence methods.
type PgFunnelAggRepo struct {
	pool *pgxpool.Pool
}

// NewPgFunnelAggRepo creates a new PostgreSQL aggregated funnel repository.
func NewPgFunnelAggRepo(pool *pgxpool.Pool) *PgFunnelAggRepo {
	return &PgFunnelAggRepo{pool: pool}
}

// InitSchema creates products and funnel_metrics_aggregated tables if they don't exist.
func (r *PgFunnelAggRepo) InitSchema(ctx context.Context) error {
	return initFunnelAggSchema(ctx, r.pool)
}

// GetFunnelAggregatedCount returns count of existing records for a period.
func (r *PgFunnelAggRepo) GetFunnelAggregatedCount(ctx context.Context, periodStart, periodEnd string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM funnel_metrics_aggregated WHERE period_start = $1 AND period_end = $2`,
		periodStart, periodEnd).Scan(&count)
	return count, err
}

// GetDistinctNmIDCount returns count of distinct nmIDs from the sales table.
// Best-effort: used for progress estimation. Returns 0 if sales table is empty.
func (r *PgFunnelAggRepo) GetDistinctNmIDCount(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT nm_id) FROM sales`).Scan(&count)
	return count, err
}

const (
	pgFunnelAggChunkSize = 200

	// Products: 12 param placeholders + 1 TO_CHAR for updated_at (not a placeholder).
	// BuildMultiRowInsert only counts $N placeholders, so cols=12.
	insertAggProductPrefixSQL = `INSERT INTO products (
	    nm_id, vendor_code, title, brand_name,
	    subject_id, subject_name,
	    product_rating, feedback_rating,
	    stock_wb, stock_mp, stock_balance_sum,
	    tags,
	    updated_at
	) VALUES `
	insertAggProductOnConflictSQL = `
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
	    tags              = EXCLUDED.tags,
	    updated_at        = EXCLUDED.updated_at`
	insertAggProductCols = 12

	// Metrics: 90 param placeholders ($1-$90).
	insertAggMetricPrefixSQL = `INSERT INTO funnel_metrics_aggregated (
	    nm_id, period_start, period_end,
	    selected_open_count, selected_cart_count, selected_order_count,
	    selected_order_sum, selected_buyout_count, selected_buyout_sum,
	    selected_cancel_count, selected_cancel_sum, selected_avg_price,
	    selected_avg_orders_count_per_day, selected_share_order_percent,
	    selected_add_to_wishlist, selected_localization_percent,
	    selected_time_to_ready_days, selected_time_to_ready_hours, selected_time_to_ready_mins,
	    selected_wb_club_order_count, selected_wb_club_order_sum,
	    selected_wb_club_buyout_count, selected_wb_club_buyout_sum,
	    selected_wb_club_cancel_count, selected_wb_club_cancel_sum,
	    selected_wb_club_avg_price, selected_wb_club_buyout_percent,
	    selected_wb_club_avg_order_count_per_day,
	    selected_conversion_add_to_cart, selected_conversion_cart_to_order,
	    selected_conversion_buyout,
	    past_period_start, past_period_end,
	    past_open_count, past_cart_count, past_order_count,
	    past_order_sum, past_buyout_count, past_buyout_sum,
	    past_cancel_count, past_cancel_sum, past_avg_price,
	    past_avg_orders_count_per_day, past_share_order_percent,
	    past_add_to_wishlist, past_localization_percent,
	    past_time_to_ready_days, past_time_to_ready_hours, past_time_to_ready_mins,
	    past_wb_club_order_count, past_wb_club_order_sum,
	    past_wb_club_buyout_count, past_wb_club_buyout_sum,
	    past_wb_club_cancel_count, past_wb_club_cancel_sum,
	    past_wb_club_avg_price, past_wb_club_buyout_percent,
	    past_wb_club_avg_order_count_per_day,
	    past_conversion_add_to_cart, past_conversion_cart_to_order,
	    past_conversion_buyout,
	    comparison_open_count_dynamic, comparison_cart_count_dynamic,
	    comparison_order_count_dynamic, comparison_order_sum_dynamic,
	    comparison_buyout_count_dynamic, comparison_buyout_sum_dynamic,
	    comparison_cancel_count_dynamic, comparison_cancel_sum_dynamic,
	    comparison_avg_orders_count_per_day_dynamic, comparison_avg_price_dynamic,
	    comparison_share_order_percent_dynamic, comparison_add_to_wishlist_dynamic,
	    comparison_localization_percent_dynamic,
	    comparison_time_to_ready_days, comparison_time_to_ready_hours,
	    comparison_time_to_ready_mins,
	    comparison_wb_club_order_count, comparison_wb_club_order_sum,
	    comparison_wb_club_buyout_count, comparison_wb_club_buyout_sum,
	    comparison_wb_club_cancel_count, comparison_wb_club_cancel_sum,
	    comparison_wb_club_avg_price, comparison_wb_club_buyout_percent,
	    comparison_wb_club_avg_order_count_per_day,
	    comparison_conversion_add_to_cart, comparison_conversion_cart_to_order,
	    comparison_conversion_buyout,
	    currency
	) VALUES `
	insertAggMetricOnConflictSQL = `
	ON CONFLICT (nm_id, period_start, period_end) DO UPDATE SET
	    selected_open_count                 = EXCLUDED.selected_open_count,
	    selected_cart_count                 = EXCLUDED.selected_cart_count,
	    selected_order_count                = EXCLUDED.selected_order_count,
	    selected_order_sum                  = EXCLUDED.selected_order_sum,
	    selected_buyout_count               = EXCLUDED.selected_buyout_count,
	    selected_buyout_sum                 = EXCLUDED.selected_buyout_sum,
	    selected_cancel_count               = EXCLUDED.selected_cancel_count,
	    selected_cancel_sum                 = EXCLUDED.selected_cancel_sum,
	    selected_avg_price                  = EXCLUDED.selected_avg_price,
	    selected_avg_orders_count_per_day   = EXCLUDED.selected_avg_orders_count_per_day,
	    selected_share_order_percent        = EXCLUDED.selected_share_order_percent,
	    selected_add_to_wishlist            = EXCLUDED.selected_add_to_wishlist,
	    selected_localization_percent       = EXCLUDED.selected_localization_percent,
	    selected_time_to_ready_days         = EXCLUDED.selected_time_to_ready_days,
	    selected_time_to_ready_hours        = EXCLUDED.selected_time_to_ready_hours,
	    selected_time_to_ready_mins         = EXCLUDED.selected_time_to_ready_mins,
	    selected_wb_club_order_count        = EXCLUDED.selected_wb_club_order_count,
	    selected_wb_club_order_sum          = EXCLUDED.selected_wb_club_order_sum,
	    selected_wb_club_buyout_count       = EXCLUDED.selected_wb_club_buyout_count,
	    selected_wb_club_buyout_sum         = EXCLUDED.selected_wb_club_buyout_sum,
	    selected_wb_club_cancel_count       = EXCLUDED.selected_wb_club_cancel_count,
	    selected_wb_club_cancel_sum         = EXCLUDED.selected_wb_club_cancel_sum,
	    selected_wb_club_avg_price          = EXCLUDED.selected_wb_club_avg_price,
	    selected_wb_club_buyout_percent     = EXCLUDED.selected_wb_club_buyout_percent,
	    selected_wb_club_avg_order_count_per_day = EXCLUDED.selected_wb_club_avg_order_count_per_day,
	    selected_conversion_add_to_cart     = EXCLUDED.selected_conversion_add_to_cart,
	    selected_conversion_cart_to_order   = EXCLUDED.selected_conversion_cart_to_order,
	    selected_conversion_buyout          = EXCLUDED.selected_conversion_buyout,
	    past_period_start                   = EXCLUDED.past_period_start,
	    past_period_end                     = EXCLUDED.past_period_end,
	    past_open_count                     = EXCLUDED.past_open_count,
	    past_cart_count                     = EXCLUDED.past_cart_count,
	    past_order_count                    = EXCLUDED.past_order_count,
	    past_order_sum                      = EXCLUDED.past_order_sum,
	    past_buyout_count                   = EXCLUDED.past_buyout_count,
	    past_buyout_sum                     = EXCLUDED.past_buyout_sum,
	    past_cancel_count                   = EXCLUDED.past_cancel_count,
	    past_cancel_sum                     = EXCLUDED.past_cancel_sum,
	    past_avg_price                      = EXCLUDED.past_avg_price,
	    past_avg_orders_count_per_day       = EXCLUDED.past_avg_orders_count_per_day,
	    past_share_order_percent            = EXCLUDED.past_share_order_percent,
	    past_add_to_wishlist                = EXCLUDED.past_add_to_wishlist,
	    past_localization_percent           = EXCLUDED.past_localization_percent,
	    past_time_to_ready_days             = EXCLUDED.past_time_to_ready_days,
	    past_time_to_ready_hours            = EXCLUDED.past_time_to_ready_hours,
	    past_time_to_ready_mins             = EXCLUDED.past_time_to_ready_mins,
	    past_wb_club_order_count            = EXCLUDED.past_wb_club_order_count,
	    past_wb_club_order_sum              = EXCLUDED.past_wb_club_order_sum,
	    past_wb_club_buyout_count           = EXCLUDED.past_wb_club_buyout_count,
	    past_wb_club_buyout_sum             = EXCLUDED.past_wb_club_buyout_sum,
	    past_wb_club_cancel_count           = EXCLUDED.past_wb_club_cancel_count,
	    past_wb_club_cancel_sum             = EXCLUDED.past_wb_club_cancel_sum,
	    past_wb_club_avg_price              = EXCLUDED.past_wb_club_avg_price,
	    past_wb_club_buyout_percent         = EXCLUDED.past_wb_club_buyout_percent,
	    past_wb_club_avg_order_count_per_day = EXCLUDED.past_wb_club_avg_order_count_per_day,
	    past_conversion_add_to_cart         = EXCLUDED.past_conversion_add_to_cart,
	    past_conversion_cart_to_order       = EXCLUDED.past_conversion_cart_to_order,
	    past_conversion_buyout              = EXCLUDED.past_conversion_buyout,
	    comparison_open_count_dynamic       = EXCLUDED.comparison_open_count_dynamic,
	    comparison_cart_count_dynamic       = EXCLUDED.comparison_cart_count_dynamic,
	    comparison_order_count_dynamic      = EXCLUDED.comparison_order_count_dynamic,
	    comparison_order_sum_dynamic        = EXCLUDED.comparison_order_sum_dynamic,
	    comparison_buyout_count_dynamic     = EXCLUDED.comparison_buyout_count_dynamic,
	    comparison_buyout_sum_dynamic       = EXCLUDED.comparison_buyout_sum_dynamic,
	    comparison_cancel_count_dynamic     = EXCLUDED.comparison_cancel_count_dynamic,
	    comparison_cancel_sum_dynamic       = EXCLUDED.comparison_cancel_sum_dynamic,
	    comparison_avg_orders_count_per_day_dynamic = EXCLUDED.comparison_avg_orders_count_per_day_dynamic,
	    comparison_avg_price_dynamic        = EXCLUDED.comparison_avg_price_dynamic,
	    comparison_share_order_percent_dynamic = EXCLUDED.comparison_share_order_percent_dynamic,
	    comparison_add_to_wishlist_dynamic  = EXCLUDED.comparison_add_to_wishlist_dynamic,
	    comparison_localization_percent_dynamic = EXCLUDED.comparison_localization_percent_dynamic,
	    comparison_time_to_ready_days       = EXCLUDED.comparison_time_to_ready_days,
	    comparison_time_to_ready_hours      = EXCLUDED.comparison_time_to_ready_hours,
	    comparison_time_to_ready_mins       = EXCLUDED.comparison_time_to_ready_mins,
	    comparison_wb_club_order_count      = EXCLUDED.comparison_wb_club_order_count,
	    comparison_wb_club_order_sum        = EXCLUDED.comparison_wb_club_order_sum,
	    comparison_wb_club_buyout_count     = EXCLUDED.comparison_wb_club_buyout_count,
	    comparison_wb_club_buyout_sum       = EXCLUDED.comparison_wb_club_buyout_sum,
	    comparison_wb_club_cancel_count     = EXCLUDED.comparison_wb_club_cancel_count,
	    comparison_wb_club_cancel_sum       = EXCLUDED.comparison_wb_club_cancel_sum,
	    comparison_wb_club_avg_price        = EXCLUDED.comparison_wb_club_avg_price,
	    comparison_wb_club_buyout_percent   = EXCLUDED.comparison_wb_club_buyout_percent,
	    comparison_wb_club_avg_order_count_per_day = EXCLUDED.comparison_wb_club_avg_order_count_per_day,
	    comparison_conversion_add_to_cart   = EXCLUDED.comparison_conversion_add_to_cart,
	    comparison_conversion_cart_to_order = EXCLUDED.comparison_conversion_cart_to_order,
	    comparison_conversion_buyout        = EXCLUDED.comparison_conversion_buyout,
	    currency                            = EXCLUDED.currency`
	insertAggMetricCols = 90
)

// Pre-built queries for full chunks (500 rows).
// Products: special case — each row ends with TO_CHAR(...) which is not a $N placeholder,
// so we build the multi-row query manually with appendProductRowPlaceholders.
var insertAggMetricFullChunkSQL = BuildMultiRowInsert(insertAggMetricPrefixSQL, insertAggMetricOnConflictSQL, pgFunnelAggChunkSize, insertAggMetricCols)

// SaveFunnelAggregatedBatch saves a batch of aggregated funnel products.
// Uses a transaction for atomicity: upserts product metadata (with tags) + metrics.
func (r *PgFunnelAggRepo) SaveFunnelAggregatedBatch(
	ctx context.Context,
	products []wb.FunnelAggregatedProduct,
	periodStart, periodEnd, currency string,
) (int, error) {
	if len(products) == 0 {
		return 0, nil
	}

	// Filter out products with invalid nmID upfront.
	valid := make([]wb.FunnelAggregatedProduct, 0, len(products))
	for _, p := range products {
		if p.Product.NmID > 0 {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		return 0, nil
	}

	// Build args for products multi-row INSERT.
	// Each row: 12 params + TO_CHAR appended as literal per row.
	productArgs := make([]any, 0, len(valid)*insertAggProductCols)
	for _, p := range valid {
		tagsJSON, _ := json.Marshal(p.Product.Tags)
		productArgs = append(productArgs,
			p.Product.NmID,
			p.Product.VendorCode,
			p.Product.Title,
			p.Product.BrandName,
			p.Product.SubjectID,
			p.Product.SubjectName,
			p.Product.ProductRating,
			p.Product.FeedbackRating,
			p.Product.Stocks.WB,
			p.Product.Stocks.MP,
			p.Product.Stocks.BalanceSum,
			string(tagsJSON),
		)
	}

	// Build args for metrics multi-row INSERT.
	// Each row: 90 params.
	metricArgs := make([]any, 0, len(valid)*insertAggMetricCols)
	rows := make([]wb.FunnelAggregatedRow, len(valid))
	for i, p := range valid {
		rows[i] = convertAPIResponseToRow(p, periodStart, periodEnd, currency)
		row := rows[i]
		metricArgs = append(metricArgs,
			// Natural key
			row.NmID, row.PeriodStart, row.PeriodEnd,
			// Selected metrics (28 fields)
			row.SelectedOpenCount, row.SelectedCartCount, row.SelectedOrderCount,
			row.SelectedOrderSum, row.SelectedBuyoutCount, row.SelectedBuyoutSum,
			row.SelectedCancelCount, row.SelectedCancelSum, row.SelectedAvgPrice,
			row.SelectedAvgOrdersCountPerDay, row.SelectedShareOrderPercent,
			row.SelectedAddToWishlist, row.SelectedLocalizationPercent,
			row.SelectedTimeToReadyDays, row.SelectedTimeToReadyHours, row.SelectedTimeToReadyMins,
			row.SelectedWBClubOrderCount, row.SelectedWBClubOrderSum,
			row.SelectedWBClubBuyoutCount, row.SelectedWBClubBuyoutSum,
			row.SelectedWBClubCancelCount, row.SelectedWBClubCancelSum,
			row.SelectedWBClubAvgPrice, row.SelectedWBClubBuyoutPercent,
			row.SelectedWBClubAvgOrderCountPerDay,
			row.SelectedConversionAddToCart, row.SelectedConversionCartToOrder,
			row.SelectedConversionBuyout,
			// Past metrics (30 fields)
			row.PastPeriodStart, row.PastPeriodEnd,
			row.PastOpenCount, row.PastCartCount, row.PastOrderCount,
			row.PastOrderSum, row.PastBuyoutCount, row.PastBuyoutSum,
			row.PastCancelCount, row.PastCancelSum, row.PastAvgPrice,
			row.PastAvgOrdersCountPerDay, row.PastShareOrderPercent,
			row.PastAddToWishlist, row.PastLocalizationPercent,
			row.PastTimeToReadyDays, row.PastTimeToReadyHours, row.PastTimeToReadyMins,
			row.PastWBClubOrderCount, row.PastWBClubOrderSum,
			row.PastWBClubBuyoutCount, row.PastWBClubBuyoutSum,
			row.PastWBClubCancelCount, row.PastWBClubCancelSum,
			row.PastWBClubAvgPrice, row.PastWBClubBuyoutPercent,
			row.PastWBClubAvgOrderCountPerDay,
			row.PastConversionAddToCart, row.PastConversionCartToOrder,
			row.PastConversionBuyout,
			// Comparison metrics (28 fields)
			row.ComparisonOpenCountDynamic, row.ComparisonCartCountDynamic,
			row.ComparisonOrderCountDynamic, row.ComparisonOrderSumDynamic,
			row.ComparisonBuyoutCountDynamic, row.ComparisonBuyoutSumDynamic,
			row.ComparisonCancelCountDynamic, row.ComparisonCancelSumDynamic,
			row.ComparisonAvgOrdersCountPerDayDynamic, row.ComparisonAvgPriceDynamic,
			row.ComparisonShareOrderPercentDynamic, row.ComparisonAddToWishlistDynamic,
			row.ComparisonLocalizationPercentDynamic,
			row.ComparisonTimeToReadyDays, row.ComparisonTimeToReadyHours,
			row.ComparisonTimeToReadyMins,
			row.ComparisonWBClubOrderCount, row.ComparisonWBClubOrderSum,
			row.ComparisonWBClubBuyoutCount, row.ComparisonWBClubBuyoutSum,
			row.ComparisonWBClubCancelCount, row.ComparisonWBClubCancelSum,
			row.ComparisonWBClubAvgPrice, row.ComparisonWBClubBuyoutPercent,
			row.ComparisonWBClubAvgOrderCountPerDay,
			row.ComparisonConversionAddToCart, row.ComparisonConversionCartToOrder,
			row.ComparisonConversionBuyout,
			// Metadata
			row.Currency,
		)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Multi-row upsert products.
	productQuery := buildAggProductQuery(len(valid))
	if _, err := tx.Exec(ctx, productQuery, productArgs...); err != nil {
		return 0, fmt.Errorf("upsert products batch (size %d): %w", len(valid), err)
	}

	// 2. Multi-row upsert metrics.
	metricQuery := insertAggMetricFullChunkSQL
	if len(valid) < pgFunnelAggChunkSize {
		metricQuery = BuildMultiRowInsert(insertAggMetricPrefixSQL, insertAggMetricOnConflictSQL, len(valid), insertAggMetricCols)
	}
	tag, err := tx.Exec(ctx, metricQuery, metricArgs...)
	if err != nil {
		return 0, fmt.Errorf("upsert metrics batch (size %d): %w", len(valid), err)
	}
	saved := int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return saved, nil
}

// buildAggProductQuery builds the multi-row INSERT query for products.
// Products have a TO_CHAR(NOW()...) literal for updated_at (not a $N placeholder),
// so we cannot use BuildMultiRowInsert directly. Instead we build the value tuples
// manually: ($1,$2,...,$12, TO_CHAR(...)), ($13,...,$24, TO_CHAR(...)), ...
func buildAggProductQuery(rowCount int) string {
	total := rowCount * insertAggProductCols
	if total > pgMaxParams {
		panic(fmt.Sprintf("buildAggProductQuery: %d rows x %d cols = %d params exceeds PG limit %d",
			rowCount, insertAggProductCols, total, pgMaxParams))
	}

	// Estimate: prefix + onConflict + per-row: "($1,$2,...,$12, TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')), "
	const toCharLiteral = "TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')"
	estimated := len(insertAggProductPrefixSQL) + len(insertAggProductOnConflictSQL) + rowCount*(insertAggProductCols*8+len(toCharLiteral)+8)

	var sb strings.Builder
	sb.Grow(estimated)
	sb.WriteString(insertAggProductPrefixSQL)

	idx := 1
	for row := 0; row < rowCount; row++ {
		if row > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for col := 0; col < insertAggProductCols; col++ {
			if col > 0 {
				sb.WriteString(", ")
			}
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(idx))
			idx++
		}
		// Append TO_CHAR literal for updated_at (not a placeholder).
		sb.WriteString(", ")
		sb.WriteString(toCharLiteral)
		sb.WriteByte(')')
	}

	sb.WriteByte(' ')
	sb.WriteString(insertAggProductOnConflictSQL)
	return sb.String()
}

// convertAPIResponseToRow converts API response to a flat DB row.
// Mirrors pkg/storage/sqlite/funnel_agg.go:convertAPIResponseToRow().
func convertAPIResponseToRow(
	p wb.FunnelAggregatedProduct,
	periodStart, periodEnd, currency string,
) wb.FunnelAggregatedRow {
	row := wb.FunnelAggregatedRow{
		NmID:        p.Product.NmID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Currency:    currency,

		// Selected metrics
		SelectedOpenCount:            p.Statistic.Selected.OpenCount,
		SelectedCartCount:            p.Statistic.Selected.CartCount,
		SelectedOrderCount:           p.Statistic.Selected.OrderCount,
		SelectedOrderSum:             p.Statistic.Selected.OrderSum,
		SelectedBuyoutCount:          p.Statistic.Selected.BuyoutCount,
		SelectedBuyoutSum:            p.Statistic.Selected.BuyoutSum,
		SelectedCancelCount:          p.Statistic.Selected.CancelCount,
		SelectedCancelSum:            p.Statistic.Selected.CancelSum,
		SelectedAvgPrice:             p.Statistic.Selected.AvgPrice,
		SelectedAvgOrdersCountPerDay: p.Statistic.Selected.AvgOrdersCountPerDay,
		SelectedShareOrderPercent:    p.Statistic.Selected.ShareOrderPercent,
		SelectedAddToWishlist:        p.Statistic.Selected.AddToWishlist,
		SelectedLocalizationPercent:  p.Statistic.Selected.LocalizationPercent,
		SelectedTimeToReadyDays:      p.Statistic.Selected.TimeToReady.Days,
		SelectedTimeToReadyHours:     p.Statistic.Selected.TimeToReady.Hours,
		SelectedTimeToReadyMins:      p.Statistic.Selected.TimeToReady.Mins,
		// Selected WBClub
		SelectedWBClubOrderCount:          p.Statistic.Selected.WBClub.OrderCount,
		SelectedWBClubOrderSum:            p.Statistic.Selected.WBClub.OrderSum,
		SelectedWBClubBuyoutCount:         p.Statistic.Selected.WBClub.BuyoutCount,
		SelectedWBClubBuyoutSum:           p.Statistic.Selected.WBClub.BuyoutSum,
		SelectedWBClubCancelCount:         p.Statistic.Selected.WBClub.CancelCount,
		SelectedWBClubCancelSum:           p.Statistic.Selected.WBClub.CancelSum,
		SelectedWBClubAvgPrice:            p.Statistic.Selected.WBClub.AvgPrice,
		SelectedWBClubBuyoutPercent:       p.Statistic.Selected.WBClub.BuyoutPercent,
		SelectedWBClubAvgOrderCountPerDay: p.Statistic.Selected.WBClub.AvgOrderCountPerDay,
		// Selected Conversions
		SelectedConversionAddToCart:   p.Statistic.Selected.Conversions.AddToCartPercent,
		SelectedConversionCartToOrder: p.Statistic.Selected.Conversions.CartToOrderPercent,
		SelectedConversionBuyout:      p.Statistic.Selected.Conversions.BuyoutPercent,
	}

	// Past period (optional)
	if p.Statistic.Past != nil {
		row.PastPeriodStart = &p.Statistic.Past.Period.Start
		row.PastPeriodEnd = &p.Statistic.Past.Period.End
		row.PastOpenCount = &p.Statistic.Past.OpenCount
		row.PastCartCount = &p.Statistic.Past.CartCount
		row.PastOrderCount = &p.Statistic.Past.OrderCount
		row.PastOrderSum = &p.Statistic.Past.OrderSum
		row.PastBuyoutCount = &p.Statistic.Past.BuyoutCount
		row.PastBuyoutSum = &p.Statistic.Past.BuyoutSum
		row.PastCancelCount = &p.Statistic.Past.CancelCount
		row.PastCancelSum = &p.Statistic.Past.CancelSum
		row.PastAvgPrice = &p.Statistic.Past.AvgPrice
		row.PastAvgOrdersCountPerDay = &p.Statistic.Past.AvgOrdersCountPerDay
		row.PastShareOrderPercent = &p.Statistic.Past.ShareOrderPercent
		row.PastAddToWishlist = &p.Statistic.Past.AddToWishlist
		row.PastLocalizationPercent = &p.Statistic.Past.LocalizationPercent
		row.PastTimeToReadyDays = &p.Statistic.Past.TimeToReady.Days
		row.PastTimeToReadyHours = &p.Statistic.Past.TimeToReady.Hours
		row.PastTimeToReadyMins = &p.Statistic.Past.TimeToReady.Mins
		// Past WBClub
		row.PastWBClubOrderCount = &p.Statistic.Past.WBClub.OrderCount
		row.PastWBClubOrderSum = &p.Statistic.Past.WBClub.OrderSum
		row.PastWBClubBuyoutCount = &p.Statistic.Past.WBClub.BuyoutCount
		row.PastWBClubBuyoutSum = &p.Statistic.Past.WBClub.BuyoutSum
		row.PastWBClubCancelCount = &p.Statistic.Past.WBClub.CancelCount
		row.PastWBClubCancelSum = &p.Statistic.Past.WBClub.CancelSum
		row.PastWBClubAvgPrice = &p.Statistic.Past.WBClub.AvgPrice
		row.PastWBClubBuyoutPercent = &p.Statistic.Past.WBClub.BuyoutPercent
		row.PastWBClubAvgOrderCountPerDay = &p.Statistic.Past.WBClub.AvgOrderCountPerDay
		// Past Conversions
		row.PastConversionAddToCart = &p.Statistic.Past.Conversions.AddToCartPercent
		row.PastConversionCartToOrder = &p.Statistic.Past.Conversions.CartToOrderPercent
		row.PastConversionBuyout = &p.Statistic.Past.Conversions.BuyoutPercent
	}

	// Comparison (optional)
	if p.Statistic.Comparison != nil {
		row.ComparisonOpenCountDynamic = &p.Statistic.Comparison.OpenCountDynamic
		row.ComparisonCartCountDynamic = &p.Statistic.Comparison.CartCountDynamic
		row.ComparisonOrderCountDynamic = &p.Statistic.Comparison.OrderCountDynamic
		row.ComparisonOrderSumDynamic = &p.Statistic.Comparison.OrderSumDynamic
		row.ComparisonBuyoutCountDynamic = &p.Statistic.Comparison.BuyoutCountDynamic
		row.ComparisonBuyoutSumDynamic = &p.Statistic.Comparison.BuyoutSumDynamic
		row.ComparisonCancelCountDynamic = &p.Statistic.Comparison.CancelCountDynamic
		row.ComparisonCancelSumDynamic = &p.Statistic.Comparison.CancelSumDynamic
		row.ComparisonAvgOrdersCountPerDayDynamic = &p.Statistic.Comparison.AvgOrdersCountPerDayDynamic
		row.ComparisonAvgPriceDynamic = &p.Statistic.Comparison.AvgPriceDynamic
		row.ComparisonShareOrderPercentDynamic = &p.Statistic.Comparison.ShareOrderPercentDynamic
		row.ComparisonAddToWishlistDynamic = &p.Statistic.Comparison.AddToWishlistDynamic
		row.ComparisonLocalizationPercentDynamic = &p.Statistic.Comparison.LocalizationPercentDynamic
		row.ComparisonTimeToReadyDays = &p.Statistic.Comparison.TimeToReadyDynamic.Days
		row.ComparisonTimeToReadyHours = &p.Statistic.Comparison.TimeToReadyDynamic.Hours
		row.ComparisonTimeToReadyMins = &p.Statistic.Comparison.TimeToReadyDynamic.Mins
		// Comparison WBClub
		row.ComparisonWBClubOrderCount = &p.Statistic.Comparison.WBClubDynamic.OrderCount
		row.ComparisonWBClubOrderSum = &p.Statistic.Comparison.WBClubDynamic.OrderSum
		row.ComparisonWBClubBuyoutCount = &p.Statistic.Comparison.WBClubDynamic.BuyoutCount
		row.ComparisonWBClubBuyoutSum = &p.Statistic.Comparison.WBClubDynamic.BuyoutSum
		row.ComparisonWBClubCancelCount = &p.Statistic.Comparison.WBClubDynamic.CancelCount
		row.ComparisonWBClubCancelSum = &p.Statistic.Comparison.WBClubDynamic.CancelSum
		row.ComparisonWBClubAvgPrice = &p.Statistic.Comparison.WBClubDynamic.AvgPrice
		row.ComparisonWBClubBuyoutPercent = &p.Statistic.Comparison.WBClubDynamic.BuyoutPercent
		row.ComparisonWBClubAvgOrderCountPerDay = &p.Statistic.Comparison.WBClubDynamic.AvgOrderCountPerDay
		// Comparison Conversions
		row.ComparisonConversionAddToCart = &p.Statistic.Comparison.ConversionsDynamic.AddToCartPercent
		row.ComparisonConversionCartToOrder = &p.Statistic.Comparison.ConversionsDynamic.CartToOrderPercent
		row.ComparisonConversionBuyout = &p.Statistic.Comparison.ConversionsDynamic.BuyoutPercent
	}

	return row
}
