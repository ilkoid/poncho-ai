package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// stagingTestSchema creates the minimal cards + measurement_penalties tables (only
// the columns the staging queries touch) and the fixer's own staging table. This
// validates the PG→SQLite port end-to-end on :memory: with no WB and no PG.
const stagingTestSchema = `
CREATE TABLE IF NOT EXISTS cards (
    nm_id       INTEGER PRIMARY KEY,
    vendor_code TEXT NOT NULL DEFAULT '',
    dim_length  REAL NOT NULL DEFAULT 0,
    dim_width   REAL NOT NULL DEFAULT 0,
    dim_height  REAL NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS measurement_penalties (
    nm_id        INTEGER NOT NULL,
    width        INTEGER NOT NULL DEFAULT 0,
    length       INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    dim_id       INTEGER NOT NULL,
    dt_bonus     TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    is_valid     INTEGER NOT NULL DEFAULT 1
);` + stagingSchemaDDL

// newStagingTestDB opens an in-memory SQLite (single connection — required for
// :memory: under database/sql) with the test schema seeded.
func newStagingTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	db.SetMaxOpenConns(1) // single conn → single in-memory DB across all queries
	if _, err := db.Exec(stagingTestSchema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return db
}

// addPenalty inserts one measurement_penalties row (is_valid=1 by default).
func addPenalty(t *testing.T, db *sql.DB, nmID, w, l, h, dimID int, dtBonus string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO measurement_penalties (nm_id, width, length, height, dim_id, dt_bonus) VALUES (?,?,?,?,?,?)`,
		nmID, w, l, h, dimID, dtBonus)
	if err != nil {
		t.Fatalf("insert penalty nm=%d: %v", nmID, err)
	}
}

// addCard inserts one cards row with the card's current dimensions.
func addCard(t *testing.T, db *sql.DB, nmID int, vc string, l, w, h float64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO cards (nm_id, vendor_code, dim_length, dim_width, dim_height) VALUES (?,?,?,?,?)`,
		nmID, vc, l, w, h)
	if err != nil {
		t.Fatalf("insert card nm=%d: %v", nmID, err)
	}
}

