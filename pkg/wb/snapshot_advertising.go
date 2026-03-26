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
// Reconstructs 4-level hierarchy (Campaign → Day → App → Nm) from nm-level table.
// Falls back to day-level reconstruction if campaign_stats_nm table doesn't exist.
func (s *snapshotAdvertisingService) GetCampaignFullstats(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	// Check if nm-level table exists (full hierarchy available)
	hasNmTable := tableExists(s.db, "campaign_stats_nm")

	if hasNmTable {
		return s.getFullstatsFromNmTable(ctx, req)
	}

	// Fallback: partial hierarchy from daily stats (legacy snapshots)
	return s.getFullstatsFromDailyTable(ctx, req)
}

// getFullstatsFromNmTable reconstructs full 4-level hierarchy from campaign_stats_nm.
// This is the single source of truth for fullstats data.
func (s *snapshotAdvertisingService) getFullstatsFromNmTable(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	if len(req.IDs) == 0 {
		return nil, nil
	}

	// Query nm-level stats
	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs)+2)
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(req.IDs)] = req.BeginDate
	args[len(req.IDs)+1] = req.EndDate

	query := fmt.Sprintf(`
		SELECT advert_id, stats_date, app_type, nm_id, nm_name,
			views, clicks, ctr, cpc, cr, orders, shks, atbs, canceled, sum, sum_price
		FROM campaign_stats_nm
		WHERE advert_id IN (%s) AND stats_date >= ? AND stats_date <= ?
		ORDER BY advert_id, stats_date, app_type, nm_id
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query nm stats: %w", err)
	}
	defer rows.Close()

	// Build hierarchy: Campaign → Day → App → Nm
	type dayAppKey struct {
		date    string
		appType int
	}
	type dayKey struct {
		advertID  int
		date      string
	}

	campaigns := make(map[int]*CampaignFullstatsResponse)
	days := make(map[dayKey]*CampaignFullstatsDay)
	apps := make(map[dayKey]map[int]*CampaignFullstatsApp) // dayKey → appType → App

	for rows.Next() {
		var advertID, appType, nmID int
		var statsDate, nmName string
		var views, clicks, orders, shks, atbs, canceled int
		var ctr, cpc, cr, sum, sumPrice float64

		if err := rows.Scan(
			&advertID, &statsDate, &appType, &nmID, &nmName,
			&views, &clicks, &ctr, &cpc, &cr, &orders, &shks, &atbs, &canceled,
			&sum, &sumPrice,
		); err != nil {
			return nil, fmt.Errorf("scan nm stats: %w", err)
		}

		// Ensure campaign exists
		fs, exists := campaigns[advertID]
		if !exists {
			fs = &CampaignFullstatsResponse{AdvertID: advertID}
			campaigns[advertID] = fs
		}

		// Ensure day exists
		dk := dayKey{advertID: advertID, date: statsDate}
		day, exists := days[dk]
		if !exists {
			day = &CampaignFullstatsDay{Date: statsDate}
			days[dk] = day
			apps[dk] = make(map[int]*CampaignFullstatsApp)
			fs.Days = append(fs.Days, *day)
		}

		// Ensure app exists
		appMap := apps[dk]
		app, exists := appMap[appType]
		if !exists {
			app = &CampaignFullstatsApp{AppType: appType}
			appMap[appType] = app
			day.Apps = append(day.Apps, *app)
		}

		// Add nm to app
		app.Nms = append(app.Nms, CampaignFullstatsNm{
			NmID: nmID, Name: nmName,
			Views: views, Clicks: clicks, CTR: ctr, CPC: cpc, CR: cr,
			Orders: orders, Shks: shks, Atbs: atbs, Canceled: canceled,
			Sum: sum, SumPrice: sumPrice,
		})

		// Aggregate up
		app.Views += views
		app.Clicks += clicks
		app.Orders += orders
		app.Shks += shks
		app.Atbs += atbs
		app.Canceled += canceled
		app.Sum += sum
		app.SumPrice += sumPrice

		day.Views += views
		day.Clicks += clicks
		day.Orders += orders
		day.Shks += shks
		day.Atbs += atbs
		day.Canceled += canceled
		day.Sum += sum
		day.SumPrice += sumPrice

		fs.Views += views
		fs.Clicks += clicks
		fs.Orders += orders
		fs.Shks += shks
		fs.Atbs += atbs
		fs.Canceled += canceled
		fs.Sum += sum
		fs.SumPrice += sumPrice
	}

	// Load booster stats if table exists
	if tableExists(s.db, "campaign_booster_stats") {
		if err := s.loadBoosterStats(ctx, req, campaigns); err != nil {
			return nil, err
		}
	}

	// Convert to slice
	result := make([]CampaignFullstatsResponse, 0, len(campaigns))
	for _, fs := range campaigns {
		result = append(result, *fs)
	}

	return result, rows.Err()
}

// loadBoosterStats enriches campaign fullstats with booster data.
func (s *snapshotAdvertisingService) loadBoosterStats(ctx context.Context, req CampaignFullstatsRequest, campaigns map[int]*CampaignFullstatsResponse) error {
	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs)+2)
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(req.IDs)] = req.BeginDate
	args[len(req.IDs)+1] = req.EndDate

	query := fmt.Sprintf(`
		SELECT advert_id, stats_date, nm_id, avg_position
		FROM campaign_booster_stats
		WHERE advert_id IN (%s) AND stats_date >= ? AND stats_date <= ?
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query booster stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var advertID, nmID int
		var statsDate string
		var avgPosition float64

		if err := rows.Scan(&advertID, &statsDate, &nmID, &avgPosition); err != nil {
			return fmt.Errorf("scan booster stats: %w", err)
		}

		if fs, ok := campaigns[advertID]; ok {
			fs.BoosterStats = append(fs.BoosterStats, CampaignFullstatsBooster{
				Date:        statsDate,
				Nm:          nmID,
				AvgPosition: avgPosition,
			})
		}
	}

	return rows.Err()
}

// getFullstatsFromDailyTable reconstructs partial hierarchy from campaign_stats_daily.
// Used as fallback for legacy snapshots without campaign_stats_nm table.
func (s *snapshotAdvertisingService) getFullstatsFromDailyTable(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	dailyStats, err := s.GetCampaignStats(ctx, req.IDs, req.BeginDate, req.EndDate)
	if err != nil {
		return nil, err
	}

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

	var result []CampaignFullstatsResponse
	for _, fs := range fullstatsMap {
		result = append(result, *fs)
	}

	return result, nil
}

// tableExists checks if a table exists in the database.
func tableExists(db *sql.DB, name string) bool {
	var tableName string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&tableName)
	return err == nil
}
