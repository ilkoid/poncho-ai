// Package wbscraper provides a collector for WB storefront competitor data
// captured by the wb-scraper browser extension (extensions/wb-scraper/).
//
// Unlike pkg/orders and siblings, wbscraper has NO Source interface: the data
// source is external — the browser extension pushes captured WB API responses
// over HTTP. This package therefore exposes only a Writer (persistence) and a
// QueryGenerator (target construction); the HTTP transport lives in server.go.
//
// Architecture (see plan iridescent-shimmying-bee.md):
//
//	extension ──POST /capture──► server ──► Decode(Intercept) → Decoded rows
//	          ◄──GET /targets──           ──► Writer (SQLite | PostgreSQL)
//	                                     ──► QueryGenerator (Static | LLM)
//
// Provenance: every decoded row carries QueryID (FK → search_queries dimension).
// The query text lives only in search_queries — fact tables store the id, never
// the text (space + queryable metadata: "data by season=зима" is a column filter,
// not a LIKE over query text).
package wbscraper

import (
	"context"
	"encoding/json"
)

// NoQuery is the QueryID sentinel meaning "this row has no associated search
// query" — i.e. the capture came from a direct nmId/url target, not a constructed
// query. It is never a valid foreign key (search_queries.query_id auto-increments
// from 1), so 0 unambiguously means "none". The storage layer maps NoQuery → NULL.
const NoQuery int64 = 0

// QueryIDValue returns the argument to bind for a query_id column, mapping the
// NoQuery sentinel (0) to nil so the column stores NULL rather than 0. Fact-table
// query_id is nullable (a direct nmId/url target has no query); search_positions.
// query_id is NOT NULL, but a position always carries a real QueryID, so routing it
// through here never produces NULL there. Kept in the domain package (the package
// that owns the sentinel owns its NULL mapping) so both storage adapters share one
// definition.
func QueryIDValue(qid int64) any {
	if qid == NoQuery {
		return nil
	}
	return qid
}

// SnapshotTs is the ISO-8601 timestamp tagging one capture session's rows.
// All fact tables are append-only by SnapshotTs (pattern: stock_history.created_at).
// It is owned by the server: the CLI stamps one value per run and passes it to
// Decode for every capture, so all rows of a session share a single SnapshotTs.
type SnapshotTs string

// SearchQuery is one row of the search_queries dimension table.
// The constructor builds it (subject × attrs → text); the server upserts it to
// obtain a stable QueryID, then propagates that id into the target queue and every
// decoded fact row. A given Query text always resolves to the same QueryID across
// sessions (UNIQUE constraint), so cross-session analysis joins cleanly.
//
// Brand/Material/Purpose/Comment are the v2 cartesian-axis provenance columns the
// poncho-wb-parser extension stamps (the v1 wb-scraper left them empty). They ride
// the same upsert so a re-pushed snapshot keeps its axis metadata across sessions.
type SearchQuery struct {
	// QueryID is 0 on input (auto-assigned by UpsertQuery); populated on return.
	// In a v2 SnapshotDump it carries the BROWSER query_id (Dexie autoinc); the
	// server re-resolves by Query text and remaps it to the server id (see
	// Server.handleSnapshot).
	QueryID int64 `json:"query_id"`
	// Query is the normalized search text (the WB search box string).
	Query string `json:"query"`
	// Subject, Brand, Gender, Season, Age, Material, Purpose, Comment record how the
	// constructor assembled the query, so analysis can filter by dimension without
	// parsing the text.
	Subject  string `json:"subject"`
	Brand    string `json:"brand"`
	Gender   string `json:"gender"`
	Season   string `json:"season"`
	Age      string `json:"age"`
	Material string `json:"material"`
	Purpose  string `json:"purpose"`
	Comment  string `json:"comment"`
}

