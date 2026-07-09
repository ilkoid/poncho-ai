package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

// ============================================================================
// Unit tests (no DB) — guard the insert-prefix ↔ colCount contract.
//
// wbscraperPgInsertAll passes a literal colCount to BuildMultiRowInsert, which
// generates $1..$(rows*colCount) placeholders. A mismatch between that literal
// and the actual column list in the matching prefix constant is a runtime PG
// error ("INSERT has more/fewer expressions than target columns") that only
// surfaces at the first real insert. These tests catch it at `go test` time.
// Mirrors sales_cols_test.go (TestInsertSaleRowCols_MatchesColumnCount).
// ============================================================================

// countColumns parses the "(a, b, c)" column list out of an INSERT VALUES prefix
// and returns the column count. Same flat-comma-split approach as sales_cols_test.
func countColumns(t *testing.T, prefix string) int {
	t.Helper()
	start := strings.Index(prefix, "(")
	end := strings.Index(prefix, ")")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("cannot locate column list parentheses in prefix:\n%s", prefix)
	}
	return len(strings.Split(prefix[start+1:end], ","))
}

// TestPgWbscraperInsertColCounts verifies every INSERT prefix's column count
// matches the literal colCount passed to wbscraperPgInsertAll in repo.go. If you
// add/drop a column in a prefix, update BOTH the prefix AND the matching entry
// here (which mirrors the colCount literal in Save*/ReplaceSnapshot).
func TestPgWbscraperInsertColCounts(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		// want mirrors the colCount literal passed to wbscraperPgInsertAll for this
		// table in Save* (v1 append) and ReplaceSnapshot (v2 replace).
		want int
	}{
		{"search_positions", pgInsertSearchPositionPrefix, 14},
		{"vitrine_ads", pgInsertVitrineAdPrefix, 9},
		{"competitor_cards", pgInsertCompetitorCardPrefix, 15},
		{"competitor_card_prices", pgInsertCompetitorCardPricePrefix, 8},
		{"competitor_card_details", pgInsertCompetitorCardDetailPrefix, 5},
		{"competitor_card_stocks", pgInsertCompetitorCardStockPrefix, 8},
		{"competitor_card_meta", pgInsertCompetitorCardMetaPrefix, 25},
		{"competitor_card_options", pgInsertCompetitorCardOptionPrefix, 9},
		{"competitor_card_compositions", pgInsertCompetitorCardCompositionPrefix, 5},
		{"competitor_card_sizes", pgInsertCompetitorCardSizePrefix, 8},
		{"competitor_card_colors", pgInsertCompetitorCardColorPrefix, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countColumns(t, c.prefix); got != c.want {
				t.Errorf("%s: prefix has %d columns but colCount literal = %d — "+
					"BuildMultiRowInsert will generate wrong $N placeholders → "+
					"runtime SQL error on first insert", c.name, got, c.want)
			}
		})
	}
}

// Local pointer helpers (the wbscraper_test package's pgIntPtr/pgInt64Ptr are not
// visible here). Kept private to avoid colliding with any sibling-test helpers.
func pgIntPtr(v int) *int       { return &v }
func pgInt64Ptr(v int64) *int64 { return &v }

// ============================================================================
// Integration tests (real PostgreSQL). Gated on WB_TEST_PG so `go test ./...`
// skips them by default (CI-safe). Enable against wb_data_test (NEVER prod):
//
//	WB_TEST_PG=1 PG_PWD=... go test ./pkg/storage/postgres/ -run TestPgWbscraper -v
//
// Coordinates come from inst.md: 192.168.10.7:15432 / postgres / $PG_PWD. The DSN
// is built via the SAME production path (config.V2StorageConfig.GetEffectiveDSN),
// so env overrides PGHOST/PGPORT/PGUSER/PGDATABASE apply. PG_PWD must hold the
// real password (the production injectPassword path reads it). Each test uses a
// unique snapshot_ts and cleans its rows from all 11 fact tables on completion.
// ============================================================================

