package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
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

// stagedDimRow is read from the staging table.
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

// runStage joins onec_dimensions with cards and writes results to staging table.
func runStage(ctx context.Context, cfg *Config) (int, error) {
	dllog.PrintHeader("fix-card-dimensions: stage",
		dllog.HeaderField{Key: "DB", Value: cfg.DBPath},
	)

	repo, err := openDB(cfg.DBPath)
	if err != nil {
		return 0, err
	}
	defer repo.Close()

	db := repo.DB()

	if _, err := db.ExecContext(ctx, stagingSchemaDDL); err != nil {
		return 0, fmt.Errorf("create staging table: %w", err)
	}

	if _, err := db.ExecContext(ctx, "DELETE FROM fix_card_dimensions_staging"); err != nil {
		return 0, fmt.Errorf("clear staging: %w", err)
	}

	// Get all aggregated dimensions (repo returns all cards with missing dims)
	aggRows, err := repo.GetAggregatedDimensions(ctx)
	if err != nil {
		return 0, fmt.Errorf("get aggregated dimensions: %w", err)
	}

	if len(aggRows) == 0 {
		dllog.Log("no cards with missing dimensions found")
		return 0, nil
	}

	// Apply filters from config
	filtered := applyFilters(aggRows, &cfg.Filters)

	// Additional SQL-based filters (in_stock, seasons, subject) — run as post-filter queries
	filtered, err = applySQLFilters(ctx, db, filtered, &cfg.Filters)
	if err != nil {
		return 0, fmt.Errorf("apply sql filters: %w", err)
	}

	if len(filtered) == 0 {
		dllog.Log("no cards passed filters")
		return 0, nil
	}

	// Insert into staging with ceil-converted dimensions
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

	// Statistics
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

	dllog.Log("staging stats: %d cards (both=%d, dim_only=%d, weight_only=%d)",
		len(filtered), both, dimOnly, weightOnly)

	return len(filtered), nil
}

// applyFilters applies in-memory filters: nm_ids, vendor_codes, allowed_years, exclude_lengths.
func applyFilters(rows []sqlite.DimensionAggRow, f *Filters) []sqlite.DimensionAggRow {
	nmSet := make(map[int]bool, len(f.NmIDs))
	for _, id := range f.NmIDs {
		nmSet[id] = true
	}
	vcSet := make(map[string]bool, len(f.VendorCodes))
	for _, vc := range f.VendorCodes {
		vcSet[vc] = true
	}
	yearSet := make(map[int]bool, len(f.AllowedYears))
	for _, y := range f.AllowedYears {
		yearSet[y] = true
	}
	exclLen := make(map[int]bool, len(f.ExcludeLengths))
	for _, l := range f.ExcludeLengths {
		exclLen[l] = true
	}

	var result []sqlite.DimensionAggRow
	for _, r := range rows {
		if len(nmSet) > 0 && !nmSet[r.NmID] {
			continue
		}
		if len(vcSet) > 0 && !vcSet[r.VendorCode] {
			continue
		}
		vc := r.VendorCode
		if len(exclLen) > 0 && exclLen[len(vc)] {
			continue
		}
		if len(yearSet) > 0 {
			if len(vc) < 3 {
				continue
			}
			var year int
			fmt.Sscanf(vc[1:3], "%d", &year)
			if !yearSet[year] {
				continue
			}
		}
		result = append(result, r)
	}
	return result
}

// applySQLFilters applies DB-based filters: in_stock, seasons, subject, subject_ids.
func applySQLFilters(ctx context.Context, db *sql.DB, rows []sqlite.DimensionAggRow, f *Filters) ([]sqlite.DimensionAggRow, error) {
	if !f.InStock && len(f.Seasons) == 0 && f.Subject == "" && len(f.SubjectIDs) == 0 {
		return rows, nil // nothing to filter
	}

	// Collect nm_ids for SQL-based filtering
	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}

	// Build WHERE conditions
	var conds []string
	var args []any

	// nm_id IN (...) scope
	ph := make([]string, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	nmIn := "c.nm_id IN (" + strings.Join(ph, ",") + ")"

	if f.InStock {
		conds = append(conds, `c.nm_id IN (
			SELECT nm_id FROM stocks_daily_warehouses
			WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses)
			GROUP BY nm_id HAVING SUM(quantity) > 0
		)`)
	}

	if len(f.SubjectIDs) > 0 {
		sph := make([]string, len(f.SubjectIDs))
		for i, id := range f.SubjectIDs {
			sph[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "c.subject_id IN ("+strings.Join(sph, ",")+")")
	}

	if f.Subject != "" {
		conds = append(conds, "LOWER(c.subject_name) = LOWER(?)")
		args = append(args, f.Subject)
	}

	if len(f.Seasons) > 0 {
		sph := make([]string, len(f.Seasons))
		for i, s := range f.Seasons {
			sph[i] = "je.value LIKE ?"
			args = append(args, "%"+s+"%")
		}
		conds = append(conds, `c.nm_id IN (
			SELECT cc.nm_id FROM card_characteristics cc, json_each(cc.json_value) je
			WHERE cc.char_id = (SELECT char_id FROM card_characteristics WHERE name = 'Сезон' LIMIT 1)
			AND (`+strings.Join(sph, " OR ")+`)
		)`)
	}

	if len(conds) == 0 {
		return rows, nil
	}

	query := "SELECT DISTINCT c.nm_id FROM cards c WHERE " + nmIn + " AND (" + strings.Join(conds, " AND ") + ")"

	sqlRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sql filter: %w", err)
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

// showDiff prints before/after for all staged cards.
func showDiff(ctx context.Context, dbPath string) error {
	repo, err := openDB(dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	rows, err := loadStagedRows(ctx, repo.DB())
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		dllog.Log("no staged cards found")
		return nil
	}

	dllog.PrintHeader("fix-card-dimensions: diff",
		dllog.HeaderField{Key: "Cards", Value: fmt.Sprintf("%d", len(rows))},
	)

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

// loadStagedRows reads all rows from the staging table.
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

// loadPendingStagedRows returns only rows with status='pending'.
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

// updateStagingStatus updates the status/error for a staged card.
func updateStagingStatus(ctx context.Context, db *sql.DB, nmID int, status, errMsg string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE fix_card_dimensions_staging SET status = ?, error_msg = ? WHERE nm_id = ?",
		status, errMsg, nmID,
	)
	return err
}