// Target is one unit of work handed to the extension via GET /targets.
// The extension navigates to URL and stamps QueryID onto every capture it pushes
// back, binding rows to their originating query.
type Target struct {
	// Kind is "search" (query text), "card" (nmId), or "url" (raw WB URL).
	Kind string `json:"kind"`
	// QueryID is NoQuery until the server upserts the query into search_queries;
	// it is populated before the target is served via /targets.
	QueryID int64 `json:"query_id"`
	// Query is the human-readable search text (Kind=="search"); empty otherwise.
	Query string `json:"query"`
	// URL is the full WB URL the extension navigates to.
	URL string `json:"url"`
	// Subject/Gender/Season/Age mirror SearchQuery — carried for reporting and
	// for the dimension upsert the server performs before serving.
	Subject string `json:"subject"`
	Gender  string `json:"gender"`
	Season  string `json:"season"`
	Age     string `json:"age"`
}

// Intercept is one captured WB API response, the wire shape of POST /capture items.
// Kind and QueryID are stamped by the extension's service worker (Kind via the
// COLLECT_PATTERNS classifier, QueryID from the active target); Body is the
// already-parsed JSON of the WB response (kept raw here for per-kind decoding).
type Intercept struct {
	// Kind is the SW-classified capture kind: search|brand|ad|card_list|card_detail.
	Kind string `json:"kind"`
	// URL is the intercepted WB request URL (used to extract dest=/page=).
	URL string `json:"url"`
	// QueryID is stamped from the active target; NoQuery for direct nmId/url targets.
	QueryID int64 `json:"query_id"`
	// Status is the HTTP status of the captured response.
	Status int `json:"status"`
	// Body is the raw JSON of the WB response (sizes[0].price kopecks, products[], etc.).
	Body json.RawMessage `json:"body"`
}

// Decoded is the per-table row bundle. It has two producers:
//   - Decode (one Intercept): routes on Kind and fills only the 6 storefront slices
//     (positions/ads/cards/prices/details/stocks); the 5 content slices stay nil.
//     The v1 /capture flush persists each non-empty slice via the matching Save*.
//   - handleSnapshot (one SnapshotDump from the v2 extension): fills ALL 11 slices
//     (the dump is already decoded in the browser), then persists them atomically
//     via ReplaceSnapshot. Decode.go is NOT involved in the /snapshot path.
//
// All rows inherit QueryID from their source.
type Decoded struct {
	SearchPositions            []SearchPosition            `json:"search_positions"`
	VitrineAds                 []VitrineAd                 `json:"vitrine_ads"`
	CompetitorCards            []CompetitorCard            `json:"competitor_cards"`
	CompetitorCardPrices       []CompetitorCardPrice       `json:"competitor_card_prices"`
	CompetitorCardDetails      []CompetitorCardDetail      `json:"competitor_card_details"`
	CompetitorCardStocks       []CompetitorCardStock       `json:"competitor_card_stocks"`
	CompetitorCardMeta         []CompetitorCardMeta        `json:"competitor_card_meta"`
	CompetitorCardOptions      []CompetitorCardOption      `json:"competitor_card_options"`
	CompetitorCardCompositions []CompetitorCardComposition `json:"competitor_card_compositions"`
	CompetitorCardSizes        []CompetitorCardSize        `json:"competitor_card_sizes"`
	CompetitorCardColors       []CompetitorCardColor       `json:"competitor_card_colors"`
}

// ---------------------------------------------------------------------------
// Fact-table row types. All are append-only by SnapshotTs and carry QueryID.
// Prices are int64 kopecks (WB serves search/list prices already in kopecks).
// Nullable numerics use pointers so "absent" is distinguishable from zero.
// ---------------------------------------------------------------------------

// SearchPosition is one product's rank in one search/brand result page.
// RegionDest is MANDATORY here: WB search is personalized by dest/cluster, so
// snapshots without a region are not comparable. Position is the global rank
// within the query: (page-1)*100 + index + 1 (WB returns 100/page).
type SearchPosition struct {
	SnapshotTs   SnapshotTs `json:"snapshot_ts"`
	QueryID      int64      `json:"query_id"` // always set for a search/brand capture
	RegionDest   *int       `json:"region_dest"`
	Page         int        `json:"page"`
	Position     int        `json:"position"`
	NmID         int64      `json:"nm_id"`
	Brand        string     `json:"brand"`
	SupplierID   *int64     `json:"supplier_id"`
	PanelPromoID *int64     `json:"panel_promo_id"` // non-nil = this listing is an ad
	PriceBasic   int64      `json:"price_basic"`    // kopecks
	PriceProduct int64      `json:"price_product"`  // kopecks
	Rating       float64    `json:"rating"`
	Feedbacks    int        `json:"feedbacks"`
}

