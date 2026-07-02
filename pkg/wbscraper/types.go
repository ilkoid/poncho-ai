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
type SearchQuery struct {
	// QueryID is 0 on input (auto-assigned by UpsertQuery); populated on return.
	QueryID int64 `json:"query_id"`
	// Query is the normalized search text (the WB search box string).
	Query string `json:"query"`
	// Subject, Gender, Season, Age record how the constructor assembled the query,
	// so analysis can filter by dimension without parsing the text.
	Subject string `json:"subject"`
	Gender  string `json:"gender"`
	Season  string `json:"season"`
	Age     string `json:"age"`
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

// Decoded is the per-table row bundle produced by Decode for one Intercept.
// Decode routes on Intercept.Kind and fills only the relevant slices; the others
// stay nil. The flush stage persists each non-empty slice via the matching Writer
// method. All rows inherit QueryID from the source Intercept.
type Decoded struct {
	SearchPositions       []SearchPosition       `json:"search_positions"`
	VitrineAds            []VitrineAd            `json:"vitrine_ads"`
	CompetitorCards       []CompetitorCard       `json:"competitor_cards"`
	CompetitorCardPrices  []CompetitorCardPrice  `json:"competitor_card_prices"`
	CompetitorCardDetails []CompetitorCardDetail `json:"competitor_card_details"`
	CompetitorCardStocks  []CompetitorCardStock  `json:"competitor_card_stocks"`
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
	Weight       int64      `json:"weight"`
	Volume       int64      `json:"volume"`
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
