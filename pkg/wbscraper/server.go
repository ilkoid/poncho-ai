package wbscraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
)

// tableCounts tallies rows per fact table. It is the shape of the "counts" object
// in /state and of flush summaries, and accumulates across flushes for the lifetime
// of one session. JSON field names are the short table labels (not the SQL names).
type tableCounts struct {
	Positions int `json:"positions"`
	Ads       int `json:"ads"`
	Cards     int `json:"cards"`
	Prices    int `json:"prices"`
	Details   int `json:"details"`
	Stocks    int `json:"stocks"`
}

// add mutates the receiver, adding each field of o (used to fold flush results into
// the session cumulative counts).
func (c *tableCounts) add(o tableCounts) {
	c.Positions += o.Positions
	c.Ads += o.Ads
	c.Cards += o.Cards
	c.Prices += o.Prices
	c.Details += o.Details
	c.Stocks += o.Stocks
}

// total returns the sum across all fact tables (rows handled this session/flush).
func (c tableCounts) total() int {
	return c.Positions + c.Ads + c.Cards + c.Prices + c.Details + c.Stocks
}

// ServerOptions configures the collector HTTP server. Primitives are resolved from
// Config by the CLI (timeouts via ParseDuration) so the server stays free of config
// parsing — it receives ready values, matching the thin-CLI / fat-pkg split (Rule 6).
type ServerOptions struct {
	// Snapshot is the session timestamp stamped onto every decoded fact row.
	// One value per run: set by the CLI at startup, shared by all captures.
	Snapshot SnapshotTs

	// SessionID is the short id surfaced in /targets and /state. Empty → derived
	// from Snapshot (digits only), so the id and snapshot never disagree.
	SessionID string

	// BatchTargets caps items per GET /targets response (the ?n query param can
	// still request fewer). Non-positive falls back to a safe default at serve time.
	BatchTargets int

	// FlushInterval is the decoded-buffer→Writer cadence. Zero disables the
	// periodic flush (the buffer still flushes on POST /done and shutdown).
	FlushInterval time.Duration

	// ReadTimeout / WriteTimeout bound the HTTP server (loopback; generous enough
	// for a large /capture batch).
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// DryRun switches flush from Writer persistence to stdout printing — decode and
	// queue logic run unchanged, so it exercises the whole pipeline minus the DB.
	DryRun bool
}

// Server is the loopback HTTP collector. It owns the authoritative target queue
// (filled once at construction, each search Target stamped with a stable QueryID)
// and the decoded-row buffer, drained to the Writer by a ticker and on shutdown.
//
// Two directions, both initiated by the browser extension: pull targets
// (GET /targets) and push captures (POST /capture). There is no server→client push;
// the extension polls /state and drives itself off the /targets stream + done flag.
//
// Concurrency (Rule 5): HTTP handlers and the flush ticker run on separate
// goroutines and share the queue cursor, buffer, and counts — all under s.mu.
// There are no package-level globals. Decode happens outside the lock (CPU-bound,
// per-request) so concurrent /capture calls do not serialize on JSON parsing.
type Server struct {
	addr string
	w    Writer
	opts ServerOptions

	// snapshot/sessionID are immutable after construction — read without the lock.
	snapshot  SnapshotTs
	sessionID string

	mu               sync.Mutex
	queue            []Target    // immutable after fillQueue; only cursor advances
	cursor           int         // next unserved index into queue
	buf              Decoded     // decoded rows awaiting the next flush
	counts           tableCounts // cumulative rows persisted (or printed, in DryRun)
	capturesReceived int         // total Intercept items accepted via /capture
	done             bool        // POST /done received
	flushedFinal     bool        // final flush already ran (idempotency guard)
}

// NewServer stamps each search Target with a stable QueryID (via Writer.UpsertQuery)
// and returns a ready-to-run collector. Card/url targets keep NoQuery: they have no
// query text to upsert, so their rows correctly persist with a NULL query_id.
//
// The Writer may be a DiscardWriter (--mock / --dry-run): upserts then yield
// synthetic ids, so /targets still serves stable ids and the whole transport+decode
// path is exercisable without a database.
func NewServer(ctx context.Context, addr string, w Writer, targets []Target, opts ServerOptions) (*Server, error) {
	if opts.SessionID == "" {
		opts.SessionID = sessionIDFromSnapshot(opts.Snapshot)
	}
	s := &Server{
		addr:      addr,
		w:         w,
		opts:      opts,
		snapshot:  opts.Snapshot,
		sessionID: opts.SessionID,
	}
	if err := s.fillQueue(ctx, targets); err != nil {
		return nil, err
	}
	dllog.Log("wbscraper: queue ready — %d targets, session %s, snapshot %s", len(s.queue), s.sessionID, s.snapshot)
	return s, nil
}

