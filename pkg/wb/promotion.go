// Package wb provides Promotion API methods.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GetPromotionCount returns list of campaigns from /adv/v1/promotion/count.
// Despite the name, this endpoint returns full campaign list grouped by type+status.
func (c *Client) GetPromotionCount(ctx context.Context) (*PromotionCountResponse, error) {
	// Use demo mode if configured
	if c.IsDemoKey() {
		return c.getMockPromotionCount(), nil
	}

	// Build URL
	endpoint := "https://advert-api.wildberries.ru"
	path := "/adv/v1/promotion/count"

	var resp PromotionCountResponse
	err := c.Get(ctx, "get_promotion_count", endpoint, 300, 5, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("promotion count: %w", err)
	}

	return &resp, nil
}

// GetCampaignFullstats returns full campaign statistics from /adv/v3/fullstats.
// Returns the complete 4-level hierarchy: Campaign → Day → App → Nm.
// Max 50 campaign IDs per request. Rate limit is configurable (swagger default: 3 req/min).
func (c *Client) GetCampaignFullstats(ctx context.Context, advertIDs []int, beginDate, endDate string, rateLimit, burst int) ([]CampaignFullstatsResponse, error) {
	// Use demo mode if configured
	if c.IsDemoKey() {
		return c.getMockCampaignFullstats(advertIDs, beginDate, endDate), nil
	}

	if len(advertIDs) == 0 {
		return nil, nil
	}

	// Build URL with query parameters
	// GET /adv/v3/fullstats?ids=123,456&beginDate=2025-01-01&endDate=2025-01-07
	endpoint := "https://advert-api.wildberries.ru"

	idStrs := make([]string, len(advertIDs))
	for i, id := range advertIDs {
		idStrs[i] = fmt.Sprintf("%d", id)
	}

	params := url.Values{}
	params.Set("ids", strings.Join(idStrs, ","))
	params.Set("beginDate", beginDate)
	params.Set("endDate", endDate)

	path := "/adv/v3/fullstats?" + params.Encode()

	// Parse into canonical type with full hierarchy
	// Use GetStream to avoid io.ReadAll + json.Unmarshal double allocation
	var response []CampaignFullstatsResponse
	err := c.GetStream(ctx, "get_campaign_fullstats", endpoint, rateLimit, burst, path, nil, &response)
	if err != nil {
		return nil, fmt.Errorf("campaign fullstats: %w", err)
	}

	return response, nil
}

// GetAdvertDetails returns campaign details from /api/advert/v2/adverts.
// If ids is non-empty, passes them as query param (max 50 per swagger).
// If ids is empty, calls without filter — API returns ALL campaigns.
// Rate limit: 5 req/sec (swagger).
// NOTE: v2 may not return details for all campaign types (e.g. type=8 legacy, type=6 booster).
func (c *Client) GetAdvertDetails(ctx context.Context, ids []int) ([]AdvertDetail, error) {
	// Use demo mode if configured
	if c.IsDemoKey() {
		return c.getMockAdvertDetails(ids), nil
	}

	endpoint := "https://advert-api.wildberries.ru"

	var path string
	if len(ids) > 0 {
		// Build URL with IDs — max 50 per swagger to avoid 414 Request-URI Too Large
		idStrs := make([]string, len(ids))
		for i, id := range ids {
			idStrs[i] = fmt.Sprintf("%d", id)
		}
		params := url.Values{}
		params.Set("id", strings.Join(idStrs, ","))
		path = "/api/advert/v2/adverts?" + params.Encode()
	} else {
		// No IDs — return all campaigns (API ignores filter anyway)
		path = "/api/advert/v2/adverts"
	}

	var response AdvertsResponse
	err := c.Get(ctx, "get_advert_details", endpoint, 300, 5, path, nil, &response)
	if err != nil {
		return nil, fmt.Errorf("advert details: %w", err)
	}

	return response.Adverts, nil
}

// Mock implementations for demo mode

func (c *Client) getMockPromotionCount() *PromotionCountResponse {
	return &PromotionCountResponse{
		Adverts: []PromotionAdvertGroup{
			{
				Type:   9,
				Status: 9,
				Count:  2,
				AdvertList: []PromotionAdvert{
					{AdvertID: 12345, ChangeTime: "2025-01-01T00:00:00Z"},
					{AdvertID: 67890, ChangeTime: "2025-01-15T00:00:00Z"},
				},
			},
		},
		All: 2,
	}
}

