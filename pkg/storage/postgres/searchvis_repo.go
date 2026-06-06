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

const pgSearchVisChunkSize = 500

const (
	// Multi-row INSERT SQL fragments for search_positions_daily (14 columns).
	insertSearchPosPrefixSQL = `INSERT INTO search_positions_daily (nm_id, snapshot_date, avg_position, avg_position_dynamics, median_position, visibility, visibility_dynamics, open_card, open_card_dynamics, cluster_first_hundred, cluster_second_hundred, cluster_below, period_start, period_end) VALUES `
	insertSearchPosOnConflictSQL = `
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
	insertSearchPosCols = 14

	// Multi-row INSERT SQL fragments for search_queries_daily (21 columns).
	insertSearchQueryPrefixSQL = `INSERT INTO search_queries_daily (nm_id, snapshot_date, search_text, frequency, frequency_dynamics, week_frequency, avg_position, avg_position_dynamics, median_position, median_position_dynamics, visibility, open_card, add_to_cart, orders, open_to_cart, cart_to_order, vendor_code, brand_name, subject_name, period_start, period_end) VALUES `
	insertSearchQueryOnConflictSQL = `
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
	insertSearchQueryCols = 21
)

// Pre-built queries for full chunks (500 rows).
var (
	insertSearchPosFullChunkSQL   = BuildMultiRowInsert(insertSearchPosPrefixSQL, insertSearchPosOnConflictSQL, pgSearchVisChunkSize, insertSearchPosCols)
	insertSearchQueryFullChunkSQL = BuildMultiRowInsert(insertSearchQueryPrefixSQL, insertSearchQueryOnConflictSQL, pgSearchVisChunkSize, insertSearchQueryCols)
)

// SaveSearchPositions saves batch of position snapshots using ON CONFLICT upsert.
func (r *PgSearchVisRepo) SaveSearchPositions(ctx context.Context, rows []searchvis.SearchPositionRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	for i := 0; i < len(rows); i += pgSearchVisChunkSize {
		end := min(i+pgSearchVisChunkSize, len(rows))
		chunk := rows[i:end]

		n, err := r.saveSearchPosChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save positions chunk at offset %d: %w", i, err)
		}
		_ = n
	}

	return len(rows), nil
}

// saveSearchPosChunk saves up to 500 position rows using a single multi-row INSERT statement.
func (r *PgSearchVisRepo) saveSearchPosChunk(ctx context.Context, chunk []searchvis.SearchPositionRow) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSearchPosCols)
	for _, row := range chunk {
		args = append(args,
			row.NmID, row.SnapshotDate,
			row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition,
			row.Visibility, row.VisibilityDynamics, row.OpenCard, row.OpenCardDynamics,
			row.ClusterFirstHundred, row.ClusterSecondHundred, row.ClusterBelow,
			row.PeriodStart, row.PeriodEnd,
		)
	}

	query := insertSearchPosFullChunkSQL
	if len(chunk) < pgSearchVisChunkSize {
		query = BuildMultiRowInsert(insertSearchPosPrefixSQL, insertSearchPosOnConflictSQL, len(chunk), insertSearchPosCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save positions batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// SaveSearchQueries saves batch of search query snapshots using ON CONFLICT upsert.
func (r *PgSearchVisRepo) SaveSearchQueries(ctx context.Context, rows []searchvis.SearchQueryRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	for i := 0; i < len(rows); i += pgSearchVisChunkSize {
		end := min(i+pgSearchVisChunkSize, len(rows))
		chunk := rows[i:end]

		n, err := r.saveSearchQueryChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save queries chunk at offset %d: %w", i, err)
		}
		_ = n
	}

	return len(rows), nil
}

// saveSearchQueryChunk saves up to 500 query rows using a single multi-row INSERT statement.
func (r *PgSearchVisRepo) saveSearchQueryChunk(ctx context.Context, chunk []searchvis.SearchQueryRow) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSearchQueryCols)
	for _, row := range chunk {
		args = append(args,
			row.NmID, row.SnapshotDate, row.SearchText,
			row.Frequency, row.FrequencyDynamics, row.WeekFrequency,
			row.AvgPosition, row.AvgPositionDynamics, row.MedianPosition, row.MedianPosDynamics,
			row.Visibility, row.OpenCard, row.AddToCart, row.Orders, row.OpenToCart, row.CartToOrder,
			row.VendorCode, row.BrandName, row.SubjectName,
			row.PeriodStart, row.PeriodEnd,
		)
	}

	query := insertSearchQueryFullChunkSQL
	if len(chunk) < pgSearchVisChunkSize {
		query = BuildMultiRowInsert(insertSearchQueryPrefixSQL, insertSearchQueryOnConflictSQL, len(chunk), insertSearchQueryCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save queries batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
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
