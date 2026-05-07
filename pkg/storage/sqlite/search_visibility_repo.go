package sqlite

import (
	"context"
	"fmt"
)

// SearchPositionRow represents one row in search_positions_daily.
type SearchPositionRow struct {
	NmID                 int
	SnapshotDate         string
	AvgPosition          float64
	AvgPositionDynamics  float64
	MedianPosition       float64
	Visibility           float64
	VisibilityDynamics   float64
	OpenCard             int
	OpenCardDynamics     float64
	ClusterFirstHundred  int
	ClusterSecondHundred int
	ClusterBelow         int
	PeriodStart          string
	PeriodEnd            string
}

// SearchQueryRow represents one row in search_queries_daily.
type SearchQueryRow struct {
	NmID                int
	SnapshotDate        string
	SearchText          string
	Frequency           int
	FrequencyDynamics   float64
	WeekFrequency       int
	AvgPosition         float64
	AvgPositionDynamics float64
	MedianPosition      float64
	MedianPosDynamics   float64
	Visibility          float64
	OpenCard            int
	AddToCart           int
	Orders              int
	OpenToCart          float64
	CartToOrder         float64
	VendorCode          string
	BrandName           string
	SubjectName         string
	PeriodStart         string
	PeriodEnd           string
}

// SaveSearchPositions saves batch of search position snapshots.
func (r *SQLiteSalesRepository) SaveSearchPositions(ctx context.Context, rows []SearchPositionRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO search_positions_daily (
			nm_id, snapshot_date,
			avg_position, avg_position_dynamics, median_position,
			visibility, visibility_dynamics, open_card, open_card_dynamics,
			cluster_first_hundred, cluster_second_hundred, cluster_below,
			period_start, period_end
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(nm_id, snapshot_date, period_start) DO UPDATE SET
			avg_position = excluded.avg_position,
			avg_position_dynamics = excluded.avg_position_dynamics,
			median_position = excluded.median_position,
			visibility = excluded.visibility,
			visibility_dynamics = excluded.visibility_dynamics,
			open_card = excluded.open_card,
			open_card_dynamics = excluded.open_card_dynamics,
			cluster_first_hundred = excluded.cluster_first_hundred,
			cluster_second_hundred = excluded.cluster_second_hundred,
			cluster_below = excluded.cluster_below,
			period_end = excluded.period_end
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.NmID, row.SnapshotDate,
			row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition,
			row.Visibility, row.VisibilityDynamics, row.OpenCard, row.OpenCardDynamics,
			row.ClusterFirstHundred, row.ClusterSecondHundred, row.ClusterBelow,
			row.PeriodStart, row.PeriodEnd,
		)
		if err != nil {
			return fmt.Errorf("insert position nm_id=%d date=%s: %w", row.NmID, row.SnapshotDate, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// SaveSearchQueries saves batch of search query snapshots.
func (r *SQLiteSalesRepository) SaveSearchQueries(ctx context.Context, rows []SearchQueryRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO search_queries_daily (
			nm_id, snapshot_date, search_text,
			frequency, frequency_dynamics, week_frequency,
			avg_position, avg_position_dynamics, median_position, median_position_dynamics,
			visibility, open_card, add_to_cart, orders, open_to_cart, cart_to_order,
			vendor_code, brand_name, subject_name,
			period_start, period_end
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(nm_id, search_text, snapshot_date) DO UPDATE SET
			frequency = excluded.frequency,
			frequency_dynamics = excluded.frequency_dynamics,
			week_frequency = excluded.week_frequency,
			avg_position = excluded.avg_position,
			avg_position_dynamics = excluded.avg_position_dynamics,
			median_position = excluded.median_position,
			median_position_dynamics = excluded.median_position_dynamics,
			visibility = excluded.visibility,
			open_card = excluded.open_card,
			add_to_cart = excluded.add_to_cart,
			orders = excluded.orders,
			open_to_cart = excluded.open_to_cart,
			cart_to_order = excluded.cart_to_order,
			vendor_code = excluded.vendor_code,
			brand_name = excluded.brand_name,
			subject_name = excluded.subject_name,
			period_end = excluded.period_end
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.NmID, row.SnapshotDate, row.SearchText,
			row.Frequency, row.FrequencyDynamics, row.WeekFrequency,
			row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition, row.MedianPosDynamics,
			row.Visibility, row.OpenCard, row.AddToCart, row.Orders, row.OpenToCart, row.CartToOrder,
			row.VendorCode, row.BrandName, row.SubjectName,
			row.PeriodStart, row.PeriodEnd,
		)
		if err != nil {
			return fmt.Errorf("insert query nm_id=%d text=%q: %w", row.NmID, row.SearchText, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// CountSearchPositions returns the number of rows in search_positions_daily.
func (r *SQLiteSalesRepository) CountSearchPositions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_positions_daily`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CountSearchQueries returns the number of rows in search_queries_daily.
func (r *SQLiteSalesRepository) CountSearchQueries(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_queries_daily`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// HasSearchPositionsForDate checks if positions data exists for a given snapshot date.
func (r *SQLiteSalesRepository) HasSearchPositionsForDate(ctx context.Context, date string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_positions_daily WHERE snapshot_date = ?`, date,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
