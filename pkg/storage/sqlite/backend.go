// Package sqlite provides SQLite storage implementation.
package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Ensure SQLiteBackend implements storage.StorageBackend.
var _ storage.StorageBackend = (*SQLiteBackend)(nil)

// SQLiteBackend implements StorageBackend for SQLite.
// Provides access to all repository types through a single interface.
type SQLiteBackend struct {
	repo *SQLiteSalesRepository
}

// NewBackend creates a new SQLite storage backend.
func NewBackend(dbPath string) (*SQLiteBackend, error) {
	repo, err := NewSQLiteSalesRepository(dbPath)
	if err != nil {
		return nil, fmt.Errorf("create repository: %w", err)
	}

	return &SQLiteBackend{repo: repo}, nil
}

// Sales returns the sales repository.
func (b *SQLiteBackend) Sales() storage.SalesRepository {
	return &salesRepoAdapter{repo: b.repo}
}

// Funnel returns the funnel analytics repository.
func (b *SQLiteBackend) Funnel() storage.FunnelRepository {
	return &funnelRepoAdapter{repo: b.repo}
}

// Promotion returns the promotion analytics repository.
func (b *SQLiteBackend) Promotion() storage.PromotionRepository {
	return &promotionRepoAdapter{repo: b.repo}
}

// Close closes all connections.
func (b *SQLiteBackend) Close() error {
	return b.repo.Close()
}

// ============================================================================
// SALES REPOSITORY ADAPTER
// ============================================================================

// Ensure salesRepoAdapter implements storage.SalesRepository.
var _ storage.SalesRepository = (*salesRepoAdapter)(nil)

type salesRepoAdapter struct {
	repo *SQLiteSalesRepository
}

func (a *salesRepoAdapter) Save(ctx context.Context, rows []wb.RealizationReportRow) error {
	return a.repo.Save(ctx, rows)
}

func (a *salesRepoAdapter) SaveServiceRecords(ctx context.Context, rows []wb.RealizationReportRow) error {
	return a.repo.SaveServiceRecords(ctx, rows)
}

func (a *salesRepoAdapter) Exists(ctx context.Context, rrdID int) (bool, error) {
	return a.repo.Exists(ctx, rrdID)
}

func (a *salesRepoAdapter) Count(ctx context.Context) (int, error) {
	return a.repo.Count(ctx)
}

func (a *salesRepoAdapter) GetFBWOnly(ctx context.Context) ([]wb.RealizationReportRow, error) {
	return a.repo.GetFBWOnly(ctx)
}

func (a *salesRepoAdapter) GetLastSaleDT(ctx context.Context) (time.Time, error) {
	return a.repo.GetLastSaleDT(ctx)
}

func (a *salesRepoAdapter) GetDeliveryMethods(ctx context.Context) ([]string, error) {
	return a.repo.GetDeliveryMethods(ctx)
}

func (a *salesRepoAdapter) GetServiceRecordStats(ctx context.Context) (*storage.ServiceRecordStats, error) {
	stats, err := a.repo.GetServiceRecordStats(ctx)
	if err != nil {
		return nil, err
	}
	return &storage.ServiceRecordStats{
		Total:       stats.Total,
		ByOperation: stats.ByOperation,
	}, nil
}

// ============================================================================
// FUNNEL REPOSITORY ADAPTER
// ============================================================================

// Ensure funnelRepoAdapter implements storage.FunnelRepository.
var _ storage.FunnelRepository = (*funnelRepoAdapter)(nil)

type funnelRepoAdapter struct {
	repo *SQLiteSalesRepository
}

func (a *funnelRepoAdapter) SaveProducts(ctx context.Context, products []storage.ProductRecord) error {
	// Convert storage types to wb types
	for _, p := range products {
		product := wb.FunnelProductMeta{
			NmID:           p.NmID,
			VendorCode:     p.VendorCode,
			Title:          p.Title,
			BrandName:      p.BrandName,
			SubjectID:      p.SubjectID,
			SubjectName:    p.SubjectName,
			ProductRating:  p.ProductRating,
			StockWB:        p.StockWB,
			StockMP:        p.StockMP,
			StockBalance:   p.StockWB + p.StockMP,
		}
		if err := a.repo.SaveProduct(ctx, product); err != nil {
			return err
		}
	}
	return nil
}