// pgEnabled reports whether the WB_TEST_PG gate is set to a truthy value.
func pgEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WB_TEST_PG"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// newPgTestRepo opens a pool against wb_data_test, builds a PgWbscraperRepo, and
// ensures the schema. It skips the test (no failure) when WB_TEST_PG is unset or
// the DB is unreachable. Cleanup closes the pool.
func newPgTestRepo(t *testing.T) (*PgWbscraperRepo, *Pool) {
	t.Helper()
	if !pgEnabled() {
		t.Skipf("skipping PostgreSQL integration test: set WB_TEST_PG=1 (and PG_PWD) to run")
	}
	ctx := context.Background()

	// Same DSN construction the server uses (config.V2StorageConfig → BuildPgDSN +
	// injectPassword). PG_PWD must be set in the environment for injectPassword.
	dsn, err := config.V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_test"}.GetEffectiveDSN()
	if err != nil {
		t.Skipf("skipping: cannot build PG DSN (is PG_PWD set?): %v", err)
	}
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot reach wb_data_test (%v) — is the PG test DB up?", err)
	}
	t.Cleanup(pool.Close)

	repo := NewPgWbscraperRepo(pool.DB())
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return repo, pool
}

// uniqueSnapshot stamps each test's rows with a distinct snapshot_ts so tests
// never collide. Uses t.Name() + nanos; sanitized to keep it a plain string.
func uniqueSnapshot(t *testing.T) wbscraper.SnapshotTs {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	return wbscraper.SnapshotTs(fmt.Sprintf("TEST-%s-%d", name, time.Now().UnixNano()))
}

// cleanupSnapshot deletes the snapshot's rows from every fact table, isolating
// tests even when run repeatedly against a shared wb_data_test. search_queries is
// intentionally NOT touched (shared dimension; re-upsert is idempotent anyway).
func cleanupSnapshot(t *testing.T, pool *Pool, snap wbscraper.SnapshotTs) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range wbscraperSnapshotTables {
		if _, err := pool.DB().Exec(ctx, `DELETE FROM `+tbl+` WHERE snapshot_ts = $1`, string(snap)); err != nil {
			t.Errorf("cleanup %s: %v", tbl, err)
		}
	}
}

// countWhere returns the row count in table for the given snapshot_ts.
func countWhere(t *testing.T, pool *Pool, table string, snap wbscraper.SnapshotTs) int {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := pool.DB().QueryRow(ctx,
		`SELECT COUNT(*) FROM `+table+` WHERE snapshot_ts = $1`, string(snap)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPgWbscraper_InitSchema verifies InitSchema is idempotent (a second call must
// not fail — CREATE TABLE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS) and that all
// 12 wbscraper tables (1 dimension + 11 facts) exist.
func TestPgWbscraper_InitSchema(t *testing.T) {
	repo, pool := newPgTestRepo(t)
	ctx := context.Background()

	// A second InitSchema must succeed (idempotent migration path).
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("second InitSchema (idempotency): %v", err)
	}

	want := append([]string{"search_queries"}, wbscraperSnapshotTables...)
	for _, tbl := range want {
		var exists bool
		err := pool.DB().QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, "public."+tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s existence: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s missing after InitSchema", tbl)
		}
	}
}

// TestPgWbscraper_UpsertQueryIdempotent verifies the dimension upsert: identical
// query text → identical id (within and across calls), different text → different id.
func TestPgWbscraper_UpsertQueryIdempotent(t *testing.T) {
	repo, _ := newPgTestRepo(t)
	ctx := context.Background()

	id1, err := repo.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "TEST-pg-ups-кроссовки", Subject: "обувь"})
	if err != nil || id1 == wbscraper.NoQuery {
		t.Fatalf("first upsert: id=%d err=%v (want non-zero, no err)", id1, err)
	}
	if id2, err := repo.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "TEST-pg-ups-кроссовки"}); err != nil || id2 != id1 {
		t.Fatalf("repeat upsert: id=%d err=%v, want %d (idempotent)", id2, err, id1)
	}
	id3, err := repo.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "TEST-pg-ups-рюкзаки"})
	if err != nil || id3 == wbscraper.NoQuery || id3 == id1 {
		t.Fatalf("distinct upsert: id=%d err=%v, want distinct non-zero", id3, err)
	}
}

