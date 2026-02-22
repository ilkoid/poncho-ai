// Package wb provides snapshot advertising service for E2E testing.
package wb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Ensure snapshotAdvertisingService implements AdvertisingService.
var _ AdvertisingService = (*snapshotAdvertisingService)(nil)

// snapshotAdvertisingService implements AdvertisingService backed by SQLite.
type snapshotAdvertisingService struct {
	db *sql.DB
}

// GetCampaigns retrieves all advertising campaigns from snapshot.
func (s *snapshotAdvertisingService) GetCampaigns(ctx context.Context) ([]PromotionAdvertGroup, error) {
	// Query all campaigns from snapshot
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT advert_id, campaign_type
		FROM campaigns
		ORDER BY campaign_type, advert_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query campaigns: %w", err)
	}
	defer rows.Close()

	// Group campaigns by type (simulating PromotionAdvertGroup structure)
	typeGroups := make(map[int][]PromotionAdvert)

	for rows.Next() {
		var advertID, campaignType int

		if err := rows.Scan(&advertID, &campaignType); err != nil {
			return nil, fmt.Errorf("scan campaign: %w", err)
		}

		advert := PromotionAdvert{
			AdvertID: advertID,
		}

		typeGroups[campaignType] = append(typeGroups[campaignType], advert)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Convert to PromotionAdvertGroup slice
	var groups []PromotionAdvertGroup
	for campaignType, adverts := range typeGroups {
		groups = append(groups, PromotionAdvertGroup{
			Type:       campaignType,
			AdvertList: adverts,
		})
	}

	return groups, nil
}

// GetCampaignStats retrieves detailed stats for campaigns from snapshot.
func (s *snapshotAdvertisingService) GetCampaignStats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]CampaignDailyStats, error) {
	if len(advertIDs) == 0 {
		return nil, nil
	}

	// Build query with advertIDs
	placeholders := make([]string, len(advertIDs))
	args := make([]interface{}, len(advertIDs)+2)
	for i, id := range advertIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(advertIDs)] = beginDate
	args[len(advertIDs)+1] = endDate

	query := fmt.Sprintf(`
		SELECT advert_id, stats_date, views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price
		FROM campaign_stats_daily
		WHERE advert_id IN (%s) AND stats_date >= ? AND stats_date <= ?
		ORDER BY advert_id, stats_date
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query campaign stats: %w", err)
	}
	defer rows.Close()

	var stats []CampaignDailyStats

	for rows.Next() {
		var ds CampaignDailyStats
		var ctr, cpc, cr, sum, sumPrice sql.NullFloat64
		var views, clicks, orders, shks, atbs, canceled sql.NullInt64

		if err := rows.Scan(
			&ds.AdvertID, &ds.StatsDate,
			&views, &clicks, &ctr, &cpc, &cr, &orders, &shks, &atbs, &canceled,
			&sum, &sumPrice,
		); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}

		if views.Valid {
			ds.Views = int(views.Int64)
		}
		if clicks.Valid {
			ds.Clicks = int(clicks.Int64)
		}
		if ctr.Valid {
			ds.CTR = ctr.Float64
		}
		if cpc.Valid {
			ds.CPC = cpc.Float64
		}
		if cr.Valid {
			ds.CR = cr.Float64
		}
		if orders.Valid {
			ds.Orders = int(orders.Int64)
		}
		if shks.Valid {
			ds.Shks = int(shks.Int64)
		}
		if atbs.Valid {
			ds.Atbs = int(atbs.Int64)
		}
		if canceled.Valid {
			ds.Canceled = int(canceled.Int64)
		}
		if sum.Valid {
			ds.Sum = sum.Float64
		}
		if sumPrice.Valid {
			ds.SumPrice = sumPrice.Float64
		}

		stats = append(stats, ds)
	}

	return stats, rows.Err()
}

// GetCampaignProducts retrieves products associated with campaigns.
// Note: campaign_products table may not be available in all snapshots.
func (s *snapshotAdvertisingService) GetCampaignProducts(ctx context.Context, advertIDs []int) ([]CampaignProduct, error) {
	// Check if campaign_products table exists
	var tableName string
	err := s.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='campaign_products'",
	).Scan(&tableName)
	if err == sql.ErrNoRows {
		// Table doesn't exist, return empty
		return nil, nil
	}

	if len(advertIDs) == 0 {
		return nil, nil
	}

	// Build query
	placeholders := make([]string, len(advertIDs))
	args := make([]interface{}, len(advertIDs))
	for i, id := range advertIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT advert_id, nm_id
		FROM campaign_products
		WHERE advert_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query campaign products: %w", err)
	}
	defer rows.Close()

	var products []CampaignProduct
	for rows.Next() {
		var p CampaignProduct
		if err := rows.Scan(&p.AdvertID, &p.NmID); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, p)
	}

	return products, rows.Err()
}

// GetCampaignFullstats retrieves full stats for campaigns from snapshot.
func (s *snapshotAdvertisingService) GetCampaignFullstats(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	// Get daily stats first
	dailyStats, err := s.GetCampaignStats(ctx, req.IDs, req.BeginDate, req.EndDate)
	if err != nil {
		return nil, err
	}

	// Aggregate by advert_id
	fullstatsMap := make(map[int]*CampaignFullstatsResponse)
	for _, ds := range dailyStats {
		fs, exists := fullstatsMap[ds.AdvertID]
		if !exists {
			fs = &CampaignFullstatsResponse{
				AdvertID: ds.AdvertID,
				Days:     []CampaignFullstatsDay{},
			}
			fullstatsMap[ds.AdvertID] = fs
		}
		fs.Views += ds.Views
		fs.Clicks += ds.Clicks
		fs.Orders += ds.Orders
		fs.Shks += ds.Shks
		fs.Atbs += ds.Atbs
		fs.Canceled += ds.Canceled
		fs.Sum += ds.Sum
		fs.SumPrice += ds.SumPrice

		// Add day entry
		fs.Days = append(fs.Days, CampaignFullstatsDay{
			Date:     ds.StatsDate,
			Views:    ds.Views,
			Clicks:   ds.Clicks,
			Orders:   ds.Orders,
			Shks:     ds.Shks,
			Atbs:     ds.Atbs,
			Canceled: ds.Canceled,
			Sum:      ds.Sum,
			SumPrice: ds.SumPrice,
		})
	}

	// Convert to slice
	var result []CampaignFullstatsResponse
	for _, fs := range fullstatsMap {
		result = append(result, *fs)
	}

	return result, nil
}
