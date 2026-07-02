package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

// Compile-time assertion: PgWbscraperRepo implements wbscraper.Writer.
var _ wbscraper.Writer = (*PgWbscraperRepo)(nil)

// pgWbscraperChunkSize caps rows per multi-row INSERT. Widest table is 14 cols →
// 500×14 = 7000 params, well under the 65535 PG limit enforced by BuildMultiRowInsert.
const pgWbscraperChunkSize = 500

// PgWbscraperRepo implements wbscraper.Writer for PostgreSQL. A focused repo (ISP),
// like PgOrdersRepo — no god-object; each domain gets its own.
type PgWbscraperRepo struct {
	pool *pgxpool.Pool
}

// NewPgWbscraperRepo creates a new PostgreSQL wb-scraper repository.
func NewPgWbscraperRepo(pool *pgxpool.Pool) *PgWbscraperRepo {
	return &PgWbscraperRepo{pool: pool}
}

// InitSchema creates the wb-scraper tables if they don't exist.
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
// touch — it changes nothing but makes PG return the id.
func (r *PgWbscraperRepo) UpsertQuery(ctx context.Context, q wbscraper.SearchQuery) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, pgUpsertSearchQuerySQL, q.Query, q.Subject, q.Gender, q.Season, q.Age).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert search query %q: %w", q.Query, err)
	}
	return id, nil
}

const pgUpsertSearchQuerySQL = `
INSERT INTO search_queries (query, subject, gender, season, age)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (query) DO UPDATE SET query = EXCLUDED.query
RETURNING query_id`

// SaveStorefrontPositions appends a batch of search-result ranking rows.
func (r *PgWbscraperRepo) SaveStorefrontPositions(ctx context.Context, rows []wbscraper.SearchPosition) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertSearchPositionPrefix, 13, rows,
		func(p wbscraper.SearchPosition) []any {
			return []any{
				string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.RegionDest, p.Page, p.Position, p.NmID,
				p.Brand, p.SupplierID, p.PanelPromoID, p.PriceBasic, p.PriceProduct, p.Rating, p.Feedbacks,
			}
		})
}

// SaveVitrineAds appends a batch of banner-ad rows.
func (r *PgWbscraperRepo) SaveVitrineAds(ctx context.Context, rows []wbscraper.VitrineAd) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertVitrineAdPrefix, 9, rows,
		func(a wbscraper.VitrineAd) []any {
			return []any{
				string(a.SnapshotTs), wbscraper.QueryIDValue(a.QueryID), a.AdvertiserName, a.AdvertiserINN, a.Erid,
				a.PromoID, a.BannerType, a.CreativeURL, a.LandingHref,
			}
		})
}

// SaveCompetitorCards appends a batch of core card rows.
func (r *PgWbscraperRepo) SaveCompetitorCards(ctx context.Context, rows []wbscraper.CompetitorCard) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardPrefix, 14, rows,
		func(c wbscraper.CompetitorCard) []any {
			return []any{
				string(c.SnapshotTs), wbscraper.QueryIDValue(c.QueryID), c.NmID, c.Brand, c.Supplier, c.SupplierID,
				c.Rating, c.Feedbacks, c.Pics, c.Weight, c.Volume, c.Colors, c.SubjectID, c.PanelPromoID,
			}
		})
}

// SaveCompetitorCardPrices appends a batch of per-size price rows.
func (r *PgWbscraperRepo) SaveCompetitorCardPrices(ctx context.Context, rows []wbscraper.CompetitorCardPrice) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardPricePrefix, 8, rows,
		func(p wbscraper.CompetitorCardPrice) []any {
			return []any{
				string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.NmID, p.SizeName, p.PriceBasic, p.PriceProduct,
				p.WhID, p.DeliveryDays,
			}
		})
}

// SaveCompetitorCardDetails appends a batch of /detail aggregate rows.
func (r *PgWbscraperRepo) SaveCompetitorCardDetails(ctx context.Context, rows []wbscraper.CompetitorCardDetail) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardDetailPrefix, 5, rows,
		func(d wbscraper.CompetitorCardDetail) []any {
			return []any{
				string(d.SnapshotTs), wbscraper.QueryIDValue(d.QueryID), d.NmID, d.TotalQuantity, d.Promotions,
			}
		})
}

// SaveCompetitorCardStocks appends a batch of per-wh stock rows.
func (r *PgWbscraperRepo) SaveCompetitorCardStocks(ctx context.Context, rows []wbscraper.CompetitorCardStock) (int, error) {
	return wbscraperPgInsertAll(ctx, r.pool, pgInsertCompetitorCardStockPrefix, 8, rows,
		func(s wbscraper.CompetitorCardStock) []any {
			return []any{
				string(s.SnapshotTs), wbscraper.QueryIDValue(s.QueryID), s.NmID, s.SizeName, s.WhID, s.Qty, s.Time1, s.Time2,
			}
		})
}

// Multi-row INSERT prefixes (append-only → no ON CONFLICT clause; BuildMultiRowInsert
// appends the empty conflict string). Column order MUST match each argsOf above.
const (
	pgInsertSearchPositionPrefix = `INSERT INTO search_positions (
    snapshot_ts, query_id, region_dest, page, position, nm_id, brand, supplier_id, panel_promo_id,
    price_basic, price_product, rating, feedbacks
) VALUES `

	pgInsertVitrineAdPrefix = `INSERT INTO vitrine_ads (
    snapshot_ts, query_id, advertiser_name, advertiser_inn, erid, promo_id, banner_type, creative_url, landing_href
) VALUES `

	pgInsertCompetitorCardPrefix = `INSERT INTO competitor_cards (
    snapshot_ts, query_id, nm_id, brand, supplier, supplier_id, rating, feedbacks, pics, weight, volume, colors, subject_id, panel_promo_id
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
)

// wbscraperPgInsertAll inserts rows in pgWbscraperChunkSize'd batches, building one
// multi-row INSERT per chunk via BuildMultiRowInsert. All six fact tables share
// this shape (append-only, no conflict clause), so one generic helper replaces six
// near-identical loops (Rule 0). Nullable pointer fields are passed straight
// through; pgx maps nil → NULL.
func wbscraperPgInsertAll[T any](
	ctx context.Context,
	pool *pgxpool.Pool,
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

		tag, err := pool.Exec(ctx, query, args...)
		if err != nil {
			return total, fmt.Errorf("insert batch (size %d) at offset %d: %w", len(chunk), i, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}
