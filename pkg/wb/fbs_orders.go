// Package wb: FBS assembly tasks API (Marketplace API v3) + Order Feed (Seller Analytics).
//
// Sources (local swagger):
//   - GET  /api/v3/orders         — docs/wb_api_swagger/03-orders-fbs.yaml:463
//   - POST /api/v3/orders/status  — docs/wb_api_swagger/03-orders-fbs.yaml:548
//   - POST /api/analytics/v1/order-feed — docs/wb_api_swagger/11-analytics.yaml:1682
//
// Rate limits (swagger, per seller account):
//   - v3 orders/status: 300 req/min, 200 ms interval, burst 20
//   - order-feed: 1 req/min (basic token: 2 req/24h)
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const (
	// FBSMarketplaceBaseURL — базовый URL Marketplace API (сборочные задания FBS).
	FBSMarketplaceBaseURL = "https://marketplace-api.wildberries.ru"

	// OrderFeedBaseURL — базовый URL Seller Analytics API (лента заказов).
	OrderFeedBaseURL = "https://seller-analytics-api.wildberries.ru"
)

// ToolID для rate limiting. Должны совпадать между SetRateLimit и запросами.
const (
	FBSOrdersToolID = "wb_fbs_orders"
	FBSStatusToolID = "wb_fbs_status"
	OrderFeedToolID = "wb_order_feed"
)

// FBSOrdersMaxWindow — максимум 30 календарных дней одним запросом (swagger).
const FBSOrdersMaxWindow = 30 * 24 * time.Hour

// ============================================================================
// Типы — GET /api/v3/orders (03-orders-fbs.yaml:4329, схема Order)
// ============================================================================

// FBSOrder — одно сборочное задание FBS (1 строка = 1 единица товара).
// rid == srid в Statistics API (orders/sales) и finance detailed — ключ связки.
type FBSOrder struct {
	ID          int64  `json:"id"`          // ID сборочного задания
	RID         string `json:"rid"`         // == srid в orders/sales/finance
	OrderUID    string `json:"orderUid"`    // ID транзакции (группирует задания одной корзины)
	CreatedAt   string `json:"createdAt"`   // RFC3339, UTC
	SupplyID    string `json:"supplyId"`    // ID поставки (WB-GI-...)
	WarehouseID int64  `json:"warehouseId"` // ID склада продавца
	OfficeID    int64  `json:"officeId"`    // ID склада WB, к которому привязан склад продавца
	NmID        int64  `json:"nmId"`
	Article     string `json:"article"` // артикул продавца
	ChrtID      int64  `json:"chrtId"`  // ID размера

	// Цены — в копейках, ×100 (swagger: price, convertedPrice).
	Price          int64 `json:"price"`          // в валюте продажи
	ConvertedPrice int64 `json:"convertedPrice"` // в валюте страны продавца
	// ISO 4217 числовой код валюты (933 = BYN, 643 = RUB) — INTEGER, не строка.
	CurrencyCode          int `json:"currencyCode"`
	ConvertedCurrencyCode int `json:"convertedCurrencyCode"`

	CargoType       int      `json:"cargoType"`       // 1 МГТ / 2 СГТ / 3 КГТ+
	CrossBorderType int      `json:"crossBorderType"` // 0 внутренняя / 1 трансграничная
	ScanPrice       int64    `json:"scanPrice"`       // цена приёмки в копейках (nullable → 0)
	Comment         string   `json:"comment"`         // комментарий покупателя (≤300, не храним)
	IsZeroOrder     bool     `json:"isZeroOrder"`     // заказ товара с нулевым остатком
	Skus            []string `json:"skus"`            // баркоды; Skus[0] → barcode
	Options         struct {
		IsB2B bool `json:"isB2B"`
	} `json:"options"`
}

// Barcode возвращает первый баркод из skus (для связки с supply_goods.barcode).
func (o FBSOrder) Barcode() string {
	if len(o.Skus) > 0 {
		return o.Skus[0]
	}
	return ""
}

type fbsOrdersResponse struct {
	Next   int64      `json:"next"` // курсор; 0 = страниц больше нет
	Orders []FBSOrder `json:"orders"`
}

// FBSOrdersPageResult — результат одной страницы /api/v3/orders.
type FBSOrdersPageResult struct {
	Orders []FBSOrder
	Next   int64 // курсор для следующей страницы (0 = конец)
}

