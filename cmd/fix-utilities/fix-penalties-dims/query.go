package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stagedRow is one staged penalty→card dimension fix.
type stagedRow struct {
	NmID        int
	VendorCode  string
	SubjectName string
	DimID       int
	DtBonus     string
	OldLength   float64
	OldWidth    float64
	OldHeight   float64
	NewLength   float64
	NewWidth    float64
	NewHeight   float64
	Status      string
	ErrorMsg    string
}

// latestMeasCTE is the shared "latest confirmed measurement per nm_id" subquery.
const latestMeasCTE = `
WITH latest AS (
    SELECT DISTINCT ON (nm_id)
           nm_id, width, length, height, dim_id, dt_bonus, subject_name
    FROM measurement_penalties
    WHERE is_valid = true
    ORDER BY nm_id, dt_bonus DESC
)`

// buildCardFilter builds the optional WHERE fragment for the main stage query,
// where both the `latest` (l) and `cards` (c) aliases are available.
// Returns the SQL fragment ("AND ... AND ...") and args; placeholders start at $1.
func buildCardFilter(f ArticleFilter) (string, []any) {
	var sb strings.Builder
	var args []any
	n := 0
	if len(f.NmIDs) > 0 {
		n++
		fmt.Fprintf(&sb, " AND l.nm_id = ANY($%d)", n)
		args = append(args, int64Slice(f.NmIDs))
	}
	if len(f.VendorCodes) > 0 {
		n++
		fmt.Fprintf(&sb, " AND c.vendor_code = ANY($%d)", n)
		args = append(args, f.VendorCodes)
	}
	if len(f.ExcludeVendorCodes) > 0 {
		n++
		fmt.Fprintf(&sb, " AND c.vendor_code <> ALL($%d)", n)
		args = append(args, f.ExcludeVendorCodes)
	}
	return sb.String(), args
}

