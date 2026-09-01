package fbsorders

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// MockSource — детерминированные данные для --mock и тестов
// ============================================================================

// MockSource generates deterministic FBS orders, statuses and feed items.
// Implements Source for --mock mode and testing.
type MockSource struct {
	mu       sync.RWMutex
	orders   []wb.FBSOrder
	statuses map[int64]wb.FBSOrderStatus
	feed     []wb.OrderFeedItem
	supplies []wb.FBSSupply
}

// NewMockSource creates a mock source with count orders (spread over the last
// 7 days), a status for each order, and feed items for every fifth order.
func NewMockSource(count int) *MockSource {
	m := &MockSource{statuses: make(map[int64]wb.FBSOrderStatus, count)}
	m.populate(count)
	return m
}

// FBSOrdersIterator calls callback once with all mock orders in the period.
func (m *MockSource) FBSOrdersIterator(
	ctx context.Context,
	from, to time.Time,
	callback func([]wb.FBSOrder) error,
) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	inPeriod := make([]wb.FBSOrder, 0, len(m.orders))
	for _, o := range m.orders {
		if t, err := time.Parse(time.RFC3339, o.CreatedAt); err == nil && !t.Before(from) && t.Before(to) {
			inPeriod = append(inPeriod, o)
		}
	}
	if len(inPeriod) == 0 {
		return 0, nil
	}
	if err := callback(inPeriod); err != nil {
		return 0, err
	}
	return len(inPeriod), nil
}

// GetFBSOrdersStatus returns mock statuses for the requested IDs.
func (m *MockSource) GetFBSOrdersStatus(_ context.Context, ids []int64) ([]wb.FBSOrderStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]wb.FBSOrderStatus, 0, len(ids))
	for _, id := range ids {
		if st, ok := m.statuses[id]; ok {
			out = append(out, st)
		}
	}
	return out, nil
}

// OrderFeedIterator calls callback once with all mock feed items.
func (m *MockSource) OrderFeedIterator(
	ctx context.Context,
	from time.Time,
	callback func([]wb.OrderFeedItem) error,
) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.feed) == 0 {
		return 0, nil
	}
	if err := callback(m.feed); err != nil {
		return 0, err
	}
	return len(m.feed), nil
}

// FBSSuppliesIterator calls callback once with all mock supplies.
func (m *MockSource) FBSSuppliesIterator(
	ctx context.Context,
	callback func([]wb.FBSSupply) error,
) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.supplies) == 0 {
		return 0, nil
	}
	if err := callback(m.supplies); err != nil {
		return 0, err
	}
	return len(m.supplies), nil
}