func (c *Client) getMockAdvertDetails(ids []int) []AdvertDetail {
	results := make([]AdvertDetail, 0, len(ids))
	paymentTypes := []string{"cpm", "cpc"}
	bidTypes := []string{"manual", "unified"}
	for i, id := range ids {
		results = append(results, AdvertDetail{
			ID:      id,
			BidType: bidTypes[i%2],
			Status:  9,
			Settings: AdvertSettings{
				Name:        fmt.Sprintf("Mock Campaign %d", id),
				PaymentType: paymentTypes[i%2],
				Placements: AdvertPlacements{
					Search:          i%2 == 0,
					Recommendations: i%3 == 0,
				},
			},
			Timestamps: AdvertTimestamps{
				Created: "2025-01-01T00:00:00Z",
				Updated: "2025-01-15T00:00:00Z",
				Started: "2025-01-02T00:00:00Z",
			},
		})
	}
	return results
}

func (c *Client) getMockCampaignFullstats(advertIDs []int, beginDate, endDate string) []CampaignFullstatsResponse {
	results := make([]CampaignFullstatsResponse, 0, len(advertIDs))
	for i, id := range advertIDs {
		results = append(results, CampaignFullstatsResponse{
			AdvertID: id,
			Views:    1000 + i*100,
			Clicks:   50 + i*5,
			CTR:      5.0 + float64(i),
			CPC:      4.5 + float64(i%3),
			CR:       2.0 + float64(i%5),
			Orders:   5 + i,
			Shks:     4 + i,
			Atbs:     0,
			Canceled: 0,
			Sum:      250.0 + float64(i*10),
			SumPrice: 5000.0 + float64(i*100),
			Days: []CampaignFullstatsDay{
				{
					Date:     beginDate,
					Views:    500 + i*50,
					Clicks:   25 + i*3,
					CTR:      5.0 + float64(i)*0.1,
					CPC:      4.5,
					CR:       2.0,
					Orders:   3 + i,
					Shks:     2 + i,
					Sum:      125.0 + float64(i*5),
					SumPrice: 2500.0 + float64(i*50),
					Apps: []CampaignFullstatsApp{
						{AppType: 1, Views: 300 + i*30, Clicks: 15 + i*2, Orders: 2 + i, Sum: 75.0 + float64(i)*3, SumPrice: 1500.0 + float64(i)*30},
						{AppType: 32, Views: 150 + i*15, Clicks: 8 + i, Orders: 1 + i, Sum: 40.0 + float64(i)*2, SumPrice: 800.0 + float64(i)*20},
						{AppType: 64, Views: 50 + i*5, Clicks: 2 + i, Orders: 0, Sum: 10.0 + float64(i), SumPrice: 200.0 + float64(i)*10},
					},
				},
			},
		})
	}
	return results
}

// ============================================================================
// V2 Promotion API Methods (normquery, bid recommendations, finance, calendar)
// ============================================================================

const promotionEndpoint = "https://advert-api.wildberries.ru"
const calendarEndpoint = "https://dp-calendar-api.wildberries.ru"

// GetNormqueryStats returns search cluster statistics from POST /adv/v0/normquery/stats.
// Rate limit: 10 req/min (swagger). Batch up to 100 items per request.
func (c *Client) GetNormqueryStats(ctx context.Context, req NormqueryStatsRequest, rateLimit, burst int) (*NormqueryStatsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockNormqueryStats(req), nil
	}
	var resp NormqueryStatsResponse
	err := c.Post(ctx, "normquery_stats", promotionEndpoint, rateLimit, burst, "/adv/v0/normquery/stats", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("normquery stats: %w", err)
	}
	return &resp, nil
}

