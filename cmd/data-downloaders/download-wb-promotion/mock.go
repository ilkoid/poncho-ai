// Package main provides mock client for testing.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockPromotionClient implements PromotionClient for testing.
type MockPromotionClient struct {
	mu sync.RWMutex

	campaigns []wb.PromotionAdvertGroup
	stats     map[int][]wb.CampaignFullstatsResponse
	details   map[int]wb.AdvertDetail

	failCount    int
	currentFails int
}

// NewMockPromotionClient creates a new mock client.
func NewMockPromotionClient() *MockPromotionClient {
	return &MockPromotionClient{
		stats:   make(map[int][]wb.CampaignFullstatsResponse),
		details: make(map[int]wb.AdvertDetail),
	}
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockPromotionClient) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.currentFails = 0
}

func (m *MockPromotionClient) maybeFail() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentFails < m.failCount {
		m.currentFails++
		return fmt.Errorf("mock failure")
	}
	return nil
}

// AddCampaign adds a mock campaign.
func (m *MockPromotionClient) AddCampaign(group wb.PromotionAdvertGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.campaigns = append(m.campaigns, group)
}

// AddFullstats adds mock fullstats for a campaign.
func (m *MockPromotionClient) AddFullstats(advertID int, responses []wb.CampaignFullstatsResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats[advertID] = responses
}

