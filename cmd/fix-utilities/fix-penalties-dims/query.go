package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
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

// buildCardFilter builds the optional WHERE fragment for the stage query, where both
// the `latest` (l) and `cards` (c) aliases are available. SQLite has no ANY()/ALL()
// array predicates, so slices expand to IN (?,?,…) / NOT IN (?,?,…) with one ? per
// value. Returns the SQL fragment (" AND … AND …") and args in order.
func buildCardFilter(f ArticleFilter) (string, []any) {
	var sb strings.Builder
	var args []any
	if len(f.NmIDs) > 0 {
		ph, a := inPlaceholders(f.NmIDs)
		fmt.Fprintf(&sb, " AND l.nm_id IN (%s)", ph)
		args = append(args, a...)
	}
	if len(f.VendorCodes) > 0 {
		ph, a := inPlaceholders(f.VendorCodes)
		fmt.Fprintf(&sb, " AND c.vendor_code IN (%s)", ph)
		args = append(args, a...)
	}
	if len(f.ExcludeVendorCodes) > 0 {
		ph, a := inPlaceholders(f.ExcludeVendorCodes)
		fmt.Fprintf(&sb, " AND c.vendor_code NOT IN (%s)", ph)
		args = append(args, a...)
	}
	return sb.String(), args
}

// fetchStageCandidates returns the latest confirmed WB measurement joined to the
// card's current dimensions, for every penalized nm_id that has a matching card.
//
// "Latest measurement per nm_id" uses a ROW_NUMBER() window (SQLite ≥3.25) since
// SQLite lacks PostgreSQL's DISTINCT ON. measurement_penalties.is_valid is INTEGER
// (1/0) in SQLite.
func fetchStageCandidates(ctx context.Context, db *sql.DB, f ArticleFilter) ([]stagedRow, error) {
	whereFrag, args := buildCardFilter(f)
	query := `
		WITH latest AS (
		    SELECT nm_id, width, length, height, dim_id, dt_bonus, subject_name,
		           ROW_NUMBER() OVER (PARTITION BY nm_id ORDER BY dt_bonus DESC) AS rn
		    FROM measurement_penalties
		    WHERE is_valid = 1
		)
		SELECT l.nm_id, c.vendor_code, l.subject_name,
		       COALESCE(c.dim_length,0), COALESCE(c.dim_width,0), COALESCE(c.dim_height,0),
		       CAST(l.length AS REAL), CAST(l.width AS REAL), CAST(l.height AS REAL),
		       l.dim_id, l.dt_bonus
		FROM latest l
		JOIN cards c ON c.nm_id = l.nm_id
		WHERE l.rn = 1` + whereFrag + `
		ORDER BY l.nm_id`

	rows, err := db.QueryContext(ctx, query, args...)
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
func countPenalizedNmIDs(ctx context.Context, db *sql.DB, f ArticleFilter) (int, error) {
	query := `SELECT count(DISTINCT nm_id) FROM measurement_penalties WHERE is_valid = 1`
	var args []any
	if len(f.NmIDs) > 0 {
		ph, a := inPlaceholders(f.NmIDs)
		query += ` AND nm_id IN (` + ph + `)`
		args = append(args, a...)
	}
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count penalized nm_ids: %w", err)
	}
	return count, nil
}

