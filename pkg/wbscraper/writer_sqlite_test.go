package wbscraper_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

// newSQLiteRepo opens a fresh SQLiteSalesRepository — which auto-applies the
// wbscraper schema in initSchema() — in a per-test temp dir (/tmp-backed, never
// /var/db). Returned concrete so the test can both drive it as wbscraper.Writer
// and read back via DB() for round-trip assertions.
func newSQLiteRepo(t *testing.T) *sqlite.SQLiteSalesRepository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wbscraper_test.db")
	repo, err := sqlite.NewSQLiteSalesRepository(path)
	if err != nil {
		t.Fatalf("open sqlite repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

// countOf is a tiny helper for round-trip row-count assertions.
func countOf(t *testing.T, repo *sqlite.SQLiteSalesRepository, query string, args ...any) int {
	t.Helper()
	var c int
	if err := repo.DB().QueryRowContext(context.Background(), query, args...).Scan(&c); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return c
}

// TestSQLiteWriterUpsertQueryIdempotent verifies the dimension upsert: identical
// query text → identical id (within a session and across re-upserts), different
// text → different id. This is the cross-session join key.
func TestSQLiteWriterUpsertQueryIdempotent(t *testing.T) {
	ctx := context.Background()
	var w wbscraper.Writer = newSQLiteRepo(t)

	id1, err := w.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "кроссовки", Subject: "кроссовки"})
	if err != nil || id1 == wbscraper.NoQuery {
		t.Fatalf("first upsert: id=%d err=%v (want non-zero, no err)", id1, err)
	}
	// Same text again → same id (idempotent, as on a re-run session).
	if id2, err := w.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "кроссовки"}); err != nil || id2 != id1 {
		t.Fatalf("repeat upsert: id=%d err=%v, want %d", id2, err, id1)
	}
	// Different text → different non-zero id.
	id3, err := w.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "рюкзаки"})
	if err != nil || id3 == wbscraper.NoQuery || id3 == id1 {
		t.Fatalf("distinct upsert: id=%d err=%v, want distinct non-zero", id3, err)
	}
}