// TestFetchStageCandidates_WindowPicksLatest verifies the ROW_NUMBER() window picks
// the freshest measurement per nm_id (dt_bonus DESC) — the SQLite replacement for
// PostgreSQL's DISTINCT ON.
func TestFetchStageCandidates_WindowPicksLatest(t *testing.T) {
	db := newStagingTestDB(t)
	defer db.Close()
	ctx := context.Background()

	addCard(t, db, 100, "VC100", 30, 31, 7)
	// Two confirmed measurements for nm 100; the newer dt_bonus must win.
	addPenalty(t, db, 100, 28, 30, 5, 111, "2026-04-01T00:00:00Z") // older
	addPenalty(t, db, 100, 30, 29, 6, 222, "2026-04-11T00:00:00Z") // newer (latest)

	rows, err := fetchStageCandidates(ctx, db, ArticleFilter{})
	if err != nil {
		t.Fatalf("fetchStageCandidates: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// Latest measurement is 30×29×6 (dim_id 222), not the older 28×30×5.
	if rows[0].DimID != 222 {
		t.Errorf("picked dim_id=%d, want 222 (latest by dt_bonus DESC)", rows[0].DimID)
	}
	if rows[0].NewLength != 29 || rows[0].NewWidth != 30 || rows[0].NewHeight != 6 {
		t.Errorf("new dims = %g×%g×%g, want 29×30×6", rows[0].NewLength, rows[0].NewWidth, rows[0].NewHeight)
	}
	// Card dims land in old_* (current card state).
	if rows[0].OldLength != 30 || rows[0].OldWidth != 31 || rows[0].OldHeight != 7 {
		t.Errorf("old dims = %g×%g×%g, want 30×31×7", rows[0].OldLength, rows[0].OldWidth, rows[0].OldHeight)
	}
}

// TestUpsertStaging_DirectionAware verifies staging classifies cards by direction:
// over-declared + exact → skipped, under-declared → pending (the heart of the fixer).
func TestUpsertStaging_DirectionAware(t *testing.T) {
	db := newStagingTestDB(t)
	defer db.Close()
	ctx := context.Background()

	// nm 100: card 30×31×7 ≥ meas 29×30×6 → OVER → skipped (the original bug case).
	addCard(t, db, 100, "VC100", 30, 31, 7)
	addPenalty(t, db, 100, 30, 29, 6, 222, "2026-04-11T00:00:00Z")
	// nm 200: card 35×30×12 < meas 43×42×12 → UNDER → pending (real fix).
	addCard(t, db, 200, "VC200", 35, 30, 12)
	addPenalty(t, db, 200, 43, 42, 12, 333, "2026-04-11T00:00:00Z")
	// nm 300: exact match → skipped.
	addCard(t, db, 300, "VC300", 10, 10, 10)
	addPenalty(t, db, 300, 10, 10, 10, 444, "2026-04-11T00:00:00Z")

	rows, err := fetchStageCandidates(ctx, db, ArticleFilter{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	pending, skipped, err := upsertStaging(ctx, db, rows, "2026-04-26 00:00:00")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if pending != 1 || skipped != 2 {
		t.Fatalf("pending=%d skipped=%d, want 1/2", pending, skipped)
	}

	got, err := selectPending(ctx, db)
	if err != nil {
		t.Fatalf("selectPending: %v", err)
	}
	if len(got) != 1 || got[0].NmID != 200 {
		t.Errorf("pending = %+v, want single nm_id=200", got)
	}

	counts, err := stagingCounts(ctx, db)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts["pending"] != 1 || counts["skipped"] != 2 {
		t.Errorf("counts = %+v, want pending=1 skipped=2", counts)
	}
}

// TestUpsertStaging_IdempotentRefresh verifies a second stage run (DELETE+INSERT)
// recomputes status — a card already matching the measurement stays skipped, and
// pending counts don't accumulate across runs.
func TestUpsertStaging_IdempotentRefresh(t *testing.T) {
	db := newStagingTestDB(t)
	defer db.Close()
	ctx := context.Background()

	addCard(t, db, 200, "VC200", 35, 30, 12)
	addPenalty(t, db, 200, 43, 42, 12, 333, "2026-04-11T00:00:00Z")

	rows, _ := fetchStageCandidates(ctx, db, ArticleFilter{})
	if _, _, err := upsertStaging(ctx, db, rows, "t1"); err != nil {
		t.Fatal(err)
	}
	// Simulate the fix applied: card now matches the measurement.
	if _, err := db.Exec(`UPDATE cards SET dim_length=43, dim_width=42, dim_height=12 WHERE nm_id=200`); err != nil {
		t.Fatal(err)
	}

	rows2, _ := fetchStageCandidates(ctx, db, ArticleFilter{})
	pending, skipped, err := upsertStaging(ctx, db, rows2, "t2")
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 || skipped != 1 {
		t.Fatalf("after fix: pending=%d skipped=%d, want 0/1 (idempotent)", pending, skipped)
	}
}

// TestBuildCardFilter_INExpansion verifies SQLite IN/NOT IN expansion produces one
// ? per value with args in order (SQLite has no ANY()/ALL()).
func TestBuildCardFilter_INExpansion(t *testing.T) {
	frag, args := buildCardFilter(ArticleFilter{
		NmIDs:              []int{7, 8, 9},
		VendorCodes:        []string{"A", "B"},
		ExcludeVendorCodes: []string{"Z"},
	})
	if got := countStr(frag, "?"); got != 6 {
		t.Errorf("placeholder count = %d, want 6 (3+2+1)", got)
	}
	wantFrag := " AND l.nm_id IN (?,?,?)" + " AND c.vendor_code IN (?,?)" + " AND c.vendor_code NOT IN (?)"
	if frag != wantFrag {
		t.Errorf("frag = %q, want %q", frag, wantFrag)
	}
	wantArgs := []any{7, 8, 9, "A", "B", "Z"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args len = %d, want %d", len(args), len(wantArgs))
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}
}

func countStr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

// TestRunStage_EmptyDB_Graceful verifies standalone --stage on a fresh fixer.db
// degrades gracefully: ensureReadSchemas creates the read tables (no 'no such table'),
// and runStage prints a clear hint + returns (0,0,nil) instead of crashing. Before the
// robustness fix this panicked the wrapper with "no such table: measurement_penalties".
func TestRunStage_EmptyDB_Graceful(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Bare DB — no tables yet (mimics fresh fixer.db before any downloader runs).
	if err := ensureReadSchemas(ctx, db); err != nil {
		t.Fatalf("ensureReadSchemas: %v", err)
	}
	if err := initStagingSchema(ctx, db); err != nil {
		t.Fatalf("initStagingSchema: %v", err)
	}

	pending, skipped, err := runStage(ctx, db, &Config{}, nil)
	if err != nil {
		t.Fatalf("runStage on empty DB returned err: %v (want nil — graceful)", err)
	}
	if pending != 0 || skipped != 0 {
		t.Errorf("pending=%d skipped=%d, want 0/0", pending, skipped)
	}
}