// fillQueue upserts each search target's query text to stamp a stable QueryID, then
// stores the stamped slice as the authoritative queue. The extension later echoes
// these QueryIDs back in its captures, binding every decoded row to its query.
//
// It also backfills a missing search URL from the query text: the generator and the
// CLI's target builder both set URLs, but a minimally-specified Target (kind+query)
// is valid too, and the extension cannot navigate without a URL — so the server
// tolerates the omission rather than serving an empty URL.
func (s *Server) fillQueue(ctx context.Context, targets []Target) error {
	stamped := make([]Target, 0, len(targets))
	for _, t := range targets {
		if t.Kind == "search" {
			qid, err := s.w.UpsertQuery(ctx, SearchQuery{
				Query:   t.Query,
				Subject: t.Subject,
				Gender:  t.Gender,
				Season:  t.Season,
				Age:     t.Age,
			})
			if err != nil {
				return fmt.Errorf("upsert query %q: %w", t.Query, err)
			}
			t.QueryID = qid
			if t.URL == "" {
				t.URL = SearchURL(t.Query)
			}
		}
		stamped = append(stamped, t)
	}
	s.mu.Lock()
	s.queue = stamped
	s.mu.Unlock()
	return nil
}

// SessionID returns the immutable session identifier (for the CLI's done-line).
func (s *Server) SessionID() string { return s.sessionID }

// Run starts the flush ticker and HTTP server, blocking until ctx is cancelled
// (Ctrl-C / SIGTERM) or the server fails to listen. On shutdown it drains
// in-flight requests (srv.Shutdown) then performs a final buffer flush so no
// decoded rows are stranded in memory.
//
// The server is an explicit *http.Server with its own ServeMux and timeouts — not
// http.DefaultServeMux + ListenAndServe. The latter (used by pkg/dashboard) has no
// graceful-shutdown path; this collector must not lose a capture to a hard exit.
func (s *Server) Run(ctx context.Context) error {
	go s.flushLoop(ctx)

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.routes(),
		ReadTimeout:  s.opts.ReadTimeout,
		WriteTimeout: s.opts.WriteTimeout,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			dllog.Error("shutdown: %v", err)
		}
		if _, err := s.finalFlush(); err != nil {
			dllog.Error("final flush: %v", err)
		}
		return nil
	case err := <-serveErr:
		// http.ErrServerClosed is the expected return from Shutdown — not an error.
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// routes builds the server's mux. A fresh ServeMux (not DefaultMux) keeps the
// server self-contained and testable with httptest, with no global registration.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/targets", s.handleTargets)
	mux.HandleFunc("/capture", s.handleCapture)
	mux.HandleFunc("/done", s.handleDone)
	mux.HandleFunc("/state", s.handleState)
	return mux
}

// ----------------------------------------------------------------------------
// Handlers — delivery only: parse request → call domain logic → write response.
// Decode and persistence live outside the handlers (dev_solid: delivery ≠ business).
// ----------------------------------------------------------------------------

// handleTargets serves the next batch of targets (pull). Each item already carries
// its QueryID (stamped at queue fill). done is true once the cursor reaches the end
// of the queue, signaling the extension to POST /done.
func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	n := s.opts.BatchTargets
	if q := r.URL.Query().Get("n"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n <= 0 {
		n = 50 // safe floor for a misconfigured BatchTargets
	}

	s.mu.Lock()
	end := s.cursor + n
	if end > len(s.queue) {
		end = len(s.queue)
	}
	// Copy the slice: the queue never grows, but a defensive copy frees the encoder
	// to run after the lock is released without aliasing the shared backing array.
	items := append([]Target(nil), s.queue[s.cursor:end]...)
	served := end
	s.cursor = end
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, targetsResponse{
		Items:     items,
		SessionID: s.sessionID,
		Total:     len(s.queue),
		Served:    served,
		Done:      served >= len(s.queue),
	})
}