// GetNormqueryList returns active/excluded search clusters from POST /adv/v0/normquery/list.
// Rate limit: 5 req/sec (swagger). Batch up to 100 items per request.
func (c *Client) GetNormqueryList(ctx context.Context, req NormqueryListRequest, rateLimit, burst int) (*NormqueryListResponse, error) {
	if c.IsDemoKey() {
		return c.getMockNormqueryList(req), nil
	}
	var resp NormqueryListResponse
	err := c.Post(ctx, "normquery_list", promotionEndpoint, rateLimit, burst, "/adv/v0/normquery/list", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("normquery list: %w", err)
	}
	return &resp, nil
}

// GetNormqueryBids returns current bids per cluster from POST /adv/v0/normquery/get-bids.
// Rate limit: 5 req/sec (swagger). Batch up to 100 items per request.
func (c *Client) GetNormqueryBids(ctx context.Context, req NormqueryBidsRequest, rateLimit, burst int) (*NormqueryBidsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockNormqueryBids(req), nil
	}
	var resp NormqueryBidsResponse
	err := c.Post(ctx, "normquery_bids", promotionEndpoint, rateLimit, burst, "/adv/v0/normquery/get-bids", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("normquery bids: %w", err)
	}
	return &resp, nil
}

// GetNormqueryMinus returns minus phrases from POST /adv/v0/normquery/get-minus.
// Rate limit: 5 req/sec (swagger). Batch up to 100 items per request.
func (c *Client) GetNormqueryMinus(ctx context.Context, req NormqueryMinusRequest, rateLimit, burst int) (*NormqueryMinusResponse, error) {
	if c.IsDemoKey() {
		return c.getMockNormqueryMinus(req), nil
	}
	var resp NormqueryMinusResponse
	err := c.Post(ctx, "normquery_minus", promotionEndpoint, rateLimit, burst, "/adv/v0/normquery/get-minus", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("normquery minus: %w", err)
	}
	return &resp, nil
}

// GetBidRecommendations returns recommended bids from GET /api/advert/v0/bids/recommendations.
// Rate limit: 5 req/min (swagger). CPM campaigns only.
func (c *Client) GetBidRecommendations(ctx context.Context, nmID, advertID int, rateLimit, burst int) (*BidRecommendationsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockBidRecommendations(nmID, advertID), nil
	}
	params := url.Values{}
	params.Set("nmId", fmt.Sprintf("%d", nmID))
	params.Set("advertId", fmt.Sprintf("%d", advertID))
	path := "/api/advert/v0/bids/recommendations?" + params.Encode()

	var resp BidRecommendationsResponse
	err := c.Get(ctx, "bid_recommendations", promotionEndpoint, rateLimit, burst, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("bid recommendations: %w", err)
	}
	return &resp, nil
}

// GetExpenses returns expense history from GET /adv/v1/upd.
// Rate limit: 1 req/sec (swagger).
func (c *Client) GetExpenses(ctx context.Context, from, to string, rateLimit, burst int) (ExpensesResponse, error) {
	if c.IsDemoKey() {
		return c.getMockExpenses(from, to), nil
	}
	params := url.Values{}
	params.Set("from", from)
	params.Set("to", to)
	path := "/adv/v1/upd?" + params.Encode()

	var resp ExpensesResponse
	err := c.Get(ctx, "expenses", promotionEndpoint, rateLimit, burst, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("expenses: %w", err)
	}
	return resp, nil
}

// GetBalance returns account balance from GET /adv/v1/balance.
// Rate limit: 1 req/sec (swagger).
func (c *Client) GetBalance(ctx context.Context, rateLimit, burst int) (*BalanceResponse, error) {
	if c.IsDemoKey() {
		return c.getMockBalance(), nil
	}
	var resp BalanceResponse
	err := c.Get(ctx, "balance", promotionEndpoint, rateLimit, burst, "/adv/v1/balance", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("balance: %w", err)
	}
	return &resp, nil
}

// GetPayments returns payment history from GET /adv/v1/payments.
// Rate limit: 1 req/sec (swagger). Max date range: 31 days.
func (c *Client) GetPayments(ctx context.Context, from, to string, rateLimit, burst int) (PaymentsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockPayments(from, to), nil
	}
	params := url.Values{}
	params.Set("from", from)
	params.Set("to", to)
	path := "/adv/v1/payments?" + params.Encode()

	var resp PaymentsResponse
	err := c.Get(ctx, "payments", promotionEndpoint, rateLimit, burst, path, nil, &resp)
	if err != nil {
		if IsNoContent(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("payments: %w", err)
	}
	return resp, nil
}