// upsertStaging replaces the staging table contents with the given rows (full
// refresh each run) inside one transaction. status is recomputed per row via
// classifyFix: 'pending' only when the card under-declares vs the measurement
// (real penalty risk); 'skipped' for exact matches and over-declared cards (no
// risk — see classifyFix). Idempotent: already-fixed cards no longer under-declare.
func upsertStaging(ctx context.Context, db *sql.DB, rows []stagedRow, now string) (pending, skipped int, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin staging tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit; safe

	if _, err := tx.ExecContext(ctx, "DELETE FROM fix_penalties_dims_staging"); err != nil {
		return 0, 0, fmt.Errorf("clear staging: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, tx.Commit()
	}

	const insertSQL = `
		INSERT INTO fix_penalties_dims_staging (
		    nm_id, vendor_code, subject_name, dim_id, dt_bonus,
		    old_length, old_width, old_height,
		    new_length, new_width, new_height,
		    status, error_msg, created_at, applied_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'',?,'')`

	for _, r := range rows {
		status, _ := classifyFix(r.OldLength, r.OldWidth, r.OldHeight,
			r.NewLength, r.NewWidth, r.NewHeight)
		if status == "pending" {
			pending++
		} else {
			skipped++
		}
		if _, err := tx.ExecContext(ctx, insertSQL,
			r.NmID, r.VendorCode, r.SubjectName, r.DimID, r.DtBonus,
			r.OldLength, r.OldWidth, r.OldHeight,
			r.NewLength, r.NewWidth, r.NewHeight,
			status, now,
		); err != nil {
			return 0, 0, fmt.Errorf("insert staging row nm_id=%d: %w", r.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit staging: %w", err)
	}
	return pending, skipped, nil
}

// selectPending returns staged rows with status='pending', ordered by nm_id.
func selectPending(ctx context.Context, db *sql.DB) ([]stagedRow, error) {
	return selectByStatus(ctx, db, "pending")
}

func selectByStatus(ctx context.Context, db *sql.DB, status string) ([]stagedRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, subject_name, dim_id, dt_bonus,
		       old_length, old_width, old_height,
		       new_length, new_width, new_height,
		       status, error_msg
		FROM fix_penalties_dims_staging
		WHERE status = ?
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

// stagingCounts returns the row count per status across the staging table. Used by
// showDiff to report the skipped total without fetching (and printing) every row.
func stagingCounts(ctx context.Context, db *sql.DB) (map[string]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT status, count(*) FROM fix_penalties_dims_staging GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("query staging counts: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan staging count: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// updateStagingStatus sets status (and optional error message / applied_at) for one nm_id.
func updateStagingStatus(ctx context.Context, db *sql.DB, nmID int, status, errMsg string) error {
	var appliedAt string
	if status == "applied" {
		appliedAt = nowUTC()
	}
	_, err := db.ExecContext(ctx, `
		UPDATE fix_penalties_dims_staging
		SET status = ?, error_msg = ?, applied_at = COALESCE(NULLIF(?, ''), applied_at)
		WHERE nm_id = ?
	`, status, errMsg, appliedAt, nmID)
	if err != nil {
		return fmt.Errorf("update staging status nm_id=%d: %w", nmID, err)
	}
	return nil
}

// --- helpers ---

// inPlaceholders builds a "(?,?,…)" placeholder group of len(vals) and the matching
// args as []any, for SQLite IN/NOT IN clauses (no array bindings).
func inPlaceholders[T any](vals []T) (string, []any) {
	ph := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		ph[i] = "?"
		args[i] = v
	}
	return strings.Join(ph, ","), args
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

// classifyFix decides whether a staged row needs a card rewrite.
//
// A penalty can only recur when the card UNDER-declares: the warehouse measured a
// bigger volume than the card declares (real > declared → prc_over > 0 → future fine).
// Over-declared cards (card ≥ measurement) and exact matches carry no penalty risk and
// are skipped — shrinking a card to match a smaller measurement fixes nothing and risks
// a needless full-card overwrite. The comparison is by volume (L×W×H product), which is
// commutative and therefore immune to the L/W axis swaps that pervade the data; prc_over
// itself is volume-based, so the fix/no-fix decision must be too.
func classifyFix(oldL, oldW, oldH, newL, newW, newH float64) (status, reason string) {
	if dimsMatch(oldL, oldW, oldH, newL, newW, newH) {
		return "skipped", "card == measurement"
	}
	if oldL*oldW*oldH >= newL*newW*newH {
		return "skipped", "over-declared: card volume >= measurement, no penalty risk"
	}
	return "pending", "under-declared: card volume < measurement"
}

// nowUTC returns the current UTC timestamp as "YYYY-MM-DD HH:MM:SS".
func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}
