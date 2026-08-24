package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Фикстуры собраны по схемам локального сваггера:
//   - Order:        docs/wb_api_swagger/03-orders-fbs.yaml:4329
//   - status items: docs/wb_api_swagger/03-orders-fbs.yaml:626
//   - Order: (feed) docs/wb_api_swagger/11-analytics.yaml:6370
func TestFBSOrderUnmarshal(t *testing.T) {
	raw := `{
		"id": 1383371198765432101,
		"rid": "f884001e44e511edb8780242ac120002",
		"orderUid": "165918930_629fbc924b984618a44354475ca58675",
		"createdAt": "2026-07-24T12:44:53Z",
		"supplyId": "WB-GI-92937123",
		"warehouseId": 658434,
		"officeId": 123,
		"nmId": 12345678,
		"article": "12500025",
		"chrtId": 987654321,
		"price": 1014,
		"convertedPrice": 28322,
		"currencyCode": 933,
		"convertedCurrencyCode": 643,
		"cargoType": 1,
		"crossBorderType": 0,
		"scanPrice": null,
		"comment": "",
		"isZeroOrder": false,
		"skus": ["6665956397512"],
		"options": {"isB2B": false}
	}`

	var o FBSOrder
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.ID != 1383371198765432101 {
		t.Errorf("ID = %d, want 1383371198765432101 (int64 precision)", o.ID)
	}
	if o.RID != "f884001e44e511edb8780242ac120002" {
		t.Errorf("RID = %q", o.RID)
	}
	if o.CreatedAt != "2026-07-24T12:44:53Z" {
		t.Errorf("CreatedAt = %q", o.CreatedAt)
	}
	if o.CurrencyCode != 933 || o.ConvertedCurrencyCode != 643 {
		t.Errorf("currency codes = %d/%d, want 933/643", o.CurrencyCode, o.ConvertedCurrencyCode)
	}
	if o.ScanPrice != 0 {
		t.Errorf("ScanPrice(null) = %d, want 0", o.ScanPrice)
	}
	if o.Barcode() != "6665956397512" {
		t.Errorf("Barcode() = %q, want первый sku", o.Barcode())
	}
	if o.Options.IsB2B {
		t.Errorf("Options.IsB2B = true, want false")
	}
}

func TestFBSOrdersResponseNextCursorPrecision(t *testing.T) {
	// next — 19-значный курсор; в bash/jq он терял точность во float,
	// Go json → int64 декодирует точно.
	raw := `{"next": 9215087736486123456, "orders": []}`
	var resp fbsOrdersResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Next != 9215087736486123456 {
		t.Errorf("Next = %d, want 9215087736486123456", resp.Next)
	}
	if len(resp.Orders) != 0 {
		t.Errorf("Orders = %d, want 0", len(resp.Orders))
	}
}

func TestFBSOrderStatusUnmarshal(t *testing.T) {
	raw := `{"orders": [
		{"id": 5632423, "isCancellable": true, "supplierStatus": "complete", "wbStatus": "ready_for_pickup"},
		{"id": 5632499, "isCancellable": false, "supplierStatus": "new", "wbStatus": "declined_by_client"}
	]}`
	var resp fbsOrdersStatusResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Orders) != 2 {
		t.Fatalf("orders = %d, want 2", len(resp.Orders))
	}
	if resp.Orders[0].SupplierStatus != "complete" || resp.Orders[0].WbStatus != "ready_for_pickup" {
		t.Errorf("order[0] = %+v", resp.Orders[0])
	}
	if !resp.Orders[0].IsCancellable || resp.Orders[1].IsCancellable {
		t.Errorf("IsCancellable = %v/%v, want true/false", resp.Orders[0].IsCancellable, resp.Orders[1].IsCancellable)
	}
}

func TestOrderFeedItemUnmarshal(t *testing.T) {
	t.Run("cancel with cancelType", func(t *testing.T) {
		raw := `{
			"nmId": 47254354, "chrtId": 91663228,
			"srid": "7513432034713632943.1.0",
			"createdAt": "2026-06-24T12:57:26+03:00",
			"updatedAt": "2026-06-26T19:19:38+03:00",
			"status": "cancel", "cancelType": "app",
			"warehouseName": "Склад продавца", "warehouseRegion": "Москва и область",
			"isMp": true,
			"destinationCity": "Санкт-Петербург", "destinationDistrict": "Северо-Западный",
			"sellerPrice": 4328, "isB2b": false
		}`
		var it OrderFeedItem
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if it.Status != "cancel" || it.CancelType == nil || *it.CancelType != "app" {
			t.Errorf("status/cancelType = %q/%v", it.Status, it.CancelType)
		}
		if !it.IsMp {
			t.Errorf("IsMp = false, want true (склад продавца)")
		}
		if it.SellerPrice != 4328 {
			t.Errorf("SellerPrice = %v", it.SellerPrice)
		}
	})
	t.Run("buyout without cancelType", func(t *testing.T) {
		raw := `{
			"nmId": 47254354, "chrtId": 91663228,
			"srid": "7513432034713632944.1.0",
			"createdAt": "2026-06-24T12:57:26+03:00",
			"updatedAt": "2026-06-27T10:00:00+03:00",
			"status": "buyout", "cancelType": null,
			"warehouseName": "Склад продавца", "warehouseRegion": "",
			"isMp": true,
			"destinationCity": "Казань", "destinationDistrict": "Приволжский",
			"sellerPrice": 4100.5, "isB2b": false
		}`
		var it OrderFeedItem
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if it.CancelType != nil {
			t.Errorf("CancelType = %q, want nil", *it.CancelType)
		}
		if it.SellerPrice != 4100.5 {
			t.Errorf("SellerPrice = %v", it.SellerPrice)
		}
	})
}

