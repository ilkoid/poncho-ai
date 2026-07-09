package wbscraper

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// snapshotFixture builds a minimal-but-complete SnapshotDump: one search query
// (browser query_id=42), one row in each of the 11 fact tables carrying 42, plus a
// null-query_id card to prove NoQuery survives the remap. Every content table is
// represented so the roundtrip covers Этап A's full capture (meta/options/
// compositions/sizes/colors).
func snapshotFixture(snapshot string) snapshotDump {
	q42 := int64(42)
	return snapshotDump{
		GeneratedAt: "2026-07-08T10:00:00Z",
		Snapshot:    snapshot,
		Counts:      map[string]int{},
		SearchQueries: []SearchQuery{
			{QueryID: q42, Query: "кроссовки", Subject: "Обувь", Brand: "Nike", Material: "текстиль"},
		},
		SearchPositions: []SearchPosition{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, Page: 1, Position: 1, NmID: 111, PriceProduct: 89900},
		},
		VitrineAds: []VitrineAd{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, AdvertiserName: "ООО Тест"},
		},
		CompetitorCards: []CompetitorCard{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, Brand: "Nike"},
			// direct nmId target — no query → stays NoQuery (0) through the remap.
			{SnapshotTs: SnapshotTs(snapshot), QueryID: NoQuery, NmID: 222, Brand: "Adidas"},
		},
		CompetitorCardPrices: []CompetitorCardPrice{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, SizeName: "42", PriceProduct: 89900},
		},
		CompetitorCardDetails: []CompetitorCardDetail{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, TotalQuantity: 50},
		},
		CompetitorCardStocks: []CompetitorCardStock{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, SizeName: "42", Qty: 5},
		},
		CompetitorCardMeta: []CompetitorCardMeta{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, VendorCode: "22123456", ImtName: "Кроссовки", BrandName: "Nike", PhotoCount: 6},
		},
		CompetitorCardOptions: []CompetitorCardOption{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, CharName: "Цвет", CharValue: "черный", GroupName: "Основная информация"},
		},
		CompetitorCardCompositions: []CompetitorCardComposition{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, Name: "текстиль 100%", Ord: 0},
		},
		CompetitorCardSizes: []CompetitorCardSize{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, TechSize: "39", PropName: "RU", PropValue: "39", PropOrder: 0},
		},
		CompetitorCardColors: []CompetitorCardColor{
			{SnapshotTs: SnapshotTs(snapshot), QueryID: q42, NmID: 111, ColorNmID: 999, Ord: 0},
		},
	}
}

// postSnapshot POSTs a fixture and decodes the snapshotResponse.
func postSnapshot(t *testing.T, base string, dump snapshotDump) snapshotResponse {
	t.Helper()
	resp := postSnapshotRaw(t, base, dump)
	defer resp.Body.Close()
	var sr snapshotResponse
	json.NewDecoder(resp.Body).Decode(&sr)
	return sr
}

// postSnapshotRaw POSTs a fixture and returns the raw response (for header checks). Caller closes Body.
func postSnapshotRaw(t *testing.T, base string, dump snapshotDump) *http.Response {
	t.Helper()
	body, _ := json.Marshal(dump)
	resp, err := http.Post(base+"/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /snapshot: %v", err)
	}
	return resp
}

// TestSnapshotReplaceRoundtrip verifies the full v2 push path: the fixture (with
// every content table) decodes, the query upserts, and ReplaceSnapshot reports the
// expected per-table counts. The DiscardWriter exercises the transport without a DB.
func TestSnapshotReplaceRoundtrip(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	sr := postSnapshot(t, ts.URL, snapshotFixture("2026-07-08T10:00:00Z"))
	want := map[string]int{
		"positions": 1, "ads": 1, "cards": 2, "prices": 1, "details": 1, "stocks": 1,
		"meta": 1, "options": 1, "compositions": 1, "sizes": 1, "colors": 1,
	}
	for k, v := range want {
		if sr.Counts[k] != v {
			t.Errorf("counts[%q] = %d, want %d", k, sr.Counts[k], v)
		}
	}
	if sr.Snapshot != "2026-07-08T10:00:00Z" {
		t.Errorf("snapshot = %q, want the fixture stamp", sr.Snapshot)
	}
}

