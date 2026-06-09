// Package sqlite provides stock products storage methods on SQLiteSalesRepository.
//
// Methods for saving and querying stock product metrics from WB Seller Analytics API v2.
// Schema is created by initSchema() in repository.go.
package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/stockproducts"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: SQLiteSalesRepository implements stockproducts.StockProductsWriter.
var _ stockproducts.StockProductsWriter = (*SQLiteSalesRepository)(nil)

const insertStockProductSQL = `
INSERT OR REPLACE INTO stock_products (
    snapshot_date, nm_id,
    is_deleted, subject_name, name, vendor_code, brand_name, main_photo, has_sizes,
    orders_count, orders_sum, avg_orders,
    buyout_count, buyout_sum, buyout_percent,
    stock_count, stock_sum,
    to_client_count, from_client_count,
    sale_rate_days, sale_rate_hours,
    avg_stock_turnover_days, avg_stock_turnover_hours,
    office_missing_time_days, office_missing_time_hours,
    lost_orders_count, lost_orders_sum,
    lost_buyouts_count, lost_buyouts_sum,
    avg_orders_by_month,
    current_price_min, current_price_max,
    availability
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

const stockProductsChunkSize = 10_000

// SaveStockProducts saves a batch of stock product metrics for a given snapshot date.
// Returns count of upserted rows.
// Uses 500-row chunked transactions to prevent WAL explosion.
func (r *SQLiteSalesRepository) SaveStockProducts(ctx context.Context, snapshotDate string, items []wb.StockProductItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(items); i += stockProductsChunkSize {
		end := i + stockProductsChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		n, err := r.saveStockProductsChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *SQLiteSalesRepository) saveStockProductsChunk(ctx context.Context, chunk []wb.StockProductItem, snapshotDate string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertStockProductSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range chunk {
		avgByMonthJSON, err := json.Marshal(item.Metrics.AvgOrdersByMonth)
		if err != nil {
			return 0, fmt.Errorf("marshal avg_orders_by_month for nm_id=%d: %w", item.NmID, err)
		}

		_, err = stmt.ExecContext(ctx,
			snapshotDate, item.NmID,
			boolToInt(item.IsDeleted), item.SubjectName, item.Name, item.VendorCode, item.BrandName, item.MainPhoto, boolToInt(item.HasSizes),
			item.Metrics.OrdersCount, item.Metrics.OrdersSum, item.Metrics.AvgOrders,
			item.Metrics.BuyoutCount, item.Metrics.BuyoutSum, item.Metrics.BuyoutPercent,
			item.Metrics.StockCount, item.Metrics.StockSum,
			item.Metrics.ToClientCount, item.Metrics.FromClientCount,
			item.Metrics.SaleRate.Days, item.Metrics.SaleRate.Hours,
			item.Metrics.AvgStockTurnover.Days, item.Metrics.AvgStockTurnover.Hours,
			item.Metrics.OfficeMissingTime.Days, item.Metrics.OfficeMissingTime.Hours,
			item.Metrics.LostOrdersCount, item.Metrics.LostOrdersSum,
			item.Metrics.LostBuyoutsCount, item.Metrics.LostBuyoutsSum,
			string(avgByMonthJSON),
			item.Metrics.CurrentPrice.MinPrice, item.Metrics.CurrentPrice.MaxPrice,
			item.Metrics.Availability,
		)
		if err != nil {
			return 0, fmt.Errorf("insert stock product nm_id=%d: %w", item.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(chunk), nil
}

// CountStockProducts returns total number of rows in stock_products table.
func (r *SQLiteSalesRepository) CountStockProducts(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM stock_products").Scan(&count)
	return count, err
}

// CountStockProductsForDate returns number of rows for a specific snapshot date.
func (r *SQLiteSalesRepository) CountStockProductsForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM stock_products WHERE snapshot_date = ?", date).Scan(&count)
	return count, err
}