// ============================================================================
// Итератор /api/v3/orders: разбиение на окна ≤ 30 дней + курсор next
// ============================================================================

func TestFBSOrdersIterator_WindowsAndCursor(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(45 * 24 * time.Hour) // 45 дней → 2 окна: 30 + 15

	order := func(id int64) string {
		return fmt.Sprintf(`{"id":%d,"rid":"r-%d","createdAt":"2026-07-02T00:00:00Z"}`, id, id)
	}

	// Высокие лимиты — limiter.Wait мгновенный.
	mock := &mockHTTPClient{responses: []*mockResponse{
		// Окно 1 (07-01..07-31): две страницы по курсору.
		{status: 200, body: `{"next": 111, "orders": [` + order(1) + `,` + order(2) + `]}`},
		{status: 200, body: `{"next": 0, "orders": [` + order(3) + `]}`},
		// Окно 2 (07-31..08-15): одна страница.
		{status: 200, body: `{"next": 0, "orders": [` + order(4) + `]}`},
	}}
	c := New("test-key")
	c.SetHTTPClient(mock)

	var pages [][]FBSOrder
	total, err := c.FBSOrdersIterator(context.Background(), 60000, 100, from, to, func(page []FBSOrder) error {
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		t.Fatalf("iterator: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if len(pages) != 3 {
		t.Fatalf("pages = %d, want 3 (2 в окне 1 + 1 в окне 2)", len(pages))
	}

	// Проверяем параметры запросов: окна и курсор.
	reqs := mock.requests
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	win1End := from.Add(FBSOrdersMaxWindow).Unix()
	win2End := to.Unix()

	q1 := reqs[0].URL.Query()
	if q1.Get("dateFrom") != strconv.FormatInt(from.Unix(), 10) ||
		q1.Get("dateTo") != strconv.FormatInt(win1End, 10) || q1.Get("next") != "0" {
		t.Errorf("req1 params = %v", q1)
	}
	q2 := reqs[1].URL.Query()
	if q2.Get("next") != "111" || q2.Get("dateTo") != strconv.FormatInt(win1End, 10) {
		t.Errorf("req2 must continue window 1 with cursor 111, got %v", q2)
	}
	q3 := reqs[2].URL.Query()
	if q3.Get("dateFrom") != strconv.FormatInt(win1End, 10) ||
		q3.Get("dateTo") != strconv.FormatInt(win2End, 10) || q3.Get("next") != "0" {
		t.Errorf("req3 params = %v (window 2)", q3)
	}
}

func TestFBSOrdersIterator_StuckCursor(t *testing.T) {
	from := time.Now().UTC().Add(-2 * 24 * time.Hour)
	mock := &mockHTTPClient{responses: []*mockResponse{
		{status: 200, body: `{"next": 555, "orders": []}`}, // next ≠ 0, но пусто
		{status: 200, body: `{"next": 555, "orders": []}`}, // курсор не двигается
	}}
	c := New("test-key")
	c.SetHTTPClient(mock)

	_, err := c.FBSOrdersIterator(context.Background(), 60000, 100, from, time.Now().UTC(), func([]FBSOrder) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cursor stuck") {
		t.Errorf("expected stuck-cursor error, got %v", err)
	}
}

// ============================================================================
// Лента заказов: snapshotTime фиксируется из первого ответа, период клампится
// ============================================================================

func TestOrderFeedIterator_SnapshotAndClamp(t *testing.T) {
	mock := &mockHTTPClient{responses: []*mockResponse{
		{status: 200, body: `{"snapshotTime": "2026-08-20T10:00:00Z", "currency": "RUB", "orders": [
			{"nmId": 1, "chrtId": 2, "srid": "s1", "createdAt": "2026-08-19T10:00:00Z",
			 "updatedAt": "2026-08-20T09:00:00Z", "status": "buyout", "cancelType": null,
			 "warehouseName": "Склад продавца", "warehouseRegion": "", "isMp": true,
			 "destinationCity": "Москва", "destinationDistrict": "Центральный",
			 "sellerPrice": 100, "isB2b": false}
		]}`},
	}}
	c := New("test-key")
	c.SetHTTPClient(mock)

	// from = 60 суток назад → кламп к 31 суткам.
	var bodies []OrderFeedItem
	total, err := c.OrderFeedIterator(context.Background(), 60000, 100,
		time.Now().UTC().Add(-60*24*time.Hour), func(page []OrderFeedItem) error {
			bodies = append(bodies, page...)
			return nil
		})
	if err != nil {
		t.Fatalf("iterator: %v", err)
	}
	if total != 1 || len(bodies) != 1 || bodies[0].Srid != "s1" {
		t.Fatalf("total=%d items=%d", total, len(bodies))
	}

	// Тело запроса: offset=0, без snapshotTime, start не старше ~31 суток.
	body := mock.requests[0].Body
	raw, _ := io.ReadAll(body)
	var req orderFeedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if req.Pagination == nil || req.Pagination.Offset != 0 || req.Pagination.SnapshotTime != "" {
		t.Errorf("first-page pagination = %+v, want offset=0 without snapshotTime", req.Pagination)
	}
	start, err := time.Parse(time.RFC3339, req.SelectedPeriod.Start)
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	if age := time.Since(start); age > 32*24*time.Hour {
		t.Errorf("start age = %v, want ≤ 31 суток (клампинг не сработал)", age)
	}
}