// TestPgWbscraper_ReplaceSnapshotRoundTrip inserts one row of each fact table via
// ReplaceSnapshot and verifies the counts returned, that the rows landed, that a
// NoQuery (0) query_id is stored as NULL, and that kopecks survive exactly.
func TestPgWbscraper_ReplaceSnapshotRoundTrip(t *testing.T) {
	repo, pool := newPgTestRepo(t)
	ctx := context.Background()
	snap := uniqueSnapshot(t)
	cleanupSnapshot(t, pool, snap)

	qid, err := repo.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "TEST-pg-rt-кроссовки", Season: "зима"})
	if err != nil {
		t.Fatalf("upsert query: %v", err)
	}

	d := wbscraper.Decoded{
		SearchPositions: []wbscraper.SearchPosition{{
			SnapshotTs: snap, QueryID: qid, RegionDest: pgIntPtr(8038), Page: 1, Position: 5,
			NmID: 17401163, Name: "Кроссовки детские беговые", Brand: "Nike",
			PanelPromoID: pgInt64Ptr(777), PriceBasic: 100000, PriceProduct: 89900, Rating: 4.5, Feedbacks: 123,
		}},
		VitrineAds: []wbscraper.VitrineAd{{
			SnapshotTs: snap, QueryID: qid, AdvertiserName: "ООО Рога", AdvertiserINN: "7700123456",
			Erid: "abcdef", PromoID: pgInt64Ptr(42), BannerType: "catalog", CreativeURL: "https://x/c.jpg",
		}},
		CompetitorCards: []wbscraper.CompetitorCard{{
			SnapshotTs: snap, QueryID: wbscraper.NoQuery, NmID: 17401163, Name: "Кроссовки детские беговые",
			Brand: "Nike", Supplier: "ООО Рога", SupplierID: pgInt64Ptr(123), Rating: 4.5, Feedbacks: 123,
			Pics: 5, Weight: 0.5, Volume: 2, SubjectID: pgInt64Ptr(8), PanelPromoID: pgInt64Ptr(777),
		}},
		CompetitorCardPrices: []wbscraper.CompetitorCardPrice{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, SizeName: "42",
			PriceBasic: 100000, PriceProduct: 89900, WhID: pgInt64Ptr(507),
		}},
		CompetitorCardDetails: []wbscraper.CompetitorCardDetail{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, TotalQuantity: 250, Promotions: `[{"name":"Скидка"}]`,
		}},
		CompetitorCardStocks: []wbscraper.CompetitorCardStock{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, SizeName: "42", WhID: pgInt64Ptr(507),
			Qty: 10, Time1: pgIntPtr(1720000000), Time2: pgIntPtr(1720003600),
		}},
		CompetitorCardMeta: []wbscraper.CompetitorCardMeta{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, VendorCode: "22123456",
			ImtName: "Кроссовки беговые", BrandName: "Nike", PhotoCount: 6,
		}},
		CompetitorCardOptions: []wbscraper.CompetitorCardOption{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, CharName: "Состав", CharValue: "хлопок 100%",
		}},
		CompetitorCardCompositions: []wbscraper.CompetitorCardComposition{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, Name: "хлопок 60%", Ord: 0,
		}},
		CompetitorCardSizes: []wbscraper.CompetitorCardSize{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, TechSize: "128", PropName: "RU", PropValue: "128",
		}},
		CompetitorCardColors: []wbscraper.CompetitorCardColor{{
			SnapshotTs: snap, QueryID: qid, NmID: 17401163, ColorNmID: 999, Ord: 0,
		}},
	}

	counts, err := repo.ReplaceSnapshot(ctx, snap, d)
	if err != nil {
		t.Fatalf("ReplaceSnapshot: %v (counts=%v)", err, counts)
	}
	for label, want := range map[string]int{
		"positions": 1, "ads": 1, "cards": 1, "prices": 1, "details": 1, "stocks": 1,
		"meta": 1, "options": 1, "compositions": 1, "sizes": 1, "colors": 1,
	} {
		if got := counts[label]; got != want {
			t.Errorf("counts[%q] = %d, want %d", label, got, want)
		}
	}

	// Each fact table landed exactly one row.
	for _, tbl := range wbscraperSnapshotTables {
		if got := countWhere(t, pool, tbl, snap); got != 1 {
			t.Errorf("%s row count = %d, want 1", tbl, got)
		}
	}

	// NoQuery (0) → NULL, not 0 (the storage-layer mapping under test).
	if got := countWhere(t, pool, "competitor_cards", snap); got != 1 {
		t.Fatalf("setup: competitor_cards count = %d, want 1", got)
	}
	var nullCards int
	if err := pool.DB().QueryRow(ctx,
		`SELECT COUNT(*) FROM competitor_cards WHERE snapshot_ts=$1 AND query_id IS NULL`, string(snap)).Scan(&nullCards); err != nil {
		t.Fatalf("count NULL query_id: %v", err)
	}
	if nullCards != 1 {
		t.Errorf("competitor_cards with NULL query_id = %d, want 1 (NoQuery→NULL)", nullCards)
	}

	// Kopecks preserved exactly.
	var priceProduct int64
	if err := pool.DB().QueryRow(ctx,
		`SELECT price_product FROM competitor_card_prices WHERE snapshot_ts=$1 AND size_name='42'`, string(snap)).Scan(&priceProduct); err != nil {
		t.Fatalf("select price_product: %v", err)
	}
	if priceProduct != 89900 {
		t.Errorf("price_product = %d, want 89900 kopecks", priceProduct)
	}
}

