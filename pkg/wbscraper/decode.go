package wbscraper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// wbPageSize is WB's search pagination size (100 products per page). The global
// position of a product is (page-1)*wbPageSize + index + 1, matching the extension
// (extensions/wb-scraper/src/background.js: PAGE_SIZE=100, verified on dumps).
const wbPageSize = 100

// Decode routes one captured WB response (Intercept) to its per-table rows. It is
// the Go equivalent of the extension's exportBuffer search flattening, extended to
// every capture kind classified by the service worker (background.js COLLECT_PATTERNS):
//
//   - search / brand → SearchPositions (page rank of each product)
//   - card_list      → CompetitorCards + CompetitorCardPrices (per size)
//   - card_detail    → CompetitorCards + Prices + CompetitorCardDetails + Stocks
//   - ad             → VitrineAds (banner advertisements)
//
// Unknown kinds yield an empty Decoded and a nil error: a new WB endpoint must never
// break the pipeline (the caller logs the kind for follow-up). QueryID is propagated
// from the Intercept into every produced row (provenance binding).
func Decode(it Intercept, snapshot SnapshotTs) (Decoded, error) {
	switch it.Kind {
	case "search", "brand":
		return decodeSearch(it, snapshot)
	case "card_list":
		return decodeCard(it, snapshot, false)
	case "card_detail":
		return decodeCard(it, snapshot, true)
	case "ad":
		return decodeAd(it, snapshot)
	default:
		return Decoded{}, nil
	}
}

// ----------------------------------------------------------------------------
// URL parsing — page/dest come from the captured WB request URL, not the body.
// ----------------------------------------------------------------------------

var (
	rePage = regexp.MustCompile(`[?&]page=(\d+)`)
	reDest = regexp.MustCompile(`[?&]dest=(\d+)`)

	// OРД marker (ordBannerMark) parsing. WB's v2/banners sends it as a literal
	// string "NAME, ИНН <digits>, ЕРИД <token>"; these pull the INN and ЕРИД out.
	reOrdINN  = regexp.MustCompile(`ИНН\s*(\d+)`)
	reOrdERID = regexp.MustCompile(`ЕРИД\s*([A-Za-z0-9]+)`)
	// External/paid ad landing hrefs carry the erid as a ?erid= query param.
	reEridParam = regexp.MustCompile(`[?&]erid=([A-Za-z0-9]+)`)
)

// ordINNLabel is the Cyrillic "ИНН" marker delimiting the advertiser name within
// an ordBannerMark string. Upper-cased for a case-insensitive substring search.
const ordINNLabel = "ИНН"

// pageAndDest extracts the page (default 1) and dest/region (nil if absent) from a
// captured WB search/brand URL. Mirrors background.js exactly.
func pageAndDest(urlStr string) (page int, dest *int) {
	page = 1
	if m := rePage.FindStringSubmatch(urlStr); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			page = n
		}
	}
	if m := reDest.FindStringSubmatch(urlStr); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			d := n
			dest = &d
		}
	}
	return page, dest
}

// ----------------------------------------------------------------------------
// search / brand — positions
// ----------------------------------------------------------------------------

// wbSearchResponse is the shape of /search and /brand responses (and the /list
// batch-hydration shares the product shape).
type wbSearchResponse struct {
	Resultset string             `json:"resultset"` // "filters" → no products; informational
	Products  []*wbSearchProduct `json:"products"`
	Metadata  struct {
		Name string `json:"name"` // WB-normalized query text; informational, not stored
	} `json:"metadata"`
}

type wbSearchProduct struct {
	ID           int64    `json:"id"`
	Brand        string   `json:"brand"`
	SupplierID   *int64   `json:"supplierId"`
	PanelPromoID *int64   `json:"panelPromoId"` // non-nil = this listing is an ad
	Rating       float64  `json:"rating"`
	Feedbacks    int      `json:"feedbacks"`
	Sizes        []wbSize `json:"sizes"`
}