// VitrineAd is one banner advertisement served alongside search results.
// Advertiser identity (name/INN/erid) comes from the OРД/ЕРИР OrdBannerMark —
// legal markers, not WB's internal ids. CPM is intentionally NOT stored (encrypted).
type VitrineAd struct {
	SnapshotTs     SnapshotTs `json:"snapshot_ts"`
	QueryID        int64      `json:"query_id"`
	AdvertiserName string     `json:"advertiser_name"`
	AdvertiserINN  string     `json:"advertiser_inn"`
	Erid           string     `json:"erid"`
	PromoID        *int64     `json:"promo_id"`
	BannerType     string     `json:"banner_type"`
	CreativeURL    string     `json:"creative_url"`
	LandingHref    string     `json:"landing_href"`
}

// CompetitorCard is the core product card from /list (and the common subset of
// /detail). One row per nmId per snapshot. SubjectID is WB's category id.
type CompetitorCard struct {
	SnapshotTs   SnapshotTs `json:"snapshot_ts"`
	QueryID      int64      `json:"query_id"`
	NmID         int64      `json:"nm_id"`
	Brand        string     `json:"brand"`
	Supplier     string     `json:"supplier"`
	SupplierID   *int64     `json:"supplier_id"`
	Rating       float64    `json:"rating"`
	Feedbacks    int        `json:"feedbacks"`
	Pics         int        `json:"pics"`
	Weight       float64    `json:"weight"` // kg, as WB sends it (fractional, e.g. 0.09)
	Volume       float64    `json:"volume"`
	Colors       string     `json:"colors"`
	SubjectID    *int64     `json:"subject_id"`
	PanelPromoID *int64     `json:"panel_promo_id"`
}

// CompetitorCardPrice is one (nmId, size, warehouse) price row from /list.
// Size is a real axis: the same nmId has multiple sizes, each with its own price.
type CompetitorCardPrice struct {
	SnapshotTs   SnapshotTs `json:"snapshot_ts"`
	QueryID      int64      `json:"query_id"`
	NmID         int64      `json:"nm_id"`
	SizeName     string     `json:"size_name"`
	PriceBasic   int64      `json:"price_basic"`
	PriceProduct int64      `json:"price_product"`
	WhID         *int64     `json:"wh_id"`
	DeliveryDays *int       `json:"delivery_days"`
}

// CompetitorCardDetail carries the /detail-exclusive aggregates: total stock and
// active promotions. Per-warehouse stock lives in CompetitorCardStock. Promotions
// is stored as a JSON blob (variable-shape array).
type CompetitorCardDetail struct {
	SnapshotTs    SnapshotTs `json:"snapshot_ts"`
	QueryID       int64      `json:"query_id"`
	NmID          int64      `json:"nm_id"`
	TotalQuantity int        `json:"total_quantity"`
	Promotions    string     `json:"promotions"` // JSON text
}

// CompetitorCardStock is one (nmId, size, warehouse) stock row — /detail-exclusive
// (absent in /list). time1/time2 are WB's stock timestamps (delivery horizons).
type CompetitorCardStock struct {
	SnapshotTs SnapshotTs `json:"snapshot_ts"`
	QueryID    int64      `json:"query_id"`
	NmID       int64      `json:"nm_id"`
	SizeName   string     `json:"size_name"`
	WhID       *int64     `json:"wh_id"`
	Qty        int        `json:"qty"`
	Time1      *int       `json:"time1"`
	Time2      *int       `json:"time2"`
}

// ----------------------------------------------------------------------------
// card.json content tables (Этап A/B). The five types below capture the flat
// wbbasket.ru card.json CDN object, decoded in the extension and pushed via the
// /snapshot dump. v1 /capture never produced these — they are v2-only. One meta
// row per nm per snapshot; options/compositions/sizes/colors are EAV (N rows).
// Nullable numerics use pointers (imt_id/supplier_id/subject_id/… → NULL when nil).
// ----------------------------------------------------------------------------

