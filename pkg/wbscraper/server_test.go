package wbscraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSessionIDFromSnapshot verifies the snapshot→digits session-id reduction used
// as the default SessionID (so the id and snapshot never disagree).
func TestSessionIDFromSnapshot(t *testing.T) {
	cases := map[string]string{
		"2026-07-02T04:10:00Z": "20260702041000",
		"":                     "0",
		"abc":                  "0",
	}
	for in, want := range cases {
		if got := sessionIDFromSnapshot(SnapshotTs(in)); got != want {
			t.Errorf("sessionIDFromSnapshot(%q) = %q, want %q", in, got, want)
		}
	}
}

// newTestServer builds a server with a DiscardWriter, FlushInterval=0 (no ticker;
// tests flush manually) and a fixed snapshot, ready to be served via httptest.
func newTestServer(t *testing.T, targets []Target, opts ...func(*ServerOptions)) *Server {
	t.Helper()
	o := ServerOptions{
		Snapshot:      "2026-07-02T04:10:00Z",
		BatchTargets:  2,
		FlushInterval: 0,
	}
	for _, opt := range opts {
		opt(&o)
	}
	s, err := NewServer(context.Background(), "127.0.0.1:0", NewDiscardWriter(), targets, o)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

// TestServerTargetsPagination verifies the pull contract: batches advance the
// cursor, search targets arrive with a stamped QueryID, a card target keeps
// NoQuery, and done flips true exactly when the cursor reaches the end.
func TestServerTargetsPagination(t *testing.T) {
	targets := []Target{
		{Kind: "search", Query: "кроссовки"},
		{Kind: "search", Query: "рюкзаки"},
		{Kind: "card", QueryID: NoQuery, URL: CardURL("17401163")},
	}
	s := newTestServer(t, targets)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	// batch 1: two search targets, not done.
	r1 := getTargets(t, ts.URL, 2)
	if len(r1.Items) != 2 || r1.Done {
		t.Fatalf("batch1 = %d items done=%v, want 2/false", len(r1.Items), r1.Done)
	}
	if r1.Items[0].QueryID == NoQuery || r1.Items[1].QueryID == NoQuery {
		t.Errorf("search targets not stamped with QueryID: %+v", r1.Items)
	}
	if r1.Items[0].Kind != "search" || r1.Items[0].Query == "" || r1.Items[0].URL == "" {
		t.Errorf("search item shape = %+v", r1.Items[0])
	}

	// batch 2: one card target, done.
	r2 := getTargets(t, ts.URL, 2)
	if len(r2.Items) != 1 || !r2.Done {
		t.Fatalf("batch2 = %d items done=%v, want 1/true", len(r2.Items), r2.Done)
	}
	if r2.Items[0].Kind != "card" || r2.Items[0].QueryID != NoQuery {
		t.Errorf("card target = %+v, want kind=card QueryID=NoQuery", r2.Items[0])
	}

	// batch 3: empty, still done.
	r3 := getTargets(t, ts.URL, 2)
	if len(r3.Items) != 0 || !r3.Done {
		t.Errorf("batch3 = %+v, want empty/done", r3)
	}
}

// TestServerCaptureAndState verifies the push→buffer→flush→state path: a /capture
// of the mock fixtures decodes to known rows; /state shows zero until a flush,
// then the persisted counts.
func TestServerCaptureAndState(t *testing.T) {
	s := newTestServer(t, []Target{{Kind: "search", Query: "кроссовки"}})
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	// push the two mock captures (search → 2 positions, card_detail → card+price+detail+stock).
	body, _ := json.Marshal(MockIntercepts())
	resp, err := http.Post(ts.URL+"/capture", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /capture: %v", err)
	}
	var cr captureResponse
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()
	if cr.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2", cr.Accepted)
	}
	if cr.Decoded.Positions != 2 || cr.Decoded.Cards != 1 || cr.Decoded.Stocks != 1 {
		t.Errorf("decoded = %+v, want positions=2 cards=1 stocks=1", cr.Decoded)
	}

	// state before flush: counts still zero (the buffer holds the rows).
	if st := getState(t, ts.URL); st.Counts.total() != 0 {
		t.Errorf("pre-flush counts = %+v, want 0", st.Counts)
	}

	// manual flush (no ticker in this test) → DiscardWriter counts accumulate.
	if _, err := s.flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	st := getState(t, ts.URL)
	if st.Counts.Positions != 2 || st.Counts.Cards != 1 || st.Counts.Stocks != 1 {
		t.Errorf("post-flush counts = %+v, want positions=2 cards=1 stocks=1", st.Counts)
	}
	if st.CapturesReceived != 2 {
		t.Errorf("capturesReceived = %d, want 2", st.CapturesReceived)
	}
}

// TestServerDoneFinalFlush verifies POST /done triggers a flush and that finalFlush
// is idempotent (POST /done then shutdown must not double-count).
func TestServerDoneFinalFlush(t *testing.T) {
	s := newTestServer(t, []Target{{Kind: "search", Query: "кроссовки"}})
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	body, _ := json.Marshal(MockIntercepts())
	resp, _ := http.Post(ts.URL+"/capture", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	resp2, err := http.Post(ts.URL+"/done", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /done: %v", err)
	}
	var dr doneResponse
	json.NewDecoder(resp2.Body).Decode(&dr)
	resp2.Body.Close()
	if !dr.OK || dr.Flushed.Positions != 2 {
		t.Errorf("done response = %+v, want ok flushed.positions=2", dr)
	}

	// idempotent: a second finalFlush is a no-op (buffer already drained + guard).
	if c, err := s.finalFlush(); err != nil || c.total() != 0 {
		t.Errorf("second finalFlush = %+v err=%v, want 0/nil", c, err)
	}
}

// TestServerDryRunFlush verifies DryRun makes flush print+count without touching
// the Writer (the buffer still accumulates identically; only the drain differs).
func TestServerDryRunFlush(t *testing.T) {
	s := newTestServer(t, []Target{{Kind: "search", Query: "кроссовки"}},
		func(o *ServerOptions) { o.DryRun = true })

	// decode the mock fixtures straight into the buffer (bypassing the handler),
	// then flush — DryRun must return the counts and print, not persist.
	for _, it := range MockIntercepts() {
		d, err := Decode(it, s.snapshot)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		mergeDecoded(&s.buf, d)
	}
	c, err := s.flush(context.Background())
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if c.Positions != 2 || c.Cards != 1 || c.Stocks != 1 {
		t.Errorf("dry-run flush counts = %+v, want positions=2 cards=1 stocks=1", c)
	}
}

// ---- helpers ----

func getTargets(t *testing.T, base string, n int) targetsResponse {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/targets?n=%d", base, n))
	if err != nil {
		t.Fatalf("GET /targets: %v", err)
	}
	defer resp.Body.Close()
	var r targetsResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return r
}

func getState(t *testing.T, base string) stateResponse {
	t.Helper()
	resp, err := http.Get(base + "/state")
	if err != nil {
		t.Fatalf("GET /state: %v", err)
	}
	defer resp.Body.Close()
	var r stateResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return r
}
