package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// dimRowAdapter wraps DimensionAggRow to implement filter.Filterable.
type dimRowAdapter struct {
	row sqlite.DimensionAggRow
}

func (d dimRowAdapter) GetNmID() int          { return d.row.NmID }
func (d dimRowAdapter) GetVendorCode() string  { return d.row.VendorCode }
func (d dimRowAdapter) GetSubjectID() int      { return 0 }
func (d dimRowAdapter) GetSubjectName() string { return "" }
func (d dimRowAdapter) GetSeasons() []string   { return nil }

// getMissingDimensions returns cards with missing dimensions that have 1C data.
// Selects the SKU with the largest volume per good_guid.
func getMissingDimensions(ctx context.Context, db *sql.DB) ([]sqlite.DimensionAggRow, error) {
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
		WHERE (c.dim_length = 0 OR c.dim_width = 0 OR c.dim_height = 0 OR c.dim_weight_brutto = 0)
		  AND CASE WHEN d.volume_cm3 > 0 THEN d.volume_cm3
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
		return nil, fmt.Errorf("query missing dimensions: %w", err)
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