// CompetitorCardMeta is the per-nm scalar content of one card.json (1 row per nm
// per snapshot). vendor_code = артикул продавца (год выпуска = символы 2-3);
// need_kiz = маркировка. Captures EVERY known scalar: title (imt_name), brand
// (selling.*), media (photo_count/has_video), subject ids (data.*), colors name,
// contents, kinds (JSON text). Joins on nm_id. description prefers markdown form.
type CompetitorCardMeta struct {
	SnapshotTs               SnapshotTs `json:"snapshot_ts"`
	QueryID                  int64      `json:"query_id"`
	NmID                     int64      `json:"nm_id"`
	VendorCode               string     `json:"vendor_code"`
	SubjName                 string     `json:"subj_name"`
	SubjRootName             string     `json:"subj_root_name"`
	Description              string     `json:"description"`
	NeedKiz                  int        `json:"need_kiz"`
	CreateDate               string     `json:"create_date"`
	UpdateDate               string     `json:"update_date"`
	ImtID                    *int64     `json:"imt_id"`
	ImtName                  string     `json:"imt_name"`
	Slug                     string     `json:"slug"`
	BrandName                string     `json:"brand_name"`
	BrandHash                string     `json:"brand_hash"`
	SupplierID               *int64     `json:"supplier_id"`
	PhotoCount               int        `json:"photo_count"`
	HasVideo                 int        `json:"has_video"`
	SubjectID                *int64     `json:"subject_id"`
	SubjectRootID            *int64     `json:"subject_root_id"`
	NmColorsNames            string     `json:"nm_colors_names"`
	Contents                 string     `json:"contents"`
	HasSellerRecommendations int        `json:"has_seller_recommendations"`
	UserFlags                int        `json:"user_flags"`
	Kinds                    string     `json:"kinds"`
}

// CompetitorCardOption is one product characteristic (Состав / Цвет / Покрой / …)
// from card.json options[] — N rows per nm per snapshot. GroupName is the section
// resolved from grouped_options[] («Основная информация» / «Дополнительная»), ” if
// none. VariableValues is a JSON-text array for variable characteristics (” if none).
type CompetitorCardOption struct {
	SnapshotTs     SnapshotTs `json:"snapshot_ts"`
	QueryID        int64      `json:"query_id"`
	NmID           int64      `json:"nm_id"`
	CharName       string     `json:"char_name"`
	CharValue      string     `json:"char_value"`
	CharcType      int        `json:"charc_type"`
	IsVariable     int        `json:"is_variable"`
	VariableValues string     `json:"variable_values"`
	GroupName      string     `json:"group_name"`
}

// CompetitorCardComposition is one material component from card.json compositions[]
// (хлопок 60% / полиэстер 40%) — N rows per nm per snapshot; Ord preserves the
// on-card order.
type CompetitorCardComposition struct {
	SnapshotTs SnapshotTs `json:"snapshot_ts"`
	QueryID    int64      `json:"query_id"`
	NmID       int64      `json:"nm_id"`
	Name       string     `json:"name"`
	Ord        int        `json:"ord"`
}

// CompetitorCardSize is one cell of the card.json size grid: a single measurement
// (PropName=PropValue) for one TechSize. Built by zipping sizes_table.details_props[i]
// × values[k].details[i]; empty cells are skipped in the decoder (a sparse cell
// conveys no data and would only bloat the table). ChrtID is WB's per-size chrt id.
type CompetitorCardSize struct {
	SnapshotTs SnapshotTs `json:"snapshot_ts"`
	QueryID    int64      `json:"query_id"`
	NmID       int64      `json:"nm_id"`
	TechSize   string     `json:"tech_size"`
	ChrtID     *int64     `json:"chrt_id"`
	PropName   string     `json:"prop_name"`
	PropValue  string     `json:"prop_value"`
	PropOrder  int        `json:"prop_order"`
}

