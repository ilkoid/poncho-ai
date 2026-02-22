// Package wb provides snapshot sales service for E2E testing.
package wb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Ensure snapshotSalesService implements SalesService.
var _ SalesService = (*snapshotSalesService)(nil)

// snapshotSalesService implements SalesService backed by SQLite.
type snapshotSalesService struct {
	db *sql.DB
}

// GetFunnelMetrics retrieves funnel metrics from snapshot database.
// Aggregates daily metrics for the specified period.
func (s *snapshotSalesService) GetFunnelMetrics(ctx context.Context, req FunnelRequest) (*FunnelMetrics, error) {
	// Validation
	if len(req.NmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(req.NmIDs) > 100 {
		return nil, fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if req.Period < 1 || req.Period > 365 {
		return nil, fmt.Errorf("period must be between 1 and 365")
	}

	// Calculate date range
	now := time.Now()
	dateFrom := now.AddDate(0, 0, -req.Period).Format("2006-01-02")
	dateTo := now.Format("2006-01-02")

	// Build query with nmIDs
	placeholders := make([]string, len(req.NmIDs))
	args := make([]interface{}, len(req.NmIDs)+2)
	for i, id := range req.NmIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(req.NmIDs)] = dateFrom
	args[len(req.NmIDs)+1] = dateTo

	query := fmt.Sprintf(`
		SELECT
			nm_id,
			SUM(open_count) as open_count,
			SUM(cart_count) as cart_count,
			SUM(order_count) as order_count,
			SUM(buyout_count) as buyout_count,
			SUM(cancel_count) as cancel_count,
			SUM(order_sum) as order_sum,
			SUM(buyout_sum) as buyout_sum,
			AVG(conversion_buyout) as conversion_buyout,
			AVG(wb_club_buyout_percent) as wb_club_percent
		FROM funnel_metrics_daily
		WHERE nm_id IN (%s) AND metric_date >= ? AND metric_date <= ?
		GROUP BY nm_id
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query funnel metrics: %w", err)
	}
	defer rows.Close()

	metrics := &FunnelMetrics{
		Products:  make(map[int]*ProductFunnelData),
		Period:    req.Period,
		Timestamp: now,
	}

	for rows.Next() {
		var data ProductFunnelData
		var conversionBuyout, wbClubPercent sql.NullFloat64

		if err := rows.Scan(
			&data.NmID,
			&data.OpenCount,
			&data.CartCount,
			&data.OrderCount,
			&data.BuyoutCount,
			&data.CancelCount,
			&data.OrderSum,
			&data.BuyoutSum,
			&conversionBuyout,
			&wbClubPercent,
		); err != nil {
			return nil, fmt.Errorf("scan funnel row: %w", err)
		}

		if conversionBuyout.Valid {
			data.ConversionRate = conversionBuyout.Float64
		}
		if wbClubPercent.Valid {
			data.WBClubPercent = wbClubPercent.Float64
		}

		metrics.Products[data.NmID] = &data
	}

	return metrics, rows.Err()
}

// GetFunnelHistory retrieves historical funnel metrics from snapshot.
// Returns daily metrics for the specified date range.
func (s *snapshotSalesService) GetFunnelHistory(ctx context.Context, req FunnelHistoryRequest) (*FunnelHistory, error) {
	// Validation
	if len(req.NmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(req.NmIDs) > 100 {
		return nil, fmt.Errorf("nmIDs cannot exceed 100 items")
	}

	// Build query
	placeholders := make([]string, len(req.NmIDs))
	args := make([]interface{}, len(req.NmIDs)+2)
	for i, id := range req.NmIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(req.NmIDs)] = req.DateFrom.Format("2006-01-02")
	args[len(req.NmIDs)+1] = req.DateTo.Format("2006-01-02")

	query := fmt.Sprintf(`
		SELECT
			nm_id,
			metric_date,
			SUM(open_count) as open_count,
			SUM(cart_count) as cart_count,
			SUM(order_count) as order_count,
			SUM(buyout_count) as buyout_count,
			SUM(cancel_count) as cancel_count
		FROM funnel_metrics_daily
		WHERE nm_id IN (%s) AND metric_date >= ? AND metric_date <= ?
		GROUP BY nm_id, metric_date
		ORDER BY metric_date, nm_id
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query funnel history: %w", err)
	}
	defer rows.Close()

	history := &FunnelHistory{
		DailyMetrics: make(map[time.Time]map[int]*ProductFunnelData),
		StartDate:    req.DateFrom,
		EndDate:      req.DateTo,
	}

	for rows.Next() {
		var nmID int
		var dateStr string
		var data ProductFunnelData

		if err := rows.Scan(
			&nmID,
			&dateStr,
			&data.OpenCount,
			&data.CartCount,
			&data.OrderCount,
			&data.BuyoutCount,
			&data.CancelCount,
		); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}

		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		data.NmID = nmID

		if history.DailyMetrics[date] == nil {
			history.DailyMetrics[date] = make(map[int]*ProductFunnelData)
		}
		history.DailyMetrics[date][nmID] = &data
	}

	return history, rows.Err()
}

// GetSalesReport returns sales report from snapshot.
func (s *snapshotSalesService) GetSalesReport(ctx context.Context, req SalesReportRequest) (*SalesReport, error) {
	// TODO: Implement with sales table
	return &SalesReport{}, nil
}

// GetSearchPositions returns search positions from snapshot.
// Note: search positions data may not be available in all snapshots.
func (s *snapshotSalesService) GetSearchPositions(ctx context.Context, nmIDs []int, period int) (string, error) {
	// Return mock data for search positions (not stored in current snapshot)
	return `{"data": {"products": []}, "mock": true, "reason": "search positions not in snapshot"}`, nil
}

// GetTopSearchQueries returns top search queries from snapshot.
func (s *snapshotSalesService) GetTopSearchQueries(ctx context.Context, nmID int, period int) (string, error) {
	// Return mock data (not stored in current snapshot)
	return `{"data": {"nmId": ` + fmt.Sprint(nmID) + `, "queries": []}, "mock": true}`, nil
}