// TestSnapshotReplaceIdempotent verifies a re-push of the same snapshot yields the
// same counts (the replace contract: DELETE+INSERT, no accumulation). The DiscardWriter
// has no DB, but its counts reflect exactly what it received — two identical pushes
// must produce identical maps (a real repo's second push would delete+reinsert).
func TestSnapshotReplaceIdempotent(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	first := postSnapshot(t, ts.URL, snapshotFixture("2026-07-08T10:00:00Z"))
	second := postSnapshot(t, ts.URL, snapshotFixture("2026-07-08T10:00:00Z"))
	for k, v := range first.Counts {
		if second.Counts[k] != v {
			t.Errorf("idempotency broken: counts[%q] first=%d second=%d", k, v, second.Counts[k])
		}
	}
}

// TestSnapshotDistinctSnapshotsCoexist checks the replace scope is ONE snapshot_ts:
// pushing a second snapshot must not zero the first's counts (they coexist). We can't
// see rows in DiscardWriter, but we assert the two pushes are independent calls that
// each report their own counts — the scoping is the repo's DELETE WHERE snapshot_ts,
// verified at the PG level by the repo's own contract (here we just confirm the handler
// dispatches each snapshot's stamp faithfully).
func TestSnapshotDistinctSnapshotsCoexist(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	a := postSnapshot(t, ts.URL, snapshotFixture("2026-07-08T10:00:00Z"))
	b := postSnapshot(t, ts.URL, snapshotFixture("2026-07-08T11:00:00Z"))
	if a.Snapshot == b.Snapshot {
		t.Fatalf("snapshots not distinct: %q", a.Snapshot)
	}
	if b.Counts["meta"] != 1 {
		t.Errorf("second snapshot meta=%d, want 1 (independent of first)", b.Counts["meta"])
	}
}

// TestRemapSnapshotQueryIDs covers the browser→server query_id rewrite directly (a
// pure function): a known browser id is remapped, an unknown one collapses to NoQuery
// (no orphan FK), and NoQuery stays NoQuery.
func TestRemapSnapshotQueryIDs(t *testing.T) {
	remap := map[int64]int64{42: 7, 100: 8}
	d := Decoded{
		SearchPositions:            []SearchPosition{{QueryID: 42}, {QueryID: 100}},
		VitrineAds:                 []VitrineAd{{QueryID: 42}},
		CompetitorCards:            []CompetitorCard{{QueryID: 42}, {QueryID: NoQuery}, {QueryID: 999}},
		CompetitorCardPrices:       []CompetitorCardPrice{{QueryID: 42}},
		CompetitorCardDetails:      []CompetitorCardDetail{{QueryID: 100}},
		CompetitorCardStocks:       []CompetitorCardStock{{QueryID: 42}},
		CompetitorCardMeta:         []CompetitorCardMeta{{QueryID: 42}},
		CompetitorCardOptions:      []CompetitorCardOption{{QueryID: 42}},
		CompetitorCardCompositions: []CompetitorCardComposition{{QueryID: 42}},
		CompetitorCardSizes:        []CompetitorCardSize{{QueryID: 100}},
		CompetitorCardColors:       []CompetitorCardColor{{QueryID: 42}},
	}
	remapSnapshotQueryIDs(&d, remap)

	if d.CompetitorCards[0].QueryID != 7 {
		t.Errorf("known browser qid 42 → %d, want server 7", d.CompetitorCards[0].QueryID)
	}
	if d.CompetitorCards[1].QueryID != NoQuery {
		t.Errorf("NoQuery → %d, want %d (unchanged)", d.CompetitorCards[1].QueryID, NoQuery)
	}
	if d.CompetitorCards[2].QueryID != NoQuery {
		t.Errorf("unknown browser qid 999 → %d, want NoQuery (collapse, no orphan FK)", d.CompetitorCards[2].QueryID)
	}
	if d.SearchPositions[1].QueryID != 8 {
		t.Errorf("browser qid 100 → %d, want server 8", d.SearchPositions[1].QueryID)
	}
}

// TestSnapshotMethodNotAllowed confirms GET /snapshot is rejected (POST-only).
func TestSnapshotMethodNotAllowed(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/snapshot")
	if err != nil {
		t.Fatalf("GET /snapshot: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /snapshot status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

// TestCORS verifies the browser-extension push can reach /snapshot cross-origin: a
// preflight OPTIONS gets 204 + the ACAO header, and the POST response also carries it.
// Without this the MV3 SW fetch is blocked by CORS before the snapshot ever lands.
func TestCORS(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	// preflight
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/snapshot", nil)
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("Access-Control-Request-Method", "POST")
	pf, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /snapshot: %v", err)
	}
	pf.Body.Close()
	if pf.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", pf.StatusCode, http.StatusNoContent)
	}
	if got := pf.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("preflight ACAO = %q, want *", got)
	}

	// actual POST also carries ACAO (the browser checks it on the real response too)
	resp := postSnapshotRaw(t, ts.URL, snapshotFixture("2026-07-08T10:00:00Z"))
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("POST ACAO = %q, want *", got)
	}
}

