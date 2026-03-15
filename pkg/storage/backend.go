// Package storage provides storage abstraction for Poncho AI.
//
// Architecture:
//
// This package defines the StorageBackend interface following the Repository pattern.
// The interface abstracts different storage implementations (SQLite, PostgreSQL, Memory)
// allowing the application to switch backends without changing business logic.
//
// Design Rationale:
//   - Interface Segregation: Small, focused interfaces per domain (Sales, Funnel, Promotion)
//   - Dependency Inversion: Business logic depends on interfaces, not implementations
//   - Open/Closed: New storage backends can be added without modifying existing code
package storage

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// STORAGE BACKEND - Root Interface
// ============================================================================

// StorageBackend defines the root interface for storage operations.
//
// This follows the Abstract Factory pattern - providing access to
// domain-specific repositories through a single interface.
//
// Usage:
//
//	backend := sqlite.NewBackend(dbPath)
//	salesRepo := backend.Sales()
//	funnelRepo := backend.Funnel()
type StorageBackend interface {
	// Sales returns the sales repository.
	Sales() SalesRepository

	// Funnel returns the funnel analytics repository.
	Funnel() FunnelRepository

	// Promotion returns the promotion analytics repository.
	Promotion() PromotionRepository

	// Close closes all connections.
	Close() error
}

// ============================================================================
// SALES REPOSITORY - Sales Data Storage
// ============================================================================

// SalesRepository defines storage operations for sales data.
//
// Methods are designed for:
//   - Batch inserts (bulk loading)
//   - Resume support (exists check, last record)
//   - Analytics queries (FBW filtering, delivery methods)
//
// Thread Safety: Implementations must be safe for concurrent use.
type SalesRepository interface {
	// Save saves batch of sales rows to storage.
	// Uses INSERT OR IGNORE to skip duplicates (rrd_id is UNIQUE).
	Save(ctx context.Context, rows []wb.RealizationReportRow) error

	// SaveServiceRecords saves batch of service records (logistics, deductions).
	// These are records with nm_id = 0 from WB API.
	SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error

	// Exists checks if row with given rrdID already exists.
	// Used for resume functionality after interruption.
	Exists(ctx context.Context, rrdID int) (bool, error)

	// Count returns total number of sales in database.
	// Used for resume mode status display.
	Count(ctx context.Context) (int, error)

	// GetFBWOnly returns only FBW sales (filtered by delivery_method).
	// Returns empty slice if no FBW sales found.
	GetFBWOnly(ctx context.Context) ([]wb.RealizationReportRow, error)

	// GetLastSaleDT returns timestamp of the last sale in database.
	// For smart resume: start loading from this moment + 1 second.
	// Returns zero time if database is empty.
	GetLastSaleDT(ctx context.Context) (time.Time, error)

	// GetDeliveryMethods returns distinct delivery methods for analysis.
	GetDeliveryMethods(ctx context.Context) ([]string, error)

	// GetServiceRecordStats returns statistics about service records.
	GetServiceRecordStats(ctx context.Context) (*ServiceRecordStats, error)
}

// ServiceRecordStats holds statistics about service records.
type ServiceRecordStats struct {
	Total       int
	ByOperation map[string]int
}

// RefreshStats holds statistics about a refresh window operation.
//
// Returned by RefreshWithinWindow to report deleted/inserted counts.
type RefreshStats struct {
	// Deleted is the number of records deleted within the refresh window.
	Deleted int

	// Inserted is the number of records inserted within the refresh window.
	Inserted int

	// WindowStart is the start date of the refresh window.
	WindowStart time.Time

	// WindowEnd is the end date of the refresh window (usually today).
	WindowEnd time.Time
}

// ============================================================================
// FUNNEL REPOSITORY - Analytics Data Storage
// ============================================================================

// FunnelRepository defines storage operations for funnel analytics.
//
// Stores product metadata and daily funnel metrics from WB Analytics API v3.
// Grain: one row per (nm_id, date) combination.
type FunnelRepository interface {
	// SaveProducts saves product metadata (upsert by nm_id).
	SaveProducts(ctx context.Context, products []ProductRecord) error

	// SaveDailyMetrics saves daily funnel metrics (upsert by nm_id + date).
	SaveDailyMetrics(ctx context.Context, metrics []FunnelMetricRecord) error

	// SaveDailyMetricsWithWindow saves metrics with refresh window logic.
	// Records within the window (today - refreshDays) use INSERT OR REPLACE.
	// Records outside the window use INSERT OR IGNORE (not modified).
	//
	// This handles WB retroactive updates: recent data is refreshed, historical data is frozen.
	SaveDailyMetricsWithWindow(ctx context.Context, metrics []FunnelMetricRecord, refreshDays int) error

	// RefreshWithinWindow deletes and returns count of records within the refresh window.
	// Used for reporting before loading new data.
	RefreshWithinWindow(ctx context.Context, nmIDs []int, refreshDays int) (*RefreshStats, error)

	// GetProduct retrieves product metadata by nmID.
	GetProduct(ctx context.Context, nmID int) (*ProductRecord, error)

	// GetMetricsForPeriod retrieves funnel metrics for a date range.
	GetMetricsForPeriod(ctx context.Context, nmIDs []int, from, to time.Time) ([]FunnelMetricRecord, error)

	// GetLatestMetricDate returns the most recent metric date in storage.
	// Used for resume mode.
	GetLatestMetricDate(ctx context.Context) (time.Time, error)

	// Count returns total number of metric records.
	Count(ctx context.Context) (int, error)
}