// FBSOrdersPage получает одну страницу сборочных заданий.
// dateFrom/dateTo — период создания заданий (UTC), окно ≤ 30 дней.
func (c *Client) FBSOrdersPage(
	ctx context.Context,
	rateLimit, burst int,
	dateFrom, dateTo time.Time,
	next int64,
) (*FBSOrdersPageResult, error) {
	params := url.Values{}
	params.Set("dateFrom", strconv.FormatInt(dateFrom.Unix(), 10))
	params.Set("dateTo", strconv.FormatInt(dateTo.Unix(), 10))
	params.Set("limit", "1000") // максимум по swagger
	params.Set("next", strconv.FormatInt(next, 10))

	var resp fbsOrdersResponse
	err := c.Get(ctx, FBSOrdersToolID, FBSMarketplaceBaseURL, rateLimit, burst, "/api/v3/orders", params, &resp)
	if err != nil {
		if IsNoContent(err) {
			return &FBSOrdersPageResult{}, nil
		}
		return nil, fmt.Errorf("fbs orders page (next=%d): %w", next, err)
	}
	return &FBSOrdersPageResult{Orders: resp.Orders, Next: resp.Next}, nil
}

// FBSOrdersIterator итерирует по всем страницам /api/v3/orders за период [from, to].
// Период автоматически режется на окна ≤ 30 дней (лимит API), внутри окна —
// курсор next (0 в первом запросе, цикл пока ответный next ≠ 0).
// Callback вызывается на каждую страницу; ошибка callback прерывает итерацию.
func (c *Client) FBSOrdersIterator(
	ctx context.Context,
	rateLimit, burst int,
	from, to time.Time,
	callback func([]FBSOrder) error,
) (int, error) {
	if !to.After(from) {
		return 0, fmt.Errorf("fbs orders: empty period (from=%s to=%s)", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	const maxRetries = 3
	const baseBackoff = 5 * time.Second

	total := 0
	for winStart := from; winStart.Before(to); {
		winEnd := winStart.Add(FBSOrdersMaxWindow)
		if winEnd.After(to) {
			winEnd = to
		}

		next := int64(0)
		for {
			var page *FBSOrdersPageResult
			var err error

			for attempt := 0; attempt < maxRetries; attempt++ {
				page, err = c.FBSOrdersPage(ctx, rateLimit, burst, winStart, winEnd, next)
				if err == nil {
					break
				}
				if !isRetryableError(err) || attempt == maxRetries-1 {
					return total, fmt.Errorf("fbs orders window [%s..%s]: %w",
						winStart.Format("2006-01-02"), winEnd.Format("2006-01-02"), err)
				}
				backoff := baseBackoff * time.Duration(1<<attempt)
				select {
				case <-ctx.Done():
					return total, ctx.Err()
				case <-time.After(backoff):
				}
			}

			if len(page.Orders) > 0 {
				if err := callback(page.Orders); err != nil {
					return total, err
				}
				total += len(page.Orders)
			}

			if page.Next == 0 {
				break
			}
			if page.Next == next && len(page.Orders) == 0 {
				// Зацикленный курсор — защита от бесконечного цикла.
				return total, fmt.Errorf("fbs orders: cursor stuck at %d in window [%s..%s]",
					next, winStart.Format("2006-01-02"), winEnd.Format("2006-01-02"))
			}
			next = page.Next
		}

		winStart = winEnd
	}
	return total, nil
}

// ============================================================================
// Типы — POST /api/v3/orders/status (03-orders-fbs.yaml:548)
// ============================================================================

// FBSOrderStatus — текущий статус одного сборочного задания.
type FBSOrderStatus struct {
	ID             int64  `json:"id"`
	IsCancellable  bool   `json:"isCancellable"`
	SupplierStatus string `json:"supplierStatus"` // new / confirm / complete / cancel / cancel_carrier
	WbStatus       string `json:"wbStatus"`       // waiting / sorted / sold / canceled / ...
}

type fbsOrdersStatusRequest struct {
	Orders []int64 `json:"orders"`
}

type fbsOrdersStatusResponse struct {
	Orders []FBSOrderStatus `json:"orders"`
}

// GetFBSOrdersStatus получает текущие статусы по ID заданий (батч ≤ 1000).
func (c *Client) GetFBSOrdersStatus(
	ctx context.Context,
	rateLimit, burst int,
	ids []int64,
) ([]FBSOrderStatus, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 1000 {
		return nil, fmt.Errorf("fbs status: batch too large (%d > 1000)", len(ids))
	}

	var resp fbsOrdersStatusResponse
	err := c.Post(ctx, FBSStatusToolID, FBSMarketplaceBaseURL, rateLimit, burst,
		"/api/v3/orders/status", fbsOrdersStatusRequest{Orders: ids}, &resp)
	if err != nil {
		return nil, fmt.Errorf("fbs status (batch %d): %w", len(ids), err)
	}
	return resp.Orders, nil
}

// ============================================================================
// Типы — POST /api/analytics/v1/order-feed (11-analytics.yaml:1682)
// ============================================================================

// OrderFeedItem — строка ленты заказов (1 заказ = 1 единица товара).
// srid == rid сборочного задания FBS. Период фильтруется по дате ТЕКУЩЕГО статуса
// (updatedAt), окно ≤ 31 сутки. Единственный источник cancelType и географии доставки.
type OrderFeedItem struct {
	NmID                int64   `json:"nmId"`
	ChrtID              int64   `json:"chrtId"`
	Srid                string  `json:"srid"`
	CreatedAt           string  `json:"createdAt"`
	UpdatedAt           string  `json:"updatedAt"`  // дата и время текущего статуса
	Status              string  `json:"status"`     // created / buyout / cancel / return / returnDefective
	CancelType          *string `json:"cancelType"` // app / receipt / expire / other (только при cancel)
	WarehouseName       string  `json:"warehouseName"`
	WarehouseRegion     string  `json:"warehouseRegion"`
	IsMp                bool    `json:"isMp"` // true = склад продавца (FBS/DBS)
	DestinationCity     string  `json:"destinationCity"`
	DestinationDistrict string  `json:"destinationDistrict"`
	SellerPrice         float64 `json:"sellerPrice"`
	IsB2b               bool    `json:"isB2b"`
}

// OrderFeedMaxWindow — лента отдаёт максимум 31 сутки от текущей даты (swagger).
const OrderFeedMaxWindow = 31 * 24 * time.Hour

type orderFeedPeriod struct {
	Start string `json:"start"`
	End   string `json:"end,omitempty"`
}

type orderFeedPagination struct {
	SnapshotTime string `json:"snapshotTime,omitempty"`
	Offset       int    `json:"offset"`
	Limit        int    `json:"limit"`
}

type orderFeedRequest struct {
	SelectedPeriod orderFeedPeriod      `json:"selectedPeriod"`
	Pagination     *orderFeedPagination `json:"pagination,omitempty"`
}

type orderFeedResponse struct {
	SnapshotTime string          `json:"snapshotTime"`
	Currency     string          `json:"currency"`
	Orders       []OrderFeedItem `json:"orders"`
}

// OrderFeedIterator итерирует по ленте заказов за период [from, now]
// (по дате текущего статуса). Пагинация offset + snapshotTime из ПЕРВОГО ответа
// (swagger: запросы одной выборки — с одним snapshotTime, иначе пропуски/дубли).
// Конец пагинации: страница короче limit. from клампится к 31 суткам назад.
func (c *Client) OrderFeedIterator(
	ctx context.Context,
	rateLimit, burst int,
	from time.Time,
	callback func([]OrderFeedItem) error,
) (int, error) {
	now := time.Now().UTC()
	oldest := now.Add(-OrderFeedMaxWindow).Add(time.Hour) // +1 час запаса от граничного 31 суток
	if from.Before(oldest) {
		from = oldest
	}

	const limit = 10000 // максимум по swagger
	req := orderFeedRequest{
		SelectedPeriod: orderFeedPeriod{
			Start: from.Format(time.RFC3339),
			End:   now.Format(time.RFC3339),
		},
	}

	total := 0
	offset := 0
	snapshotTime := ""

	for {
		pg := orderFeedPagination{Offset: offset, Limit: limit}
		if snapshotTime != "" {
			pg.SnapshotTime = snapshotTime
		}
		req.Pagination = &pg

		var resp orderFeedResponse
		if err := c.Post(ctx, OrderFeedToolID, OrderFeedBaseURL, rateLimit, burst,
			"/api/analytics/v1/order-feed", req, &resp); err != nil {
			return total, fmt.Errorf("order-feed page (offset=%d): %w", offset, err)
		}

		// snapshotTime фиксируем только из первого ответа выборки.
		if snapshotTime == "" {
			snapshotTime = resp.SnapshotTime
		}

		if len(resp.Orders) > 0 {
			if err := callback(resp.Orders); err != nil {
				return total, err
			}
			total += len(resp.Orders)
		}

		if len(resp.Orders) < limit {
			return total, nil
		}
		offset += len(resp.Orders)
	}
}
