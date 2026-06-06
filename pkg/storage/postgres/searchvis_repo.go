package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/searchvis"
)

// Compile-time assertions: PgSearchVisRepo implements searchvis Writer + Reader.
var (
	_ searchvis.Writer = (*PgSearchVisRepo)(nil)
	_ searchvis.Reader = (*PgSearchVisRepo)(nil)
)

// PgSearchVisRepo implements searchvis.Writer and searchvis.Reader for PostgreSQL.
// Focused repository (ISP) — only search visibility persistence + nmID resolution.
type PgSearchVisRepo struct {
	pool *pgxpool.Pool
}

// NewPgSearchVisRepo creates a new PostgreSQL search visibility repository.
func NewPgSearchVisRepo(pool *pgxpool.Pool) *PgSearchVisRepo {
	return &PgSearchVisRepo{pool: pool}
}

// InitSchema creates search visibility tables if they don't exist.
func (r *PgSearchVisRepo) InitSchema(ctx context.Context) error {
	return initSearchvisSchema(ctx, r.pool)
}

// ============================================================================
// Writer methods
// ============================================================================

const pgSearchvisChunkSize = 500

// SaveSearchPositions saves batch of position snapshots using ON CONFLICT upsert.
func (r *PgSearchVisRepo) SaveSearchPositions(ctx context.Context, rows []searchvis.SearchPositionRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	for i := 0; i < len(rows); i += pgSearchvisChunkSize {
		end := min(i+pgSearchvisChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return 0, fmt.Errorf("begin transaction: %w", err)
		}

		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertPositionSQL,
				row.NmID, row.SnapshotDate,
				row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition,
				row.Visibility, row.VisibilityDynamics, row.OpenCard, row.OpenCardDynamics,
				row.ClusterFirstHundred, row.ClusterSecondHundred, row.ClusterBelow,
				row.PeriodStart, row.PeriodEnd,
			)
			if err != nil {
				tx.Rollback(ctx)
				return 0, fmt.Errorf("upsert position nm_id=%d date=%s: %w", row.NmID, row.SnapshotDate, err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit positions: %w", err)
		}
	}

	return len(rows), nil
}

// SaveSearchQueries saves batch of search query snapshots using ON CONFLICT upsert.
func (r *PgSearchVisRepo) SaveSearchQueries(ctx context.Context, rows []searchvis.SearchQueryRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	for i := 0; i < len(rows); i += pgSearchvisChunkSize {
		end := min(i+pgSearchvisChunkSize, len(rows))
		chunk := rows[i:end]

		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return 0, fmt.Errorf("begin transaction: %w", err)
		}

		for _, row := range chunk {
			_, err := tx.Exec(ctx, pgUpsertQuerySQL,
				row.NmID, row.SnapshotDate, row.SearchText,
				row.Frequency, row.FrequencyDynamics, row.WeekFrequency,
				row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition, row.MedianPosDynamics,
				row.Visibility, row.OpenCard, row.AddToCart, row.Orders, row.OpenToCart, row.CartToOrder,
				row.VendorCode, row.BrandName, row.SubjectName,
				row.PeriodStart, row.PeriodEnd,
			)
			if err != nil {
				tx.Rollback(ctx)
				return 0, fmt.Errorf("upsert query nm_id=%d text=%q: %w", row.NmID, row.SearchText, err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit queries: %w", err)
		}
	}

	return len(rows), nil
}

// CountSearchPositions returns total rows in search_positions_daily.
func (r *PgSearchVisRepo) CountSearchPositions(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM search_positions_daily`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CountSearchQueries returns total rows in search_queries_daily.
func (r *PgSearchVisRepo) CountSearchQueries(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM search_queries_daily`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ============================================================================
// Reader methods — cross-domain reads from orders/cards tables
// ============================================================================

// GetDistinctNmIDs returns list of distinct nm_id from orders table.
func (r *PgSearchVisRepo) GetDistinctNmIDs(ctx context.Context) ([]int, error) {
	rows, err := r.pool.Query(ctx, "SELECT DISTINCT nm_id FROM orders ORDER BY nm_id")
	if err != nil {
		return nil, fmt.Errorf("query distinct nm_id: %w", err)
	}
	defer rows.Close()

	var nmIDs []int
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		nmIDs = append(nmIDs, nmID)
	}
	return nmIDs, rows.Err()
}

// GetSupplierArticlesByNmIDs returns nm_id → supplier_article map for filtering.
func (r *PgSearchVisRepo) GetSupplierArticlesByNmIDs(ctx context.Context, nmIDs []int) (map[int]string, error) {
	if len(nmIDs) == 0 {
		return make(map[int]string), nil
	}

	placeholders := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(
		"SELECT DISTINCT nm_id, supplier_article FROM orders WHERE nm_id IN (%s) AND supplier_article IS NOT NULL AND supplier_article != ''",
		strings.Join(placeholders, ","),
	)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query supplier articles: %w", err)
	}
	defer rows.Close()

	result := make(map[int]string)
	for rows.Next() {
		var nmID int
		var article string
		if err := rows.Scan(&nmID, &article); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		result[nmID] = article
	}
	return result, rows.Err()
}

