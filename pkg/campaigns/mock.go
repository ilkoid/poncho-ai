package campaigns

import (
	"context"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockCampaignsSource returns deterministic fake campaign data.
// Implements CampaignsSource for --mock mode and testing.
type MockCampaignsSource struct {
	mu        sync.RWMutex
	campaigns *wb.PromotionCountResponse
	details   []wb.AdvertDetail
	fullstats []wb.CampaignFullstatsResponse
}

// NewMockCampaignsSource creates a mock source with deterministic fake data.
// Generates `count` campaigns across 3 status groups (active, paused, finished).
func NewMockCampaignsSource(count int) *MockCampaignsSource {
	m := &MockCampaignsSource{}
	m.populate(count)
	return m
}

// GetCampaignList returns mock campaign list grouped by type+status.
func (m *MockCampaignsSource) GetCampaignList(ctx context.Context) (*wb.PromotionCountResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.campaigns, nil
}

// GetAdvertDetails returns mock campaign details for the requested IDs.
func (m *MockCampaignsSource) GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return details matching requested IDs
	result := make([]wb.AdvertDetail, 0, len(ids))
	for _, id := range ids {
		for _, d := range m.details {
			if d.ID == id {
				result = append(result, d)
				break
			}
		}
	}
	return result, nil
}

// GetCampaignFullstats returns mock fullstats for the requested IDs.
func (m *MockCampaignsSource) GetCampaignFullstats(ctx context.Context, ids []int, begin, end string, rl, burst int) ([]wb.CampaignFullstatsResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return fullstats matching requested IDs
	result := make([]wb.CampaignFullstatsResponse, 0, len(ids))
	for _, id := range ids {
		for _, fs := range m.fullstats {
			if fs.AdvertID == id {
				result = append(result, fs)
				break
			}
		}
	}
	return result, nil
}

// populate fills mock with deterministic campaign data.
func (m *MockCampaignsSource) populate(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	statuses := []int{wb.CampaignStatusActive, wb.CampaignStatusPaused, wb.CampaignStatusFinished}
	bidTypes := []string{"manual", "unified"}
	paymentTypes := []string{"cpm", "cpc"}

	// Distribute campaigns across groups
	groupSize := (count + len(statuses) - 1) / len(statuses)
	groups := make([]wb.PromotionAdvertGroup, 0, len(statuses))
	details := make([]wb.AdvertDetail, 0, count)
	fullstats := make([]wb.CampaignFullstatsResponse, 0, count)

	id := 10001
	for gi, status := range statuses {
		n := min(groupSize, count-gi*groupSize)
		if n <= 0 {
			break
		}

		adverts := make([]wb.PromotionAdvert, n)
		for i := 0; i < n; i++ {
			adverts[i] = wb.PromotionAdvert{
				AdvertID:   id,
				ChangeTime: time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			}

			details = append(details, wb.AdvertDetail{
				ID:      id,
				BidType: bidTypes[id%2],
				Status:  status,
				Settings: wb.AdvertSettings{
					Name:        "Mock Campaign " + string(rune('A'+id%26)),
					PaymentType: paymentTypes[id%2],
					Placements: wb.AdvertPlacements{
						Search:          id%2 == 0,
						Recommendations: id%3 == 0,
					},
				},
				Timestamps: wb.AdvertTimestamps{
					Created: time.Now().AddDate(0, 0, -30).Format(time.RFC3339),
					Updated: time.Now().AddDate(0, 0, -1).Format(time.RFC3339),
					Started: time.Now().AddDate(0, 0, -29).Format(time.RFC3339),
				},
			})

			fullstats = append(fullstats, wb.CampaignFullstatsResponse{
				AdvertID: id,
				Views:    1000 + id%500,
				Clicks:   50 + id%25,
				CTR:      5.0 + float64(id%10)/10,
				CPC:      4.5 + float64(id%5)/10,
				CR:       2.0 + float64(id%5)/10,
				Orders:   5 + id%10,
				Shks:     4 + id%8,
				Sum:      250.0 + float64(id%100)*10,
				SumPrice: 5000.0 + float64(id%200)*50,
				Days: []wb.CampaignFullstatsDay{
					{
						Date:     time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
						Views:    500 + id%250,
						Clicks:   25 + id%12,
						CTR:      5.0,
						CPC:      4.5,
						Orders:   3 + id%5,
						Shks:     2 + id%4,
						Sum:      125.0 + float64(id%50)*5,
						SumPrice: 2500.0 + float64(id%100)*25,
						Apps: []wb.CampaignFullstatsApp{
							{AppType: 1, Views: 300 + id%150, Clicks: 15 + id%8, Orders: 2 + id%3, Sum: 75.0 + float64(id%30)*3, SumPrice: 1500.0 + float64(id%60)*15},
							{AppType: 32, Views: 150 + id%75, Clicks: 8 + id%4, Orders: 1 + id%2, Sum: 40.0 + float64(id%20)*2, SumPrice: 800.0 + float64(id%40)*10},
						},
					},
				},
			})

			id++
		}

		groups = append(groups, wb.PromotionAdvertGroup{
			Type:       wb.CampaignTypeAuto,
			Status:     status,
			Count:      n,
			AdvertList: adverts,
		})
	}

	m.campaigns = &wb.PromotionCountResponse{Adverts: groups, All: count}
	m.details = details
	m.fullstats = fullstats
}

// DiscardWriter implements CampaignsWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu             sync.Mutex
	savedCampaigns int
	savedDetails   int
	savedDaily     int
	savedApp       int
	savedNm        int
	savedBooster   int
	populated      bool
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveCampaigns counts groups but never writes to any database.
func (w *DiscardWriter) SaveCampaigns(_ context.Context, groups []wb.PromotionAdvertGroup) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, g := range groups {
		w.savedCampaigns += len(g.AdvertList)
	}
	return nil
}

// SaveCampaignDetails counts details but never writes.
func (w *DiscardWriter) SaveCampaignDetails(_ context.Context, details []wb.AdvertDetail) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedDetails += len(details)
	return nil
}

// SaveFullstats counts rows from all 4 stat tables but never writes.
func (w *DiscardWriter) SaveFullstats(_ context.Context, flat wb.FlattenAllResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedDaily += len(flat.Daily)
	w.savedApp += len(flat.App)
	w.savedNm += len(flat.Nm)
	w.savedBooster += len(flat.Booster)
	return nil
}

// GetLastCampaignStatsDateAll returns empty map (no resume in mock mode).
func (w *DiscardWriter) GetLastCampaignStatsDateAll(_ context.Context) (map[int]time.Time, error) {
	return make(map[int]time.Time), nil
}

// PopulateCampaignProducts records that it was called but does nothing.
func (w *DiscardWriter) PopulateCampaignProducts(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.populated = true
	return nil
}

// SavedCampaigns returns count of campaigns "saved" (counted).
func (w *DiscardWriter) SavedCampaigns() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedCampaigns
}

// SavedDetails returns count of details "saved" (counted).
func (w *DiscardWriter) SavedDetails() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedDetails
}

// SavedDaily returns count of daily stats "saved" (counted).
func (w *DiscardWriter) SavedDaily() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedDaily
}

// Populated returns whether PopulateCampaignProducts was called.
func (w *DiscardWriter) Populated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.populated
}
