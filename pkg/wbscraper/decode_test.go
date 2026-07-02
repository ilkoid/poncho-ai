package wbscraper

import (
	"encoding/json"
	"testing"
)

// raw is a test helper: build an Intercept with the given kind/url/body.
func raw(kind, url string, body string) Intercept {
	return Intercept{Kind: kind, URL: url, QueryID: 7, Status: 200, Body: json.RawMessage(body)}
}

const ts = SnapshotTs("2026-07-01T10:00:00Z")

// TestDecodeSearch_PositionAndKopecks verifies the global rank formula
// (page-1)*100 + idx + 1 and that kopeck prices survive verbatim. Two products on
// page 2 → positions 101 and 102.
func TestDecodeSearch_PositionAndKopecks(t *testing.T) {
	d, err := Decode(raw("search",
		"https://w.ru/s?search=x&page=2&dest=8038", `{
		"products": [
		  {"id":111,"sizes":[{"price":{"basic":100000,"product":89900}}]},
		  {"id":222,"sizes":[{"price":{"basic":50000,"product":45000}}]}
		]}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.SearchPositions) != 2 {
		t.Fatalf("positions = %d, want 2", len(d.SearchPositions))
	}
	want := []struct {
		pos         int
		nm          int64
		basic, prod int64
		region      *int
	}{
		{101, 111, 100000, 89900, intPtr(8038)},
		{102, 222, 50000, 45000, intPtr(8038)},
	}
	for i, w := range want {
		r := d.SearchPositions[i]
		if r.Position != w.pos {
			t.Errorf("row %d position = %d, want %d", i, r.Position, w.pos)
		}
		if r.NmID != w.nm || r.PriceBasic != w.basic || r.PriceProduct != w.prod {
			t.Errorf("row %d = nm%d basic%d prod%d, want nm%d basic%d prod%d",
				i, r.NmID, r.PriceBasic, r.PriceProduct, w.nm, w.basic, w.prod)
		}
		if r.RegionDest == nil || *r.RegionDest != 8038 {
			t.Errorf("row %d region_dest = %v, want 8038", i, r.RegionDest)
		}
		if r.Page != 2 {
			t.Errorf("row %d page = %d, want 2", i, r.Page)
		}
	}
}

// TestDecodeSearch_AdVsOrganic verifies panel_promo_id null → organic (nil) and
// non-null → ad (value carried through).
func TestDecodeSearch_AdVsOrganic(t *testing.T) {
	d, err := Decode(raw("search", "https://w.ru/s?page=1", `{
		"products": [
		  {"id":1,"panelPromoId":null},
		  {"id":2,"panelPromoId":555}
		]}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if d.SearchPositions[0].PanelPromoID != nil {
		t.Errorf("organic PanelPromoID = %v, want nil", d.SearchPositions[0].PanelPromoID)
	}
	if d.SearchPositions[1].PanelPromoID == nil || *d.SearchPositions[1].PanelPromoID != 555 {
		t.Errorf("ad PanelPromoID = %v, want 555", d.SearchPositions[1].PanelPromoID)
	}
}

// TestDecodeSearch_ResultsetFilters verifies resultset=filters (products null) is
// skipped entirely — no positions.
func TestDecodeSearch_ResultsetFilters(t *testing.T) {
	d, err := Decode(raw("search", "https://w.ru/s?page=1", `{"resultset":"filters","products":null}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.SearchPositions) != 0 {
		t.Errorf("positions = %d, want 0 for resultset=filters", len(d.SearchPositions))
	}
}

// TestDecodeSearch_QueryIDPropagated verifies the Intercept's QueryID is stamped on
// every produced row (provenance binding).
func TestDecodeSearch_QueryIDPropagated(t *testing.T) {
	d, err := Decode(Intercept{
		Kind: "search", URL: "https://w.ru/s?page=1", QueryID: 42, Status: 200,
		Body: json.RawMessage(`{"products":[{"id":1},{"id":2}]}`),
	}, ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, r := range d.SearchPositions {
		if r.QueryID != 42 {
			t.Errorf("row %d QueryID = %d, want 42", i, r.QueryID)
		}
	}
}

// TestDecodeCard_List verifies /list produces cards + per-size prices, but no
// details or stocks (those are /detail-exclusive).
func TestDecodeCard_List(t *testing.T) {
	d, err := Decode(raw("card_list", "https://w.ru/l", `{
		"products": [{
		  "id":111,"brand":"Nike","supplier":"ООО Рога","supplierId":900,"rating":4.5,"feedbacks":10,
		  "pics":["a.jpg","b.jpg"],"colors":[{"name":"черный"}],"subjectId":81,
		  "sizes":[{"name":"42","price":{"basic":100000,"product":89900}},
		           {"name":"43","price":{"basic":110000,"product":99000}}]
		}]}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.CompetitorCards) != 1 {
		t.Fatalf("cards = %d, want 1", len(d.CompetitorCards))
	}
	c := d.CompetitorCards[0]
	if c.NmID != 111 || c.Brand != "Nike" || c.Supplier != "ООО Рога" || c.Pics != 2 || c.Colors != "черный" {
		t.Errorf("card = %+v", c)
	}
	if c.QueryID != 7 {
		t.Errorf("card QueryID = %d, want 7", c.QueryID)
	}
	if len(d.CompetitorCardPrices) != 2 {
		t.Fatalf("prices = %d, want 2 (one per size)", len(d.CompetitorCardPrices))
	}
	if d.CompetitorCardPrices[0].PriceProduct != 89900 || d.CompetitorCardPrices[1].PriceProduct != 99000 {
		t.Errorf("prices product = %d/%d", d.CompetitorCardPrices[0].PriceProduct, d.CompetitorCardPrices[1].PriceProduct)
	}
	if len(d.CompetitorCardDetails) != 0 || len(d.CompetitorCardStocks) != 0 {
		t.Errorf("card_list must not produce details/stocks: %d/%d", len(d.CompetitorCardDetails), len(d.CompetitorCardStocks))
	}
}

// TestDecodeCard_Detail verifies /detail adds the aggregate details and per-wh
// stocks on top of cards + prices.
func TestDecodeCard_Detail(t *testing.T) {
	d, err := Decode(raw("card_detail", "https://w.ru/d", `{
		"products": [{
		  "id":111,"brand":"Nike","totalQuantity":250,"promotions":[{"name":"Скидка"}],
		  "sizes":[{"name":"42","price":{"basic":100000,"product":89900},
		            "stocks":[{"wh":507,"qty":10,"time1":1720000000,"time2":1720003600}]}]
		}]}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.CompetitorCards) != 1 || len(d.CompetitorCardPrices) != 1 {
		t.Fatalf("cards/prices = %d/%d, want 1/1", len(d.CompetitorCards), len(d.CompetitorCardPrices))
	}
	if len(d.CompetitorCardDetails) != 1 {
		t.Fatalf("details = %d, want 1", len(d.CompetitorCardDetails))
	}
	det := d.CompetitorCardDetails[0]
	if det.TotalQuantity != 250 || det.Promotions == "" {
		t.Errorf("detail = %+v", det)
	}
	if len(d.CompetitorCardStocks) != 1 {
		t.Fatalf("stocks = %d, want 1", len(d.CompetitorCardStocks))
	}
	st := d.CompetitorCardStocks[0]
	if st.WhID == nil || *st.WhID != 507 || st.Qty != 10 {
		t.Errorf("stock = %+v", st)
	}
}

// TestDecodeAd verifies banner decoding: advertiser name/INN lifted from the
// OrdBannerMark object, erid and promo_id carried.
func TestDecodeAd(t *testing.T) {
	d, err := Decode(raw("ad", "https://w.ru/ad", `{
		"data":{"shelfs":[{"data":[{
		  "promoId": 42, "bannerType": "catalog",
		  "creative": {"url":"https://x/c.jpg"}, "landing": "https://x/land",
		  "ordBannerErid": "erid123",
		  "ordBannerMark": {"advertiserName":"ООО Рога","advertiserInn":"7700123456"}
		}]}]}}`), ts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.VitrineAds) != 1 {
		t.Fatalf("vitrine_ads = %d, want 1", len(d.VitrineAds))
	}
	a := d.VitrineAds[0]
	if a.AdvertiserName != "ООО Рога" || a.AdvertiserINN != "7700123456" {
		t.Errorf("advertiser = %q/%q", a.AdvertiserName, a.AdvertiserINN)
	}
	if a.Erid != "erid123" || a.CreativeURL != "https://x/c.jpg" || a.PromoID == nil || *a.PromoID != 42 {
		t.Errorf("ad = %+v", a)
	}
}

// TestDecodeUnknownKind verifies an unrecognized kind yields no rows and no error
// (a new WB endpoint must never break the pipeline).
func TestDecodeUnknownKind(t *testing.T) {
	d, err := Decode(Intercept{Kind: "mystery", URL: "https://w.ru/x", Body: json.RawMessage(`{}`)}, ts)
	if err != nil {
		t.Fatalf("Decode unknown kind: %v", err)
	}
	if len(d.SearchPositions)+len(d.VitrineAds)+len(d.CompetitorCards)+
		len(d.CompetitorCardPrices)+len(d.CompetitorCardDetails)+len(d.CompetitorCardStocks) != 0 {
		t.Errorf("unknown kind produced rows: %+v", d)
	}
}

// TestDecodeMalformedBody verifies a bad JSON body returns an error (not a silent
// empty result) so the caller can log the bad capture.
func TestDecodeMalformedBody(t *testing.T) {
	if _, err := Decode(raw("search", "https://w.ru/s", `{not json`), ts); err == nil {
		t.Fatal("expected error for malformed body, got nil")
	}
}

// TestMockIntercepts verifies the synthetic fixtures decode to the expected rows.
func TestMockIntercepts(t *testing.T) {
	caps := MockIntercepts()
	if len(caps) != 2 {
		t.Fatalf("MockIntercepts = %d captures, want 2", len(caps))
	}

	// search capture: page=2 → positions 101/102, one organic + one ad.
	d, err := Decode(caps[0], ts)
	if err != nil {
		t.Fatalf("decode mock search: %v", err)
	}
	if len(d.SearchPositions) != 2 {
		t.Fatalf("search positions = %d, want 2", len(d.SearchPositions))
	}
	if d.SearchPositions[0].Position != 101 || d.SearchPositions[1].Position != 102 {
		t.Errorf("positions = %d/%d, want 101/102", d.SearchPositions[0].Position, d.SearchPositions[1].Position)
	}
	if d.SearchPositions[0].PriceProduct != 89900 {
		t.Errorf("kopecks = %d, want 89900", d.SearchPositions[0].PriceProduct)
	}
	if d.SearchPositions[1].PanelPromoID == nil {
		t.Error("second product should be an ad (panelPromoId=99)")
	}

	// card_detail capture: card + price + detail + stock.
	d, err = Decode(caps[1], ts)
	if err != nil {
		t.Fatalf("decode mock card_detail: %v", err)
	}
	if len(d.CompetitorCards) != 1 || len(d.CompetitorCardStocks) != 1 || len(d.CompetitorCardDetails) != 1 {
		t.Errorf("card_detail rows = cards%d prices%d details%d stocks%d",
			len(d.CompetitorCards), len(d.CompetitorCardPrices), len(d.CompetitorCardDetails), len(d.CompetitorCardStocks))
	}
}

// intPtr is a local test helper (the external writer test has its own copy).
func intPtr(v int) *int { return &v }