func (a *funnelRepoAdapter) SaveDailyMetrics(ctx context.Context, metrics []storage.FunnelMetricRecord) error {
	// Group metrics by product for batch save
	for _, m := range metrics {
		product := wb.FunnelProductMeta{NmID: m.NmID}
		rows := []wb.FunnelHistoryRow{{
			NmID:                  m.NmID,
			MetricDate:            m.MetricDate.Format("2006-01-02"),
			OpenCount:             m.OpenCount,
			CartCount:             m.CartCount,
			OrderCount:            m.OrderCount,
			BuyoutCount:           m.BuyoutCount,
			CancelCount:           m.CancelCount,
			AddToWishlist:         m.AddToWishlist,
			OrderSum:              m.OrderSum,
			BuyoutSum:             m.BuyoutSum,
			AvgPrice:              m.AvgPrice,
			ConversionAddToCart:   m.ConversionAddToCart,
			ConversionCartToOrder: m.ConversionCartToOrder,
			ConversionBuyout:      m.ConversionBuyout,
			WBClubOrderCount:      m.WBClubOrderCount,
			WBClubBuyoutCount:     m.WBClubBuyoutCount,
			WBClubBuyoutPercent:   m.WBClubBuyoutPercent,
		}}
		if err := a.repo.SaveFunnelHistory(ctx, product, rows); err != nil {
			return err
		}
	}
	return nil
}

func (a *funnelRepoAdapter) GetProduct(ctx context.Context, nmID int) (*storage.ProductRecord, error) {
	// TODO: Implement direct query
	return nil, fmt.Errorf("not implemented")
}

func (a *funnelRepoAdapter) GetMetricsForPeriod(ctx context.Context, nmIDs []int, from, to time.Time) ([]storage.FunnelMetricRecord, error) {
	// TODO: Implement query with date range
	return nil, fmt.Errorf("not implemented")
}

