package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/orders"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: SQLiteSalesRepository satisfies orders.OrdersWriter.
var _ orders.OrdersWriter = (*SQLiteSalesRepository)(nil)

const (
	insertOrderSQL = `
INSERT OR REPLACE INTO orders (
    srid,
    order_date, last_change_date,
    warehouse_name, warehouse_type, country_name, oblast_okrug_name, region_name,
    supplier_article, nm_id, barcode, category, subject, brand, tech_size,
    income_id, is_supply, is_realization,
    total_price, discount_percent, spp, finished_price, price_with_disc,
    is_cancel, cancel_date,
    sticker, g_number,
    downloaded_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`

	deleteOrdersOlderThanSQL = `DELETE FROM orders WHERE order_date < ?`
)

const ordersChunkSize = 500

// SaveOrders saves a batch of orders using INSERT OR REPLACE.
// Chunk size: 500 orders per transaction.
// Returns count of inserted orders.
func (r *SQLiteSalesRepository) SaveOrders(ctx context.Context, orders []wb.OrdersItem) (int, error) {
	if len(orders) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(orders); i += ordersChunkSize {
		end := i + ordersChunkSize
		if end > len(orders) {
			end = len(orders)
		}
		chunk := orders[i:end]

		n, err := r.saveOrdersChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save orders chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveOrdersChunk saves up to 500 orders in a single transaction.
func (r *SQLiteSalesRepository) saveOrdersChunk(ctx context.Context, chunk []wb.OrdersItem) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertOrderSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare order statement: %w", err)
	}
	defer stmt.Close()

	for _, o := range chunk {
		_, err := stmt.ExecContext(ctx,
			o.Srid,
			o.Date, o.LastChangeDate,
			o.WarehouseName, o.WarehouseType, o.CountryName, o.OblastOkrugName, o.RegionName,
			o.SupplierArticle, o.NmID, o.Barcode, o.Category, o.Subject, o.Brand, o.TechSize,
			o.IncomeID, boolToInt(o.IsSupply), boolToInt(o.IsRealization),
			o.TotalPrice, o.DiscountPercent, o.Spp, o.FinishedPrice, o.PriceWithDisc,
			boolToInt(o.IsCancel), o.CancelDate,
			o.Sticker, o.GNumber,
		)
		if err != nil {
			return 0, fmt.Errorf("insert order srid=%s: %w", o.Srid, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// DeleteOrdersOlderThan removes orders with order_date before the given time.
func (r *SQLiteSalesRepository) DeleteOrdersOlderThan(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, deleteOrdersOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old orders: %w", err)
	}
	return result.RowsAffected()
}
