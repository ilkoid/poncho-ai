// Package wb provides service layer implementations for Wildberries API.
//
// This file contains the AdvertisingService implementation with business logic
// for advertising campaign management and statistics.
package wb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Ensure advertisingService implements AdvertisingService.
var _ AdvertisingService = (*advertisingService)(nil)

// advertisingService implements AdvertisingService using the WB Client.
type advertisingService struct {
	client *Client
}

// GetCampaigns retrieves all advertising campaigns.
//
// Uses Promotion API: GET /adv/v0/promotion/adverts
func (s *advertisingService) GetCampaigns(ctx context.Context) ([]PromotionAdvertGroup, error) {
	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCampaigns()
	}

	var response []PromotionAdvertGroup
	err := s.client.Get(ctx, "get_wb_campaigns",
		"https://advert-api.wildberries.ru", 60, 1,
		"/adv/v0/promotion/adverts", nil, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get campaigns: %w", err)
	}

	return response, nil
}

// getMockCampaigns returns mock campaigns for demo mode.
func (s *advertisingService) getMockCampaigns() ([]PromotionAdvertGroup, error) {
	return []PromotionAdvertGroup{
		{
			Type:   CampaignTypeSearch, // 8
			Status: CampaignStatusActive, // 9
			Count:  2,
			AdvertList: []PromotionAdvert{
				{AdvertID: 1001, ChangeTime: time.Now().Format("2006-01-02T15:04:05Z")},
				{AdvertID: 1002, ChangeTime: time.Now().Format("2006-01-02T15:04:05Z")},
			},
		},
		{
			Type:   CampaignTypeAuto, // 9
			Status: CampaignStatusActive, // 9
			Count:  1,
			AdvertList: []PromotionAdvert{
				{AdvertID: 1003, ChangeTime: time.Now().Format("2006-01-02T15:04:05Z")},
			},
		},
	}, nil
}

// GetCampaignStats retrieves detailed stats for campaigns.
// Batches requests for efficiency (max 50 IDs per request).
//
// Uses Promotion API: POST /adv/v2/fullstats
func (s *advertisingService) GetCampaignStats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]CampaignDailyStats, error) {
	// Validation
	if len(advertIDs) == 0 {
		return nil, fmt.Errorf("advertIDs cannot be empty")
	}
	if len(advertIDs) > 50 {
		return nil, fmt.Errorf("maximum 50 campaign IDs allowed")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCampaignStats(advertIDs, beginDate, endDate)
	}

	// Parse dates and generate date list
	begin, err := time.Parse("2006-01-02", beginDate)
	if err != nil {
		return nil, fmt.Errorf("invalid beginDate format: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid endDate format: %w", err)
	}

	var dates []string
	for d := begin; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}

	// Build request
	reqBody := map[string]interface{}{
		"adverIds": advertIDs,
		"dates":    dates,
	}

	var response []CampaignDailyStats
	err = s.client.Post(ctx, "get_wb_campaign_stats2",
		"https://advert-api.wildberries.ru", 60, 1,
		"/adv/v2/fullstats", reqBody, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get campaign stats: %w", err)
	}

	return response, nil
}

// getMockCampaignStats returns mock campaign stats for demo mode.
func (s *advertisingService) getMockCampaignStats(advertIDs []int, beginDate, endDate string) ([]CampaignDailyStats, error) {
	stats := make([]CampaignDailyStats, 0, len(advertIDs))

	for _, id := range advertIDs {
		views := 5000 + id*100
		clicks := views / 20

		stats = append(stats, CampaignDailyStats{
			AdvertID: id,
			Views:    views,
			Clicks:   clicks,
			CTR:      float64(clicks) / float64(views) * 100,
			CPC:      5,
			Sum:      float64(clicks) * 5.0,
			Orders:   clicks / 10,
		})
	}

	return stats, nil
}

