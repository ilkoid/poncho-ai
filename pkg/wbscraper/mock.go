package wbscraper

import (
	"context"
	"encoding/json"
	"sync"
)

// DiscardWriter implements Writer with no-op persistence. Used in --mock mode to
// guarantee zero DB interaction (Writer creation goes inside the non-mock branch,
// per the DiscardWriter pattern — see CLAUDE.md "V2 Downloader Architecture").
//
// UpsertQuery is faithful, not vacuous: it assigns synthetic ids and records the
// query text so identical text maps to one id across calls (mirroring the real DB's
// UNIQUE constraint). This lets --mock mode exercise the server's query-stamping
// logic end-to-end — targets still get stable QueryIDs, captures still carry them —
// without touching storage.
type DiscardWriter struct {
	mu sync.Mutex

	// synthetic query ids (idempotent by query text, like a DB UNIQUE).
	nextQueryID int64
	queries     map[string]int64

	// per-table "saved" counters, for test assertions (parity with pkg/supplies).
	savedPositions  int
	savedVitrineAds int
	savedCards      int
	savedPrices     int
	savedDetails    int
	savedStocks     int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{queries: make(map[string]int64)}
}

// UpsertQuery returns the existing synthetic id for q.Query, or assigns the next
// one. Idempotent: the same text always yields the same id, as a real
// search_queries UNIQUE would. Ids start at 1, so NoQuery (0) stays reserved.
func (w *DiscardWriter) UpsertQuery(_ context.Context, q SearchQuery) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id, ok := w.queries[q.Query]; ok {
		return id, nil
	}
	w.nextQueryID++
	w.queries[q.Query] = w.nextQueryID
	return w.nextQueryID, nil
}

// SaveStorefrontPositions counts rows but never writes to any database.
func (w *DiscardWriter) SaveStorefrontPositions(_ context.Context, rows []SearchPosition) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedPositions += len(rows)
	return len(rows), nil
}

// SaveVitrineAds counts rows but never writes.
func (w *DiscardWriter) SaveVitrineAds(_ context.Context, rows []VitrineAd) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedVitrineAds += len(rows)
	return len(rows), nil
}

// SaveCompetitorCards counts rows but never writes.
func (w *DiscardWriter) SaveCompetitorCards(_ context.Context, rows []CompetitorCard) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedCards += len(rows)
	return len(rows), nil
}

// SaveCompetitorCardPrices counts rows but never writes.
func (w *DiscardWriter) SaveCompetitorCardPrices(_ context.Context, rows []CompetitorCardPrice) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedPrices += len(rows)
	return len(rows), nil
}

// SaveCompetitorCardDetails counts rows but never writes.
func (w *DiscardWriter) SaveCompetitorCardDetails(_ context.Context, rows []CompetitorCardDetail) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedDetails += len(rows)
	return len(rows), nil
}

// SaveCompetitorCardStocks counts rows but never writes.
func (w *DiscardWriter) SaveCompetitorCardStocks(_ context.Context, rows []CompetitorCardStock) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.savedStocks += len(rows)
	return len(rows), nil
}

// SavedQueries returns the number of distinct query texts upserted.
func (w *DiscardWriter) SavedQueries() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queries)
}

// SavedStorefrontPositions returns the count of position rows "saved" (counted).
func (w *DiscardWriter) SavedStorefrontPositions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedPositions
}

// SavedVitrineAds returns the count of ad rows "saved" (counted).
func (w *DiscardWriter) SavedVitrineAds() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedVitrineAds
}

// SavedCompetitorCards returns the count of card rows "saved" (counted).
func (w *DiscardWriter) SavedCompetitorCards() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedCards
}

// SavedCompetitorCardPrices returns the count of price rows "saved" (counted).
func (w *DiscardWriter) SavedCompetitorCardPrices() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedPrices
}

// SavedCompetitorCardDetails returns the count of detail rows "saved" (counted).
func (w *DiscardWriter) SavedCompetitorCardDetails() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedDetails
}

// SavedCompetitorCardStocks returns the count of stock rows "saved" (counted).
func (w *DiscardWriter) SavedCompetitorCardStocks() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedStocks
}

// Compile-time assertion that DiscardWriter satisfies Writer (Stage 3 storage
// adapters carry the same assertion).
var _ Writer = (*DiscardWriter)(nil)

// MockIntercepts returns synthetic captures (one search + one card_detail) for
// unit-testing Decode and the server loop without the browser extension or fixture
// files. Bodies match the WB response shapes documented in decode.go. The search
// capture sits on page=2 with dest=8038 so position math is non-trivial
// ((2-1)*100+idx+1), and mixes an organic listing (panelPromoId null) with an ad.
func MockIntercepts() []Intercept {
	return []Intercept{
		{
			Kind:    "search",
			URL:     "https://www.wildberries.ru/catalog/0/search.aspx?search=кроссовки&page=2&dest=8038",
			QueryID: 7,
			Status:  200,
			Body: json.RawMessage(`{
  "metadata": {"name": "кроссовки"},
  "products": [
    {"id": 111, "brand": "Nike", "supplierId": 900, "panelPromoId": null, "rating": 4.5, "feedbacks": 10,
     "sizes": [{"price": {"basic": 100000, "product": 89900}}]},
    {"id": 222, "brand": "Adidas", "supplierId": 901, "panelPromoId": 99, "rating": 4.0, "feedbacks": 5,
     "sizes": [{"price": {"basic": 50000, "product": 45000}}]}
  ]
}`),
		},
		{
			Kind:    "card_detail",
			URL:     "https://www.wildberries.ru/catalog/111/detail.aspx",
			QueryID: 7,
			Status:  200,
			Body: json.RawMessage(`{
  "products": [
    {"id": 111, "brand": "Nike", "supplier": "ООО Рога", "supplierId": 900, "rating": 4.5, "feedbacks": 10,
     "pics": ["a.jpg","b.jpg"], "colors": [{"name":"черный"}], "subjectId": 81,
     "panelPromoId": null, "totalQuantity": 250, "promotions": [{"name":"Скидка"}],
     "sizes": [
       {"name": "42", "price": {"basic": 100000, "product": 89900},
        "stocks": [{"wh": 507, "qty": 10, "time1": 1720000000, "time2": 1720003600}]}
     ]}
  ]
}`),
		},
	}
}
