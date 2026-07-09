package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

// Compile-time assertions: PgWbscraperRepo satisfies BOTH the v1 append Writer and
// the v2 replace-by-snapshot SnapshotReplacer (the SQLite adapter satisfies only
// Writer — /snapshot is PostgreSQL-only in this build).
var (
	_ wbscraper.Writer           = (*PgWbscraperRepo)(nil)
	_ wbscraper.SnapshotReplacer = (*PgWbscraperRepo)(nil)
)

// pgWbscraperChunkSize caps rows per multi-row INSERT. Widest table is competitor_card_meta
// at 25 cols → 500×25 = 12500 params, well under the 65535 PG limit enforced by BuildMultiRowInsert.
const pgWbscraperChunkSize = 500

// pgExecer is satisfied by both *pgxpool.Pool (v1 Save* append path) and pgx.Tx
// (v2 ReplaceSnapshot), which share an identical Exec signature. Defining it at the
// use site lets the single generic insert helper serve both — no duplicate *Tx variant
// and no duplicated args mappers (Interface Segregation at the helper level).
type pgExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PgWbscraperRepo implements wbscraper.Writer + wbscraper.SnapshotReplacer for PostgreSQL.
// A focused repo (ISP), like PgOrdersRepo — no god-object; each domain gets its own.
type PgWbscraperRepo struct {
	pool *pgxpool.Pool
}

// NewPgWbscraperRepo creates a new PostgreSQL wb-scraper repository.
func NewPgWbscraperRepo(pool *pgxpool.Pool) *PgWbscraperRepo {
	return &PgWbscraperRepo{pool: pool}
}

// InitSchema creates the wb-scraper tables if they don't exist (and runs v2 migrations).
func (r *PgWbscraperRepo) InitSchema(ctx context.Context) error {
	return initWbscraperSchema(ctx, r.pool)
}

// UpsertQuery inserts the search query if its text is new and returns the stable
// id; or returns the existing id if the text is already present. Idempotent across
// sessions (UNIQUE on query text).
//
// One-shot in PG (unlike SQLite's two-step): ON CONFLICT DO UPDATE touches the row
// even on conflict, so RETURNING yields the id in both the insert and the
// already-exists branch. The "SET query = EXCLUDED.query" is an intentional no-op
// touch — it changes nothing but makes PG return the id. The v2 cartesian axes
// (brand/material/purpose/comment) are carried through so a re-pushed snapshot keeps
// its provenance; v1 callers leave them "".
func (r *PgWbscraperRepo) UpsertQuery(ctx context.Context, q wbscraper.SearchQuery) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, pgUpsertSearchQuerySQL,
		q.Query, q.Subject, q.Brand, q.Gender, q.Season, q.Age, q.Material, q.Purpose, q.Comment).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert search query %q: %w", q.Query, err)
	}
	return id, nil
}

const pgUpsertSearchQuerySQL = `
INSERT INTO search_queries (query, subject, brand, gender, season, age, material, purpose, comment)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (query) DO UPDATE SET query = EXCLUDED.query
RETURNING query_id`

// SaveStorefrontPositions appends a batch of search-result ranking rows.
func (r *PgWbscraperRepo) SaveStorefrontPositions(ctx context.Context, rows []wbscraper.SearchPosition) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertSearchPositionPrefix, 14, rows, pgSearchPositionArgs)
}

// SaveVitrineAds appends a batch of banner-ad rows.
func (r *PgWbscraperRepo) SaveVitrineAds(ctx context.Context, rows []wbscraper.VitrineAd) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertVitrineAdPrefix, 9, rows, pgVitrineAdArgs)
}

// SaveCompetitorCards appends a batch of core card rows.
func (r *PgWbscraperRepo) SaveCompetitorCards(ctx context.Context, rows []wbscraper.CompetitorCard) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardPrefix, 15, rows, pgCompetitorCardArgs)
}

// SaveCompetitorCardPrices appends a batch of per-size price rows.
func (r *PgWbscraperRepo) SaveCompetitorCardPrices(ctx context.Context, rows []wbscraper.CompetitorCardPrice) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardPricePrefix, 8, rows, pgCompetitorCardPriceArgs)
}

