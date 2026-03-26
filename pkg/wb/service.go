// Package wb provides a reusable SDK for Wildberries API.
package wb

import (
	"context"
	"time"
)

// ============================================================================
// SERVICE LAYER - Business Logic Orchestration
// ============================================================================

// ProductService defines operations for product-related business logic.
//
// This service layer sits between the tools (adapters) and the client (SDK).
// It orchestrates complex operations that may involve multiple API calls,
// data transformation, caching, and business rules.
//
// Design Pattern: Service Layer ( Fowler )
// - Encapsulates business logic
// - Coordinates multiple API operations
// - Provides transaction-like semantics for complex operations
type ProductService interface {
	// GetProducts retrieves products with optional filtering.
	// Returns products from WB API, potentially with caching.
	GetProducts(ctx context.Context, filter ProductFilter) ([]ProductInfo, error)

	// GetProductByID retrieves a single product by nmID.
	// May use cache if available.
	GetProductByID(ctx context.Context, nmID int) (*ProductInfo, error)

	// SyncProducts synchronizes products from WB API to local storage.
	// Returns count of new/updated products.
	SyncProducts(ctx context.Context) (int, error)
}

// SalesService defines operations for sales/analytics business logic.
//
// Provides high-level operations for sales analysis, funnel metrics,
// and aggregated statistics.
type SalesService interface {
	// GetFunnelMetrics retrieves funnel metrics for products.
	// Aggregates data from Analytics API v3.
	GetFunnelMetrics(ctx context.Context, req FunnelRequest) (*FunnelMetrics, error)

	// GetFunnelHistory retrieves historical funnel metrics.
	// Supports up to 365 days with appropriate subscription.
	GetFunnelHistory(ctx context.Context, req FunnelHistoryRequest) (*FunnelHistory, error)

	// GetSalesReport downloads sales data for a period.
	// Handles pagination and resume logic internally.
	GetSalesReport(ctx context.Context, req SalesReportRequest) (*SalesReport, error)

	// GetSearchPositions retrieves search positions for products.
	// Returns average position, visibility, and query counts.
	GetSearchPositions(ctx context.Context, nmIDs []int, period int) (string, error)

	// GetTopSearchQueries retrieves top search queries for a product.
	// Returns queries sorted by orders.
	GetTopSearchQueries(ctx context.Context, nmID int, period int) (string, error)
}

// AdvertisingService defines operations for advertising/promotion logic.
//
// Manages campaigns, stats, and advertising performance analysis.
type AdvertisingService interface {
	// GetCampaigns retrieves all advertising campaigns.
	GetCampaigns(ctx context.Context) ([]PromotionAdvertGroup, error)

	// GetCampaignStats retrieves detailed stats for campaigns.
	// Batches requests for efficiency (max 50 IDs per request).
	GetCampaignStats(ctx context.Context, advertIDs []int, beginDate, endDate string) ([]CampaignDailyStats, error)

	// GetCampaignProducts retrieves products associated with campaigns.
	GetCampaignProducts(ctx context.Context, advertIDs []int) ([]CampaignProduct, error)

	// GetCampaignFullstats retrieves detailed campaign statistics with daily/apps/products breakdown.
	// Uses API v3: GET /adv/v3/fullstats
	// Maximum 50 campaign IDs per request, period up to 31 days.
	GetCampaignFullstats(ctx context.Context, req CampaignFullstatsRequest) ([]CampaignFullstatsResponse, error)
}

// FeedbackService defines operations for feedback and questions management.
//
// Provides access to product feedbacks and customer questions from WB API.
type FeedbackService interface {
	// GetFeedbacks retrieves product feedbacks with optional filtering.
	// Supports pagination via take/noffset and filtering by isAnswered/nmID.
	GetFeedbacks(ctx context.Context, req FeedbacksRequest) (*FeedbacksResponse, error)

	// GetQuestions retrieves product questions with optional filtering.
	// Supports pagination via take/noffset and filtering by isAnswered/nmID.
	GetQuestions(ctx context.Context, req QuestionsRequest) (*QuestionsResponse, error)

	// GetUnansweredCounts retrieves counts of unanswered feedbacks and questions.
	GetUnansweredCounts(ctx context.Context) (*UnansweredCountsResponse, error)
}

