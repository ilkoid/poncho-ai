// Package main provides mock client for V2 promotion downloader testing.
package main

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockV2Client implements V2Client for testing without API calls.
type MockV2Client struct{}

// NewMockV2Client creates a mock client with simulated data.
func NewMockV2Client() *MockV2Client {
	return &MockV2Client{}
}

func (m *MockV2Client) GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error) {
	details := make([]wb.AdvertDetail, len(ids))
	for i, id := range ids {
		details[i] = wb.AdvertDetail{
			ID:      id,
			BidType: "unified",
			Status:  9,
			Settings: wb.AdvertSettings{
				Name:        "Mock Campaign",
				PaymentType: "cpm",
			},
			NmSettings: []wb.AdvertNmSetting{
				{
					NmID: 100000 + i,
					Subject: wb.AdvertSubject{ID: 10, Name: "Платья женские"},
					BidsKopecks: wb.AdvertBids{Search: 300, Recommendations: 250},
				},
			},
		}
	}
	return details, nil
}

func (m *MockV2Client) GetNormqueryStats(ctx context.Context, req wb.NormqueryStatsRequest, rateLimit, burst int) (*wb.NormqueryStatsResponse, error) {
	groups := make([]wb.NormqueryStatsGroup, len(req.Items))
	for i, item := range req.Items {
		views := 1000 + i*100
		clicks := 50 + i*5
		groups[i] = wb.NormqueryStatsGroup{
			AdvertID: item.AdvertID,
			NmID:     item.NmID,
			Stats: []wb.NormqueryStatRow{
				{NormQuery: "платье женское", Views: &views, Clicks: clicks, Orders: 5 + i, Spend: 250.0 + float64(i)*10, AvgPos: 3.2},
				{NormQuery: "платье летнее", Views: &views, Clicks: clicks / 2, Orders: 2 + i, Spend: 120.0 + float64(i)*5, AvgPos: 5.1},
			},
		}
	}
	return &wb.NormqueryStatsResponse{Stats: groups}, nil
}

func (m *MockV2Client) GetNormqueryList(ctx context.Context, req wb.NormqueryListRequest, rateLimit, burst int) (*wb.NormqueryListResponse, error) {
	items := make([]wb.NormqueryListItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = wb.NormqueryListItem{
			AdvertID: item.AdvertID,
			NmID:     item.NmID,
			NormQueries: wb.NormqueryClusters{
				Active:   []string{"платье женское", "платье летнее"},
				Excluded: []string{"платье мужское"},
			},
		}
	}
	return &wb.NormqueryListResponse{Items: items}, nil
}

func (m *MockV2Client) GetNormqueryBids(ctx context.Context, req wb.NormqueryBidsRequest, rateLimit, burst int) (*wb.NormqueryBidsResponse, error) {
	bids := make([]wb.NormqueryBidItem, len(req.Items))
	for i, item := range req.Items {
		bids[i] = wb.NormqueryBidItem{AdvertID: item.AdvertID, NmID: item.NmID, Bid: 350 + i}
	}
	return &wb.NormqueryBidsResponse{Bids: bids}, nil
}

func (m *MockV2Client) GetNormqueryMinus(ctx context.Context, req wb.NormqueryMinusRequest, rateLimit, burst int) (*wb.NormqueryMinusResponse, error) {
	items := make([]wb.NormqueryMinusItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = wb.NormqueryMinusItem{
			AdvertID:    item.AdvertID,
			NmID:        item.NmID,
			NormQueries: []string{"платье мужское", "костюм"},
		}
	}
	return &wb.NormqueryMinusResponse{Items: items}, nil
}

func (m *MockV2Client) GetBidRecommendations(ctx context.Context, nmID, advertID int, rateLimit, burst int) (*wb.BidRecommendationsResponse, error) {
	return &wb.BidRecommendationsResponse{
		AdvertID: advertID,
		NmID:     nmID,
		Base: wb.BidRecommendBase{
			CompetitiveBid: wb.BidRecommendLevel{BidKopecks: 280},
			LeadersBid:     wb.BidRecommendLevel{BidKopecks: 450},
			Top2:           wb.BidRecommendLevel{BidKopecks: 600},
		},
		NormQueries: []wb.BidRecommendNormQ{
			{
				NormQuery:   "платье женское",
				ReachMax:    wb.BidRecommendReach{BidKopecks: 500, BidKopecksMin: 300},
				ReachMedium: wb.BidRecommendReach{BidKopecks: 350, BidKopecksMin: 200},
				ReachMin:    wb.BidRecommendReach{BidKopecks: 250, BidKopecksMin: 150},
			},
		},
	}, nil
}

