package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/stocks"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgStocksRepo implements stocks.StocksWriter.
var _ stocks.StocksWriter = (*PgStocksRepo)(nil)

// PgStocksRepo implements stocks.StocksWriter for PostgreSQL.
// Focused repository (ISP) — only stock snapshot persistence methods.
type PgStocksRepo struct {
	pool *pgxpool.Pool
}

// NewPgStocksRepo creates a new PostgreSQL stocks repository.
func NewPgStocksRepo(pool *pgxpool.Pool) *PgStocksRepo {
	return &PgStocksRepo{pool: pool}
}

// InitSchema creates stocks_daily_warehouses table if it doesn't exist.
func (r *PgStocksRepo) InitSchema(ctx context.Context) error {
	return initStocksSchema(ctx, r.pool)
}

const pgStocksChunkSize = 500

// Multi-row INSERT SQL fragments for stocks_daily_warehouses.
const (
	insertStockCols = 9 // $1-$9 (created_at uses column DEFAULT)

	insertStockPrefixSQL = `INSERT INTO stocks_daily_warehouses (
	    snapshot_date, nm_id, chrt_id, warehouse_id,
	    warehouse_name, region_name,
	    quantity, in_way_to_client, in_way_from_client
	) VALUES `

	insertStockOnConflictSQL = `
	ON CONFLICT (snapshot_date, nm_id, chrt_id, warehouse_id) DO UPDATE SET
	    warehouse_name      = EXCLUDED.warehouse_name,
	    region_name         = EXCLUDED.region_name,
	    quantity            = EXCLUDED.quantity,
	    in_way_to_client    = EXCLUDED.in_way_to_client,
	    in_way_from_client  = EXCLUDED.in_way_from_client`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertStockFullChunkSQL = BuildMultiRowInsert(insertStockPrefixSQL, insertStockOnConflictSQL, pgStocksChunkSize, insertStockCols)

// SaveStocks saves a batch of warehouse stock items for a given snapshot date.
// Returns count of inserted rows. Splits into 500-row transactions.
func (r *PgStocksRepo) SaveStocks(ctx context.Context, snapshotDate string, items []wb.StockWarehouseItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgStocksChunkSize {
		end := min(i+pgStocksChunkSize, len(items))
		chunk := items[i:end]

		n, err := r.saveStocksChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveStocksChunk saves up to pgStocksChunkSize items using a single multi-row INSERT.
func (r *PgStocksRepo) saveStocksChunk(ctx context.Context, chunk []wb.StockWarehouseItem, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertStockCols)
	for _, item := range chunk {
		args = append(args,
			snapshotDate,
			item.NmID, item.ChrtID, item.WarehouseID,
			item.WarehouseName, item.RegionName,
			item.Quantity,
			item.InWayToClient, item.InWayFromClient,
		)
	}

	query := insertStockFullChunkSQL
	if len(chunk) < pgStocksChunkSize {
		query = BuildMultiRowInsert(insertStockPrefixSQL, insertStockOnConflictSQL, len(chunk), insertStockCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save stocks batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// CountStocks returns total number of stock rows in the database.
func (r *PgStocksRepo) CountStocks(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM stocks_daily_warehouses").Scan(&count)
	return count, err
}

// CountStocksForDate returns number of stock rows for a specific snapshot date.
func (r *PgStocksRepo) CountStocksForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT count(*) FROM stocks_daily_warehouses WHERE snapshot_date = $1", date).Scan(&count)
	return count, err
}

// GetDistinctSnapshotDates returns all dates that have stock snapshots, for gap detection.
func (r *PgStocksRepo) GetDistinctSnapshotDates(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT DISTINCT snapshot_date FROM stocks_daily_warehouses ORDER BY snapshot_date")
	if err != nil {
		return nil, fmt.Errorf("query snapshot dates: %w", err)
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan date: %w", err)
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}

// ensure pgx import is used
var _ = pgx.Tx(nil)