// handleCapture accepts a batch of intercepted WB responses (push), decodes each by
// kind, logs a one-line summary, and accumulates rows into the flush buffer. One
// malformed capture is logged and skipped; it must not abort the rest of the batch.
// The buffer is persisted by the ticker (or POST /done / shutdown).
func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	var items []Intercept
	if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{"decode capture body: " + err.Error()})
		return
	}

	decoded := tableCounts{}
	acc := Decoded{}
	for _, it := range items {
		d, err := Decode(it, s.snapshot)
		if err != nil {
			dllog.Error("decode kind=%s url=%s: %v", it.Kind, it.URL, err)
			continue
		}
		decoded.add(countDecoded(d))
		mergeDecoded(&acc, d)
	}

	s.mu.Lock()
	s.capturesReceived += len(items)
	mergeDecoded(&s.buf, acc)
	s.mu.Unlock()

	dllog.Log("capture: %d items → positions=%d ads=%d cards=%d prices=%d details=%d stocks=%d",
		len(items), decoded.Positions, decoded.Ads, decoded.Cards, decoded.Prices, decoded.Details, decoded.Stocks)

	writeJSON(w, http.StatusOK, captureResponse{Accepted: len(items), Decoded: decoded})
}

// handleDone marks the session complete and flushes any buffered rows immediately,
// so the operator sees the final counts before the extension tears down.
func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	flushed, err := s.finalFlush()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{"final flush: " + err.Error()})
		return
	}
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, doneResponse{OK: true, Flushed: flushed})
}

// handleState reports queue progress and per-table counts for the extension popup.
// Read-only; cheap under the lock.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	served := s.cursor
	total := len(s.queue)
	counts := s.counts
	caps := s.capturesReceived
	done := s.done
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, stateResponse{
		SessionID:        s.sessionID,
		Total:            total,
		Served:           served,
		Remaining:        total - served,
		Done:             done,
		CapturesReceived: caps,
		Counts:           counts,
	})
}

// ----------------------------------------------------------------------------
// Flush loop + persistence
// ----------------------------------------------------------------------------

// flushLoop periodically drains the buffer until ctx is cancelled. Disabled when
// FlushInterval <= 0 (tests, or configs that flush only on /done/shutdown).
func (s *Server) flushLoop(ctx context.Context) {
	if s.opts.FlushInterval <= 0 {
		return
	}
	t := time.NewTicker(s.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.flush(context.Background()); err != nil {
				dllog.Error("flush: %v", err)
			}
		}
	}
}

// flush swaps out the buffer under the lock and persists the snapshot. The swap
// makes each flush own a disjoint set of rows, so concurrent /capture appends and
// the ticker never contend on the same rows.
//
// On a persist error the batch is dropped (not re-queued): append-only tables have
// no UNIQUE to dedup a retry, so re-queuing risks duplicates. The error is logged
// loudly; the operator re-runs the session. New captures keep accumulating and will
// flush on the next tick once the DB is reachable.
func (s *Server) flush(ctx context.Context) (tableCounts, error) {
	s.mu.Lock()
	buf := s.buf
	s.buf = Decoded{}
	s.mu.Unlock()

	if emptyDecoded(buf) {
		return tableCounts{}, nil
	}

	if s.opts.DryRun {
		return s.dryRunPrint(buf), nil
	}

	saved, err := s.persist(ctx, buf)
	if err != nil {
		dllog.Error("flush persist failed, %d rows dropped: %v", countDecoded(buf).total(), err)
		return saved, err
	}
	s.mu.Lock()
	s.counts.add(saved)
	s.mu.Unlock()
	return saved, nil
}

// finalFlush runs flush once and is idempotent: POST /done then Ctrl-C must not
// double-flush (and the buffer is empty after the first flush anyway, but the guard
// keeps the contract explicit).
func (s *Server) finalFlush() (tableCounts, error) {
	s.mu.Lock()
	if s.flushedFinal {
		s.mu.Unlock()
		return tableCounts{}, nil
	}
	s.flushedFinal = true
	s.mu.Unlock()
	return s.flush(context.Background())
}