// fetchStageCandidates returns the latest confirmed WB measurement joined to the
// card's current dimensions, for every penalized nm_id that has a matching card.
func fetchStageCandidates(ctx context.Context, pool *pgxpool.Pool, f ArticleFilter) ([]stagedRow, error) {
	whereFrag, args := buildCardFilter(f)
	query := latestMeasCTE + `
		SELECT l.nm_id, c.vendor_code, l.subject_name,
		       COALESCE(c.dim_length,0), COALESCE(c.dim_width,0), COALESCE(c.dim_height,0),
		       l.length::float8, l.width::float8, l.height::float8,
		       l.dim_id, l.dt_bonus
		FROM latest l
		JOIN cards c ON c.nm_id = l.nm_id
		WHERE TRUE` + whereFrag + `
		ORDER BY l.nm_id`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query stage candidates: %w", err)
	}
	defer rows.Close()

	var result []stagedRow
	for rows.Next() {
		var r stagedRow
		if err := rows.Scan(
			&r.NmID, &r.VendorCode, &r.SubjectName,
			&r.OldLength, &r.OldWidth, &r.OldHeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight,
			&r.DimID, &r.DtBonus,
		); err != nil {
			return nil, fmt.Errorf("scan stage candidate: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// countPenalizedNmIDs returns the number of distinct confirmed-penalized nm_ids
// (matching the nm_ids filter). Compared against the staged count, this tells the
// manager how many penalized articles had no matching card (or were filtered out).
func countPenalizedNmIDs(ctx context.Context, pool *pgxpool.Pool, f ArticleFilter) (int, error) {
	query := `SELECT count(DISTINCT nm_id) FROM measurement_penalties WHERE is_valid = true`
	args := []any{}
	if len(f.NmIDs) > 0 {
		query += ` AND nm_id = ANY($1)`
		args = append(args, int64Slice(f.NmIDs))
	}
	var count int
	if err := pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count penalized nm_ids: %w", err)
	}
	return count, nil
}

// upsertStaging replaces the staging table contents with the given rows (full
// refresh each run) inside one transaction. status is recomputed per row:
// 'pending' if any dimension differs, 'skipped' if the card already matches the
// measurement (idempotent — already-fixed cards are not rewritten).
func upsertStaging(ctx context.Context, pool *pgxpool.Pool, rows []stagedRow, now string) (pending, skipped int, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin staging tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM fix_penalties_dims_staging"); err != nil {
		return 0, 0, fmt.Errorf("clear staging: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, tx.Commit(ctx)
	}

	const insertSQL = `
		INSERT INTO fix_penalties_dims_staging (
		    nm_id, vendor_code, subject_name, dim_id, dt_bonus,
		    old_length, old_width, old_height,
		    new_length, new_width, new_height,
		    status, error_msg, created_at, applied_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'',$13,'')`

	for _, r := range rows {
		status := "pending"
		if dimsMatch(r.OldLength, r.OldWidth, r.OldHeight, r.NewLength, r.NewWidth, r.NewHeight) {
			status = "skipped"
		}
		if status == "pending" {
			pending++
		} else {
			skipped++
		}
		if _, err := tx.Exec(ctx, insertSQL,
			r.NmID, r.VendorCode, r.SubjectName, r.DimID, r.DtBonus,
			r.OldLength, r.OldWidth, r.OldHeight,
			r.NewLength, r.NewWidth, r.NewHeight,
			status, now,
		); err != nil {
			return 0, 0, fmt.Errorf("insert staging row nm_id=%d: %w", r.NmID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit staging: %w", err)
	}
	return pending, skipped, nil
}

// selectPending returns staged rows with status='pending', ordered by nm_id.
func selectPending(ctx context.Context, pool *pgxpool.Pool) ([]stagedRow, error) {
	return selectByStatus(ctx, pool, "pending")
}

func selectByStatus(ctx context.Context, pool *pgxpool.Pool, status string) ([]stagedRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT nm_id, vendor_code, subject_name, dim_id, dt_bonus,
		       old_length, old_width, old_height,
		       new_length, new_width, new_height,
		       status, error_msg
		FROM fix_penalties_dims_staging
		WHERE status = $1
		ORDER BY nm_id
	`, status)
	if err != nil {
		return nil, fmt.Errorf("query staging %q: %w", status, err)
	}
	defer rows.Close()

	var result []stagedRow
	for rows.Next() {
		var r stagedRow
		if err := rows.Scan(
			&r.NmID, &r.VendorCode, &r.SubjectName, &r.DimID, &r.DtBonus,
			&r.OldLength, &r.OldWidth, &r.OldHeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight,
			&r.Status, &r.ErrorMsg,
		); err != nil {
			return nil, fmt.Errorf("scan staging row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// updateStagingStatus sets status (and optional error message / applied_at) for one nm_id.
func updateStagingStatus(ctx context.Context, pool *pgxpool.Pool, nmID int, status, errMsg string) error {
	var appliedAt string
	if status == "applied" {
		appliedAt = nowUTC()
	}
	_, err := pool.Exec(ctx, `
		UPDATE fix_penalties_dims_staging
		SET status = $1, error_msg = $2, applied_at = COALESCE(NULLIF($3,''), applied_at)
		WHERE nm_id = $4
	`, status, errMsg, appliedAt, nmID)
	if err != nil {
		return fmt.Errorf("update staging status nm_id=%d: %w", nmID, err)
	}
	return nil
}

// --- helpers ---

func int64Slice(s []int) []int64 {
	out := make([]int64, len(s))
	for i, v := range s {
		out[i] = int64(v)
	}
	return out
}

// dimsMatch reports whether the card's current dimensions already equal the WB
// measurement (rounded to integer cm). Measurement values are integer cm; after
// the first fix the card holds the same integer, so this is exact — rounding is
// a defensive guard against float drift.
func dimsMatch(oldL, oldW, oldH, newL, newW, newH float64) bool {
	return roundCm(oldL) == roundCm(newL) &&
		roundCm(oldW) == roundCm(newW) &&
		roundCm(oldH) == roundCm(newH)
}

func roundCm(v float64) int { return int(math.Round(v)) }

// nowUTC returns the current UTC timestamp as "YYYY-MM-DD HH:MM:SS".
func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}
