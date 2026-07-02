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

// TestDecodeAd covers the two real WB ad-endpoint shapes captured in Stage 6.5:
//
//   - banners-website v2/banners → top-level ARRAY (the primary source): each
//     banner is {href, src, alt, promoText?, ordBannerMark?, bannerType}.
//   - __internal/banners/shelfs/search → {data:{banners:{data:[]}, shelfs:{data:[]}}}.
//
// Advertiser identity is parsed from the ordBannerMark string
// "NAME, ИНН <digits>, ЕРИД <token>"; internal WB promos (no mark) fall back to
// promoText/alt; erid falls back to the landing href's ?erid= param.
func TestDecodeAd(t *testing.T) {
	t.Run("v2 banners array", func(t *testing.T) {
		// Three banners lifted from a live capture: social ad (ordBannerMark),
		// internal promo (promoText), and a bare link (alt only).
		d, err := Decode(raw("ad",
			"https://banners-website.wildberries.ru/public/v2/banners?urltype=1024", `[
			  {"href":"https://projects.pervye.ru/?utm=x&erid=L71GTkMSi","src":"/adsf/1782856217708260210.webp",
			   "alt":"Социальная реклама","ordBannerMark":"ДВИЖЕНИЕ ПЕРВЫХ, ИНН 9709087880, ЕРИД L71GTkMSi","bannerType":"static"},
			  {"href":"/promotions/vse-dlya-uborki","src":"/poster/ru/action2/c660x210/tab_hozztov_12_22574745.jpg",
			   "alt":"Хозяйственные товары","promoText":"Хозяйственные товары","bannerType":"static"},
			  {"href":"/wbclub","src":"/poster/ru/horizontal1/960x412/960x412.jpg",
			   "alt":"Wb Клуб","bannerType":""}
			]`), ts)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got, want := len(d.VitrineAds), 3; got != want {
			t.Fatalf("vitrine_ads = %d, want %d", got, want)
		}
		a := d.VitrineAds[0]
		if a.AdvertiserName != "ДВИЖЕНИЕ ПЕРВЫХ" || a.AdvertiserINN != "9709087880" || a.Erid != "L71GTkMSi" {
			t.Errorf("banner0 identity = %q/%q/%q", a.AdvertiserName, a.AdvertiserINN, a.Erid)
		}
		if a.BannerType != "static" || a.CreativeURL != "/adsf/1782856217708260210.webp" ||
			a.LandingHref != "https://projects.pervye.ru/?utm=x&erid=L71GTkMSi" {
			t.Errorf("banner0 fields = %+v", a)
		}
		if a.PromoID != nil {
			t.Errorf("banner0 promo_id = %v, want nil (v2/banners has none)", *a.PromoID)
		}
		b := d.VitrineAds[1]
		if b.AdvertiserName != "Хозяйственные товары" || b.AdvertiserINN != "" || b.Erid != "" {
			t.Errorf("banner1 (internal promo) = %q/%q/%q", b.AdvertiserName, b.AdvertiserINN, b.Erid)
		}
		c := d.VitrineAds[2]
		if c.AdvertiserName != "Wb Клуб" {
			t.Errorf("banner2 (alt fallback) name = %q", c.AdvertiserName)
		}
	})

	t.Run("shelfs search object both slots", func(t *testing.T) {
		// data.banners and data.shelfs each populated with one banner → 2 ads total.
		d, err := Decode(raw("ad",
			"/__internal/banners/shelfs/search?query=x", `{
			  "metadata":{"query":"x"},
			  "data":{
			    "banners":{"data":[{"href":"/b1","src":"/s1.jpg","alt":"B1","bannerType":"static"}],"total":1},
			    "shelfs":{"data":[{"href":"/b2","src":"/s2.jpg","alt":"B2",
			      "ordBannerMark":"ООО ТЕСТ, ИНН 1234567890, ЕРИД Lj1"}],"total":1}}} `), ts)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got, want := len(d.VitrineAds), 2; got != want {
			t.Fatalf("vitrine_ads = %d, want %d", got, want)
		}
		if d.VitrineAds[0].AdvertiserName != "B1" {
			t.Errorf("banners-slot ad name = %q", d.VitrineAds[0].AdvertiserName)
		}
		s := d.VitrineAds[1]
		if s.AdvertiserName != "ООО ТЕСТ" || s.AdvertiserINN != "1234567890" || s.Erid != "Lj1" {
			t.Errorf("shelfs-slot ad = %q/%q/%q", s.AdvertiserName, s.AdvertiserINN, s.Erid)
		}
	})

	t.Run("shelfs search empty slots", func(t *testing.T) {
		// The shape actually captured for a low-ad query: both slots total=0.
		d, err := Decode(raw("ad",
			"/__internal/banners/shelfs/search?query=x", `{
			  "metadata":{"query":"x"},
			  "data":{"banners":{"data":[],"total":0},"shelfs":{"data":[],"total":0}}}`), ts)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(d.VitrineAds) != 0 {
			t.Fatalf("vitrine_ads = %d, want 0", len(d.VitrineAds))
		}
	})

	t.Run("empty array yields nothing", func(t *testing.T) {
		// v2/banners returns [] when no banners match the urltype — no error.
		d, err := Decode(raw("ad", "https://banners-website.wildberries.ru/public/v2/banners", `[]`), ts)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(d.VitrineAds) != 0 {
			t.Fatalf("vitrine_ads = %d, want 0", len(d.VitrineAds))
		}
	})

	t.Run("erid falls back to href param", func(t *testing.T) {
		// ordBannerMark carries name+INN but no ЕРИД; the href's ?erid= fills it.
		d, err := Decode(raw("ad", "https://banners-website.wildberries.ru/public/v2/banners", `[
		  {"href":"https://adv.example/?erid=Lj9","src":"/s.jpg","alt":"A",
		   "ordBannerMark":"ООО ФИРМА, ИНН 9999999999"}]`), ts)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		a := d.VitrineAds[0]
		if a.AdvertiserName != "ООО ФИРМА" || a.AdvertiserINN != "9999999999" {
			t.Errorf("identity = %q/%q", a.AdvertiserName, a.AdvertiserINN)
		}
		if a.Erid != "Lj9" {
			t.Errorf("erid = %q, want Lj9 (href fallback)", a.Erid)
		}
	})
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
