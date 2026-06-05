package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/penalties"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: SQLiteSalesRepository satisfies penalties.PenaltiesWriter.
var _ penalties.PenaltiesWriter = (*SQLiteSalesRepository)(nil)

const (
	// upsertPenaltySQL uses INSERT OR REPLACE on dim_id (natural key).
	// Penalties can be updated: cancelled (isValid=false), reversal amounts changed.
	upsertPenaltySQL = `
INSERT OR REPLACE INTO measurement_penalties (
    dim_id, nm_id, subject_name, prc_over,
    volume, width, length, height,
    volume_sup, width_sup, length_sup, height_sup,
    photo_urls, dt_bonus,
    is_valid, is_valid_dt,
    reversal_amount, penalty_amount,
    downloaded_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`

	deletePenaltiesOlderThanSQL = `DELETE FROM measurement_penalties WHERE dt_bonus < ?`
)

const penaltiesChunkSize = 500

// SavePenalties saves a batch of measurement penalties using INSERT OR REPLACE.
// Chunk size: 500 per transaction.
// Returns count of inserted penalties.
func (r *SQLiteSalesRepository) SavePenalties(ctx context.Context, items []wb.MeasurementPenaltyItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(items); i += penaltiesChunkSize {
		end := i + penaltiesChunkSize
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

// savePenaltiesChunk saves up to 500 penalties in a single transaction.
func (r *SQLiteSalesRepository) savePenaltiesChunk(ctx context.Context, chunk []wb.MeasurementPenaltyItem) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsertPenaltySQL)
	if err != nil {
		return 0, fmt.Errorf("prepare penalty statement: %w", err)
	}
	defer stmt.Close()

	for _, p := range chunk {
		photoURLs, err := json.Marshal(p.PhotoUrls)
		if err != nil {
			photoURLs = []byte("[]")
		}

		_, err = stmt.ExecContext(ctx,
			p.DimId, p.NmId, p.SubjectName, p.PrcOver,
			p.Volume, p.Width, p.Length, p.Height,
			p.VolumeSup, p.WidthSup, p.LengthSup, p.HeightSup,
			string(photoURLs), p.DtBonus,
			boolToInt(p.IsValid), p.IsValidDt,
			p.ReversalAmount, p.PenaltyAmount,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert penalty dim_id=%d: %w", p.DimId, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// DeletePenaltiesOlderThan removes penalties with dt_bonus before the given time.
func (r *SQLiteSalesRepository) DeletePenaltiesOlderThan(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, deletePenaltiesOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old penalties: %w", err)
	}
	return result.RowsAffected()
}
