package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/penalties"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgPenaltiesRepo implements penalties.PenaltiesWriter.
var _ penalties.PenaltiesWriter = (*PgPenaltiesRepo)(nil)

// PgPenaltiesRepo implements penalties.PenaltiesWriter for PostgreSQL.
// Focused repository (ISP) — only penalties persistence methods.
type PgPenaltiesRepo struct {
	pool *pgxpool.Pool
}

// NewPgPenaltiesRepo creates a new PostgreSQL penalties repository.
func NewPgPenaltiesRepo(pool *pgxpool.Pool) *PgPenaltiesRepo {
	return &PgPenaltiesRepo{pool: pool}
}

// InitSchema creates measurement_penalties table if it doesn't exist.
func (r *PgPenaltiesRepo) InitSchema(ctx context.Context) error {
	return initPenaltiesSchema(ctx, r.pool)
}

// SavePenalties saves a batch of penalties using ON CONFLICT for upsert.
// Chunk size: 500 penalties per transaction.
// Returns count of saved penalties.
func (r *PgPenaltiesRepo) SavePenalties(ctx context.Context, items []wb.MeasurementPenaltyItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgPenaltiesChunkSize {
		end := i + pgPenaltiesChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		n, err := r.savePenaltiesChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save penalties chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// DeletePenaltiesOlderThan removes penalties with dt_bonus before the given time.
func (r *PgPenaltiesRepo) DeletePenaltiesOlderThan(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, pgDeletePenaltiesOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old penalties: %w", err)
	}
	return tag.RowsAffected(), nil
}

const pgPenaltiesChunkSize = 500

// savePenaltiesChunk saves up to 500 penalties in a single transaction.
func (r *PgPenaltiesRepo) savePenaltiesChunk(ctx context.Context, chunk []wb.MeasurementPenaltyItem) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, p := range chunk {
		photoURLs, err := json.Marshal(p.PhotoUrls)
		if err != nil {
			photoURLs = []byte("[]")
		}

		_, err = tx.Exec(ctx, pgUpsertPenaltySQL,
			p.DimId, p.NmId, p.SubjectName, p.PrcOver,
			p.Volume, p.Width, p.Length, p.Height,
			p.VolumeSup, p.WidthSup, p.LengthSup, p.HeightSup,
			string(photoURLs), p.DtBonus,
			p.IsValid, p.IsValidDt,
			p.ReversalAmount, p.PenaltyAmount,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert penalty dim_id=%d: %w", p.DimId, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

var (
	// PostgreSQL upsert — update all fields on conflict (dim_id).
	pgUpsertPenaltySQL = `
INSERT INTO measurement_penalties (
    dim_id, nm_id, subject_name, prc_over,
    volume, width, length, height,
    volume_sup, width_sup, length_sup, height_sup,
    photo_urls, dt_bonus,
    is_valid, is_valid_dt,
    reversal_amount, penalty_amount,
    downloaded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
ON CONFLICT (dim_id) DO UPDATE SET
    nm_id = EXCLUDED.nm_id,
    subject_name = EXCLUDED.subject_name,
    prc_over = EXCLUDED.prc_over,
    volume = EXCLUDED.volume,
    width = EXCLUDED.width,
    length = EXCLUDED.length,
    height = EXCLUDED.height,
    volume_sup = EXCLUDED.volume_sup,
    width_sup = EXCLUDED.width_sup,
    length_sup = EXCLUDED.length_sup,
    height_sup = EXCLUDED.height_sup,
    photo_urls = EXCLUDED.photo_urls,
    dt_bonus = EXCLUDED.dt_bonus,
    is_valid = EXCLUDED.is_valid,
    is_valid_dt = EXCLUDED.is_valid_dt,
    reversal_amount = EXCLUDED.reversal_amount,
    penalty_amount = EXCLUDED.penalty_amount,
    downloaded_at = EXCLUDED.downloaded_at`

	pgDeletePenaltiesOlderThanSQL = `DELETE FROM measurement_penalties WHERE dt_bonus < $1`
)
