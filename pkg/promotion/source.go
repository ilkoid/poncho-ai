package promotion

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource implements Source by delegating to *wb.Client.
// Thin adapter — all 14 methods are direct delegation, no transformation.
//
// *wb.Client already has matching method signatures through structural typing.
// WBSource exists to make the dependency on Source interface explicit,
// following the established v2 downloader pattern.
type WBSource struct {
	client *wb.Client
}

// NewWBSource creates a new WBSource wrapping *wb.Client.
func NewWBSource(client *wb.Client) *WBSource {
	return &WBSource{client: client}
}

func (s *WBSource) GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error) {
	return s.client.GetAdvertDetails(ctx, ids)
}

func (s *WBSource) GetNormqueryStats(ctx context.Context, req wb.NormqueryStatsRequest, rateLimit, burst int) (*wb.NormqueryStatsResponse, error) {
	return s.client.GetNormqueryStats(ctx, req, rateLimit, burst)
}

func (s *WBSource) GetNormqueryList(ctx context.Context, req wb.NormqueryListRequest, rateLimit, burst int) (*wb.NormqueryListResponse, error) {
	return s.client.GetNormqueryList(ctx, req, rateLimit, burst)
}

func (s *WBSource) GetNormqueryBids(ctx context.Context, req wb.NormqueryBidsRequest, rateLimit, burst int) (*wb.NormqueryBidsResponse, error) {
	return s.client.GetNormqueryBids(ctx, req, rateLimit, burst)
}

func (s *WBSource) GetNormqueryMinus(ctx context.Context, req wb.NormqueryMinusRequest, rateLimit, burst int) (*wb.NormqueryMinusResponse, error) {
	return s.client.GetNormqueryMinus(ctx, req, rateLimit, burst)
}

func (s *WBSource) GetBidRecommendations(ctx context.Context, nmID, advertID int, rateLimit, burst int) (*wb.BidRecommendationsResponse, error) {
	return s.client.GetBidRecommendations(ctx, nmID, advertID, rateLimit, burst)
}

func (s *WBSource) GetExpenses(ctx context.Context, from, to string, rateLimit, burst int) (wb.ExpensesResponse, error) {
	return s.client.GetExpenses(ctx, from, to, rateLimit, burst)
}

func (s *WBSource) GetBalance(ctx context.Context, rateLimit, burst int) (*wb.BalanceResponse, error) {
	return s.client.GetBalance(ctx, rateLimit, burst)
}

func (s *WBSource) GetPayments(ctx context.Context, from, to string, rateLimit, burst int) (wb.PaymentsResponse, error) {
	return s.client.GetPayments(ctx, from, to, rateLimit, burst)
}

func (s *WBSource) GetCalendarPromotions(ctx context.Context, start, end string, allPromo bool, rateLimit, burst int) (*wb.CalendarPromotionsResponse, error) {
	return s.client.GetCalendarPromotions(ctx, start, end, allPromo, rateLimit, burst)
}

func (s *WBSource) GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*wb.CalendarPromotionDetailsResponse, error) {
	return s.client.GetCalendarPromotionDetails(ctx, ids, rateLimit, burst)
}

func (s *WBSource) GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*wb.CalendarPromotionNomsResponse, error) {
	return s.client.GetCalendarPromotionNomenclatures(ctx, promotionID, inAction, limit, offset, rateLimit, burst)
}

func (s *WBSource) GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*wb.BudgetResponse, error) {
	return s.client.GetCampaignBudget(ctx, advertID, rateLimit, burst)
}

func (s *WBSource) GetMinBids(ctx context.Context, req wb.MinBidsRequest, rateLimit, burst int) (*wb.MinBidsResponse, error) {
	return s.client.GetMinBids(ctx, req, rateLimit, burst)
}