// GetPromotionCount returns mock campaigns.
func (m *MockPromotionClient) GetPromotionCount(ctx context.Context) (*wb.PromotionCountResponse, error) {
	if err := m.maybeFail(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, g := range m.campaigns {
		total += len(g.AdvertList)
	}

	return &wb.PromotionCountResponse{
		Adverts: m.campaigns,
		All:     total,
	}, nil
}

// GetCampaignFullstats returns mock fullstats with full 4-level hierarchy.
func (m *MockPromotionClient) GetCampaignFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string, rateLimit, burst int) ([]wb.CampaignFullstatsResponse, error) {
	if err := m.maybeFail(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []wb.CampaignFullstatsResponse
	for _, id := range advertIDs {
		if responses, ok := m.stats[id]; ok {
			results = append(results, responses...)
		}
	}

	return results, nil
}

// GetAdvertDetails returns mock campaign details.
func (m *MockPromotionClient) GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error) {
	if err := m.maybeFail(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []wb.AdvertDetail
	for _, id := range ids {
		if detail, ok := m.details[id]; ok {
			results = append(results, detail)
		}
	}

	return results, nil
}

// PopulateMockData fills mock client with realistic test data.
// Generates full 4-level hierarchy: Campaign → Day → App → Nm.
func PopulateMockData(m *MockPromotionClient, campaignCount, days int) {
	now := time.Now()

	for i := 0; i < campaignCount; i++ {
		advertID := 1000000 + i
		campaignType := 9 // Auto
		if i%3 == 0 {
			campaignType = 8 // Search
		} else if i%3 == 1 {
			campaignType = 50 // Catalog
		}

		status := 9 // Active
		if i%5 == 0 {
			status = 11 // Paused
		} else if i%5 == 1 {
			status = 7 // Finished
		}

		group := wb.PromotionAdvertGroup{
			Type:   campaignType,
			Status: status,
			Count:  1,
			AdvertList: []wb.PromotionAdvert{
				{
					AdvertID:   advertID,
					ChangeTime: now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
				},
			},
		}
		m.AddCampaign(group)

		// Generate campaign details
		paymentTypes := []string{"cpm", "cpc"}
		bidTypes := []string{"manual", "unified"}
		m.mu.Lock()
		m.details[advertID] = wb.AdvertDetail{
			ID:      advertID,
			BidType: bidTypes[i%2],
			Status:  status,
			Settings: wb.AdvertSettings{
				Name:        fmt.Sprintf("Test Campaign %d", advertID),
				PaymentType: paymentTypes[i%2],
				Placements: wb.AdvertPlacements{
					Search:          i%2 == 0,
					Recommendations: i%3 == 0,
				},
			},
			Timestamps: wb.AdvertTimestamps{
				Created: now.Add(-time.Duration(30+i) * 24 * time.Hour).Format(time.RFC3339),
				Updated: now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
				Started: now.Add(-time.Duration(29+i) * 24 * time.Hour).Format(time.RFC3339),
			},
		}
		m.mu.Unlock()

		// Generate daily stats with full hierarchy
		var daysList []wb.CampaignFullstatsDay
		totalViews, totalClicks, totalOrders, totalShks := 0, 0, 0, 0
		var totalSum, totalSumPrice float64

		for d := 0; d < days; d++ {
			date := now.AddDate(0, 0, -d).Format(time.RFC3339)
			views := 1000 + (i*100) - (d*10)
			if views < 100 {
				views = 100
			}
			clicks := views / 20
			orders := clicks / 10
			shks := orders - orders/5
			atbs := orders / 10
			canceled := orders / 20
			sum := float64(clicks) * 5.0
			sumPrice := float64(orders) * 1500.0

			totalViews += views
			totalClicks += clicks
			totalOrders += orders
			totalShks += shks
			totalSum += sum
			totalSumPrice += sumPrice

			// Platform breakdown: site=1, android=32, ios=64
			apps := []wb.CampaignFullstatsApp{
				{AppType: 1, Views: views * 60 / 100, Clicks: clicks * 60 / 100, Orders: orders * 60 / 100, Shks: shks * 60 / 100, Sum: sum * 0.6, SumPrice: sumPrice * 0.6},
				{AppType: 32, Views: views * 25 / 100, Clicks: clicks * 25 / 100, Orders: orders * 25 / 100, Shks: shks * 25 / 100, Sum: sum * 0.25, SumPrice: sumPrice * 0.25},
				{AppType: 64, Views: views * 15 / 100, Clicks: clicks * 15 / 100, Orders: orders * 15 / 100, Shks: shks * 15 / 100, Sum: sum * 0.15, SumPrice: sumPrice * 0.15},
			}

			// Product breakdown per platform (2 products per app)
			for ai := range apps {
				nmBase := 2000000 + i*10
				apps[ai].Nms = []wb.CampaignFullstatsNm{
					{NmID: nmBase, Name: fmt.Sprintf("Product %d-A", i), Views: apps[ai].Views * 70 / 100, Clicks: apps[ai].Clicks * 70 / 100, Orders: apps[ai].Orders * 70 / 100, Shks: apps[ai].Shks * 70 / 100, Sum: apps[ai].Sum * 0.7, SumPrice: apps[ai].SumPrice * 0.7},
					{NmID: nmBase + 1, Name: fmt.Sprintf("Product %d-B", i), Views: apps[ai].Views * 30 / 100, Clicks: apps[ai].Clicks * 30 / 100, Orders: apps[ai].Orders * 30 / 100, Shks: apps[ai].Shks * 30 / 100, Sum: apps[ai].Sum * 0.3, SumPrice: apps[ai].SumPrice * 0.3},
				}
			}

			daysList = append(daysList, wb.CampaignFullstatsDay{
				Date:     date,
				Views:    views,
				Clicks:   clicks,
				CTR:      float64(clicks) / float64(views) * 100,
				CPC:      5.0 + float64(i%5),
				CR:       float64(orders) / float64(clicks) * 100,
				Orders:   orders,
				Shks:     shks,
				Atbs:     atbs,
				Canceled: canceled,
				Sum:      sum,
				SumPrice: sumPrice,
				Apps:     apps,
			})
		}

		m.AddFullstats(advertID, []wb.CampaignFullstatsResponse{
			{
				AdvertID: advertID,
				Views:    totalViews,
				Clicks:   totalClicks,
				CTR:      float64(totalClicks) / float64(totalViews) * 100,
				CPC:      5.0 + float64(i%5),
				CR:       float64(totalOrders) / float64(totalClicks) * 100,
				Orders:   totalOrders,
				Shks:     totalShks,
				Sum:      totalSum,
				SumPrice: totalSumPrice,
				Days:     daysList,
			},
		})
	}
}