func (m *MockV2Client) GetExpenses(ctx context.Context, from, to string, rateLimit, burst int) (wb.ExpensesResponse, error) {
	return wb.ExpensesResponse{
		{UpdNum: 1, UpdTime: from + "T12:00:00Z", UpdSum: 1500, AdvertID: 12345, CampName: "Mock Campaign 1", AdvertType: 9, PaymentType: "Баланс", AdvertStatus: 9},
		{UpdNum: 2, UpdTime: to + "T15:30:00Z", UpdSum: 2300, AdvertID: 67890, CampName: "Mock Campaign 2", AdvertType: 9, PaymentType: "Бонусы", AdvertStatus: 11},
	}, nil
}

func (m *MockV2Client) GetBalance(ctx context.Context, rateLimit, burst int) (*wb.BalanceResponse, error) {
	return &wb.BalanceResponse{
		Balance: 150000,
		Net:     120000,
		Bonus:   30000,
		Cashbacks: []wb.BalanceCashback{
			{Sum: 5000, Percent: 5, ExpirationDate: "2026-06-01"},
		},
	}, nil
}

func (m *MockV2Client) GetPayments(ctx context.Context, from, to string, rateLimit, burst int) (wb.PaymentsResponse, error) {
	return wb.PaymentsResponse{
		{ID: 1, Date: from, Sum: 50000, Type: 0, StatusID: 1, CardStatus: "success"},
		{ID: 2, Date: to, Sum: 30000, Type: 3, StatusID: 1, CardStatus: "success"},
	}, nil
}

func (m *MockV2Client) GetCalendarPromotions(ctx context.Context, start, end string, allPromo bool, rateLimit, burst int) (*wb.CalendarPromotionsResponse, error) {
	return &wb.CalendarPromotionsResponse{
		Data: wb.CalendarPromotionsData{Promotions: []wb.CalendarPromotion{
			{ID: 1, Name: "Mock Sale", Start: "2026-05-01T00:00:00Z", End: "2026-05-03T23:59:59Z", Type: "mega"},
			{ID: 2, Name: "Mock Seasonal", Start: "2026-04-10T00:00:00Z", End: "2026-04-15T23:59:59Z", Type: "seasonal"},
		}},
	}, nil
}

func (m *MockV2Client) GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*wb.CalendarPromotionDetailsResponse, error) {
	promotions := make([]wb.CalendarPromotionDetail, len(ids))
	for i, id := range ids {
		promotions[i] = wb.CalendarPromotionDetail{
			ID:                        id,
			Name:                      fmt.Sprintf("Акция %d", id),
			Description:               fmt.Sprintf("Описание акции %d", id),
			Advantages:                []string{"Бейдж", "Баннер на главной"},
			StartDateTime:             "2026-05-01T00:00:00Z",
			EndDateTime:               "2026-05-03T23:59:59Z",
			InPromoActionLeftovers:    45 + i,
			InPromoActionTotal:        123 + i,
			NotInPromoActionLeftovers: 3 + i,
			NotInPromoActionTotal:     10 + i,
			ParticipationPercentage:   80 + i,
			Type:                      "regular",
			ExceptionProductsCount:    5 + i,
			Ranging: []wb.CalendarPromotionRanging{
				{Condition: "productsInPromotion", ParticipationRate: 100, Boost: 30},
				{Condition: "calculateProducts", ParticipationRate: 50, Boost: 20},
			},
		}
	}
	return &wb.CalendarPromotionDetailsResponse{
		Data: wb.CalendarPromotionDetailsData{Promotions: promotions},
	}, nil
}

func (m *MockV2Client) GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*wb.CalendarPromotionNomsResponse, error) {
	return &wb.CalendarPromotionNomsResponse{
		Data: wb.CalendarPromotionNomsData{Nomenclatures: []wb.CalendarPromotionNom{
			{ID: 100001, InAction: inAction, Price: 1500, CurrencyCode: "RUB", PlanPrice: 1000, Discount: 15, PlanDiscount: 34},
			{ID: 100002, InAction: inAction, Price: 2500, CurrencyCode: "RUB", PlanPrice: 1800, Discount: 10, PlanDiscount: 28},
		}},
	}, nil
}

func (m *MockV2Client) GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*wb.BudgetResponse, error) {
	return &wb.BudgetResponse{Total: 5000 + advertID%1000}, nil
}

func (m *MockV2Client) GetMinBids(ctx context.Context, req wb.MinBidsRequest, rateLimit, burst int) (*wb.MinBidsResponse, error) {
	items := make([]wb.MinBidItem, len(req.NmIDs))
	for i, nmID := range req.NmIDs {
		items[i] = wb.MinBidItem{
			NmID: nmID,
			Bids: []wb.MinBidEntry{
				{Placement: "combined", Bid: 155 + i},
				{Placement: "search", Bid: 250 + i},
				{Placement: "recommendation", Bid: 250 + i},
			},
		}
	}
	return &wb.MinBidsResponse{Bids: items}, nil
}