// TestSQLiteWriterRoundTrip saves one row of each fact table and verifies the rows
// landed, that QueryID is propagated, and that a NoQuery (0) query_id is stored as
// NULL (direct nmId target) rather than 0.
func TestSQLiteWriterRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newSQLiteRepo(t)
	var w wbscraper.Writer = repo
	const ts = wbscraper.SnapshotTs("2026-07-01T10:00:00Z")

	qid, err := w.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "кроссовки", Season: "зима"})
	if err != nil {
		t.Fatalf("upsert query: %v", err)
	}

	// search_position: carries the real query_id, region/price/position filled.
	if n, err := w.SaveStorefrontPositions(ctx, []wbscraper.SearchPosition{{
		SnapshotTs: ts, QueryID: qid, RegionDest: intPtr(8038), Page: 1, Position: 5,
		NmID: 17401163, Brand: "Nike", PanelPromoID: int64Ptr(777),
		PriceBasic: 100000, PriceProduct: 89900, Rating: 4.5, Feedbacks: 123,
	}}); err != nil || n != 1 {
		t.Fatalf("SaveStorefrontPositions: n=%d err=%v", n, err)
	}

	// vitrine_ad: banner with advertiser identity.
	if n, err := w.SaveVitrineAds(ctx, []wbscraper.VitrineAd{{
		SnapshotTs: ts, QueryID: qid, AdvertiserName: "ООО Рога", AdvertiserINN: "7700123456",
		Erid: "abcdef", PromoID: int64Ptr(42), BannerType: "catalog", CreativeURL: "https://x/c.jpg",
	}}); err != nil || n != 1 {
		t.Fatalf("SaveVitrineAds: n=%d err=%v", n, err)
	}

	// competitor_card with NoQuery → must be stored as NULL query_id (provenance test).
	if n, err := w.SaveCompetitorCards(ctx, []wbscraper.CompetitorCard{{
		SnapshotTs: ts, QueryID: wbscraper.NoQuery, NmID: 17401163, Brand: "Nike", Supplier: "ООО Рога",
		SupplierID: int64Ptr(123), Rating: 4.5, Feedbacks: 123, Pics: 5, Weight: 500, Volume: 2000,
		SubjectID: int64Ptr(8), PanelPromoID: int64Ptr(777),
	}}); err != nil || n != 1 {
		t.Fatalf("SaveCompetitorCards: n=%d err=%v", n, err)
	}

	if n, err := w.SaveCompetitorCardPrices(ctx, []wbscraper.CompetitorCardPrice{{
		SnapshotTs: ts, QueryID: qid, NmID: 17401163, SizeName: "42",
		PriceBasic: 100000, PriceProduct: 89900, WhID: int64Ptr(507), DeliveryDays: intPtr(1),
	}}); err != nil || n != 1 {
		t.Fatalf("SaveCompetitorCardPrices: n=%d err=%v", n, err)
	}

	if n, err := w.SaveCompetitorCardDetails(ctx, []wbscraper.CompetitorCardDetail{{
		SnapshotTs: ts, QueryID: qid, NmID: 17401163, TotalQuantity: 250, Promotions: `[{"name":"Скидка"}]`,
	}}); err != nil || n != 1 {
		t.Fatalf("SaveCompetitorCardDetails: n=%d err=%v", n, err)
	}

	if n, err := w.SaveCompetitorCardStocks(ctx, []wbscraper.CompetitorCardStock{{
		SnapshotTs: ts, QueryID: qid, NmID: 17401163, SizeName: "42", WhID: int64Ptr(507),
		Qty: 10, Time1: intPtr(1720000000), Time2: intPtr(1720003600),
	}}); err != nil || n != 1 {
		t.Fatalf("SaveCompetitorCardStocks: n=%d err=%v", n, err)
	}

	// Every fact table received exactly one row.
	for _, table := range []string{
		"search_positions", "vitrine_ads", "competitor_cards",
		"competitor_card_prices", "competitor_card_details", "competitor_card_stocks",
	} {
		if c := countOf(t, repo, "SELECT COUNT(*) FROM "+table); c != 1 {
			t.Errorf("%s row count = %d, want 1", table, c)
		}
	}

	// NoQuery (0) → NULL, not 0 (the storage-layer mapping under test).
	if c := countOf(t, repo, "SELECT COUNT(*) FROM competitor_cards WHERE query_id IS NULL"); c != 1 {
		t.Errorf("competitor_cards with NULL query_id = %d, want 1 (NoQuery→NULL)", c)
	}

	// QueryID propagated into a fact row that carried a real id.
	var posQID int64
	if err := repo.DB().QueryRowContext(ctx,
		"SELECT query_id FROM search_positions WHERE nm_id = ?", 17401163).Scan(&posQID); err != nil {
		t.Fatalf("select position query_id: %v", err)
	}
	if posQID != qid {
		t.Errorf("search_positions.query_id = %d, want %d", posQID, qid)
	}

	// Kopecks preserved exactly.
	var priceProduct int64
	repo.DB().QueryRowContext(ctx,
		"SELECT price_product FROM competitor_card_prices WHERE size_name = ?", "42").Scan(&priceProduct)
	if priceProduct != 89900 {
		t.Errorf("price_product = %d, want 89900 kopecks", priceProduct)
	}
}

// TestSQLiteWriterChunks exercises the generic chunk helper: a batch larger than
// the 500-row chunk size must be split across transactions and all rows persisted.
func TestSQLiteWriterChunks(t *testing.T) {
	ctx := context.Background()
	repo := newSQLiteRepo(t)
	var w wbscraper.Writer = repo

	const n = 750 // 500 + 250 → two chunks
	rows := make([]wbscraper.CompetitorCardStock, n)
	for i := range rows {
		rows[i] = wbscraper.CompetitorCardStock{
			SnapshotTs: "2026-07-01T11:00:00Z", NmID: int64(i + 1), SizeName: "42", Qty: i,
		}
	}
	got, err := w.SaveCompetitorCardStocks(ctx, rows)
	if err != nil {
		t.Fatalf("SaveCompetitorCardStocks: %v", err)
	}
	if got != n {
		t.Errorf("returned count = %d, want %d", got, n)
	}
	if c := countOf(t, repo, "SELECT COUNT(*) FROM competitor_card_stocks"); c != n {
		t.Errorf("row count = %d, want %d (chunk loop dropped rows)", c, n)
	}
}