// TestSnapshotBackendNotSupported verifies a backend that does NOT implement
// SnapshotReplacer (the SQLite case) answers /snapshot with 501, not a crash.
// writerOnlyStub satisfies Writer but intentionally omits ReplaceSnapshot.
func TestSnapshotBackendNotSupported(t *testing.T) {
	s, err := NewServer(context.Background(), "127.0.0.1:0", &writerOnlyStub{}, nil, ServerOptions{
		Snapshot: "2026-07-08T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	body, _ := json.Marshal(snapshotFixture("2026-07-08T10:00:00Z"))
	resp, err := http.Post(ts.URL+"/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /snapshot: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d (backend without SnapshotReplacer)", resp.StatusCode, http.StatusNotImplemented)
	}
}

// ----------------------------------------------------------------------------
// ipFilter — allowlist middleware
// ----------------------------------------------------------------------------

// TestAllowed covers the allowlist logic directly with crafted RemoteAddrs: empty
// allowlist permits all; exact IP and CIDR match; a non-listed IP is denied.
func TestAllowed(t *testing.T) {
	cases := []struct {
		name      string
		allowedIP []string
		remote    string
		want      bool
	}{
		{"empty allowlist = allow all", nil, "8.8.8.8:1", true},
		{"exact match", []string{"127.0.0.1"}, "127.0.0.1:54321", true},
		{"exact mismatch", []string{"10.0.0.1"}, "127.0.0.1:54321", false},
		{"CIDR contains", []string{"10.0.0.0/8"}, "10.5.6.7:9", true},
		{"CIDR excludes", []string{"10.0.0.0/8"}, "192.168.1.1:9", false},
		{"malformed remote denied", []string{"127.0.0.1"}, "not-an-ip", false},
		{"malformed entry skipped (never grants)", []string{"not-a-cidr", "127.0.0.1"}, "10.1.1.1:1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{allowedIPs: c.allowedIP}
			r := &http.Request{RemoteAddr: c.remote}
			if got := s.allowed(r); got != c.want {
				t.Errorf("allowed(%q) with allowlist %v = %v, want %v", c.remote, c.allowedIP, got, c.want)
			}
		})
	}
}

// TestIPFilterHTTP verifies the middleware gates a real request: with allowed_ips
// including localhost, /snapshot returns 200; with an allowlist that excludes
// localhost, every route returns 403.
func TestIPFilterHTTP(t *testing.T) {
	allow := newTestServer(t, nil, func(o *ServerOptions) { o.AllowedIPs = []string{"127.0.0.1"} })
	tsAllow := httptest.NewServer(allow.routes())
	defer tsAllow.Close()
	if sr := postSnapshot(t, tsAllow.URL, snapshotFixture("2026-07-08T10:00:00Z")); sr.Counts["cards"] != 2 {
		t.Errorf("allow-listed localhost: cards=%d, want 2", sr.Counts["cards"])
	}

	deny := newTestServer(t, nil, func(o *ServerOptions) { o.AllowedIPs = []string{"10.0.0.1"} })
	tsDeny := httptest.NewServer(deny.routes())
	defer tsDeny.Close()
	body, _ := json.Marshal(snapshotFixture("2026-07-08T10:00:00Z"))
	resp, err := http.Post(tsDeny.URL+"/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /snapshot: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("deny-listed localhost status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// ----------------------------------------------------------------------------
// writerOnlyStub — a Writer that is NOT a SnapshotReplacer (the SQLite stand-in).
// ----------------------------------------------------------------------------

type writerOnlyStub struct{}

func (writerOnlyStub) UpsertQuery(context.Context, SearchQuery) (int64, error) { return 1, nil }
func (writerOnlyStub) SaveStorefrontPositions(context.Context, []SearchPosition) (int, error) {
	return 0, nil
}
func (writerOnlyStub) SaveVitrineAds(context.Context, []VitrineAd) (int, error) { return 0, nil }
func (writerOnlyStub) SaveCompetitorCards(context.Context, []CompetitorCard) (int, error) {
	return 0, nil
}
func (writerOnlyStub) SaveCompetitorCardPrices(context.Context, []CompetitorCardPrice) (int, error) {
	return 0, nil
}
func (writerOnlyStub) SaveCompetitorCardDetails(context.Context, []CompetitorCardDetail) (int, error) {
	return 0, nil
}
func (writerOnlyStub) SaveCompetitorCardStocks(context.Context, []CompetitorCardStock) (int, error) {
	return 0, nil
}