// persist writes each non-empty slice via the matching Writer method. A failure in
// one method does not skip the others (independent tables); the first error is
// returned for the caller to log, alongside whatever partial counts succeeded.
func (s *Server) persist(ctx context.Context, buf Decoded) (tableCounts, error) {
	var c tableCounts
	var firstErr error

	if len(buf.SearchPositions) > 0 {
		n, err := s.w.SaveStorefrontPositions(ctx, buf.SearchPositions)
		c.Positions = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("positions: %w", err)
		}
	}
	if len(buf.VitrineAds) > 0 {
		n, err := s.w.SaveVitrineAds(ctx, buf.VitrineAds)
		c.Ads = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("ads: %w", err)
		}
	}
	if len(buf.CompetitorCards) > 0 {
		n, err := s.w.SaveCompetitorCards(ctx, buf.CompetitorCards)
		c.Cards = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("cards: %w", err)
		}
	}
	if len(buf.CompetitorCardPrices) > 0 {
		n, err := s.w.SaveCompetitorCardPrices(ctx, buf.CompetitorCardPrices)
		c.Prices = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("prices: %w", err)
		}
	}
	if len(buf.CompetitorCardDetails) > 0 {
		n, err := s.w.SaveCompetitorCardDetails(ctx, buf.CompetitorCardDetails)
		c.Details = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("details: %w", err)
		}
	}
	if len(buf.CompetitorCardStocks) > 0 {
		n, err := s.w.SaveCompetitorCardStocks(ctx, buf.CompetitorCardStocks)
		c.Stocks = n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stocks: %w", err)
		}
	}
	return c, firstErr
}

// dryRunPrint dumps the decoded buffer as indented JSON and logs the per-table
// counts — the "show exactly what would be written" contract of --dry-run.
func (s *Server) dryRunPrint(buf Decoded) tableCounts {
	c := countDecoded(buf)
	raw, _ := json.MarshalIndent(buf, "", "  ")
	fmt.Println(string(raw))
	dllog.Log("dry-run: would save positions=%d ads=%d cards=%d prices=%d details=%d stocks=%d",
		c.Positions, c.Ads, c.Cards, c.Prices, c.Details, c.Stocks)
	return c
}

// ----------------------------------------------------------------------------
// JSON response shapes (wire contract — frozen at Stage 5)
// ----------------------------------------------------------------------------

type targetsResponse struct {
	Items     []Target `json:"items"`
	SessionID string   `json:"sessionId"`
	Total     int      `json:"total"`
	Served    int      `json:"served"`
	Done      bool     `json:"done"`
}

type captureResponse struct {
	Accepted int         `json:"accepted"`
	Decoded  tableCounts `json:"decoded"`
}

type stateResponse struct {
	SessionID        string      `json:"sessionId"`
	Total            int         `json:"total"`
	Served           int         `json:"served"`
	Remaining        int         `json:"remaining"`
	Done             bool        `json:"done"`
	CapturesReceived int         `json:"capturesReceived"`
	Counts           tableCounts `json:"counts"`
}

type doneResponse struct {
	OK      bool        `json:"ok"`
	Flushed tableCounts `json:"flushed"`
}

type errorBody struct {
	Error string `json:"error"`
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// writeJSON encodes v with a status. Encode errors are ignored: the body is already
// partially written, and there is no recovery path mid-response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// countDecoded maps a Decoded bundle to its per-table counts.
func countDecoded(d Decoded) tableCounts {
	return tableCounts{
		Positions: len(d.SearchPositions),
		Ads:       len(d.VitrineAds),
		Cards:     len(d.CompetitorCards),
		Prices:    len(d.CompetitorCardPrices),
		Details:   len(d.CompetitorCardDetails),
		Stocks:    len(d.CompetitorCardStocks),
	}
}

// emptyDecoded reports whether a Decoded bundle has no rows across any table.
func emptyDecoded(d Decoded) bool { return countDecoded(d).total() == 0 }

// mergeDecoded appends every slice of src onto *dst in place (used to fold decode
// output into the shared buffer under the lock). dst is a pointer so the appended
// slice headers write back to the caller's Decoded.
func mergeDecoded(dst *Decoded, src Decoded) {
	dst.SearchPositions = append(dst.SearchPositions, src.SearchPositions...)
	dst.VitrineAds = append(dst.VitrineAds, src.VitrineAds...)
	dst.CompetitorCards = append(dst.CompetitorCards, src.CompetitorCards...)
	dst.CompetitorCardPrices = append(dst.CompetitorCardPrices, src.CompetitorCardPrices...)
	dst.CompetitorCardDetails = append(dst.CompetitorCardDetails, src.CompetitorCardDetails...)
	dst.CompetitorCardStocks = append(dst.CompetitorCardStocks, src.CompetitorCardStocks...)
}

// sessionIDFromSnapshot reduces a SnapshotTs to its digits — a stable, meaningful
// session id ("2026-07-02T04:10:00Z" → "20260702041000"). "0" for anything without
// digits, so the id is never empty.
func sessionIDFromSnapshot(s SnapshotTs) string {
	var b strings.Builder
	for _, r := range string(s) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}
