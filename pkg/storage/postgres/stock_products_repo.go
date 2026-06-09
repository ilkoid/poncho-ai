package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/stockproducts"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgStockProductsRepo implements stockproducts.StockProductsWriter.
var _ stockproducts.StockProductsWriter = (*PgStockProductsRepo)(nil)

// PgStockProductsRepo implements stockproducts.StockProductsWriter for PostgreSQL.
type PgStockProductsRepo struct {
	pool *pgxpool.Pool
}

// NewPgStockProductsRepo creates a new PostgreSQL stock products repository.
func NewPgStockProductsRepo(pool *pgxpool.Pool) *PgStockProductsRepo {
	return &PgStockProductsRepo{pool: pool}
}

// InitSchema creates stock_products table if it doesn't exist.
func (r *PgStockProductsRepo) InitSchema(ctx context.Context) error {
	return initStockProductsSchema(ctx, r.pool)
}

const pgStockProductsChunkSize = 1000

// Multi-row INSERT SQL fragments for stock_products.
// Column count: 33 (all EXCEPT id and created_at which have DEFAULTs — Bug #4/#6).
// 33 cols × 1000 rows = 33,000 params < 65,535 PG limit. Safe.
const (
	insertSPCols = 33

	insertSPPrefixSQL = `INSERT INTO stock_products (
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
) VALUES `

	insertSPOnConflictSQL = `
ON CONFLICT (snapshot_date, nm_id) DO UPDATE SET
    is_deleted               = EXCLUDED.is_deleted,
    subject_name             = EXCLUDED.subject_name,
    name                     = EXCLUDED.name,
    vendor_code              = EXCLUDED.vendor_code,
    brand_name               = EXCLUDED.brand_name,
    main_photo               = EXCLUDED.main_photo,
    has_sizes                = EXCLUDED.has_sizes,
    orders_count             = EXCLUDED.orders_count,
    orders_sum               = EXCLUDED.orders_sum,
    avg_orders               = EXCLUDED.avg_orders,
    buyout_count             = EXCLUDED.buyout_count,
    buyout_sum               = EXCLUDED.buyout_sum,
    buyout_percent           = EXCLUDED.buyout_percent,
    stock_count              = EXCLUDED.stock_count,
    stock_sum                = EXCLUDED.stock_sum,
    to_client_count          = EXCLUDED.to_client_count,
    from_client_count        = EXCLUDED.from_client_count,
    sale_rate_days           = EXCLUDED.sale_rate_days,
    sale_rate_hours          = EXCLUDED.sale_rate_hours,
    avg_stock_turnover_days  = EXCLUDED.avg_stock_turnover_days,
    avg_stock_turnover_hours = EXCLUDED.avg_stock_turnover_hours,
    office_missing_time_days    = EXCLUDED.office_missing_time_days,
    office_missing_time_hours   = EXCLUDED.office_missing_time_hours,
    lost_orders_count        = EXCLUDED.lost_orders_count,
    lost_orders_sum          = EXCLUDED.lost_orders_sum,
    lost_buyouts_count       = EXCLUDED.lost_buyouts_count,
    lost_buyouts_sum         = EXCLUDED.lost_buyouts_sum,
    avg_orders_by_month      = EXCLUDED.avg_orders_by_month,
    current_price_min        = EXCLUDED.current_price_min,
    current_price_max        = EXCLUDED.current_price_max,
    availability             = EXCLUDED.availability`
)

// Pre-built query for full chunks (500 rows).
var insertSPFullChunkSQL = BuildMultiRowInsert(insertSPPrefixSQL, insertSPOnConflictSQL, pgStockProductsChunkSize, insertSPCols)

// SaveStockProducts saves a batch of stock product metrics for a given snapshot date.
// Returns count of upserted rows. Splits into 500-row chunks (Bug #2: multi-row INSERT).
func (r *PgStockProductsRepo) SaveStockProducts(ctx context.Context, snapshotDate string, items []wb.StockProductItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	// Defensive dedup: sort by nm_id and deduplicate to avoid SQLSTATE 21000 (Bug #5).
	deduped := dedupStockProducts(items)

	total := 0
	for i := 0; i < len(deduped); i += pgStockProductsChunkSize {
		end := min(i+pgStockProductsChunkSize, len(deduped))
		chunk := deduped[i:end]

		n, err := r.saveSPChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgStockProductsRepo) saveSPChunk(ctx context.Context, chunk []wb.StockProductItem, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSPCols)
	for _, item := range chunk {
		avgByMonthJSON, err := json.Marshal(item.Metrics.AvgOrdersByMonth)
		if err != nil {
			return 0, fmt.Errorf("marshal avg_orders_by_month for nm_id=%d: %w", item.NmID, err)
		}

		args = append(args,
			snapshotDate, item.NmID,
			item.IsDeleted, item.SubjectName, item.Name, item.VendorCode, item.BrandName, item.MainPhoto, item.HasSizes,
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
	}

	query := insertSPFullChunkSQL
	if len(chunk) < pgStockProductsChunkSize {
		query = BuildMultiRowInsert(insertSPPrefixSQL, insertSPOnConflictSQL, len(chunk), insertSPCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save stock products batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// CountStockProducts returns total number of rows in stock_products table.
func (r *PgStockProductsRepo) CountStockProducts(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM stock_products").Scan(&count)
	return count, err
}

// CountStockProductsForDate returns number of rows for a specific snapshot date.
func (r *PgStockProductsRepo) CountStockProductsForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT count(*) FROM stock_products WHERE snapshot_date = $1", date).Scan(&count)
	return count, err
}

// dedupStockProducts removes duplicate nmIDs within a batch (Bug #5 defense).
// Keeps the last occurrence (most recent data).
func dedupStockProducts(items []wb.StockProductItem) []wb.StockProductItem {
	seen := make(map[int64]int, len(items))
	for i, item := range items {
		seen[item.NmID] = i
	}
	result := make([]wb.StockProductItem, 0, len(seen))
	for _, idx := range seen {
		result = append(result, items[idx])
	}
	return result
}

// ensure pgx import is used
var _ = pgx.Tx(nil)
