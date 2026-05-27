package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
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

// dimRowAdapter wraps DimensionAggRow to implement filter.Filterable.
// Only NmID and VendorCode are available from aggregated dimensions.
type dimRowAdapter struct {
	row sqlite.DimensionAggRow
}

func (d dimRowAdapter) GetNmID() int            { return d.row.NmID }
func (d dimRowAdapter) GetVendorCode() string    { return d.row.VendorCode }
func (d dimRowAdapter) GetSubjectID() int        { return 0 }
func (d dimRowAdapter) GetSubjectName() string   { return "" }
func (d dimRowAdapter) GetSeasons() []string     { return nil }

// runStage joins onec_dimensions with cards and writes results to staging table.
func runStage(ctx context.Context, cfg *Config, force bool) (int, error) {
	label := "fix-card-dimensions v1.5: stage"
	if force {
		label += " [FORCE]"
	}
	dllog.PrintHeader(label,
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

	// Get all aggregated dimensions
	var aggRows []sqlite.DimensionAggRow
	if force {
		// --force: include all cards with 1C dimension data, even if dims already set
		aggRows, err = getAllAggregatedDimensions(ctx, db)
	} else {
		// Default: only cards with missing dims
		aggRows, err = repo.GetAggregatedDimensions(ctx)
	}
	if err != nil {
		return 0, fmt.Errorf("get aggregated dimensions: %w", err)
	}

	if len(aggRows) == 0 {
		dllog.Log("no cards with missing dimensions found")
		return 0, nil
	}

	// Stage 1: in-memory filter (nm_ids, vendor_codes, allowed_years, exclude_lengths, vendor_code_prefix)
	filtered := applyFilters(aggRows, &cfg.Filters)
	dllog.Log("in-memory filter: %d → %d cards", len(aggRows), len(filtered))

	// Stage 2: SQL-based filter (in_stock, seasons, subject, subject_ids, 1C fields)
	filtered, err = applySQLFilters(ctx, db, filtered, &cfg.Filters)
	if err != nil {
		return 0, fmt.Errorf("apply sql filters: %w", err)
	}
	dllog.Log("sql filter: → %d cards", len(filtered))

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

// applyFilters uses filter.Matches() for in-memory filtering on fields
// available in DimensionAggRow (nm_ids, vendor_codes, allowed_years, exclude_lengths, vendor_code_prefix).
func applyFilters(rows []sqlite.DimensionAggRow, f *filter.Filter) []sqlite.DimensionAggRow {
	var result []sqlite.DimensionAggRow
	for _, r := range rows {
		if f.Matches(dimRowAdapter{row: r}, nil) {
			result = append(result, r)
		}
	}
	return result
}

// applySQLFilters uses filter.BuildSQL() for DB-based filters that require
// data not available in DimensionAggRow (in_stock, seasons, subject, subject_ids, 1C fields).
func applySQLFilters(ctx context.Context, db *sql.DB, rows []sqlite.DimensionAggRow, f *filter.Filter) ([]sqlite.DimensionAggRow, error) {
	// Extract only DB-dependent fields into a sub-filter
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

	// Collect nm_ids from pre-filtered rows
	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}

	// Build scoped query: filter only within our nm_ids
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

	dllog.PrintHeader("fix-card-dimensions v1.5: diff",
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

// getAllAggregatedDimensions returns all cards with 1C dimension data,
// selecting the SKU with the largest volume per good_guid (--force mode).
func getAllAggregatedDimensions(ctx context.Context, db *sql.DB) ([]sqlite.DimensionAggRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			c.nm_id,
			c.vendor_code,
			COALESCE(c.dim_length, 0),
			COALESCE(c.dim_width, 0),
			COALESCE(c.dim_height, 0),
			COALESCE(c.dim_weight_brutto, 0),
			d.length_dm * 10,
			d.width_dm * 10,
			d.height_dm * 10,
			d.weight_kg
		FROM onec_dimensions d
		JOIN onec_goods og ON og.guid = d.good_guid
		JOIN cards c ON c.vendor_code = og.article
		WHERE CASE WHEN d.volume_cm3 > 0 THEN d.volume_cm3
		           ELSE (d.length_dm * 10) * (d.width_dm * 10) * (d.height_dm * 10)
		      END = (
			  SELECT MAX(CASE WHEN d2.volume_cm3 > 0 THEN d2.volume_cm3
			                  ELSE (d2.length_dm * 10) * (d2.width_dm * 10) * (d2.height_dm * 10)
			             END)
			  FROM onec_dimensions d2 WHERE d2.good_guid = d.good_guid
		  )
		GROUP BY c.nm_id ORDER BY c.vendor_code
	`)
	if err != nil {
		return nil, fmt.Errorf("query all aggregated dimensions: %w", err)
	}
	defer rows.Close()

	var result []sqlite.DimensionAggRow
	for rows.Next() {
		var r sqlite.DimensionAggRow
		if err := rows.Scan(
			&r.NmID, &r.VendorCode,
			&r.OldLength, &r.OldWidth, &r.OldHeight, &r.OldWeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight, &r.NewWeight,
		); err != nil {
			return nil, fmt.Errorf("scan dimension agg row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// local helpers for SQL scoping (pkg/filter helpers are unexported)
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