// SaveCompetitorCardDetails appends a batch of /detail aggregate rows.
func (r *PgWbscraperRepo) SaveCompetitorCardDetails(ctx context.Context, rows []wbscraper.CompetitorCardDetail) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardDetailPrefix, 5, rows, pgCompetitorCardDetailArgs)
}

// SaveCompetitorCardStocks appends a batch of per-wh stock rows.
func (r *PgWbscraperRepo) SaveCompetitorCardStocks(ctx context.Context, rows []wbscraper.CompetitorCardStock) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardStockPrefix, 8, rows, pgCompetitorCardStockArgs)
}

// ReplaceSnapshot atomically replaces ALL 11 fact tables' rows for one snapshot_ts,
// implementing wbscraper.SnapshotReplacer (the v2 /snapshot path). One transaction:
// DELETE every row of the snapshot across the 11 tables, then bulk-INSERT the bundle
// in pgWbscraperChunkSize'd batches. A re-push of the same snapshot yields the same
// row set (idempotent). search_queries is NOT touched — the handler upserts queries
// and remaps query_id before calling this.
//
// Returns a per-table count map (rows inserted) keyed by the short labels. On error
// the transaction is rolled back (defer), so a partial replace never lands.
func (r *PgWbscraperRepo) ReplaceSnapshot(ctx context.Context, snapshot wbscraper.SnapshotTs, d wbscraper.Decoded) (map[string]int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	snap := string(snapshot)
	for _, tbl := range wbscraperSnapshotTables {
		if _, err := tx.Exec(ctx, `DELETE FROM `+tbl+` WHERE snapshot_ts = $1`, snap); err != nil {
			return nil, fmt.Errorf("delete %s for snapshot %s: %w", tbl, snap, err)
		}
	}

	counts := map[string]int{}
	var firstErr error

	// Each insert runs on the tx (pgExecer) via the shared helper. Counts capture
	// rows actually inserted; on the first error we stop and return counts + err.
	counts["positions"], err = wbscraperPgInsertAll(ctx, tx, pgInsertSearchPositionPrefix, 14, d.SearchPositions, pgSearchPositionArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("positions: %w", err)
	}
	counts["ads"], err = wbscraperPgInsertAll(ctx, tx, pgInsertVitrineAdPrefix, 9, d.VitrineAds, pgVitrineAdArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("ads: %w", err)
	}
	counts["cards"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardPrefix, 15, d.CompetitorCards, pgCompetitorCardArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("cards: %w", err)
	}
	counts["prices"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardPricePrefix, 8, d.CompetitorCardPrices, pgCompetitorCardPriceArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("prices: %w", err)
	}
	counts["details"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardDetailPrefix, 5, d.CompetitorCardDetails, pgCompetitorCardDetailArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("details: %w", err)
	}
	counts["stocks"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardStockPrefix, 8, d.CompetitorCardStocks, pgCompetitorCardStockArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("stocks: %w", err)
	}
	counts["meta"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardMetaPrefix, 25, d.CompetitorCardMeta, pgCompetitorCardMetaArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("meta: %w", err)
	}
	counts["options"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardOptionPrefix, 9, d.CompetitorCardOptions, pgCompetitorCardOptionArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("options: %w", err)
	}
	counts["compositions"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardCompositionPrefix, 5, d.CompetitorCardCompositions, pgCompetitorCardCompositionArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("compositions: %w", err)
	}
	counts["sizes"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardSizePrefix, 8, d.CompetitorCardSizes, pgCompetitorCardSizeArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("sizes: %w", err)
	}
	counts["colors"], err = wbscraperPgInsertAll(ctx, tx, pgInsertCompetitorCardColorPrefix, 5, d.CompetitorCardColors, pgCompetitorCardColorArgs)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("colors: %w", err)
	}
	if firstErr != nil {
		return counts, firstErr
	}

	if err := tx.Commit(ctx); err != nil {
		return counts, fmt.Errorf("commit: %w", err)
	}
	return counts, nil
}

