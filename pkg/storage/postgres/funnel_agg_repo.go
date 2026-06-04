package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
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

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	saved := 0
	for _, p := range products {
		// 1. Upsert product metadata (with tags JSON).
		if err := r.upsertProductWithTags(ctx, tx, p.Product); err != nil {
			continue // skip on error, continue with next product
		}

		// 2. Convert API response to flat row and upsert metrics.
		row := convertAPIResponseToRow(p, periodStart, periodEnd, currency)
		if err := r.upsertMetricRow(ctx, tx, row); err != nil {
			continue
		}
		saved++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return saved, nil
}

// upsertProductWithTags saves or updates product metadata including tags (JSON).
// Separate from pgUpsertProductSQL in funnel_repo.go because funnel-agg includes tags.
func (r *PgFunnelAggRepo) upsertProductWithTags(ctx context.Context, tx pgx.Tx, p wb.FunnelProductExtended) error {
	if p.NmID <= 0 {
		return nil
	}

	// Copy nested Stocks fields into flat fields.
	stockWB := p.Stocks.WB
	stockMP := p.Stocks.MP
	stockBalance := p.Stocks.BalanceSum

	// Serialize tags to JSON.
	tagsJSON, _ := json.Marshal(p.Tags)

	_, err := tx.Exec(ctx, pgUpsertProductWithTagsSQL,
		p.NmID,
		p.VendorCode,
		p.Title,
		p.BrandName,
		p.SubjectID,
		p.SubjectName,
		p.ProductRating,
		p.FeedbackRating,
		stockWB,
		stockMP,
		stockBalance,
		string(tagsJSON),
	)
	if err != nil {
		return fmt.Errorf("upsert product nm_id=%d: %w", p.NmID, err)
	}
	return nil
}

// upsertMetricRow inserts or updates one aggregated metric row.
func (r *PgFunnelAggRepo) upsertMetricRow(ctx context.Context, tx pgx.Tx, row wb.FunnelAggregatedRow) error {
	_, err := tx.Exec(ctx, pgUpsertFunnelAggSQL,
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
		// Past metrics (30 fields — nullable pointers)
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
		// Comparison metrics (28 fields — nullable pointers)
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
	if err != nil {
		return fmt.Errorf("upsert metric nm_id=%d: %w", row.NmID, err)
	}
	return nil
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

var (
	// pgUpsertProductWithTagsSQL upserts product metadata with tags.
	// 12 placeholders ($1-$12) + 1 SQL function (TO_CHAR for updated_at).
	// Separate from pgUpsertProductSQL in funnel_repo.go because funnel-agg includes tags.
	pgUpsertProductWithTagsSQL = `
INSERT INTO products (
    nm_id, vendor_code, title, brand_name,
    subject_id, subject_name,
    product_rating, feedback_rating,
    stock_wb, stock_mp, stock_balance_sum,
    tags,
    updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
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
    tags              = EXCLUDED.tags,
    updated_at        = EXCLUDED.updated_at`

	// pgUpsertFunnelAggSQL upserts aggregated funnel metrics.
	// 90 placeholders ($1-$90) = 90 columns.
	// ON CONFLICT (nm_id, period_start, period_end) — natural key.
	//
	// Column layout:
	//   $1-$3   natural key (nm_id, period_start, period_end)
	//   $4-$31  selected metrics (28 NOT NULL columns)
	//   $32-$61 past metrics (30 nullable columns)
	//   $62-$89 comparison metrics (28 nullable columns)
	//   $90     currency
	pgUpsertFunnelAggSQL = `
INSERT INTO funnel_metrics_aggregated (
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
) VALUES (
    $1,$2,$3,
    $4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
    $20,$21,$22,$23,$24,$25,$26,$27,$28,
    $29,$30,$31,
    $32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48,$49,
    $50,$51,$52,$53,$54,$55,$56,$57,$58,
    $59,$60,$61,
    $62,$63,$64,$65,$66,$67,$68,$69,$70,$71,$72,$73,$74,$75,$76,$77,
    $78,$79,$80,$81,$82,$83,$84,$85,$86,
    $87,$88,$89,
    $90
)
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
)
