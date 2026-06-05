// Package sqlite provides warehouse remains storage methods on SQLiteSalesRepository.
//
// Methods for saving and querying warehouse remains snapshots from WB Seller Analytics API.
// Schema is created by initSchema() in repository.go (via schema.go DDL).
package sqlite

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/whremains"
)

// Compile-time assertion: SQLiteSalesRepository implements whremains.WhRemainsWriter.
var _ whremains.WhRemainsWriter = (*SQLiteSalesRepository)(nil)

const insertWhRemainsSQL = `
INSERT OR REPLACE INTO warehouse_remains (
    snapshot_date, nm_id, barcode, tech_size, warehouse_name,
    brand, subject_name, vendor_code, volume, quantity
) VALUES (?,?,?,?,?,?,?,?,?,?)`

const whRemainsChunkSize = 50_000

// SaveRemains saves a batch of flattened warehouse remains rows for a given snapshot date.
// Returns count of upserted rows.
// Splits into 50K-row transactions to prevent WAL explosion.
func (r *SQLiteSalesRepository) SaveRemains(ctx context.Context, snapshotDate string, rows []whremains.WhRemainsFlatRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(rows); i += whRemainsChunkSize {
		end := i + whRemainsChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		n, err := r.saveWhRemainsChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveWhRemainsChunk saves up to 50K rows in a single transaction.
func (r *SQLiteSalesRepository) saveWhRemainsChunk(ctx context.Context, chunk []whremains.WhRemainsFlatRow, snapshotDate string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertWhRemainsSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range chunk {
		_, err := stmt.ExecContext(ctx,
			snapshotDate,
			row.NmID, row.Barcode, row.TechSize, row.WarehouseName,
			row.Brand, row.SubjectName, row.VendorCode, row.Volume, row.Quantity,
		)
		if err != nil {
			return 0, fmt.Errorf("insert wh_remains nm_id=%d tech_size=%s warehouse=%s: %w",
				row.NmID, row.TechSize, row.WarehouseName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// CountRemainsForDate returns number of warehouse remains rows for a specific snapshot date.
func (r *SQLiteSalesRepository) CountRemainsForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM warehouse_remains WHERE snapshot_date = ?", date).Scan(&count)
	return count, err
}
