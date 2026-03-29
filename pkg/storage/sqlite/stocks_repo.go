// Package sqlite provides stock warehouse storage methods on SQLiteSalesRepository.
//
// Methods for saving and querying warehouse stock snapshots from WB Analytics API.
// Schema is created by initSchema() in schema.go.
package sqlite

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const insertStockSQL = `
INSERT OR REPLACE INTO stocks_daily_warehouses (
    snapshot_date, nm_id, chrt_id, warehouse_id,
    warehouse_name, region_name,
    quantity, in_way_to_client, in_way_from_client
) VALUES (?,?,?,?,?,?,?,?,?)`

const stocksChunkSize = 50_000

// SaveStocks saves a batch of warehouse stock items for a given snapshot date.
// Returns count of inserted rows.
// Splits into 50K-row transactions to prevent WAL explosion.
func (r *SQLiteSalesRepository) SaveStocks(ctx context.Context, snapshotDate string, items []wb.StockWarehouseItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += stocksChunkSize {
		end := i + stocksChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		n, err := r.saveStocksChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveStocksChunk saves up to 50K items in a single transaction.
func (r *SQLiteSalesRepository) saveStocksChunk(ctx context.Context, chunk []wb.StockWarehouseItem, snapshotDate string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertStockSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range chunk {
		_, err := stmt.ExecContext(ctx,
			snapshotDate,
			item.NmID, item.ChrtID, item.WarehouseID,
			item.WarehouseName, item.RegionName,
			item.Quantity,
			item.InWayToClient, item.InWayFromClient,
		)
		if err != nil {
			return 0, fmt.Errorf("insert stock nm_id=%d chrt_id=%d warehouse_id=%d: %w",
				item.NmID, item.ChrtID, item.WarehouseID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// CountStocks returns total number of stock rows in the database.
func (r *SQLiteSalesRepository) CountStocks(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM stocks_daily_warehouses").Scan(&count)
	return count, err
}

// CountStocksForDate returns number of stock rows for a specific date.
func (r *SQLiteSalesRepository) CountStocksForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM stocks_daily_warehouses WHERE snapshot_date = ?", date).Scan(&count)
	return count, err
}

// GetDistinctSnapshotDates returns all dates that have stock snapshots, for gap detection.
func (r *SQLiteSalesRepository) GetDistinctSnapshotDates(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT snapshot_date FROM stocks_daily_warehouses ORDER BY snapshot_date")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}