// AttributionService defines operations for sales attribution analysis.
//
// Provides aggregated analysis of organic vs advertising sales attribution.
type AttributionService interface {
	// GetAttributionSummary analyzes attribution of orders to organic vs advertising sources.
	// Combines data from funnel metrics and campaign statistics.
	GetAttributionSummary(ctx context.Context, req AttributionRequest) (*AttributionSummary, error)
}

// ============================================================================
// REQUEST/RESPONSE TYPES
// ============================================================================

// ProductFilter defines filtering options for product queries.
type ProductFilter struct {
	// CategoryID filters by category
	CategoryID int
	// Brand filters by brand name
	Brand string
	// Subject filters by subject name
	Subject string
	// Limit limits the number of results
	Limit int
}

// FunnelRequest defines parameters for funnel metrics queries.
type FunnelRequest struct {
	// NmIDs are product IDs to query
	NmIDs []int
	// Period in days (1-7 free, up to 365 with subscription)
	Period int
}

// FunnelMetrics represents aggregated funnel data.
type FunnelMetrics struct {
	// Products is a map of nmID to product funnel data
	Products map[int]*ProductFunnelData
	// Period is the analyzed period in days
	Period int
	// Timestamp is when the data was fetched
	Timestamp time.Time
}

// ProductFunnelData contains funnel metrics for a single product.
type ProductFunnelData struct {
	NmID           int
	OpenCount      int64
	CartCount      int64
	OrderCount     int64
	BuyoutCount    int64
	CancelCount    int64
	OrderSum       int64
	BuyoutSum      int64
	ConversionRate float64
	WBClubPercent  float64
}

// FunnelHistoryRequest defines parameters for historical queries.
type FunnelHistoryRequest struct {
	// NmIDs are product IDs to query
	NmIDs []int
	// DateFrom is the start date
	DateFrom time.Time
	// DateTo is the end date
	DateTo time.Time
}

// FunnelHistory represents historical funnel data.
type FunnelHistory struct {
	// DailyMetrics is a map of date to product metrics
	DailyMetrics map[time.Time]map[int]*ProductFunnelData
	// StartDate of the period
	StartDate time.Time
	// EndDate of the period
	EndDate time.Time
}

// SalesReportRequest defines parameters for sales report queries.
type SalesReportRequest struct {
	// DateFrom is the start of the period
	DateFrom time.Time
	// DateTo is the end of the period
	DateTo time.Time
	// Resume enables smart resume mode
	Resume bool
}

// SalesReport represents downloaded sales data.
type SalesReport struct {
	// TotalRows is the total count of downloaded rows
	TotalRows int
	// PeriodsCount is the number of periods processed
	PeriodsCount int
	// Duration is the time taken to download
	Duration time.Duration
}

// CampaignFullstatsRequest defines parameters for campaign fullstats queries.
type CampaignFullstatsRequest struct {
	// IDs are campaign IDs (max 50)
	IDs []int
	// BeginDate in YYYY-MM-DD format
	BeginDate string
	// EndDate in YYYY-MM-DD format
	EndDate string
}

// FeedbacksRequest defines parameters for feedback queries.
type FeedbacksRequest struct {
	// Take is the number of feedbacks to retrieve (max 100)
	Take int
	// Noffset is the pagination offset
	Noffset int
	// IsAnswered filters by answered status (nil = all)
	IsAnswered *bool
	// NmID filters by product ID (0 = all)
	NmID int
}

// QuestionsRequest defines parameters for question queries.
type QuestionsRequest struct {
	// Take is the number of questions to retrieve (max 100)
	Take int
	// Noffset is the pagination offset
	Noffset int
	// IsAnswered filters by answered status (nil = all)
	IsAnswered *bool
	// NmID filters by product ID (0 = all)
	NmID int
}

// UnansweredCountsResponse contains counts of unanswered items.
type UnansweredCountsResponse struct {
	FeedbacksUnanswered      int
	FeedbacksUnansweredToday int
	QuestionsUnanswered      int
	QuestionsUnansweredToday int
}

// AttributionRequest defines parameters for attribution analysis.
type AttributionRequest struct {
	// NmIDs are product IDs to analyze
	NmIDs []int
	// AdvertIDs are campaign IDs to attribute
	AdvertIDs []int
	// Period in days (1-90)
	Period int
}

