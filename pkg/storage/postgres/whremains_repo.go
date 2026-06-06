package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
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
	pgWhRemainsChunkSize = 500

	// Multi-row INSERT SQL fragments.
	insertWhRemainsPrefixSQL = `INSERT INTO warehouse_remains (snapshot_date, nm_id, barcode, tech_size, warehouse_name, brand, subject_name, vendor_code, volume, quantity) VALUES `
	insertWhRemainsOnConflictSQL = `
ON CONFLICT (snapshot_date, nm_id, tech_size, warehouse_name) DO UPDATE SET
    barcode        = EXCLUDED.barcode,
    brand          = EXCLUDED.brand,
    subject_name   = EXCLUDED.subject_name,
    vendor_code    = EXCLUDED.vendor_code,
    volume         = EXCLUDED.volume,
    quantity       = EXCLUDED.quantity`
	insertWhRemainsCols = 10
)

// Pre-built query for full chunks (500 rows).
var insertWhRemainsFullChunkSQL = BuildMultiRowInsert(insertWhRemainsPrefixSQL, insertWhRemainsOnConflictSQL, pgWhRemainsChunkSize, insertWhRemainsCols)

// SaveRemains saves a batch of flattened warehouse remains rows for a given snapshot date.
// Returns count of upserted rows. Splits into 500-row transactions.
func (r *PgWhRemainsRepo) SaveRemains(ctx context.Context, snapshotDate string, rows []whremains.WhRemainsFlatRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	totalChunks := (len(rows) + pgWhRemainsChunkSize - 1) / pgWhRemainsChunkSize
	total := 0
	start := time.Now()

	for i := 0; i < len(rows); i += pgWhRemainsChunkSize {
		end := min(i+pgWhRemainsChunkSize, len(rows))
		chunk := rows[i:end]
		chunkNum := i/pgWhRemainsChunkSize + 1

		n, err := r.saveWhRemainsChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n

		dllog.Progress(chunkNum, totalChunks, "whremains", "💾 Writing rows", start)
	}
	return total, nil
}

// saveWhRemainsChunk saves up to 500 rows using a single multi-row INSERT statement.
func (r *PgWhRemainsRepo) saveWhRemainsChunk(ctx context.Context, chunk []whremains.WhRemainsFlatRow, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertWhRemainsCols)
	for _, row := range chunk {
		args = append(args,
			snapshotDate,
			row.NmID, row.Barcode, row.TechSize, row.WarehouseName,
			row.Brand, row.SubjectName, row.VendorCode, row.Volume, row.Quantity,
		)
	}

	query := insertWhRemainsFullChunkSQL
	if len(chunk) < pgWhRemainsChunkSize {
		query = BuildMultiRowInsert(insertWhRemainsPrefixSQL, insertWhRemainsOnConflictSQL, len(chunk), insertWhRemainsCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save wh_remains batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// CountRemainsForDate returns number of warehouse remains rows for a specific snapshot date.
func (r *PgWhRemainsRepo) CountRemainsForDate(ctx context.Context, date string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT count(*) FROM warehouse_remains WHERE snapshot_date = $1", date).Scan(&count)
	return count, err
}