func (m *MockSource) populate(count int) {
	base := time.Now().UTC().Add(-7 * 24 * time.Hour)
	orders := make([]wb.FBSOrder, count)
	statuses := make(map[int64]wb.FBSOrderStatus, count)
	feed := make([]wb.OrderFeedItem, 0, count/5+1)
	supplies := make([]wb.FBSSupply, 0, 50)

	supplierStatuses := []string{"new", "confirm", "complete", "complete", "complete"}
	wbStatuses := []string{"waiting", "sorted", "sold", "ready_for_pickup", "canceled_by_client"}

	for i := 0; i < count; i++ {
		id := int64(1_000_000 + i)
		createdAt := base.Add(time.Duration(i) * time.Minute)
		supplier := supplierStatuses[i%len(supplierStatuses)]
		wbSt := wbStatuses[i%len(wbStatuses)]

		orders[i] = wb.FBSOrder{
			ID:          id,
			RID:         fmt.Sprintf("mock-rid-%d", id),
			OrderUID:    fmt.Sprintf("mock-orderuid-%d", id/3),
			CreatedAt:   createdAt.Format(time.RFC3339),
			SupplyID:    fmt.Sprintf("WB-GI-%d", 900000+i%50),
			WarehouseID: 100 + int64(i%3),
			OfficeID:    200 + int64(i%3),
			NmID:        int64(150000 + i%20),
			Article:     fmt.Sprintf("%d26%04d", i%3+1, i),
			ChrtID:      int64(300000 + i%20),
			Price:       int64(1000 + i%500),
			// 643 — ISO 4217, RUB
			CurrencyCode: 643, ConvertedCurrencyCode: 643,
			CargoType: 1, CrossBorderType: 0,
			IsZeroOrder: i%97 == 0,
			Skus:        []string{fmt.Sprintf("2000%010d", i)},
		}
		orders[i].Options.IsB2B = i%53 == 0

		statuses[id] = wb.FBSOrderStatus{
			ID:             id,
			IsCancellable:  supplier == "new",
			SupplierStatus: supplier,
			WbStatus:       wbSt,
		}

		if i%5 == 0 {
			item := wb.OrderFeedItem{
				NmID:          orders[i].NmID,
				ChrtID:        orders[i].ChrtID,
				Srid:          orders[i].RID,
				CreatedAt:     orders[i].CreatedAt,
				UpdatedAt:     createdAt.Add(36 * time.Hour).Format(time.RFC3339),
				Status:        []string{"buyout", "cancel", "created", "return", "returnDefective"}[(i/5)%5],
				WarehouseName: "Склад продавца", WarehouseRegion: "Москва и область",
				IsMp: true, DestinationCity: "Москва", DestinationDistrict: "Центральный",
				SellerPrice: float64(orders[i].Price) / 100,
			}
			if item.Status == "cancel" {
				ct := "app"
				item.CancelType = &ct
			}
			feed = append(feed, item)
		}
	}

	// Поставки: 50 штук под mock-задания; задержка приёмки (scan_dt − created_at)
	// варьируется 6..48 ч — есть и «успели в 24 часа», и «не успели».
	for i := 0; i < 50; i++ {
		created := base.Add(time.Duration(i) * 3 * time.Hour)
		scanDelay := time.Duration(6+i) * time.Hour // 6..55 ч
		sup := wb.FBSSupply{
			ID:                  fmt.Sprintf("WB-GI-%d", 900000+i),
			Name:                fmt.Sprintf("Mock-поставка %d", i),
			CargoType:           1,
			CreatedAt:           created.Format(time.RFC3339),
			DestinationOfficeID: 236,
			RecommendedWhID:     205228,
		}
		cb := int64(0)
		sup.CrossBorderType = &cb
		done := i%6 != 0 // каждая шестая ещё не закрыта
		sup.Done = done
		if done {
			closed := created.Add(scanDelay - 30*time.Minute)
			scan := created.Add(scanDelay)
			sup.ClosedAt = strPtr(closed.Format(time.RFC3339))
			sup.ScanDt = strPtr(scan.Format(time.RFC3339))
		}
		supplies = append(supplies, sup)
	}

	m.mu.Lock()
	m.orders = orders
	m.statuses = statuses
	m.feed = feed
	m.supplies = supplies
	m.mu.Unlock()
}

// strPtr — компактный хелпер для nullable-полей mock-поставок.
func strPtr(s string) *string { return &s }

// ============================================================================
// DiscardWriter — no-op для --mock (гарантированный ноль взаимодействий с БД)
// ============================================================================

// DiscardWriter implements Writer with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu            sync.Mutex
	savedOrders   int
	savedStatus   int
	savedFeed     int
	savedSupplies int
	loadedIDs     []int64
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveOrders counts orders but never writes.
func (w *DiscardWriter) SaveOrders(_ context.Context, orders []wb.FBSOrder) (int, error) {
	w.mu.Lock()
	w.savedOrders += len(orders)
	w.mu.Unlock()
	return len(orders), nil
}

// SaveStatuses counts statuses but never writes.
func (w *DiscardWriter) SaveStatuses(_ context.Context, statuses []wb.FBSOrderStatus) (int, error) {
	w.mu.Lock()
	w.savedStatus += len(statuses)
	w.mu.Unlock()
	return len(statuses), nil
}

// SaveOrderFeed counts feed rows but never writes.
func (w *DiscardWriter) SaveOrderFeed(_ context.Context, items []wb.OrderFeedItem) (int, error) {
	w.mu.Lock()
	w.savedFeed += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// SaveSupplies counts supplies but never writes.
func (w *DiscardWriter) SaveSupplies(_ context.Context, supplies []wb.FBSSupply) (int, error) {
	w.mu.Lock()
	w.savedSupplies += len(supplies)
	w.mu.Unlock()
	return len(supplies), nil
}

// LoadStatusCandidateIDs returns a small deterministic ID set so the status
// phase exercises batching logic in --mock runs too.
func (w *DiscardWriter) LoadStatusCandidateIDs(_ context.Context, _ time.Time) ([]int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadedIDs = []int64{1_000_000, 1_000_001, 1_000_002}
	return w.loadedIDs, nil
}

// Saved returns total counts of discarded records (orders, statuses, feed, supplies).
func (w *DiscardWriter) Saved() (int, int, int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedOrders, w.savedStatus, w.savedFeed, w.savedSupplies
}
