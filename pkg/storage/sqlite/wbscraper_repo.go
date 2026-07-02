package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

// Compile-time assertion: SQLiteSalesRepository satisfies wbscraper.Writer.
var _ wbscraper.Writer = (*SQLiteSalesRepository)(nil)

// wbscraperChunkSize is the max fact rows inserted per transaction.
const wbscraperChunkSize = 500

// UpsertQuery inserts the search query if its text is new and returns the stable
// id; or returns the existing id if the text is already present. Idempotent across
// sessions (UNIQUE on query text).
//
// Two-step (INSERT ... ON CONFLICT DO NOTHING, then SELECT): SQLite's ON CONFLICT
// DO NOTHING does not return the id in either branch, so a follow-up SELECT by the
// UNIQUE column is the authoritative id source. No transaction is needed — the
// constraint guarantees the row exists after the INSERT (inserted or pre-existing),
// and two concurrent upserts of the same text both resolve to one id.
func (r *SQLiteSalesRepository) UpsertQuery(ctx context.Context, q wbscraper.SearchQuery) (int64, error) {
	if _, err := r.db.ExecContext(ctx, upsertSearchQuerySQL, q.Query, q.Subject, q.Gender, q.Season, q.Age); err != nil {
		return 0, fmt.Errorf("upsert search query %q: %w", q.Query, err)
	}
	var id int64
	if err := r.db.QueryRowContext(ctx, selectSearchQueryIDSQL, q.Query).Scan(&id); err != nil {
		return 0, fmt.Errorf("select search query id for %q: %w", q.Query, err)
	}
	return id, nil
}

