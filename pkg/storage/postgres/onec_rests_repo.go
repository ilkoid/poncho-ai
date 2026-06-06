package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/onec"
)

// Compile-time assertion: PgOneCRestsRepo implements onec.RestsWriter.
var _ onec.RestsWriter = (*PgOneCRestsRepo)(nil)

const pgRestsChunkSize = 500

// PgOneCRestsRepo implements onec.RestsWriter for PostgreSQL.
type PgOneCRestsRepo struct {
	pool *pgxpool.Pool
}

// NewPgOneCRestsRepo creates a new PostgreSQL 1C rests repository.
func NewPgOneCRestsRepo(pool *pgxpool.Pool) *PgOneCRestsRepo {
	return &PgOneCRestsRepo{pool: pool}
}

// InitSchema creates onec_rests table if it doesn't exist.
func (r *PgOneCRestsRepo) InitSchema(ctx context.Context) error {
	return initOneCRestsSchema(ctx, r.pool)
}

// ---------------------------------------------------------------------------
// RestsWriter interface
// ---------------------------------------------------------------------------

// SaveRests saves a batch of rests rows using ON CONFLICT upsert.
func (r *PgOneCRestsRepo) SaveRests(ctx context.Context, rows []onec.RestsRow, snapshotDate string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(rows); i += pgRestsChunkSize {
		end := min(i+pgRestsChunkSize, len(rows))
		n, err := r.saveRestsChunk(ctx, rows[i:end], snapshotDate)
		if err != nil {
			return total, fmt.Errorf("save rests chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// CountRests returns total number of rests rows.
func (r *PgOneCRestsRepo) CountRests(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM onec_rests").Scan(&count)
	return count, err
}

// CleanRests deletes all rows from onec_rests table.
func (r *PgOneCRestsRepo) CleanRests(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM onec_rests")
	return err
}

// PurgeOldRestsSnapshots deletes snapshots older than retentionDays counting from yesterday.
// retentionDays=7 → keep snapshots from yesterday through 6 days before.
// Today's snapshot is always kept (day still in progress).
func (r *PgOneCRestsRepo) PurgeOldRestsSnapshots(ctx context.Context, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	tag, err := r.pool.Exec(ctx,
		`DELETE FROM onec_rests WHERE snapshot_date < TO_CHAR(CURRENT_DATE - ($1 + 1) * INTERVAL '1 day', 'YYYY-MM-DD')`,
		retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("purge old rests snapshots: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Multi-row INSERT SQL fragments
// ---------------------------------------------------------------------------

const insertRestPrefixSQL = `INSERT INTO onec_rests (good_guid, sku_guid, storage_guid, snapshot_date, storage_name, stock, reserv, free, first_stage) VALUES `

const insertRestOnConflictSQL = `
ON CONFLICT (good_guid, sku_guid, storage_guid, snapshot_date) DO UPDATE SET
    storage_name = EXCLUDED.storage_name,
    stock = EXCLUDED.stock,
    reserv = EXCLUDED.reserv,
    free = EXCLUDED.free,
    first_stage = EXCLUDED.first_stage,
    downloaded_at = EXCLUDED.downloaded_at`

const insertRestCols = 9

var insertRestFullChunkSQL = BuildMultiRowInsert(insertRestPrefixSQL, insertRestOnConflictSQL, pgRestsChunkSize, insertRestCols)

// ---------------------------------------------------------------------------
// Chunk-level save (multi-row INSERT per chunk)
// ---------------------------------------------------------------------------

func (r *PgOneCRestsRepo) saveRestsChunk(ctx context.Context, chunk []onec.RestsRow, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertRestCols)
	for _, row := range chunk {
		args = append(args,
			row.GoodGUID, row.SKUGUID, row.StorageGUID, snapshotDate,
			row.StorageName, row.Stock, row.Reserv, row.Free, row.FirstStage,
		)
	}

	query := insertRestFullChunkSQL
	if len(chunk) < pgRestsChunkSize {
		query = BuildMultiRowInsert(insertRestPrefixSQL, insertRestOnConflictSQL, len(chunk), insertRestCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save rests batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
