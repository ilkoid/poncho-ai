// Package wb provides service layer implementations for Wildberries API.
//
// This file contains the AttributionService implementation with business logic
// for analyzing organic vs advertising sales attribution.
package wb

import (
	"context"
	"fmt"
	"time"
)

// Ensure attributionService implements AttributionService.
var _ AttributionService = (*attributionService)(nil)

// attributionService implements AttributionService using Sales and Advertising services.
type attributionService struct {
	sales       *salesService
	advertising *advertisingService
}

// GetAttributionSummary analyzes attribution of orders to organic vs advertising sources.
//
// This is an aggregator that combines data from:
//   - Sales funnel metrics (total orders/views)
//   - Campaign statistics (ad orders/views/spend)
//
// The result provides a breakdown of organic vs advertising attribution per product
// and per campaign.
func (s *attributionService) GetAttributionSummary(ctx context.Context, req AttributionRequest) (*AttributionSummary, error) {
	// Validation
	if len(req.NmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(req.AdvertIDs) == 0 {
		return nil, fmt.Errorf("advertIDs cannot be empty")
	}
	if req.Period < 1 || req.Period > 90 {
		return nil, fmt.Errorf("period must be between 1 and 90")
	}

	// Mock mode
	if s.sales.client.IsDemoKey() {
		return s.getMockAttributionSummary(req)
	}

	// Get funnel metrics
	funnelMetrics, err := s.sales.GetFunnelMetrics(ctx, FunnelRequest{
		NmIDs:  req.NmIDs,
		Period: req.Period,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get funnel metrics: %w", err)
	}

	// Get campaign fullstats
	now := time.Now()
	begin := now.AddDate(0, 0, -req.Period)

	campaignStats, err := s.advertising.GetCampaignFullstats(ctx, CampaignFullstatsRequest{
		IDs:       req.AdvertIDs,
		BeginDate: begin.Format("2006-01-02"),
		EndDate:   now.Format("2006-01-02"),
	})
	if err != nil {
		// Continue without campaign data if error
		campaignStats = nil
	}

	// Aggregate data
	summary := &AttributionSummary{
		PeriodStart: begin,
		PeriodEnd:   now,
		ByProduct:   make(map[int]*ProductAttribution),
		ByCampaign:  make(map[int]*CampaignAttribution),
	}

	// Calculate totals from campaigns
	var totalAdOrders int
	var totalAdViews int
	var totalAdSpent float64

	if campaignStats != nil {
		for _, cs := range campaignStats {
			totalAdOrders += cs.Orders
			totalAdViews += cs.Views
			totalAdSpent += cs.Sum

			summary.ByCampaign[cs.AdvertID] = &CampaignAttribution{
				AdvertID: cs.AdvertID,
				Orders:   cs.Orders,
				Spent:    cs.Sum,
			}
		}
	}

	// Calculate per-product attribution
	for nmID, metrics := range funnelMetrics.Products {
		totalOrders := int(metrics.OrderCount)
		totalViews := int(metrics.OpenCount)

		organicOrders := totalOrders - totalAdOrders
		if organicOrders < 0 {
			organicOrders = 0
		}

		organicViews := totalViews - totalAdViews
		if organicViews < 0 {
			organicViews = 0
		}

		summary.ByProduct[nmID] = &ProductAttribution{
			NmID:          nmID,
			TotalOrders:   totalOrders,
			OrganicOrders: organicOrders,
			AdOrders:      totalAdOrders,
			TotalViews:    totalViews,
			OrganicViews:  organicViews,
			AdViews:       totalAdViews,
		}

		summary.TotalOrders += totalOrders
		summary.OrganicOrders += organicOrders
		summary.AdOrders += totalAdOrders
		summary.TotalViews += totalViews
		summary.OrganicViews += organicViews
		summary.AdViews += totalAdViews
	}

	summary.AdSpent = totalAdSpent

	return summary, nil
}

// getMockAttributionSummary returns mock attribution data for demo mode.
func (s *attributionService) getMockAttributionSummary(req AttributionRequest) (*AttributionSummary, error) {
	now := time.Now()
	begin := now.AddDate(0, 0, -req.Period)

	summary := &AttributionSummary{
		PeriodStart:   begin,
		PeriodEnd:     now,
		TotalOrders:   150,
		OrganicOrders: 100,
		AdOrders:      50,
		TotalViews:    5000,
		OrganicViews:  3500,
		AdViews:       1500,
		AdSpent:       2500.00,
		ByProduct:     make(map[int]*ProductAttribution),
		ByCampaign:    make(map[int]*CampaignAttribution),
	}

	// Mock per-product attribution
	for _, nmID := range req.NmIDs {
		totalOrders := 50 + nmID%20
		adOrders := 15 + nmID%10
		totalViews := 1500 + nmID%500
		adViews := 400 + nmID%100

		summary.ByProduct[nmID] = &ProductAttribution{
			NmID:          nmID,
			TotalOrders:   totalOrders,
			OrganicOrders: totalOrders - adOrders,
			AdOrders:      adOrders,
			TotalViews:    totalViews,
			OrganicViews:  totalViews - adViews,
			AdViews:       adViews,
		}
	}

	// Mock per-campaign attribution
	for _, advertID := range req.AdvertIDs {
		orders := 10 + advertID%5
		spent := float64(500 + advertID*10)

		summary.ByCampaign[advertID] = &CampaignAttribution{
			AdvertID: advertID,
			Orders:   orders,
			Spent:    spent,
		}
	}

	return summary, nil
}