// AttributionSummary represents the attribution analysis result.
type AttributionSummary struct {
	// PeriodStart is the analysis period start
	PeriodStart time.Time
	// PeriodEnd is the analysis period end
	PeriodEnd time.Time
	// TotalOrders is the total order count
	TotalOrders int
	// OrganicOrders is orders from organic traffic
	OrganicOrders int
	// AdOrders is orders from advertising
	AdOrders int
	// TotalViews is total product views
	TotalViews int
	// OrganicViews is views from organic traffic
	OrganicViews int
	// AdViews is views from advertising
	AdViews int
	// AdSpent is total advertising spend
	AdSpent float64
	// ByProduct contains per-product attribution
	ByProduct map[int]*ProductAttribution
	// ByCampaign contains per-campaign attribution
	ByCampaign map[int]*CampaignAttribution
}

// ProductAttribution contains attribution data for a single product.
type ProductAttribution struct {
	NmID          int
	TotalOrders   int
	OrganicOrders int
	AdOrders      int
	TotalViews    int
	OrganicViews  int
	AdViews       int
}

// CampaignAttribution contains attribution data for a single campaign.
type CampaignAttribution struct {
	AdvertID int
	Orders   int
	Spent    float64
}

// ============================================================================
// WB SERVICE - COMPOSITE SERVICE
// ============================================================================

// WbService is the main service interface combining all WB operations.
//
// This follows the Facade pattern - providing a unified interface to all
// WB-related subsystems (products, sales, advertising, feedbacks, attribution).
//
// Usage:
//
//	svc := wb.NewService(client, storage)
//	products, _ := svc.Products().GetProducts(ctx, filter)
//	funnel, _ := svc.Sales().GetFunnelMetrics(ctx, req)
type WbService interface {
	// Products returns the product service.
	Products() ProductService

	// Sales returns the sales/analytics service.
	Sales() SalesService

	// Advertising returns the advertising service.
	Advertising() AdvertisingService

	// Feedbacks returns the feedbacks service.
	Feedbacks() FeedbackService

	// Attribution returns the attribution service.
	Attribution() AttributionService
}

// Ensure DefaultWbService implements WbService.
var _ WbService = (*DefaultWbService)(nil)

// DefaultWbService is the default implementation of WbService.
type DefaultWbService struct {
	client *Client
	// Storage could be added later for caching/persistence
	// storage StorageBackend
}

// NewService creates a new WB service with the given client.
func NewService(client *Client) *DefaultWbService {
	return &DefaultWbService{
		client: client,
	}
}

// Products returns the product service.
func (s *DefaultWbService) Products() ProductService {
	return &productService{client: s.client}
}

// Sales returns the sales/analytics service.
func (s *DefaultWbService) Sales() SalesService {
	return &salesService{client: s.client}
}

// Advertising returns the advertising service.
func (s *DefaultWbService) Advertising() AdvertisingService {
	return &advertisingService{client: s.client}
}

// Feedbacks returns the feedbacks service.
func (s *DefaultWbService) Feedbacks() FeedbackService {
	return &feedbacksService{client: s.client}
}

// Attribution returns the attribution service.
func (s *DefaultWbService) Attribution() AttributionService {
	return &attributionService{
		sales:       &salesService{client: s.client},
		advertising: &advertisingService{client: s.client},
	}
}

// ============================================================================
// PRODUCT SERVICE IMPLEMENTATION
// ============================================================================

// Ensure productService implements ProductService.
var _ ProductService = (*productService)(nil)

type productService struct {
	client *Client
}

// GetProducts retrieves products with optional filtering.
func (s *productService) GetProducts(ctx context.Context, filter ProductFilter) ([]ProductInfo, error) {
	// Business logic: prepare request, call API, transform response
	// For now, delegate to client methods
	// Future: add caching, validation, business rules
	return nil, nil // TODO: implement
}

// GetProductByID retrieves a single product by nmID.
func (s *productService) GetProductByID(ctx context.Context, nmID int) (*ProductInfo, error) {
	// Future: check cache first, then API, update cache
	return nil, nil // TODO: implement
}

// SyncProducts synchronizes products from WB API to local storage.
func (s *productService) SyncProducts(ctx context.Context) (int, error) {
	// Future: fetch all products, compare with storage, update changes
	return 0, nil // TODO: implement
}

// Note: SalesService, AdvertisingService, FeedbackService, and AttributionService
// implementations are in separate files:
// - service_sales.go
// - service_advertising.go
// - service_feedbacks.go
// - service_attribution.go