// GetCalendarPromotions returns WB promotions from GET /api/v1/calendar/promotions.
// Rate limit: 10 req/6sec (swagger). Different base URL: dp-calendar-api.wildberries.ru.
// start/end format: YYYY-MM-DDTHH:MM:SSZ (e.g. "2026-01-01T00:00:00Z").
// allPromo: false = available for participation, true = all promotions.
func (c *Client) GetCalendarPromotions(ctx context.Context, start, end string, allPromo bool, rateLimit, burst int) (*CalendarPromotionsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockCalendarPromotions(), nil
	}
	params := url.Values{}
	params.Set("startDateTime", start)
	params.Set("endDateTime", end)
	params.Set("allPromo", fmt.Sprintf("%t", allPromo))
	var resp CalendarPromotionsResponse
	err := c.getWithKey(ctx, "calendar_promotions", calendarEndpoint, rateLimit, burst, "/api/v1/calendar/promotions", params, c.calendarKey, &resp)
	if err != nil {
		return nil, fmt.Errorf("calendar promotions: %w", err)
	}
	return &resp, nil
}

// GetCalendarPromotionDetails returns detailed info about promotions.
// Rate limit: 10 req/6sec (shared with calendar). Max 100 IDs per request.
// Source: GET /api/v1/calendar/promotions/details?promotionIDs=1,2,3
func (c *Client) GetCalendarPromotionDetails(ctx context.Context, ids []int, rateLimit, burst int) (*CalendarPromotionDetailsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockCalendarPromotionDetails(ids), nil
	}
	if len(ids) == 0 {
		return &CalendarPromotionDetailsResponse{}, nil
	}

	params := url.Values{}
	for _, id := range ids {
		params.Add("promotionIDs", fmt.Sprintf("%d", id))
	}
	path := "/api/v1/calendar/promotions/details?" + params.Encode()

	var resp CalendarPromotionDetailsResponse
	err := c.getWithKey(ctx, "calendar_promotions", calendarEndpoint, rateLimit, burst, path, nil, c.calendarKey, &resp)
	if err != nil {
		return nil, fmt.Errorf("calendar promotion details: %w", err)
	}
	return &resp, nil
}

// GetCalendarPromotionNomenclatures returns products eligible for promotion.
// Rate limit: 10 req/6sec (shared). Paginated (max 1000 per request).
// Not applicable for auto-promotions (returns 422).
// Source: GET /api/v1/calendar/promotions/nomenclatures
func (c *Client) GetCalendarPromotionNomenclatures(ctx context.Context, promotionID int, inAction bool, limit, offset, rateLimit, burst int) (*CalendarPromotionNomsResponse, error) {
	if c.IsDemoKey() {
		return c.getMockCalendarPromotionNomenclatures(promotionID, inAction), nil
	}
	params := url.Values{}
	params.Set("promotionID", fmt.Sprintf("%d", promotionID))
	params.Set("inAction", fmt.Sprintf("%t", inAction))
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("offset", fmt.Sprintf("%d", offset))
	path := "/api/v1/calendar/promotions/nomenclatures?" + params.Encode()

	var resp CalendarPromotionNomsResponse
	err := c.getWithKey(ctx, "calendar_promotions", calendarEndpoint, rateLimit, burst, path, nil, c.calendarKey, &resp)
	if err != nil {
		return nil, fmt.Errorf("calendar promotion nomenclatures: %w", err)
	}
	return &resp, nil
}

// GetCampaignBudget returns campaign budget from GET /adv/v1/budget.
// Rate limit: 4 req/sec (swagger).
func (c *Client) GetCampaignBudget(ctx context.Context, advertID int, rateLimit, burst int) (*BudgetResponse, error) {
	if c.IsDemoKey() {
		return &BudgetResponse{Total: 5000}, nil
	}
	params := url.Values{}
	params.Set("id", fmt.Sprintf("%d", advertID))
	path := "/adv/v1/budget?" + params.Encode()

	var resp BudgetResponse
	err := c.Get(ctx, "budget", promotionEndpoint, rateLimit, burst, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("campaign budget: %w", err)
	}
	return &resp, nil
}

