// Package wb: FBS supplies API (Marketplace API v3).
//
// Source (local swagger):
//   - GET /api/v3/supplies — docs/wb_api_swagger/03-orders-fbs.yaml:2089
//     (схема Supply — 03-orders-fbs.yaml:4518; limit/next обязательны, 3610/3618)
//
// Rate limits (swagger): общий бакет методов сборочных заданий и поставок FBS —
// 300 req/min, burst 20.
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// FBSSuppliesToolID — ToolID для rate limiting GET /api/v3/supplies.
const FBSSuppliesToolID = "wb_fbs_supplies"

// FBSSupply — одна поставка FBS.
//
// Сканирование (ScanDt) — момент приёмки поставки на СЦ: «дата сканирования
// поставки или первого заказа» (swagger 03-orders-fbs.yaml:4561). В живых
// данных ClosedAt на ~0.5–1 ч РАНЬШЕ ScanDt: «закрытие» = передача поставки
// в доставку, физическая приёмка на СЦ наступает позже — поэтому за дату
// приёмки отвечает ScanDt, а не ClosedAt.
type FBSSupply struct {
	ID        string `json:"id"` // WB-GI-...
	Name      string `json:"name"`
	Done      bool   `json:"done"`      // закрыта (передана в доставку)
	CargoType int    `json:"cargoType"` // 1 МГТ / 2 СГТ / 3 КГТ+

	CreatedAt string  `json:"createdAt"` // RFC3339, UTC
	ClosedAt  *string `json:"closedAt"`  // nullable
	ScanDt    *string `json:"scanDt"`    // nullable до приёмки на СЦ
	RejectDt  *string `json:"rejectDt"`  // nullable, отказ приёмки

	CrossBorderType     *int64 `json:"crossBorderType"`     // 0 внутренняя / 1 трансграничная; null = нет заданий
	IsB2b               *bool  `json:"isB2b"`               // null = задания не добавлены
	DestinationOfficeID int64  `json:"destinationOfficeId"` // СЦ приёмки
	RecommendedWhID     int64  `json:"recommendedWhId"`

	IsPickupPointShipmentAllowed bool `json:"isPickupPointShipmentAllowed"`
}

type fbsSuppliesResponse struct {
	Next     int64       `json:"next"` // курсор; 0 = страниц больше нет
	Supplies []FBSSupply `json:"supplies"`
}

// FBSSuppliesIterator итерирует по ВСЕМУ списку поставок (limit=1000, курсор
// next). Список маленький (сотни строк), качается целиком каждый прогон —
// upsert по supply_id обновляет scan_dt/closed_at у ранее открытых поставок.
func (c *Client) FBSSuppliesIterator(
	ctx context.Context,
	rateLimit, burst int,
	callback func([]FBSSupply) error,
) (int, error) {
	total := 0
	next := int64(0)
	for {
		params := url.Values{}
		params.Set("limit", "1000")
		params.Set("next", strconv.FormatInt(next, 10))

		var resp fbsSuppliesResponse
		if err := c.Get(ctx, FBSSuppliesToolID, FBSMarketplaceBaseURL, rateLimit, burst,
			"/api/v3/supplies", params, &resp); err != nil {
			return total, fmt.Errorf("fbs supplies page (next=%d): %w", next, err)
		}

		if len(resp.Supplies) > 0 {
			if err := callback(resp.Supplies); err != nil {
				return total, err
			}
			total += len(resp.Supplies)
		}

		if resp.Next == 0 {
			return total, nil
		}
		if resp.Next == next {
			return total, fmt.Errorf("fbs supplies: cursor stuck at %d", next)
		}
		next = resp.Next
	}
}