// ProductRecord represents product metadata for storage.
type ProductRecord struct {
	NmID          int
	VendorCode    string
	Title         string
	BrandName     string
	SubjectID     int
	SubjectName   string
	ProductRating float64
	StockWB       int
	StockMP       int
	UpdatedAt     time.Time
}

// FunnelMetricRecord represents daily funnel metrics for storage.
type FunnelMetricRecord struct {
	NmID                 int
	MetricDate           time.Time
	OpenCount            int
	CartCount            int
	OrderCount           int
	BuyoutCount          int
	CancelCount          int
	AddToWishlist        int
	OrderSum             int
	BuyoutSum            int
	AvgPrice             int
	ConversionAddToCart  float64
	ConversionCartToOrder float64
	ConversionBuyout     float64
	WBClubOrderCount     int
	WBClubBuyoutCount    int
	WBClubBuyoutPercent  float64
	CreatedAt            time.Time
}

// ============================================================================
// PROMOTION REPOSITORY - Advertising Data Storage
// ============================================================================

// PromotionRepository defines storage operations for advertising data.
//
// Stores campaign metadata and daily stats from WB Promotion API.
// Grain: one row per (advert_id, date) combination for stats.
type PromotionRepository interface {
	// SaveCampaigns saves campaign metadata (upsert by advert_id).
	SaveCampaigns(ctx context.Context, campaigns []CampaignRecord) error

	// SaveDailyStats saves daily campaign statistics (upsert by advert_id + date).
	SaveDailyStats(ctx context.Context, stats []CampaignStatsRecord) error

	// SaveCampaignProducts saves campaign-product relationships.
	SaveCampaignProducts(ctx context.Context, relations []CampaignProductRecord) error

	// GetCampaign retrieves campaign metadata by advertID.
	GetCampaign(ctx context.Context, advertID int) (*CampaignRecord, error)

	// GetActiveCampaigns retrieves all active campaigns.
	GetActiveCampaigns(ctx context.Context) ([]CampaignRecord, error)

	// GetStatsForPeriod retrieves campaign stats for a date range.
	GetStatsForPeriod(ctx context.Context, advertIDs []int, from, to time.Time) ([]CampaignStatsRecord, error)

	// GetLastChangeTime returns the most recent change time in campaigns.
	// Used for resume mode.
	GetLastChangeTime(ctx context.Context) (time.Time, error)

	// Count returns total number of campaign records.
	Count(ctx context.Context) (int, error)
}

// CampaignRecord represents campaign metadata for storage.
type CampaignRecord struct {
	AdvertID      int
	CampaignType  int
	Status        int
	ChangeTime    time.Time
	TotalViews    int
	TotalClicks   int
	TotalOrders   int
	TotalSum      float64
	TotalSumPrice float64
	UpdatedAt     time.Time
}

// CampaignStatsRecord represents daily campaign statistics for storage.
type CampaignStatsRecord struct {
	AdvertID  int
	StatsDate time.Time
	Views     int
	Clicks    int
	CTR       float64
	CPC       float64
	CR        float64
	Orders    int
	SHKs      int
	ATBs      int
	Canceled  int
	Sum       float64
	SumPrice  float64
	CreatedAt time.Time
}

// CampaignProductRecord represents campaign-product relationship.
type CampaignProductRecord struct {
	AdvertID    int
	NmID        int
	ProductName string
	TotalViews  int
	TotalClicks int
	TotalOrders int
	TotalSum    float64
}

// ============================================================================
// BACKEND FACTORY - Configuration-Based Creation
// ============================================================================

// BackendConfig holds configuration for storage backend creation.
type BackendConfig struct {
	// Type specifies the backend type: "sqlite", "postgres", "memory"
	Type string

	// ConnectionString for database backends
	ConnectionString string

	// SQLite-specific options
	EnableWAL bool
}

// BackendFactory creates storage backends from configuration.
//
// This follows the Factory pattern - encapsulating backend creation logic.
// New backends can be registered without modifying existing code.
type BackendFactory interface {
	// Create creates a storage backend from configuration.
	Create(cfg BackendConfig) (StorageBackend, error)

	// Register registers a backend creator for a type.
	Register(backendType string, creator BackendCreator)
}

// BackendCreator is a function that creates a storage backend.
type BackendCreator func(cfg BackendConfig) (StorageBackend, error)

// DefaultBackendFactory is the default implementation of BackendFactory.
type DefaultBackendFactory struct {
	creators map[string]BackendCreator
}

// NewBackendFactory creates a new backend factory with default creators.
func NewBackendFactory() *DefaultBackendFactory {
	f := &DefaultBackendFactory{
		creators: make(map[string]BackendCreator),
	}
	// SQLite is registered by the sqlite package to avoid import cycles
	return f
}

// Create creates a storage backend from configuration.
func (f *DefaultBackendFactory) Create(cfg BackendConfig) (StorageBackend, error) {
	creator, ok := f.creators[cfg.Type]
	if !ok {
		return nil, &UnsupportedBackendError{Type: cfg.Type}
	}
	return creator(cfg)
}

// Register registers a backend creator for a type.
func (f *DefaultBackendFactory) Register(backendType string, creator BackendCreator) {
	f.creators[backendType] = creator
}

// ============================================================================
// ERRORS
// ============================================================================

// UnsupportedBackendError indicates the requested backend type is not supported.
type UnsupportedBackendError struct {
	Type string
}

// Error implements the error interface.
func (e *UnsupportedBackendError) Error() string {
	return "unsupported storage backend: " + e.Type
}

// Ensure UnsupportedBackendError implements error.
var _ error = (*UnsupportedBackendError)(nil)