// TestPgWbscraper_ReplaceSnapshotIdempotent pushes the same snapshot twice and
// verifies the row counts are unchanged (DELETE WHERE snapshot_ts + INSERT in one
// tx yields no duplicates). This is the v2 /snapshot retry-safety contract.
func TestPgWbscraper_ReplaceSnapshotIdempotent(t *testing.T) {
	repo, pool := newPgTestRepo(t)
	ctx := context.Background()
	snap := uniqueSnapshot(t)
	cleanupSnapshot(t, pool, snap)

	d := wbscraper.Decoded{
		CompetitorCards: []wbscraper.CompetitorCard{
			{SnapshotTs: snap, NmID: 1001, Brand: "A"},
			{SnapshotTs: snap, NmID: 1002, Brand: "B"},
		},
		CompetitorCardPrices: []wbscraper.CompetitorCardPrice{
			{SnapshotTs: snap, NmID: 1001, SizeName: "S", PriceProduct: 5000},
			{SnapshotTs: snap, NmID: 1001, SizeName: "M", PriceProduct: 5500},
			{SnapshotTs: snap, NmID: 1002, SizeName: "L", PriceProduct: 6000},
		},
	}
	if _, err := repo.ReplaceSnapshot(ctx, snap, d); err != nil {
		t.Fatalf("first ReplaceSnapshot: %v", err)
	}
	// Re-push the SAME snapshot_ts (e.g. a retry after a network blip).
	if _, err := repo.ReplaceSnapshot(ctx, snap, d); err != nil {
		t.Fatalf("second ReplaceSnapshot: %v", err)
	}

	if got := countWhere(t, pool, "competitor_cards", snap); got != 2 {
		t.Errorf("competitor_cards after 2x replace = %d, want 2 (idempotent, no duplicates)", got)
	}
	if got := countWhere(t, pool, "competitor_card_prices", snap); got != 3 {
		t.Errorf("competitor_card_prices after 2x replace = %d, want 3 (idempotent, no duplicates)", got)
	}
}