// CompetitorCardColor is one color-variant nm_id from card.json colors[] /
// full_colors[] (другие цвета того же товара) — N rows per nm per snapshot; Ord
// preserves the on-card order.
type CompetitorCardColor struct {
	SnapshotTs SnapshotTs `json:"snapshot_ts"`
	QueryID    int64      `json:"query_id"`
	NmID       int64      `json:"nm_id"`
	ColorNmID  int64      `json:"color_nm_id"`
	Ord        int        `json:"ord"`
}

// Writer is the persistence interface for collected competitor data.
// Declared in the consumer package (Rule 6), implemented by the SQLite and
// PostgreSQL storage adapters (Stage 3) with compile-time assertions.
//
// Seven methods, one cohesive pipeline stage (persist decoded captures): the flush
// loop calls every Save* it has rows for, plus UpsertQuery while filling the
// target queue. Cohesion — not segregation — fits here because the single consumer
// uses all methods; precedent: pkg/supplies (7 Writer methods).
type Writer interface {
	// UpsertQuery inserts q if its text is new, returning the stable QueryID;
	// or returns the existing QueryID if the text is already present. Idempotent
	// across sessions (UNIQUE on query text). Called while filling the target queue.
	UpsertQuery(ctx context.Context, q SearchQuery) (QueryID int64, err error)

	// SaveStorefrontPositions appends a batch of search-result ranking rows.
	// (Renamed from SaveSearchPositions to avoid a collision with the searchvis
	// domain's method of that name on the shared *SQLiteSalesRepository receiver;
	// searchvis positions are the seller's OWN visibility from the Analytics API,
	// these are competitor ranks scraped from the storefront — different domains.)
	SaveStorefrontPositions(ctx context.Context, rows []SearchPosition) (int, error)
	// SaveVitrineAds appends a batch of banner-ad rows.
	SaveVitrineAds(ctx context.Context, rows []VitrineAd) (int, error)
	// SaveCompetitorCards appends a batch of core card rows.
	SaveCompetitorCards(ctx context.Context, rows []CompetitorCard) (int, error)
	// SaveCompetitorCardPrices appends a batch of per-size price rows.
	SaveCompetitorCardPrices(ctx context.Context, rows []CompetitorCardPrice) (int, error)
	// SaveCompetitorCardDetails appends a batch of /detail aggregate rows.
	SaveCompetitorCardDetails(ctx context.Context, rows []CompetitorCardDetail) (int, error)
	// SaveCompetitorCardStocks appends a batch of per-wh stock rows.
	SaveCompetitorCardStocks(ctx context.Context, rows []CompetitorCardStock) (int, error)
}

// SnapshotReplacer is the v2 replace-by-snapshot persistence path, used by the
// POST /snapshot handler (the poncho-wb-parser extension pushes a fully-decoded
// SnapshotDump). Distinct from Writer's append Save* methods: ReplaceSnapshot is
// TRANSACTIONAL and IDEMPOTENT — within one tx it DELETEs every fact row of the
// given snapshot_ts across ALL 11 fact tables, then bulk-INSERTs the bundle. A
// re-push of the same snapshot yields the same row set (no duplicates).
//
// PostgreSQL-only in this build: the SQLite adapter intentionally does NOT
// implement it, so a SQLite-backed server answers POST /snapshot with 501 (the v2
// snapshot server runs against PostgreSQL; SQLite remains the v1 /capture path).
// handleSnapshot discovers support via a `Writer.(SnapshotReplacer)` type-assertion,
// so SQLite stays untouched — no stub, no forced method (Interface Segregation).
// DiscardWriter implements it so --mock/--dry-run exercise the full /snapshot path.
//
// The return is a per-table row-count map (keyed by the short table labels:
// positions/ads/cards/prices/details/stocks/meta/options/compositions/sizes/colors),
// not the unexported tableCounts — a cross-package method cannot name an unexported
// wbscraper type, and the dump's own `counts` is already a string→int map.
type SnapshotReplacer interface {
	// ReplaceSnapshot atomically replaces all 11 fact tables' rows for snapshot,
	// returning the per-table counts actually inserted. search_queries is NOT
	// touched here (the handler upserts queries and remaps query_id beforehand).
	ReplaceSnapshot(ctx context.Context, snapshot SnapshotTs, d Decoded) (map[string]int, error)
}