func (a *funnelRepoAdapter) GetLatestMetricDate(ctx context.Context) (time.Time, error) {
	dateStr, err := a.repo.GetLastFunnelDate(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if dateStr == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", dateStr)
}

func (a *funnelRepoAdapter) Count(ctx context.Context) (int, error) {
	return a.repo.CountFunnelMetrics(ctx)
}

func (a *funnelRepoAdapter) SaveDailyMetricsWithWindow(ctx context.Context, metrics []storage.FunnelMetricRecord, refreshDays int) error {
	// Group metrics by product for batch save
	productMap := make(map[int][]wb.FunnelHistoryRow)
	for _, m := range metrics {
		productMap[m.NmID] = append(productMap[m.NmID], wb.FunnelHistoryRow{
			NmID:                  m.NmID,
			MetricDate:            m.MetricDate.Format("2006-01-02"),
			OpenCount:             m.OpenCount,
			CartCount:             m.CartCount,
			OrderCount:            m.OrderCount,
			BuyoutCount:           m.BuyoutCount,
			CancelCount:           m.CancelCount,
			AddToWishlist:         m.AddToWishlist,
			OrderSum:              m.OrderSum,
			BuyoutSum:             m.BuyoutSum,
			AvgPrice:              m.AvgPrice,
			ConversionAddToCart:   m.ConversionAddToCart,
			ConversionCartToOrder: m.ConversionCartToOrder,
			ConversionBuyout:      m.ConversionBuyout,
			WBClubOrderCount:      m.WBClubOrderCount,
			WBClubBuyoutCount:     m.WBClubBuyoutCount,
			WBClubBuyoutPercent:   m.WBClubBuyoutPercent,
		})
	}

	// Save each product's metrics
	for nmID, rows := range productMap {
		product := wb.FunnelProductMeta{NmID: nmID}
		if err := a.repo.SaveFunnelHistoryWithWindow(ctx, product, rows, refreshDays); err != nil {
			return err
		}
	}
	return nil
}

func (a *funnelRepoAdapter) RefreshWithinWindow(ctx context.Context, nmIDs []int, refreshDays int) (*storage.RefreshStats, error) {
	return a.repo.RefreshWithinWindow(ctx, nmIDs, refreshDays)
}

// ============================================================================
// PROMOTION REPOSITORY ADAPTER
// ============================================================================

// Ensure promotionRepoAdapter implements storage.PromotionRepository.
var _ storage.PromotionRepository = (*promotionRepoAdapter)(nil)

type promotionRepoAdapter struct {
	repo *SQLiteSalesRepository
}

func (a *promotionRepoAdapter) SaveCampaigns(ctx context.Context, campaigns []storage.CampaignRecord) error {
	// Convert storage types to wb types
	groups := make([]wb.PromotionAdvertGroup, 0)
	for _, c := range campaigns {
		// Create a pseudo-group for each campaign
		groups = append(groups, wb.PromotionAdvertGroup{
			Type:   c.CampaignType,
			Status: c.Status,
			AdvertList: []wb.PromotionAdvert{{
				AdvertID:   c.AdvertID,
				ChangeTime: c.ChangeTime.Format("2006-01-02 15:04:05"),
			}},
		})
	}
	return a.repo.SaveCampaigns(ctx, groups)
}

func (a *promotionRepoAdapter) SaveDailyStats(ctx context.Context, stats []storage.CampaignStatsRecord) error {
	// Convert storage types to wb types
	dailyStats := make([]wb.CampaignDailyStats, len(stats))
	for i, s := range stats {
		dailyStats[i] = wb.CampaignDailyStats{
			AdvertID:  s.AdvertID,
			StatsDate: s.StatsDate.Format("2006-01-02"),
			Views:     s.Views,
			Clicks:    s.Clicks,
			CTR:       s.CTR,
			CPC:       s.CPC,
			CR:        s.CR,
			Orders:    s.Orders,
			Shks:      s.SHKs,
			Atbs:      s.ATBs,
			Canceled:  s.Canceled,
			Sum:       s.Sum,
			SumPrice:  s.SumPrice,
		}
	}
	return a.repo.SaveCampaignStats(ctx, dailyStats)
}

func (a *promotionRepoAdapter) SaveCampaignProducts(ctx context.Context, relations []storage.CampaignProductRecord) error {
	products := make([]wb.CampaignProduct, len(relations))
	for i, r := range relations {
		products[i] = wb.CampaignProduct{
			AdvertID: r.AdvertID,
			NmID:     r.NmID,
			Name:     r.ProductName,
			Views:    r.TotalViews,
			Clicks:   r.TotalClicks,
			Orders:   r.TotalOrders,
			Sum:      r.TotalSum,
		}
	}
	return a.repo.SaveCampaignProducts(ctx, products)
}

func (a *promotionRepoAdapter) GetCampaign(ctx context.Context, advertID int) (*storage.CampaignRecord, error) {
	// TODO: Implement direct query
	return nil, fmt.Errorf("not implemented")
}

func (a *promotionRepoAdapter) GetActiveCampaigns(ctx context.Context) ([]storage.CampaignRecord, error) {
	// Status 9 = active
	ids, err := a.repo.GetCampaignIDsByStatus(ctx, []int{9})
	if err != nil {
		return nil, err
	}
	campaigns := make([]storage.CampaignRecord, len(ids))
	for i, id := range ids {
		campaigns[i] = storage.CampaignRecord{AdvertID: id}
	}
	return campaigns, nil
}

func (a *promotionRepoAdapter) GetStatsForPeriod(ctx context.Context, advertIDs []int, from, to time.Time) ([]storage.CampaignStatsRecord, error) {
	// TODO: Implement query with date range
	return nil, fmt.Errorf("not implemented")
}

func (a *promotionRepoAdapter) GetLastChangeTime(ctx context.Context) (time.Time, error) {
	// TODO: Implement
	return time.Time{}, nil
}

func (a *promotionRepoAdapter) Count(ctx context.Context) (int, error) {
	return a.repo.CountCampaigns(ctx)
}

// ============================================================================
// REGISTRATION - Factory Support
// ============================================================================

func init() {
	// Register SQLite backend in global factory
	// This is called automatically when the package is imported
}

// RegisterInFactory registers the SQLite backend creator in a factory.
// Call this during application initialization.
func RegisterInFactory(factory storage.BackendFactory) {
	factory.Register("sqlite", func(cfg storage.BackendConfig) (storage.StorageBackend, error) {
		return NewBackend(cfg.ConnectionString)
	})
}
