package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/orders"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgOrdersRepo implements orders.OrdersWriter.
var _ orders.OrdersWriter = (*PgOrdersRepo)(nil)

// PgOrdersRepo implements orders.OrdersWriter for PostgreSQL.
// Focused repository (ISP) — only orders persistence methods.
type PgOrdersRepo struct {
	pool *pgxpool.Pool
}

// NewPgOrdersRepo creates a new PostgreSQL orders repository.
func NewPgOrdersRepo(pool *pgxpool.Pool) *PgOrdersRepo {
	return &PgOrdersRepo{pool: pool}
}

// InitSchema creates orders table if it doesn't exist.
func (r *PgOrdersRepo) InitSchema(ctx context.Context) error {
	return initOrdersSchema(ctx, r.pool)
}

// SaveOrders saves a batch of orders using ON CONFLICT for upsert.
// Chunk size: 500 orders per transaction.
// Returns count of saved orders.
func (r *PgOrdersRepo) SaveOrders(ctx context.Context, orders []wb.OrdersItem) (int, error) {
	if len(orders) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(orders); i += pgOrdersChunkSize {
		end := i + pgOrdersChunkSize
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

// DeleteOrdersOlderThan removes orders with order_date before the given time.
func (r *PgOrdersRepo) DeleteOrdersOlderThan(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, pgDeleteOrdersOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old orders: %w", err)
	}
	return tag.RowsAffected(), nil
}

const pgOrdersChunkSize = 500

// saveOrdersChunk saves up to 500 orders using a single multi-row INSERT.
func (r *PgOrdersRepo) saveOrdersChunk(ctx context.Context, chunk []wb.OrdersItem) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertOrderCols)
	for _, o := range chunk {
		args = append(args,
			o.Srid,
			o.Date, o.LastChangeDate,
			o.WarehouseName, o.WarehouseType, o.CountryName, o.OblastOkrugName, o.RegionName,
			o.SupplierArticle, o.NmID, o.Barcode, o.Category, o.Subject, o.Brand, o.TechSize,
			o.IncomeID, o.IsSupply, o.IsRealization,
			o.TotalPrice, o.DiscountPercent, o.Spp, o.FinishedPrice, o.PriceWithDisc,
			o.IsCancel, o.CancelDate,
			o.Sticker, o.GNumber,
		)
	}

	query := insertOrderFullChunkSQL
	if len(chunk) < pgOrdersChunkSize {
		query = BuildMultiRowInsert(insertOrderPrefixSQL, insertOrderOnConflictSQL, len(chunk), insertOrderCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save orders batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// Multi-row INSERT SQL fragments for orders.
const (
	insertOrderCols = 27 // $1-$27 (downloaded_at uses TO_CHAR, not a placeholder)

	insertOrderPrefixSQL = `INSERT INTO orders (
	    srid,
	    order_date, last_change_date,
	    warehouse_name, warehouse_type, country_name, oblast_okrug_name, region_name,
	    supplier_article, nm_id, barcode, category, subject, brand, tech_size,
	    income_id, is_supply, is_realization,
	    total_price, discount_percent, spp, finished_price, price_with_disc,
	    is_cancel, cancel_date,
	    sticker, g_number,
	    downloaded_at
	) VALUES `

	insertOrderOnConflictSQL = `
	ON CONFLICT (srid) DO UPDATE SET
	    order_date = EXCLUDED.order_date,
	    last_change_date = EXCLUDED.last_change_date,
	    warehouse_name = EXCLUDED.warehouse_name,
	    warehouse_type = EXCLUDED.warehouse_type,
	    country_name = EXCLUDED.country_name,
	    oblast_okrug_name = EXCLUDED.oblast_okrug_name,
	    region_name = EXCLUDED.region_name,
	    supplier_article = EXCLUDED.supplier_article,
	    nm_id = EXCLUDED.nm_id,
	    barcode = EXCLUDED.barcode,
	    category = EXCLUDED.category,
	    subject = EXCLUDED.subject,
	    brand = EXCLUDED.brand,
	    tech_size = EXCLUDED.tech_size,
	    income_id = EXCLUDED.income_id,
	    is_supply = EXCLUDED.is_supply,
	    is_realization = EXCLUDED.is_realization,
	    total_price = EXCLUDED.total_price,
	    discount_percent = EXCLUDED.discount_percent,
	    spp = EXCLUDED.spp,
	    finished_price = EXCLUDED.finished_price,
	    price_with_disc = EXCLUDED.price_with_disc,
	    is_cancel = EXCLUDED.is_cancel,
	    cancel_date = EXCLUDED.cancel_date,
	    sticker = EXCLUDED.sticker,
	    g_number = EXCLUDED.g_number,
	    downloaded_at = EXCLUDED.downloaded_at`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertOrderFullChunkSQL = BuildMultiRowInsert(insertOrderPrefixSQL, insertOrderOnConflictSQL, pgOrdersChunkSize, insertOrderCols)

var pgDeleteOrdersOlderThanSQL = `DELETE FROM orders WHERE order_date < $1`