// GetMinBids returns minimum bids from POST /api/advert/v1/bids/min.
// Rate limit: 20 req/min (swagger).
func (c *Client) GetMinBids(ctx context.Context, req MinBidsRequest, rateLimit, burst int) (*MinBidsResponse, error) {
	if c.IsDemoKey() {
		return &MinBidsResponse{Bids: []MinBidItem{
			{NmID: req.NmIDs[0], Bids: []MinBidEntry{
				{Placement: "combined", Bid: 155},
				{Placement: "search", Bid: 250},
				{Placement: "recommendation", Bid: 250},
			}},
		}}, nil
	}
	var resp MinBidsResponse
	err := c.Post(ctx, "min_bids", promotionEndpoint, rateLimit, burst, "/api/advert/v1/bids/min", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("min bids: %w", err)
	}
	return &resp, nil
}

// ============================================================================
// V2 Mock implementations for demo mode
// ============================================================================

func (c *Client) getMockNormqueryStats(req NormqueryStatsRequest) *NormqueryStatsResponse {
	groups := make([]NormqueryStatsGroup, 0, len(req.Items))
	for _, item := range req.Items {
		groups = append(groups, NormqueryStatsGroup{
			AdvertID: item.AdvertID,
			NmID:     item.NmID,
			Stats: []NormqueryStatRow{
				{NormQuery: "платье женское", Clicks: 50, Orders: 5, Spend: 250.0, AvgPos: 3.2},
				{NormQuery: "платье летнее", Clicks: 30, Orders: 3, Spend: 150.0, AvgPos: 5.1},
			},
		})
	}
	return &NormqueryStatsResponse{Stats: groups}
}

func (c *Client) getMockNormqueryList(req NormqueryListRequest) *NormqueryListResponse {
	items := make([]NormqueryListItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, NormqueryListItem{
			AdvertID: item.AdvertID,
			NmID:     item.NmID,
			NormQueries: NormqueryClusters{
				Active:   []string{"платье женское", "платье летнее"},
				Excluded: []string{"платье мужское"},
			},
		})
	}
	return &NormqueryListResponse{Items: items}
}

func (c *Client) getMockNormqueryBids(req NormqueryBidsRequest) *NormqueryBidsResponse {
	bids := make([]NormqueryBidItem, 0, len(req.Items))
	for _, item := range req.Items {
		bids = append(bids, NormqueryBidItem{
			AdvertID: item.AdvertID,
			NmID:     item.NmID,
			Bid:      350,
		})
	}
	return &NormqueryBidsResponse{Bids: bids}
}

func (c *Client) getMockNormqueryMinus(req NormqueryMinusRequest) *NormqueryMinusResponse {
	items := make([]NormqueryMinusItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, NormqueryMinusItem{
			AdvertID:    item.AdvertID,
			NmID:        item.NmID,
			NormQueries: []string{"платье мужское", "костюм"},
		})
	}
	return &NormqueryMinusResponse{Items: items}
}

func (c *Client) getMockBidRecommendations(nmID, advertID int) *BidRecommendationsResponse {
	return &BidRecommendationsResponse{
		AdvertID: advertID,
		NmID:     nmID,
		Base: BidRecommendBase{
			CompetitiveBid: BidRecommendLevel{BidKopecks: 280},
			LeadersBid:     BidRecommendLevel{BidKopecks: 450},
			Top2:           BidRecommendLevel{BidKopecks: 600},
		},
		NormQueries: []BidRecommendNormQ{
			{
				NormQuery:   "платье женское",
				ReachMax:    BidRecommendReach{BidKopecks: 500, BidKopecksMin: 300},
				ReachMedium: BidRecommendReach{BidKopecks: 350, BidKopecksMin: 200},
				ReachMin:    BidRecommendReach{BidKopecks: 250, BidKopecksMin: 150},
			},
		},
	}
}

func (c *Client) getMockExpenses(from, to string) ExpensesResponse {
	return ExpensesResponse{
		{UpdNum: 1, UpdTime: from + "T12:00:00Z", UpdSum: 1500, AdvertID: 12345, CampName: "Mock Campaign 1", AdvertType: 9, PaymentType: "Баланс", AdvertStatus: 9},
		{UpdNum: 2, UpdTime: to + "T15:30:00Z", UpdSum: 2300, AdvertID: 67890, CampName: "Mock Campaign 2", AdvertType: 9, PaymentType: "Бонусы", AdvertStatus: 11},
	}
}