// decodeSearch flattens search/brand products into one SearchPosition per product.
// resultset="filters" and null/absent products produce nothing (skip).
func decodeSearch(it Intercept, snapshot SnapshotTs) (Decoded, error) {
	var resp wbSearchResponse
	if err := json.Unmarshal(it.Body, &resp); err != nil {
		return Decoded{}, fmt.Errorf("decode search body: %w", err)
	}
	if strings.EqualFold(resp.Resultset, "filters") {
		return Decoded{}, nil // skip facet-only responses (products is null)
	}

	page, dest := pageAndDest(it.URL)
	var d Decoded
	for idx, p := range resp.Products {
		if p == nil {
			continue // sparse slot — defensive, matches background.js `if (!p) return`
		}
		basic, product := firstSizePrice(p.Sizes)
		d.SearchPositions = append(d.SearchPositions, SearchPosition{
			SnapshotTs:   snapshot,
			QueryID:      it.QueryID,
			RegionDest:   dest,
			Page:         page,
			Position:     (page-1)*wbPageSize + idx + 1,
			NmID:         p.ID,
			Brand:        p.Brand,
			SupplierID:   p.SupplierID,
			PanelPromoID: p.PanelPromoID, // nil → organic
			PriceBasic:   basic,          // kopecks
			PriceProduct: product,        // kopecks
			Rating:       p.Rating,
			Feedbacks:    p.Feedbacks,
		})
	}
	return d, nil
}

// firstSizePrice returns the representative first-size price (kopecks). WB search
// responses carry the per-size price under sizes[0].price.
func firstSizePrice(sizes []wbSize) (basic, product int64) {
	if len(sizes) == 0 || sizes[0].Price == nil {
		return 0, 0
	}
	return sizes[0].Price.Basic, sizes[0].Price.Product
}

// ----------------------------------------------------------------------------
// card_list / card_detail — competitor cards, prices, details, stocks
// ----------------------------------------------------------------------------

// wbCardProduct extends the search product with the /list + /detail card fields.
// Shared json tags with wbSearchProduct are inherited via embedding.
type wbCardProduct struct {
	wbSearchProduct
	Supplier      string          `json:"supplier"`
	Pics          json.RawMessage `json:"pics"`   // number OR array → count
	Weight        float64         `json:"weight"` // kg, fractional (WB sends floats, not ints)
	Volume        float64         `json:"volume"`
	Colors        json.RawMessage `json:"colors"` // array of {name} OR strings
	SubjectID     *int64          `json:"subjectId"`
	TotalQuantity int             `json:"totalQuantity"` // /detail-exclusive
	Promotions    json.RawMessage `json:"promotions"`    // /detail-exclusive, JSON blob
}

type wbSize struct {
	Name   string    `json:"name"`
	Price  *wbPrice  `json:"price"`
	Wh     *int64    `json:"wh"`
	Stocks []wbStock `json:"stocks"` // /detail-exclusive
}

type wbPrice struct {
	Basic   int64 `json:"basic"`   // kopecks
	Product int64 `json:"product"` // kopecks
}

type wbStock struct {
	Wh    int64 `json:"wh"`
	Qty   int   `json:"qty"`
	Time1 *int  `json:"time1"`
	Time2 *int  `json:"time2"`
}

// decodeCard extracts competitor cards (always), per-size prices, and — only for
// /detail — the aggregate details and per-warehouse stocks.
func decodeCard(it Intercept, snapshot SnapshotTs, detail bool) (Decoded, error) {
	var resp struct {
		Products []*wbCardProduct `json:"products"`
	}
	if err := json.Unmarshal(it.Body, &resp); err != nil {
		return Decoded{}, fmt.Errorf("decode card body: %w", err)
	}

	var d Decoded
	for _, p := range resp.Products {
		if p == nil {
			continue
		}
		card, prices, stocks := extractCard(p, it.QueryID, snapshot)
		d.CompetitorCards = append(d.CompetitorCards, card)
		d.CompetitorCardPrices = append(d.CompetitorCardPrices, prices...)
		if detail {
			d.CompetitorCardStocks = append(d.CompetitorCardStocks, stocks...)
			d.CompetitorCardDetails = append(d.CompetitorCardDetails, extractDetail(p, it.QueryID, snapshot))
		}
	}
	return d, nil
}