// wbscraperSnapshotTables is the full set of snapshot_ts-scoped fact tables emptied
// then refilled by ReplaceSnapshot. Order is cosmetic (each DELETE is independent,
// scoped to one snapshot); listed storefront tables first, then the v2 content tables.
var wbscraperSnapshotTables = []string{
	"search_positions", "vitrine_ads", "competitor_cards",
	"competitor_card_prices", "competitor_card_details", "competitor_card_stocks",
	"competitor_card_meta", "competitor_card_options", "competitor_card_compositions",
	"competitor_card_sizes", "competitor_card_colors",
}

// Multi-row INSERT prefixes (append-only → no ON CONFLICT clause; BuildMultiRowInsert
// appends the empty conflict string). Column order MUST match each args mapper below.
const (
	pgInsertSearchPositionPrefix = `INSERT INTO search_positions (
    snapshot_ts, query_id, region_dest, page, position, nm_id, name, brand, supplier_id, panel_promo_id,
    price_basic, price_product, rating, feedbacks
) VALUES `

	pgInsertVitrineAdPrefix = `INSERT INTO vitrine_ads (
    snapshot_ts, query_id, advertiser_name, advertiser_inn, erid, promo_id, banner_type, creative_url, landing_href
) VALUES `

	pgInsertCompetitorCardPrefix = `INSERT INTO competitor_cards (
    snapshot_ts, query_id, nm_id, name, brand, supplier, supplier_id, rating, feedbacks, pics, weight, volume, colors, subject_id, panel_promo_id
) VALUES `

	pgInsertCompetitorCardPricePrefix = `INSERT INTO competitor_card_prices (
    snapshot_ts, query_id, nm_id, size_name, price_basic, price_product, wh_id, delivery_days
) VALUES `

	pgInsertCompetitorCardDetailPrefix = `INSERT INTO competitor_card_details (
    snapshot_ts, query_id, nm_id, total_quantity, promotions
) VALUES `

	pgInsertCompetitorCardStockPrefix = `INSERT INTO competitor_card_stocks (
    snapshot_ts, query_id, nm_id, size_name, wh_id, qty, time1, time2
) VALUES `

	pgInsertCompetitorCardMetaPrefix = `INSERT INTO competitor_card_meta (
    snapshot_ts, query_id, nm_id, vendor_code, subj_name, subj_root_name, description, need_kiz,
    create_date, update_date, imt_id, imt_name, slug, brand_name, brand_hash, supplier_id,
    photo_count, has_video, subject_id, subject_root_id, nm_colors_names, contents,
    has_seller_recommendations, user_flags, kinds
) VALUES `

	pgInsertCompetitorCardOptionPrefix = `INSERT INTO competitor_card_options (
    snapshot_ts, query_id, nm_id, char_name, char_value, charc_type, is_variable, variable_values, group_name
) VALUES `

	pgInsertCompetitorCardCompositionPrefix = `INSERT INTO competitor_card_compositions (
    snapshot_ts, query_id, nm_id, name, ord
) VALUES `

	pgInsertCompetitorCardSizePrefix = `INSERT INTO competitor_card_sizes (
    snapshot_ts, query_id, nm_id, tech_size, chrt_id, prop_name, prop_value, prop_order
) VALUES `

	pgInsertCompetitorCardColorPrefix = `INSERT INTO competitor_card_colors (
    snapshot_ts, query_id, nm_id, color_nm_id, ord
) VALUES `
)

// Args mappers — one per table, package-level so the v1 Save* append path and the v2
// ReplaceSnapshot tx path share them. Column order MUST match the prefix constants.
// Nullable pointer fields (*int/*int64) are passed straight through; pgx maps nil→NULL.
// query_id routes through wbscraper.QueryIDValue so NoQuery (0) → NULL.

func pgSearchPositionArgs(p wbscraper.SearchPosition) []any {
	return []any{
		string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.RegionDest, p.Page, p.Position, p.NmID, p.Name,
		p.Brand, p.SupplierID, p.PanelPromoID, p.PriceBasic, p.PriceProduct, p.Rating, p.Feedbacks,
	}
}

func pgVitrineAdArgs(a wbscraper.VitrineAd) []any {
	return []any{
		string(a.SnapshotTs), wbscraper.QueryIDValue(a.QueryID), a.AdvertiserName, a.AdvertiserINN, a.Erid,
		a.PromoID, a.BannerType, a.CreativeURL, a.LandingHref,
	}
}