// TestPgWbscraper_ReplaceSnapshotDistinctSnapshotsCoexist verifies two different
// snapshot_ts do not erase each other (the DELETE scope is one snapshot_ts only).
func TestPgWbscraper_ReplaceSnapshotDistinctSnapshotsCoexist(t *testing.T) {
	repo, pool := newPgTestRepo(t)
	ctx := context.Background()
	snap1 := uniqueSnapshot(t) + "-A"
	snap2 := uniqueSnapshot(t) + "-B"
	cleanupSnapshot(t, pool, snap1)
	cleanupSnapshot(t, pool, snap2)

	if _, err := repo.ReplaceSnapshot(ctx, snap1, wbscraper.Decoded{
		CompetitorCards: []wbscraper.CompetitorCard{{SnapshotTs: snap1, NmID: 1, Brand: "A"}},
	}); err != nil {
		t.Fatalf("replace snap1: %v", err)
	}
	if _, err := repo.ReplaceSnapshot(ctx, snap2, wbscraper.Decoded{
		CompetitorCards: []wbscraper.CompetitorCard{{SnapshotTs: snap2, NmID: 2, Brand: "B"}},
	}); err != nil {
		t.Fatalf("replace snap2: %v", err)
	}

	if got := countWhere(t, pool, "competitor_cards", snap1); got != 1 {
		t.Errorf("snap1 rows = %d, want 1 (snap2 must not erase snap1)", got)
	}
	if got := countWhere(t, pool, "competitor_cards", snap2); got != 1 {
		t.Errorf("snap2 rows = %d, want 1", got)
	}
}

// TestPgWbscraper_NameFieldPersisted confirms the v2 `name` product-title column
// (added to search_positions and competitor_cards) survives the /snapshot path
// end-to-end: the v2 extension always emits it, and PG now stores it.
func TestPgWbscraper_NameFieldPersisted(t *testing.T) {
	repo, pool := newPgTestRepo(t)
	ctx := context.Background()
	snap := uniqueSnapshot(t)
	cleanupSnapshot(t, pool, snap)

	const wantName = "Бейсболка детская летняя"
	// search_positions.query_id is NOT NULL (a position always comes from a query),
	// so this row needs a real query_id; competitor_cards.query_id is nullable, so it
	// can stay NoQuery (→ NULL) — exercising both column nullabilities.
	qid, err := repo.UpsertQuery(ctx, wbscraper.SearchQuery{Query: "TEST-pg-name-бейсболки"})
	if err != nil {
		t.Fatalf("upsert query: %v", err)
	}
	if _, err := repo.ReplaceSnapshot(ctx, snap, wbscraper.Decoded{
		SearchPositions: []wbscraper.SearchPosition{{
			SnapshotTs: snap, QueryID: qid, NmID: 555, Name: wantName, Brand: "Cap",
		}},
		CompetitorCards: []wbscraper.CompetitorCard{{
			SnapshotTs: snap, QueryID: wbscraper.NoQuery, NmID: 555, Name: wantName, Brand: "Cap",
		}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}

	var posName, cardName string
	if err := pool.DB().QueryRow(ctx,
		`SELECT name FROM search_positions WHERE snapshot_ts=$1 AND nm_id=555`, string(snap)).Scan(&posName); err != nil {
		t.Fatalf("select search_positions.name: %v", err)
	}
	if err := pool.DB().QueryRow(ctx,
		`SELECT name FROM competitor_cards WHERE snapshot_ts=$1 AND nm_id=555`, string(snap)).Scan(&cardName); err != nil {
		t.Fatalf("select competitor_cards.name: %v", err)
	}
	if posName != wantName {
		t.Errorf("search_positions.name = %q, want %q", posName, wantName)
	}
	if cardName != wantName {
		t.Errorf("competitor_cards.name = %q, want %q", cardName, wantName)
	}
}