// extractCard builds the core card row plus per-size price rows and per-wh stock
// rows (stocks are only produced when sizes carry them, i.e. /detail).
func extractCard(p *wbCardProduct, qid int64, snapshot SnapshotTs) (CompetitorCard, []CompetitorCardPrice, []CompetitorCardStock) {
	card := CompetitorCard{
		SnapshotTs:   snapshot,
		QueryID:      qid,
		NmID:         p.ID,
		Brand:        p.Brand,
		Supplier:     p.Supplier,
		SupplierID:   p.SupplierID,
		Rating:       p.Rating,
		Feedbacks:    p.Feedbacks,
		Pics:         picsCount(p.Pics),
		Weight:       p.Weight,
		Volume:       p.Volume,
		Colors:       joinColors(p.Colors),
		SubjectID:    p.SubjectID,
		PanelPromoID: p.PanelPromoID,
	}

	var prices []CompetitorCardPrice
	var stocks []CompetitorCardStock
	for _, sz := range p.Sizes {
		if sz.Price != nil {
			prices = append(prices, CompetitorCardPrice{
				SnapshotTs:   snapshot,
				QueryID:      qid,
				NmID:         p.ID,
				SizeName:     sz.Name,
				PriceBasic:   sz.Price.Basic,
				PriceProduct: sz.Price.Product,
				WhID:         sz.Wh,
			})
		}
		for _, st := range sz.Stocks {
			wh := st.Wh // copy so &wh is distinct per stock
			stocks = append(stocks, CompetitorCardStock{
				SnapshotTs: snapshot,
				QueryID:    qid,
				NmID:       p.ID,
				SizeName:   sz.Name,
				WhID:       &wh,
				Qty:        st.Qty,
				Time1:      st.Time1,
				Time2:      st.Time2,
			})
		}
	}
	return card, prices, stocks
}

// extractDetail builds the /detail-exclusive aggregate row (total stock + promos).
func extractDetail(p *wbCardProduct, qid int64, snapshot SnapshotTs) CompetitorCardDetail {
	return CompetitorCardDetail{
		SnapshotTs:    snapshot,
		QueryID:       qid,
		NmID:          p.ID,
		TotalQuantity: p.TotalQuantity,
		Promotions:    rawJSONOrEmpty(p.Promotions),
	}
}

// ----------------------------------------------------------------------------
// ad — banner advertisements
//
// WB serves vitrine ads from two endpoints with different shapes (refined from
// live captures, Stage 6.5):
//
//  1. banners-website.../public/v2/banners  →  top-level ARRAY of wbBanner. This
//     is the primary source: a single search page fires several v2/banners calls
//     (one per urltype), each returning 0..N banners.
//  2. __internal/banners/shelfs/search      →  {data:{banners:{data:[],total},
//     shelfs:{data:[],total}}}. Slot banners share the wbBanner shape; for low-ad
//     queries both slots are empty (total=0). Handled defensively regardless.
//
// A wbBanner carries the landing href, creative src, and an OРД marker
// (ordBannerMark) encoding the legal advertiser identity. Decoding never errors on
// a structural mismatch — it yields 0 ads and lets the caller log the kind, so a
// future third endpoint shape cannot break the pipeline.
// ----------------------------------------------------------------------------

type wbAdResponse struct {
	Data struct {
		Banners wbAdSlot `json:"banners"`
		Shelfs  wbAdSlot `json:"shelfs"`
	} `json:"data"`
}

// wbAdSlot is one named slot of the shelfs/search response: a banner array + count.
type wbAdSlot struct {
	Data []wbBanner `json:"data"`
}

// wbBanner is one banner from v2/banners (or a shelfs/search slot). Only the
// fields we persist are decoded; the rest (uid, locationType, urlType, tgoData, …)
// are ignored by Go's json decoder. ordBannerMark stays RawMessage because it is a
// string in v2/banners ("NAME, ИНН N, ЕРИД E") but may be an object elsewhere.
type wbBanner struct {
	Href          string          `json:"href"`              // landing URL (absolute or WB-relative)
	Src           string          `json:"src"`               // creative image path (WB-relative)
	Alt           string          `json:"alt"`               // alt text; for internal promos = the promo name
	PromoText     string          `json:"promoText"`         // internal-WB promo text (no OРД marker)
	OrdBannerMark json.RawMessage `json:"ordBannerMark"`
	BannerType    string          `json:"bannerType"`        // "" | "static" | "banner"
}

func decodeAd(it Intercept, snapshot SnapshotTs) (Decoded, error) {
	var banners []wbBanner

	// Shape 1: top-level array (banners-website v2/banners).
	if err := json.Unmarshal(it.Body, &banners); err == nil {
		return adsFromBanners(banners, it.QueryID, snapshot), nil
	}

	// Shape 2: object wrapper with data.{banners,shelfs} slots (shelfs/search).
	var resp wbAdResponse
	if err := json.Unmarshal(it.Body, &resp); err == nil {
		banners = append(banners, resp.Data.Banners.Data...)
		banners = append(banners, resp.Data.Shelfs.Data...)
		return adsFromBanners(banners, it.QueryID, snapshot), nil
	}

	// Neither known shape matched — defensive: yield nothing, never error.
	return Decoded{}, nil
}