func pgCompetitorCardArgs(c wbscraper.CompetitorCard) []any {
	return []any{
		string(c.SnapshotTs), wbscraper.QueryIDValue(c.QueryID), c.NmID, c.Name, c.Brand, c.Supplier, c.SupplierID,
		c.Rating, c.Feedbacks, c.Pics, c.Weight, c.Volume, c.Colors, c.SubjectID, c.PanelPromoID,
	}
}

func pgCompetitorCardPriceArgs(p wbscraper.CompetitorCardPrice) []any {
	return []any{
		string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.NmID, p.SizeName, p.PriceBasic, p.PriceProduct,
		p.WhID, p.DeliveryDays,
	}
}

func pgCompetitorCardDetailArgs(d wbscraper.CompetitorCardDetail) []any {
	return []any{
		string(d.SnapshotTs), wbscraper.QueryIDValue(d.QueryID), d.NmID, d.TotalQuantity, d.Promotions,
	}
}

func pgCompetitorCardStockArgs(s wbscraper.CompetitorCardStock) []any {
	return []any{
		string(s.SnapshotTs), wbscraper.QueryIDValue(s.QueryID), s.NmID, s.SizeName, s.WhID, s.Qty, s.Time1, s.Time2,
	}
}

func pgCompetitorCardMetaArgs(m wbscraper.CompetitorCardMeta) []any {
	return []any{
		string(m.SnapshotTs), wbscraper.QueryIDValue(m.QueryID), m.NmID, m.VendorCode, m.SubjName, m.SubjRootName,
		m.Description, m.NeedKiz, m.CreateDate, m.UpdateDate, m.ImtID, m.ImtName, m.Slug, m.BrandName, m.BrandHash,
		m.SupplierID, m.PhotoCount, m.HasVideo, m.SubjectID, m.SubjectRootID, m.NmColorsNames, m.Contents,
		m.HasSellerRecommendations, m.UserFlags, m.Kinds,
	}
}

func pgCompetitorCardOptionArgs(o wbscraper.CompetitorCardOption) []any {
	return []any{
		string(o.SnapshotTs), wbscraper.QueryIDValue(o.QueryID), o.NmID, o.CharName, o.CharValue, o.CharcType,
		o.IsVariable, o.VariableValues, o.GroupName,
	}
}

func pgCompetitorCardCompositionArgs(c wbscraper.CompetitorCardComposition) []any {
	return []any{
		string(c.SnapshotTs), wbscraper.QueryIDValue(c.QueryID), c.NmID, c.Name, c.Ord,
	}
}

func pgCompetitorCardSizeArgs(s wbscraper.CompetitorCardSize) []any {
	return []any{
		string(s.SnapshotTs), wbscraper.QueryIDValue(s.QueryID), s.NmID, s.TechSize, s.ChrtID, s.PropName, s.PropValue, s.PropOrder,
	}
}

func pgCompetitorCardColorArgs(c wbscraper.CompetitorCardColor) []any {
	return []any{
		string(c.SnapshotTs), wbscraper.QueryIDValue(c.QueryID), c.NmID, c.ColorNmID, c.Ord,
	}
}

// wbscraperPgInsertAll inserts rows in pgWbscraperChunkSize'd batches, building one
// multi-row INSERT per chunk via BuildMultiRowInsert. Works on both a *pgxpool.Pool
// (v1 Save*) and a pgx.Tx (v2 ReplaceSnapshot) via the pgExecer interface. All eleven
// fact tables share this shape (plain INSERT, no conflict clause), so one generic
// helper serves them (Rule 0). Nullable pointer fields are passed straight through;
// pgx maps nil → NULL.
func wbscraperPgInsertAll[T any](
	ctx context.Context,
	exec pgExecer,
	prefix string,
	colCount int,
	rows []T,
	argsOf func(v T) []any,
) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	total := 0
	for i := 0; i < len(rows); i += pgWbscraperChunkSize {
		end := i + pgWbscraperChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		query := BuildMultiRowInsert(prefix, "", len(chunk), colCount)
		args := make([]any, 0, len(chunk)*colCount)
		for _, v := range chunk {
			args = append(args, argsOf(v)...)
		}

		tag, err := exec.Exec(ctx, query, args...)
		if err != nil {
			return total, fmt.Errorf("insert batch (size %d) at offset %d: %w", len(chunk), i, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}