// FilterActiveNmIDs filters to only nmIDs with recent orders activity.
func (r *PgSearchVisRepo) FilterActiveNmIDs(ctx context.Context, nmIDs []int, activeDays int) ([]int, error) {
	if activeDays <= 0 || len(nmIDs) == 0 {
		return nmIDs, nil
	}

	placeholders := make([]string, len(nmIDs))
	args := make([]any, 0, len(nmIDs))
	for i, id := range nmIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, id)
	}

	// Interval is a literal (from config, not user input) — safe to inline.
	query := fmt.Sprintf(
		"SELECT DISTINCT nm_id FROM operational_sales WHERE nm_id IN (%s) AND sale_date >= TO_CHAR(NOW() - INTERVAL '%d days', 'YYYY-MM-DD')",
		strings.Join(placeholders, ","),
		activeDays,
	)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("filter active nm_ids: %w", err)
	}
	defer rows.Close()

	var result []int
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		result = append(result, nmID)
	}
	return result, rows.Err()
}

// ============================================================================
// SQL statements
// ============================================================================

var (
	// Upsert search position — 14 columns ($1-$14).
	// PK: (nm_id, snapshot_date, period_start). All other columns in DO UPDATE SET.
	pgUpsertPositionSQL = `
INSERT INTO search_positions_daily (
    nm_id, snapshot_date,
    avg_position, avg_position_dynamics, median_position,
    visibility, visibility_dynamics, open_card, open_card_dynamics,
    cluster_first_hundred, cluster_second_hundred, cluster_below,
    period_start, period_end
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (nm_id, snapshot_date, period_start) DO UPDATE SET
    avg_position = EXCLUDED.avg_position,
    avg_position_dynamics = EXCLUDED.avg_position_dynamics,
    median_position = EXCLUDED.median_position,
    visibility = EXCLUDED.visibility,
    visibility_dynamics = EXCLUDED.visibility_dynamics,
    open_card = EXCLUDED.open_card,
    open_card_dynamics = EXCLUDED.open_card_dynamics,
    cluster_first_hundred = EXCLUDED.cluster_first_hundred,
    cluster_second_hundred = EXCLUDED.cluster_second_hundred,
    cluster_below = EXCLUDED.cluster_below,
    period_end = EXCLUDED.period_end`

	// Upsert search query — 21 columns ($1-$21).
	// PK: (nm_id, search_text, snapshot_date). All other columns in DO UPDATE SET.
	pgUpsertQuerySQL = `
INSERT INTO search_queries_daily (
    nm_id, snapshot_date, search_text,
    frequency, frequency_dynamics, week_frequency,
    avg_position, avg_position_dynamics, median_position, median_position_dynamics,
    visibility, open_card, add_to_cart, orders, open_to_cart, cart_to_order,
    vendor_code, brand_name, subject_name,
    period_start, period_end
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (nm_id, search_text, snapshot_date) DO UPDATE SET
    frequency = EXCLUDED.frequency,
    frequency_dynamics = EXCLUDED.frequency_dynamics,
    week_frequency = EXCLUDED.week_frequency,
    avg_position = EXCLUDED.avg_position,
    avg_position_dynamics = EXCLUDED.avg_position_dynamics,
    median_position = EXCLUDED.median_position,
    median_position_dynamics = EXCLUDED.median_position_dynamics,
    visibility = EXCLUDED.visibility,
    open_card = EXCLUDED.open_card,
    add_to_cart = EXCLUDED.add_to_cart,
    orders = EXCLUDED.orders,
    open_to_cart = EXCLUDED.open_to_cart,
    cart_to_order = EXCLUDED.cart_to_order,
    vendor_code = EXCLUDED.vendor_code,
    brand_name = EXCLUDED.brand_name,
    subject_name = EXCLUDED.subject_name,
    period_end = EXCLUDED.period_end`
)