func (c *Client) getMockBalance() *BalanceResponse {
	return &BalanceResponse{
		Balance: 150000,
		Net:     120000,
		Bonus:   30000,
		Cashbacks: []BalanceCashback{
			{Sum: 5000, Percent: 5, ExpirationDate: "2026-06-01"},
		},
	}
}

func (c *Client) getMockPayments(from, to string) PaymentsResponse {
	return PaymentsResponse{
		{ID: 1, Date: from, Sum: 50000, Type: 0, StatusID: 1, CardStatus: "success"},
		{ID: 2, Date: to, Sum: 30000, Type: 3, StatusID: 1, CardStatus: "success"},
	}
}

func (c *Client) getMockCalendarPromotions() *CalendarPromotionsResponse {
	return &CalendarPromotionsResponse{
		Data: CalendarPromotionsData{Promotions: []CalendarPromotion{
			{ID: 1, Name: "Мегасейл Весна 2026", Start: "2026-03-20T00:00:00Z", End: "2026-03-23T23:59:59Z", Type: "mega"},
			{ID: 2, Name: "Распродажа 8 марта", Start: "2026-03-05T00:00:00Z", End: "2026-03-08T23:59:59Z", Type: "seasonal"},
		}},
	}
}

func (c *Client) getMockCalendarPromotionDetails(ids []int) *CalendarPromotionDetailsResponse {
	promotions := make([]CalendarPromotionDetail, len(ids))
	for i, id := range ids {
		promotions[i] = CalendarPromotionDetail{
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
			Ranging: []CalendarPromotionRanging{
				{Condition: "productsInPromotion", ParticipationRate: 100, Boost: 30},
				{Condition: "calculateProducts", ParticipationRate: 50, Boost: 20},
			},
		}
	}
	return &CalendarPromotionDetailsResponse{
		Data: CalendarPromotionDetailsData{Promotions: promotions},
	}
}

func (c *Client) getMockCalendarPromotionNomenclatures(promotionID int, inAction bool) *CalendarPromotionNomsResponse {
	noms := []CalendarPromotionNom{
		{ID: 100001, InAction: inAction, Price: 1500, CurrencyCode: "RUB", PlanPrice: 1000, Discount: 15, PlanDiscount: 34},
		{ID: 100002, InAction: inAction, Price: 2500, CurrencyCode: "RUB", PlanPrice: 1800, Discount: 10, PlanDiscount: 28},
	}
	return &CalendarPromotionNomsResponse{
		Data: CalendarPromotionNomsData{Nomenclatures: noms},
	}
}

// ============================================================================
// Utility wrappers (unchanged)
// ============================================================================

// Get is a wrapper around the internal Get method for external use.
// This provides a simple way to call the WB API from tools.
func (c *Client) GetAPI(ctx context.Context, toolID, endpoint string, rateLimit, burst int, path string, params map[string]string, result any) error {
	// Convert map[string]string to url.Values
	values := make(url.Values)
	for k, v := range params {
		values.Set(k, v)
	}
	return c.Get(ctx, toolID, endpoint, rateLimit, burst, path, values, result)
}

// PostAPI is a wrapper around the internal Post method for external use.
func (c *Client) PostAPI(ctx context.Context, toolID, endpoint string, rateLimit, burst int, path string, body any, result any) error {
	return c.Post(ctx, toolID, endpoint, rateLimit, burst, path, body, result)
}

// ParseJSON is a helper for parsing JSON responses.
func ParseJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// IsHTTPError checks if error is an HTTP error with specific status.
func IsHTTPError(err error, status int) bool {
	if httpErr, ok := err.(*HTTPError); ok {
		return httpErr.StatusCode == status
	}
	// Also check for status in error message
	return strings.Contains(err.Error(), fmt.Sprintf("status %d", status))
}

// WrapHTTPError wraps HTTP response to error if status >= 400.
func WrapHTTPError(resp *http.Response, body []byte) error {
	if resp.StatusCode < 400 {
		return nil
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