// adsFromBanners maps a slice of wbBanner into VitrineAd rows.
func adsFromBanners(banners []wbBanner, qid int64, snapshot SnapshotTs) Decoded {
	var d Decoded
	for _, b := range banners {
		d.VitrineAds = append(d.VitrineAds, adFromBanner(b, qid, snapshot))
	}
	return d
}

// adFromBanner builds one VitrineAd. Advertiser identity (name/INN/erid) comes
// from the OРД marker; for internal WB promos (no marker) the promo/alt text is
// used as the name. PromoID is left nil — v2/banners exposes no numeric promo id.
func adFromBanner(b wbBanner, qid int64, snapshot SnapshotTs) VitrineAd {
	name, inn, erid := parseOrdMark(b.OrdBannerMark)
	if erid == "" {
		erid = eridFromHref(b.Href) // external ad hrefs carry ?erid=
	}
	if name == "" {
		// Internal WB promo (no OРД marker): "Хозяйственные товары", "Wb Клуб", …
		name = b.PromoText
		if name == "" {
			name = b.Alt
		}
	}
	return VitrineAd{
		SnapshotTs:     snapshot,
		QueryID:        qid,
		AdvertiserName: name,
		AdvertiserINN:  inn,
		Erid:           erid,
		BannerType:     b.BannerType,
		CreativeURL:    b.Src,
		LandingHref:    b.Href,
	}
}

// parseOrdMark extracts advertiser name/INN/erid from an ordBannerMark. WB's
// v2/banners sends it as a string "NAME, ИНН <digits>, ЕРИД <token>"; an object
// form {advertiserName, advertiserInn, erid} is also tolerated (defensive).
func parseOrdMark(raw json.RawMessage) (name, inn, erid string) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", "", ""
	}
	// Object form (not observed in v2/banners, but tolerated).
	var obj struct {
		Name string `json:"advertiserName"`
		INN  string `json:"advertiserInn"`
		Erid string `json:"erid"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && (obj.Name != "" || obj.INN != "") {
		return obj.Name, obj.INN, obj.Erid
	}
	// String form: "ДВИЖЕНИЕ ПЕРВЫХ, ИНН 9709087880, ЕРИД L71GTkMSi".
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return s, "", "" // give up: store the raw bytes as the name
	}
	if m := reOrdINN.FindStringSubmatch(str); m != nil {
		inn = m[1]
	}
	if m := reOrdERID.FindStringSubmatch(str); m != nil {
		erid = m[1]
	}
	// Name = everything before the "ИНН" marker, trimmed of the trailing comma.
	if idx := strings.Index(strings.ToUpper(str), ordINNLabel); idx > 0 {
		name = strings.Trim(strings.TrimSpace(str[:idx]), ",")
	} else {
		name = strings.TrimSpace(str)
	}
	return name, inn, erid
}

// eridFromHref pulls the erid token from a landing href's ?erid= query param
// (external/paid ad hrefs carry it as a fallback when ordBannerMark is absent).
func eridFromHref(href string) string {
	if m := reEridParam.FindStringSubmatch(href); m != nil {
		return m[1]
	}
	return ""
}

// ----------------------------------------------------------------------------
// defensive JSON helpers (pics count, colors join, promotions text)
// ----------------------------------------------------------------------------

// picsCount interprets a "pics" field that may be a count number or a URL array.
func picsCount(raw json.RawMessage) int {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr)
	}
	return 0
}

// joinColors joins color names from an array of {name} objects or plain strings.
func joinColors(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var objs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &objs); err == nil {
		names := make([]string, 0, len(objs))
		for _, o := range objs {
			if o.Name != "" {
				names = append(names, o.Name)
			}
		}
		return strings.Join(names, ", ")
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		return strings.Join(strs, ", ")
	}
	return ""
}

// rawJSONOrEmpty normalizes a raw JSON field to "" for null/empty (so "null" is not
// stored as text), otherwise returns the verbatim JSON text.
func rawJSONOrEmpty(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return s
}
