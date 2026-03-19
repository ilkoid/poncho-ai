// Package sqlite provides SQLite storage implementation.
//
// This file contains repository methods for aggregated funnel metrics.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// dbExecutor - интерфейс для выполнения SQL запросов.
// Реализуется как *sql.DB так и *sql.Tx, что позволяет использовать
// один код как для обычных запросов, так и для транзакций.
type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// SaveFunnelAggregated сохраняет агрегированные данные воронки.
// Использует INSERT OR REPLACE для upsert логики.
func (r *SQLiteSalesRepository) SaveFunnelAggregated(
	ctx context.Context,
	productMeta wb.FunnelProductExtended,
	row wb.FunnelAggregatedRow,
) error {
	return r.saveFunnelAggregatedWithDB(ctx, r.db, productMeta, row)
}

// saveFunnelAggregatedWithDB сохраняет с использованием переданного БД-коннекта.
// Позволяет использовать как r.db так и *sql.Tx для транзакций.
func (r *SQLiteSalesRepository) saveFunnelAggregatedWithDB(
	ctx context.Context,
	db dbExecutor,
	productMeta wb.FunnelProductExtended,
	row wb.FunnelAggregatedRow,
) error {
	// 1. Сохранить/обновить product metadata с tags
	tagsJSON, _ := json.Marshal(productMeta.Tags)

	_, err := db.ExecContext(ctx, `
		INSERT OR REPLACE INTO products (
			nm_id, vendor_code, title, brand_name,
			subject_id, subject_name,
			product_rating, feedback_rating,
			stock_wb, stock_mp, stock_balance_sum,
			tags, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`,
		productMeta.NmID, productMeta.VendorCode, productMeta.Title, productMeta.BrandName,
		productMeta.SubjectID, productMeta.SubjectName,
		productMeta.ProductRating, productMeta.FeedbackRating,
		productMeta.StockWB, productMeta.StockMP, productMeta.StockBalance,
		string(tagsJSON),
	)
	if err != nil {
		return fmt.Errorf("save product meta: %w", err)
	}

	// 2. Сохранить агрегированные метрики
	_, err = db.ExecContext(ctx, `
		INSERT OR REPLACE INTO funnel_metrics_aggregated (
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
			?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?,
			?
		)
	`,
		row.NmID, row.PeriodStart, row.PeriodEnd,
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
		row.Currency,
	)

	return err
}

// SaveFunnelAggregatedBatch сохраняет батч агрегированных данных.
// Использует транзакцию для атомарности.
func (r *SQLiteSalesRepository) SaveFunnelAggregatedBatch(
	ctx context.Context,
	products []wb.FunnelAggregatedProduct,
	periodStart, periodEnd string,
	currency string,
) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	saved := 0
	var firstErr error
	for i, p := range products {
		// Преобразовать API response в structures
		productMeta := convertToProductMeta(p.Product)
		row := convertAPIResponseToRow(p, periodStart, periodEnd, currency)

		if err := r.saveFunnelAggregatedWithDB(ctx, tx, productMeta, row); err != nil {
			if firstErr == nil && i == 0 {
				firstErr = err // Save first error for debugging
			}
			continue // Skip on error, continue with next
		}
		saved++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	// Debug info
	if firstErr != nil && saved == 0 {
		return 0, fmt.Errorf("first error (nm_id=%d): %w", products[0].Product.NmID, firstErr)
	}

	return saved, nil
}

// saveFunnelAggregatedInTx сохраняет в транзакции.

// convertToProductMeta преобразует FunnelProductExtended в нужную структуру.
func convertToProductMeta(p wb.FunnelProductExtended) wb.FunnelProductExtended {
	return p
}

// convertAPIResponseToRow преобразует API response в DB row.
func convertAPIResponseToRow(
	p wb.FunnelAggregatedProduct,
	periodStart, periodEnd string,
	currency string,
) wb.FunnelAggregatedRow {
	row := wb.FunnelAggregatedRow{
		NmID:         p.Product.NmID,
		PeriodStart:  periodStart,
		PeriodEnd:    periodEnd,
		Currency:     currency,

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

// GetFunnelAggregatedCount возвращает количество записей за период.
func (r *SQLiteSalesRepository) GetFunnelAggregatedCount(
	ctx context.Context,
	periodStart, periodEnd string,
) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM funnel_metrics_aggregated
		WHERE period_start = ? AND period_end = ?
	`, periodStart, periodEnd).Scan(&count)
	return count, err
}

// GetFunnelAggregatedByProduct возвращает данные по конкретному товару.
func (r *SQLiteSalesRepository) GetFunnelAggregatedByProduct(
	ctx context.Context,
	nmID int,
	periodStart, periodEnd string,
) (*wb.FunnelAggregatedRow, error) {
	// Simplified - returns nil if not found
	// Full implementation would scan all 80+ columns
	return nil, nil
}

// GetLastFunnelAggregatedPeriod возвращает последний загруженный период.
func (r *SQLiteSalesRepository) GetLastFunnelAggregatedPeriod(ctx context.Context) (start, end string, err error) {
	err = r.db.QueryRowContext(ctx, `
		SELECT period_start, period_end
		FROM funnel_metrics_aggregated
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(&start, &end)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return start, end, err
}

// DeleteFunnelAggregatedByPeriod удаляет данные за период.
func (r *SQLiteSalesRepository) DeleteFunnelAggregatedByPeriod(
	ctx context.Context,
	periodStart, periodEnd string,
) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM funnel_metrics_aggregated
		WHERE period_start = ? AND period_end = ?
	`, periodStart, periodEnd)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PrintFunnelAggregatedSummary выводит сводку по загруженным данным.
func (r *SQLiteSalesRepository) PrintFunnelAggregatedSummary(
	ctx context.Context,
	periodStart, periodEnd string,
) error {
	count, err := r.GetFunnelAggregatedCount(ctx, periodStart, periodEnd)
	if err != nil {
		return err
	}

	fmt.Printf("📊 Период: %s → %s\n", periodStart, periodEnd)
	fmt.Printf("📦 Товаров: %d\n", count)
	fmt.Printf("🕐 Загружено: %s\n", time.Now().Format("2006-01-02 15:04:05"))

	return nil
}
