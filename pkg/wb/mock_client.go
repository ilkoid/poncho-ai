// Package wb provides mock client for testing.
package wb

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockClient implements Client interface for testing.
type MockClient struct {
	mu sync.RWMutex

	// Mock data storage
	mockSales []RealizationReportRow

	// Promotion API mock data
	mockCampaigns []PromotionAdvertGroup
	mockStats     map[int][]CampaignDailyStats // advertID -> stats

	// Fail simulation
	failCount    int
	currentFails int
}

// NewMockClient creates a new mock client for testing.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// Clear removes all mock data (for test isolation).
func (m *MockClient) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockSales = nil
	m.mockCampaigns = nil
	m.mockStats = nil
	m.currentFails = 0
	m.failCount = 0
}

// AddMockSales adds mock sales data to the client.
func (m *MockClient) AddMockSales(rows ...RealizationReportRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockSales = append(m.mockSales, rows...)
}

// SetFailCount sets how many requests should fail before succeeding.
func (m *MockClient) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.currentFails = 0
}

// maybeFail simulates network failures for retry testing.
// Returns true if the request should fail.
func (m *MockClient) maybeFail() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentFails < m.failCount {
		m.currentFails++
		return true
	}
	return false
}

// ReportDetailByPeriodPageWithTime returns mock data for testing.
// Simulates the real API pagination behavior.
// NOTE: For simplicity, returns ALL data on first call (rrdid=0), then nothing.
func (m *MockClient) ReportDetailByPeriodPageWithTime(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	rrdid int,
	limit int,
) (*ReportDetailByPeriodPageResult, error) {
	// Simulate failures if configured
	if m.maybeFail() {
		return nil, fmt.Errorf("simulated network error: i/o timeout")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// If rrdid > 0, we already returned all data (pagination complete)
	if rrdid > 0 {
		return &ReportDetailByPeriodPageResult{
			Rows:    nil,
			HasMore: false,
		}, nil
	}

	// Return all mock data on first call (simple behavior for tests)
	return &ReportDetailByPeriodPageResult{
		Rows:      m.mockSales,
		HasMore:   false,
		LastRrdID: 0,
	}, nil
}

// ReportDetailByPeriodPage returns mock data (date-based).
func (m *MockClient) ReportDetailByPeriodPage(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom int,
	dateTo int,
	rrdid int,
	limit int,
) (*ReportDetailByPeriodPageResult, error) {
	// Convert dates to strings and delegate to time-based version
	return m.ReportDetailByPeriodPageWithTime(ctx, baseURL, rateLimit, burst,
		fmt.Sprintf("%d", dateFrom), fmt.Sprintf("%d", dateTo), rrdid, limit)
}

// ReportDetailByPeriodIteratorWithTime iterates over mock data with retry support.
func (m *MockClient) ReportDetailByPeriodIteratorWithTime(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	callback func([]RealizationReportRow) error,
) (int, error) {
	totalCount := 0
	rrdid := 0
	limit := 100000

	// Retry settings (same as real client)
	const maxRetries = 3
	const baseBackoff = 5 * time.Millisecond // Faster for tests

	for {
		var page *ReportDetailByPeriodPageResult
		var err error

		// Retry loop with backoff
		for attempt := 0; attempt < maxRetries; attempt++ {
			page, err = m.ReportDetailByPeriodPageWithTime(ctx, baseURL, rateLimit, burst, dateFrom, dateTo, rrdid, limit)
			if err == nil {
				break // Success
			}

			// Check if retryable
			isRetryable := isRetryableError(err)
			if !isRetryable || attempt == maxRetries-1 {
				return totalCount, err
			}

			// Wait before retry
			backoff := baseBackoff * time.Duration(1<<attempt)
			time.Sleep(backoff)
		}

		if !page.HasMore {
			// Process last page
			if len(page.Rows) > 0 {
				if err := callback(page.Rows); err != nil {
					return totalCount, err
				}
				totalCount += len(page.Rows)
			}
			break
		}

		if err := callback(page.Rows); err != nil {
			return totalCount, err
		}

		totalCount += len(page.Rows)
		rrdid = page.LastRrdID
	}

	return totalCount, nil
}

// ReportDetailByPeriodIterator iterates over mock data (date-based).
func (m *MockClient) ReportDetailByPeriodIterator(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom int,
	dateTo int,
	callback func([]RealizationReportRow) error,
) (int, error) {
	return m.ReportDetailByPeriodIteratorWithTime(ctx, baseURL, rateLimit, burst,
		fmt.Sprintf("%d", dateFrom), fmt.Sprintf("%d", dateTo), callback)
}

// ============================================================================
// Promotion API Mock Methods
// ============================================================================

// AddMockCampaigns adds mock campaign data for testing.
func (m *MockClient) AddMockCampaigns(groups ...PromotionAdvertGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockCampaigns = append(m.mockCampaigns, groups...)
}

// AddMockCampaignStats adds mock stats for a campaign.
func (m *MockClient) AddMockCampaignStats(advertID int, stats []CampaignDailyStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mockStats == nil {
		m.mockStats = make(map[int][]CampaignDailyStats)
	}
	m.mockStats[advertID] = stats
}

// GetPromotionCount returns mock campaign list (despite the name, it returns full data).
func (m *MockClient) GetPromotionCount(ctx context.Context) (*PromotionCountResponse, error) {
	if m.maybeFail() {
		return nil, fmt.Errorf("simulated network error: i/o timeout")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Calculate total count
	totalCount := 0
	for _, group := range m.mockCampaigns {
		totalCount += len(group.AdvertList)
	}

	return &PromotionCountResponse{
		Adverts: m.mockCampaigns,
		All:     totalCount,
	}, nil
}

// GetCampaignFullstats returns mock stats for campaigns.
func (m *MockClient) GetCampaignFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]CampaignDailyStats, error) {
	if m.maybeFail() {
		return nil, fmt.Errorf("simulated network error: i/o timeout")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []CampaignDailyStats
	for _, id := range advertIDs {
		if stats, ok := m.mockStats[id]; ok {
			// Filter by date range
			for _, s := range stats {
				if s.StatsDate >= beginDate && s.StatsDate <= endDate {
					results = append(results, s)
				}
			}
		}
	}

	return results, nil
}