// GetCampaignProducts retrieves products associated with campaigns.
//
// Extracts product IDs from campaign fullstats data.
func (s *advertisingService) GetCampaignProducts(ctx context.Context, advertIDs []int) ([]CampaignProduct, error) {
	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCampaignProducts(advertIDs)
	}

	// Get fullstats to extract products
	now := time.Now()
	begin := now.AddDate(0, 0, -7)

	fullstats, err := s.GetCampaignFullstats(ctx, CampaignFullstatsRequest{
		IDs:       advertIDs,
		BeginDate: begin.Format("2006-01-02"),
		EndDate:   now.Format("2006-01-02"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get fullstats: %w", err)
	}

	// Extract unique products
	productMap := make(map[int]*CampaignProduct)
	for _, fs := range fullstats {
		for _, day := range fs.Days {
			for _, app := range day.Apps {
				for _, nm := range app.Nms {
					if _, exists := productMap[nm.NmID]; !exists {
						productMap[nm.NmID] = &CampaignProduct{
							NmID:   nm.NmID,
							Name:   nm.Name,
							Orders: 0,
							Views:  0,
						}
					}
					productMap[nm.NmID].Orders += nm.Orders
					productMap[nm.NmID].Views += nm.Views
				}
			}
		}
	}

	products := make([]CampaignProduct, 0, len(productMap))
	for _, p := range productMap {
		products = append(products, *p)
	}

	return products, nil
}

// getMockCampaignProducts returns mock campaign products for demo mode.
func (s *advertisingService) getMockCampaignProducts(advertIDs []int) ([]CampaignProduct, error) {
	return []CampaignProduct{
		{NmID: 123456, Name: "Mock Product 1", Orders: 25, Views: 1200},
		{NmID: 234567, Name: "Mock Product 2", Orders: 15, Views: 800},
		{NmID: 345678, Name: "Mock Product 3", Orders: 10, Views: 500},
	}, nil
}

// GetCampaignFullstats retrieves detailed campaign statistics with daily/apps/products breakdown.
//
// Uses Promotion API v3: GET /adv/v3/fullstats
// Maximum 50 campaign IDs per request, period up to 31 days.
func (s *advertisingService) GetCampaignFullstats(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	// Validation
	if len(req.IDs) == 0 {
		return nil, fmt.Errorf("campaign IDs cannot be empty")
	}
	if len(req.IDs) > 50 {
		return nil, fmt.Errorf("maximum 50 campaign IDs allowed")
	}

	// Validate date format
	_, err := time.Parse("2006-01-02", req.BeginDate)
	if err != nil {
		return nil, fmt.Errorf("invalid beginDate format: use YYYY-MM-DD")
	}
	_, err = time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return nil, fmt.Errorf("invalid endDate format: use YYYY-MM-DD")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCampaignFullstats(req)
	}

	// Build URL parameters
	idStrs := make([]string, len(req.IDs))
	for i, id := range req.IDs {
		idStrs[i] = strconv.Itoa(id)
	}

	path := fmt.Sprintf("/adv/v3/fullstats?ids=%s&beginDate=%s&endDate=%s",
		strings.Join(idStrs, ","), req.BeginDate, req.EndDate)

	var response []CampaignFullstatsResponse
	err = s.client.Get(ctx, "get_wb_campaign_fullstats2",
		"https://advert-api.wildberries.ru", 20, 1,
		path, nil, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get campaign fullstats: %w", err)
	}

	return response, nil
}

// getMockCampaignFullstats returns mock fullstats for demo mode.
func (s *advertisingService) getMockCampaignFullstats(req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error) {
	results := make([]CampaignFullstatsResponse, 0, len(req.IDs))

	for _, id := range req.IDs {
		views := 1000 + id*10
		clicks := views / 20

		results = append(results, CampaignFullstatsResponse{
			AdvertID: id,
			Views:    views,
			Clicks:   clicks,
			CTR:      5.0 + float64(id%10)/10,
			CPC:      4.5 + float64(id%5),
			CR:       2.0 + float64(id%3),
			Orders:   clicks / 10,
			Atbs:     clicks / 50,
			Canceled: clicks / 100,
			Shks:     clicks / 15,
			Sum:      float64(clicks) * 5.0,
			SumPrice: float64(clicks) * 500.0,
			Days: []CampaignFullstatsDay{
				{
					Date:   req.BeginDate,
					Views:  views / 2,
					Clicks: clicks / 2,
					Orders: clicks / 20,
					Apps: []CampaignFullstatsApp{
						{
							AppType: 1, // Site
							Views:    views / 3,
							Clicks:   clicks / 3,
							Orders:   clicks / 30,
							Nms: []CampaignFullstatsNm{
								{NmID: 123456, Name: "Mock Product", Views: views / 6, Clicks: clicks / 6, Orders: clicks / 60},
							},
						},
					},
				},
			},
		})
	}

	return results, nil
}

// Note: CampaignDailyStats, CampaignProduct, and PromotionAdvertGroup types
// are defined in promotion_types.go