const (
	upsertSearchQuerySQL = `
INSERT INTO search_queries (query, subject, gender, season, age, created_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(query) DO NOTHING`

	selectSearchQueryIDSQL = `SELECT query_id FROM search_queries WHERE query = ?`

	insertSearchPositionSQL = `
INSERT INTO search_positions (
    snapshot_ts, query_id, region_dest, page, position, nm_id, brand, supplier_id, panel_promo_id,
    price_basic, price_product, rating, feedbacks
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertVitrineAdSQL = `
INSERT INTO vitrine_ads (
    snapshot_ts, query_id, advertiser_name, advertiser_inn, erid, promo_id, banner_type, creative_url, landing_href
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertCompetitorCardSQL = `
INSERT INTO competitor_cards (
    snapshot_ts, query_id, nm_id, brand, supplier, supplier_id, rating, feedbacks, pics, weight, volume, colors, subject_id, panel_promo_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertCompetitorCardPriceSQL = `
INSERT INTO competitor_card_prices (
    snapshot_ts, query_id, nm_id, size_name, price_basic, price_product, wh_id, delivery_days
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	insertCompetitorCardDetailSQL = `
INSERT INTO competitor_card_details (
    snapshot_ts, query_id, nm_id, total_quantity, promotions
) VALUES (?, ?, ?, ?, ?)`

	insertCompetitorCardStockSQL = `
INSERT INTO competitor_card_stocks (
    snapshot_ts, query_id, nm_id, size_name, wh_id, qty, time1, time2
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
)

// SaveStorefrontPositions appends a batch of search-result ranking rows.
// (Name diverges from the table to avoid colliding with searchvis.SaveSearchPositions
// on this shared receiver — see the wbscraper.Writer godoc.)
func (r *SQLiteSalesRepository) SaveStorefrontPositions(ctx context.Context, rows []wbscraper.SearchPosition) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertSearchPositionSQL, rows,
		func(stmt *sql.Stmt, p wbscraper.SearchPosition) error {
			_, err := stmt.Exec(
				string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.RegionDest, p.Page, p.Position, p.NmID,
				p.Brand, p.SupplierID, p.PanelPromoID, p.PriceBasic, p.PriceProduct, p.Rating, p.Feedbacks,
			)
			return err
		})
}

// SaveVitrineAds appends a batch of banner-ad rows.
func (r *SQLiteSalesRepository) SaveVitrineAds(ctx context.Context, rows []wbscraper.VitrineAd) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertVitrineAdSQL, rows,
		func(stmt *sql.Stmt, a wbscraper.VitrineAd) error {
			_, err := stmt.Exec(
				string(a.SnapshotTs), wbscraper.QueryIDValue(a.QueryID), a.AdvertiserName, a.AdvertiserINN, a.Erid,
				a.PromoID, a.BannerType, a.CreativeURL, a.LandingHref,
			)
			return err
		})
}

// SaveCompetitorCards appends a batch of core card rows.
func (r *SQLiteSalesRepository) SaveCompetitorCards(ctx context.Context, rows []wbscraper.CompetitorCard) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertCompetitorCardSQL, rows,
		func(stmt *sql.Stmt, c wbscraper.CompetitorCard) error {
			_, err := stmt.Exec(
				string(c.SnapshotTs), wbscraper.QueryIDValue(c.QueryID), c.NmID, c.Brand, c.Supplier, c.SupplierID,
				c.Rating, c.Feedbacks, c.Pics, c.Weight, c.Volume, c.Colors, c.SubjectID, c.PanelPromoID,
			)
			return err
		})
}

// SaveCompetitorCardPrices appends a batch of per-size price rows.
func (r *SQLiteSalesRepository) SaveCompetitorCardPrices(ctx context.Context, rows []wbscraper.CompetitorCardPrice) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertCompetitorCardPriceSQL, rows,
		func(stmt *sql.Stmt, p wbscraper.CompetitorCardPrice) error {
			_, err := stmt.Exec(
				string(p.SnapshotTs), wbscraper.QueryIDValue(p.QueryID), p.NmID, p.SizeName, p.PriceBasic, p.PriceProduct,
				p.WhID, p.DeliveryDays,
			)
			return err
		})
}

// SaveCompetitorCardDetails appends a batch of /detail aggregate rows.
func (r *SQLiteSalesRepository) SaveCompetitorCardDetails(ctx context.Context, rows []wbscraper.CompetitorCardDetail) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertCompetitorCardDetailSQL, rows,
		func(stmt *sql.Stmt, d wbscraper.CompetitorCardDetail) error {
			_, err := stmt.Exec(
				string(d.SnapshotTs), wbscraper.QueryIDValue(d.QueryID), d.NmID, d.TotalQuantity, d.Promotions,
			)
			return err
		})
}

// SaveCompetitorCardStocks appends a batch of per-wh stock rows.
func (r *SQLiteSalesRepository) SaveCompetitorCardStocks(ctx context.Context, rows []wbscraper.CompetitorCardStock) (int, error) {
	return wbscraperInsertAll(ctx, r.db, insertCompetitorCardStockSQL, rows,
		func(stmt *sql.Stmt, s wbscraper.CompetitorCardStock) error {
			_, err := stmt.Exec(
				string(s.SnapshotTs), wbscraper.QueryIDValue(s.QueryID), s.NmID, s.SizeName, s.WhID, s.Qty, s.Time1, s.Time2,
			)
			return err
		})
}

// wbscraperInsertAll inserts rows in wbscraperChunkSize'd transactions via a
// prepared single-row INSERT, calling bind to emit each row's args. All six fact
// tables share this exact shape (plain append-only INSERT, no conflict clause),
// so one generic helper replaces six near-identical chunk loops (Rule 0 — reuse,
// DRY). Nullable pointer fields (*int / *int64) are passed straight through;
// database/sql maps nil → NULL.
func wbscraperInsertAll[T any](
	ctx context.Context,
	db *sql.DB,
	insertSQL string,
	rows []T,
	bind func(stmt *sql.Stmt, v T) error,
) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	db.Exec("PRAGMA synchronous = OFF")
	defer db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(rows); i += wbscraperChunkSize {
		end := i + wbscraperChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, fmt.Errorf("begin transaction: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("prepare insert: %w", err)
		}
		for _, v := range chunk {
			if err := bind(stmt, v); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return 0, fmt.Errorf("insert row at chunk offset %d: %w", i, err)
			}
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("close statement: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit: %w", err)
		}
		total += len(chunk)
	}
	return total, nil
}
