package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/whremains"
)

// Compile-time assertion: PgWhRemainsRepo implements whremains.WhRemainsWriter.
var _ whremains.WhRemainsWriter = (*PgWhRemainsRepo)(nil)

// PgWhRemainsRepo implements whremains.WhRemainsWriter for PostgreSQL.
// Focused repository (ISP) — only warehouse remains persistence methods.
type PgWhRemainsRepo struct {
	pool *pgxpool.Pool
}

// NewPgWhRemainsRepo creates a new PostgreSQL warehouse remains repository.
func NewPgWhRemainsRepo(pool *pgxpool.Pool) *PgWhRemainsRepo {
	return &PgWhRemainsRepo{pool: pool}
}

// InitSchema creates warehouse_remains table if it doesn't exist.
func (r *PgWhRemainsRepo) InitSchema(ctx context.Context) error {
	return initWhRemainsSchema(ctx, r.pool)
}

const (
	// pgUpsertWhRemainsSQL upserts a warehouse remains row.
	// 10 placeholders ($1-$10) = snapshot_date + 9 data columns.
	pgUpsertWhRemainsSQL = `
INSERT INTO warehouse_remains (
    snapshot_date, nm_id, barcode, tech_size, warehouse_name,
    brand, subject_name, vendor_code, volume, quantity
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (snapshot_date, nm_id, tech_size, warehouse_name) DO UPDATE SET
    barcode        = EXCLUDED.barcode,
    brand          = EXCLUDED.brand,
    subject_name   = EXCLUDED.subject_name,
    vendor_code    = EXCLUDED.vendor_code,
    volume         = EXCLUDED.volume,
    quantity       = EXCLUDED.quantity`

	pgWhRemainsChunkSize = 500
)

// SaveRemains saves a batch of flattened warehouse remains rows for a given snapshot date.
// Returns count of upserted rows. Splits into 500-row transactions.
func (r *PgWhRemainsRepo) SaveRemains(ctx context.Context, snapshotDate string, rows []whremains.WhRemainsFlatRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(rows); i += pgWhRemainsChunkSize {
		end := min(i+pgWhRemainsChunkSize, len(rows))
		chunk := rows[i:end]

		n, err := r.saveWhRemainsChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveWhRemainsChunk saves up to 500 rows in a single transaction.
func (r *PgWhRemainsRepo) saveWhRemainsChunk(ctx context.Context, chunk []whremains.WhRemainsFlatRow, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, row := range chunk {
		_, err := tx.Exec(ctx, pgUpsertWhRemainsSQL,
			snapshotDate,
			row.NmID, row.Barcode, row.TechSize, row.WarehouseName,
			row.Brand, row.SubjectName, row.VendorCode, row.Volume, row.Quantity,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert wh_remains nm_id=%d tech_size=%s warehouse=%s: %w",
				row.NmID, row.TechSize, row.WarehouseName, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(chunk), nil
}

// CountRemainsForDate returns number of warehouse remains rows for a specific snapshot date.
func (r *PgWhRemainsRepo) CountRemainsForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT count(*) FROM warehouse_remains WHERE snapshot_date = $1", date).Scan(&count)
	return count, err
}
