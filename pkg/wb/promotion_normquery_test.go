package wb

import (
	"encoding/json"
	"testing"
)

// TestNormqueryBidItemUnits — семантика единиц ставки нормграфика (аудит
// dev_wb_promo_fixes.md §2): bid — БАЗОВЫЕ единицы (рубли, integer, усечённо),
// bid_kopecks — разменные (0,01), канон без потерь; currency обязателен.
// Пример полей — V0GetNormQueryBidsItem, 08-promotion.yaml:5494+.
func TestNormqueryBidItemUnits(t *testing.T) {
	raw := `{"bids":[
		{"advert_id":123,"nm_id":456,"norm_query":"футболка поло","bid":5,"bid_kopecks":550,"currency":"RUB"},
		{"advert_id":123,"nm_id":456,"norm_query":"поло мужское","bid":0,"bid_kopecks":50,"currency":"RUB"}
	]}`
	var resp NormqueryBidsResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Bids) != 2 {
		t.Fatalf("bids = %d, want 2", len(resp.Bids))
	}
	b := resp.Bids[0]
	if b.Bid != 5 || b.BidKopecks != 550 || b.Currency != "RUB" {
		t.Errorf("bids[0] = %+v, want bid=5 (рубли), bid_kopecks=550, currency=RUB", b)
	}
	// Кросс-валидация канона: bid — УСЕЧЁННЫЕ рубли, т.е. bid = floor(kopecks/100)
	// (5,50 ₽ → bid=5, bid_kopecks=550: расхождение до 99 коп., не ±1).
	if lo, hi := b.Bid*100, (b.Bid+1)*100; b.BidKopecks < lo || b.BidKopecks >= hi {
		t.Errorf("bids[0]: bid_kopecks=%d вне [bid*100, (bid+1)*100)=[%d,%d) — проверить единицы", b.BidKopecks, lo, hi)
	}
	// Дробный рубль: bid не может передать 0,50 ₽ — только канон в копейках.
	if resp.Bids[1].Bid != 0 || resp.Bids[1].BidKopecks != 50 {
		t.Errorf("bids[1] = %+v, want bid=0, bid_kopecks=50 (дробный рубль живёт только в копейках)", resp.Bids[1])
	}
}

// TestNormqueryClustersArchived — архивные кластеры присутствуют в ответе list
// (пример — 08-promotion.yaml:4310-4319) и больше не теряются при маппинге.
func TestNormqueryClustersArchived(t *testing.T) {
	raw := `{"items":[{"advertId":1,"nmId":2,"normQueries":{
		"active":null,
		"excluded":["футболка поло"],
		"archived":["поло мужское","поло мужское летнее"]
	}}]}`
	var resp NormqueryListResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c := resp.Items[0].NormQueries
	if len(c.Excluded) != 1 || c.Excluded[0] != "футболка поло" {
		t.Errorf("excluded = %v, want [футболка поло]", c.Excluded)
	}
	if len(c.Archived) != 2 {
		t.Fatalf("archived = %v, want 2 кластера (до фикса терялись)", c.Archived)
	}
	if c.Archived[1] != "поло мужское летнее" {
		t.Errorf("archived[1] = %q, want %q", c.Archived[1], "поло мужское летнее")
	}
}

// TestNormqueryListRequestCamelCase — тело list-запроса обязано сериализоваться
// camelCase-ключами (спека 08-promotion.yaml:5071): snake_case-ключи общего
// NormqueryItem могли молча терять кластеры (аудит §3).
func TestNormqueryListRequestCamelCase(t *testing.T) {
	body, err := json.Marshal(NormqueryListRequest{Items: []NormqueryListRequestItem{{AdvertID: 7, NmID: 8}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"items":[{"advertId":7,"nmId":8}]}`
	if string(body) != want {
		t.Errorf("body = %s, want %s (camelCase)", body, want)
	}
}
