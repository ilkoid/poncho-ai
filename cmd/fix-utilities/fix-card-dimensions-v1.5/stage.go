package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/filter"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

const stagingSchemaDDL = `
CREATE TABLE IF NOT EXISTS fix_card_dimensions_staging (
    nm_id        INTEGER PRIMARY KEY,
    vendor_code  TEXT NOT NULL,
    old_length   REAL, old_width REAL, old_height REAL, old_weight REAL,
    new_length   REAL, new_width  REAL, new_height  REAL, new_weight  REAL,
    status       TEXT NOT NULL DEFAULT 'pending',
    error_msg    TEXT,
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

type stagedDimRow struct {
	NmID       int
	VendorCode string
	OldLength  float64
	OldWidth   float64
	OldHeight  float64
	OldWeight  float64
	NewLength  float64
	NewWidth   float64
	NewHeight  float64
	NewWeight  float64
	Status     string
	ErrorMsg   string
}

func runStage(ctx context.Context, db *sql.DB, f *filter.Filter, force bool) (int, error) {
	label := "fix-card-dimensions: stage"
	if force {
		label += " [FORCE]"
	}
	fmt.Printf("=== %s ===\n\n", label)

	if _, err := db.ExecContext(ctx, stagingSchemaDDL); err != nil {
		return 0, fmt.Errorf("create staging table: %w", err)
	}

	if _, err := db.ExecContext(ctx, "DELETE FROM fix_card_dimensions_staging"); err != nil {
		return 0, fmt.Errorf("clear staging: %w", err)
	}

	var aggRows []sqlite.DimensionAggRow
	var err error
	if force {
		aggRows, err = getAllAggregatedDimensions(ctx, db)
	} else {
		aggRows, err = getMissingDimensions(ctx, db)
	}
	if err != nil {
		return 0, fmt.Errorf("get aggregated dimensions: %w", err)
	}

	if len(aggRows) == 0 {
		fmt.Println("no cards with missing dimensions found")
		return 0, nil
	}

	filtered := applyFilters(aggRows, f)
	fmt.Printf("in-memory filter: %d → %d cards\n", len(aggRows), len(filtered))

	filtered, err = applySQLFilters(ctx, db, filtered, f)
	if err != nil {
		return 0, fmt.Errorf("apply sql filters: %w", err)
	}
	fmt.Printf("sql filter: → %d cards\n", len(filtered))

	if len(filtered) == 0 {
		fmt.Println("no cards passed filters")
		return 0, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin staging tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_card_dimensions_staging
			(nm_id, vendor_code, old_length, old_width, old_height, old_weight,
			 new_length, new_width, new_height, new_weight, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare staging insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range filtered {
		_, err := stmt.ExecContext(ctx,
			r.NmID, r.VendorCode,
			r.OldLength, r.OldWidth, r.OldHeight, r.OldWeight,
			math.Ceil(r.NewLength), math.Ceil(r.NewWidth), math.Ceil(r.NewHeight), r.NewWeight,
		)
		if err != nil {
			return 0, fmt.Errorf("insert staging row nm_id=%d: %w", r.NmID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit staging: %w", err)
	}

	var both, dimOnly, weightOnly int
	for _, r := range filtered {
		hasZeroDim := r.OldLength == 0 || r.OldWidth == 0 || r.OldHeight == 0
		hasZeroWeight := r.OldWeight == 0
		switch {
		case hasZeroDim && hasZeroWeight:
			both++
		case hasZeroDim:
			dimOnly++
		case hasZeroWeight:
			weightOnly++
		}
	}

	fmt.Printf("staging stats: %d cards (both=%d, dim_only=%d, weight_only=%d)\n",
		len(filtered), both, dimOnly, weightOnly)

	return len(filtered), nil
}

func applyFilters(rows []sqlite.DimensionAggRow, f *filter.Filter) []sqlite.DimensionAggRow {
	var result []sqlite.DimensionAggRow
	for _, r := range rows {
		if f.Matches(dimRowAdapter{row: r}, nil) {
			result = append(result, r)
		}
	}
	return result
}

func applySQLFilters(ctx context.Context, db *sql.DB, rows []sqlite.DimensionAggRow, f *filter.Filter) ([]sqlite.DimensionAggRow, error) {
	sqlFilter := filter.Filter{
		InStock:        f.InStock,
		Seasons:        f.Seasons,
		SubjectIDs:     f.SubjectIDs,
		SubjectName:    f.SubjectName,
		OneCType:       f.OneCType,
		CategoryLevel1: f.CategoryLevel1,
		CategoryLevel2: f.CategoryLevel2,
		ActiveOnly:     f.ActiveOnly,
	}
	if sqlFilter.Empty() {
		return rows, nil
	}

	r, err := sqlFilter.BuildSQL(filter.SQLConfig{CardsAlias: "c"})
	if err != nil {
		return nil, fmt.Errorf("build sql filter: %w", err)
	}

	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}

	query := "SELECT DISTINCT c.nm_id FROM cards c"
	for _, join := range r.JOINs {
		query += " " + join
	}

	scopePh := localPlaceholders(len(nmIDs))
	scopeArgs := localIntSliceToAny(nmIDs)
	if r.Where != "" {
		query += " WHERE c.nm_id IN (" + scopePh + ") AND " + r.Where
	} else {
		query += " WHERE c.nm_id IN (" + scopePh + ")"
	}
	args := append(scopeArgs, r.Args...)

	sqlRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sql filter query: %w", err)
	}
	defer sqlRows.Close()

	passSet := make(map[int]bool)
	for sqlRows.Next() {
		var id int
		if err := sqlRows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan filter: %w", err)
		}
		passSet[id] = true
	}
	if err := sqlRows.Err(); err != nil {
		return nil, err
	}

	var result []sqlite.DimensionAggRow
	for _, r := range rows {
		if passSet[r.NmID] {
			result = append(result, r)
		}
	}
	return result, nil
}

func showDiff(ctx context.Context, db *sql.DB) error {
	rows, err := loadStagedRows(ctx, db)
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		fmt.Println("no staged cards found")
		return nil
	}

	fmt.Printf("=== fix-card-dimensions: diff (%d cards) ===\n\n", len(rows))

	fmt.Printf("%-12s %-20s | %-30s | %-30s\n", "nm_id", "vendor_code", "old (L×W×H, W)", "new (L×W×H, W)")
	fmt.Println(strings.Repeat("-", 100))

	for _, r := range rows {
		old := fmt.Sprintf("%.0f×%.0f×%.0f, %.3f kg", r.OldLength, r.OldWidth, r.OldHeight, r.OldWeight)
		new := fmt.Sprintf("%.0f×%.0f×%.0f, %.3f kg", r.NewLength, r.NewWidth, r.NewHeight, r.NewWeight)
		status := ""
		if r.Status == "applied" {
			status = " [APPLIED]"
		} else if r.Status == "error" {
			status = " [ERROR: " + r.ErrorMsg + "]"
		}
		fmt.Printf("%-12d %-20s | %-30s | %-30s%s\n", r.NmID, r.VendorCode, old, new, status)
	}

	fmt.Printf("\nTotal: %d cards\n", len(rows))
	return nil
}

func loadStagedRows(ctx context.Context, db *sql.DB) ([]stagedDimRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, old_length, old_width, old_height, old_weight,
		       new_length, new_width, new_height, new_weight, status, error_msg
		FROM fix_card_dimensions_staging
		ORDER BY vendor_code
	`)
	if err != nil {
		return nil, fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	var result []stagedDimRow
	for rows.Next() {
		var r stagedDimRow
		var errMsg sql.NullString
		if err := rows.Scan(
			&r.NmID, &r.VendorCode,
			&r.OldLength, &r.OldWidth, &r.OldHeight, &r.OldWeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight, &r.NewWeight,
			&r.Status, &errMsg,
		); err != nil {
			return nil, fmt.Errorf("scan staging row: %w", err)
		}
		if errMsg.Valid {
			r.ErrorMsg = errMsg.String
		}
		result = append(result, r)
	}

	return result, rows.Err()
}

func loadPendingStagedRows(ctx context.Context, db *sql.DB) ([]stagedDimRow, error) {
	all, err := loadStagedRows(ctx, db)
	if err != nil {
		return nil, err
	}
	var pending []stagedDimRow
	for _, r := range all {
		if r.Status == "pending" {
			pending = append(pending, r)
		}
	}
	return pending, nil
}

func updateStagingStatus(ctx context.Context, db *sql.DB, nmID int, status, errMsg string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE fix_card_dimensions_staging SET status = ?, error_msg = ? WHERE nm_id = ?",
		status, errMsg, nmID,
	)
	return err
}

func localPlaceholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "?"
	}
	return strings.Join(p, ",")
}

func localIntSliceToAny(s []int) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}
