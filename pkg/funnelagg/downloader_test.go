package funnelagg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// testWriter tracks calls for test assertions.
type testWriter struct {
	saved   int
	count   int // GetFunnelAggregatedCount result
	nmCount int // GetDistinctNmIDCount result
	history int // GetHistoryProductCount result (0 = истории нет)
	saveErr error
	batches [][]wb.FunnelAggregatedProduct
}

func (w *testWriter) SaveFunnelAggregatedBatch(_ context.Context, products []wb.FunnelAggregatedProduct, _, _, _ string) (int, error) {
	w.batches = append(w.batches, products)
	if w.saveErr != nil {
		return 0, w.saveErr
	}
	w.saved += len(products)
	return len(products), nil
}

func (w *testWriter) GetFunnelAggregatedCount(_ context.Context, _, _ string) (int, error) {
	return w.count, nil
}

func (w *testWriter) GetHistoryProductCount(_ context.Context, _ int, _ string) (int, error) {
	return w.history, nil
}

func (w *testWriter) GetDistinctNmIDCount(_ context.Context) (int, error) {
	return w.nmCount, nil
}

// emptySource always returns empty products (simulates no data for period).
type emptySource struct{}

func (s *emptySource) LoadAggregatedPage(_ context.Context, _ wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	return nil, "RUB", nil
}

// errorSource always returns an error.
type errorSource struct{}

func (s *errorSource) LoadAggregatedPage(_ context.Context, _ wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	return nil, "", errors.New("api unavailable")
}

// TestBasicDownload verifies happy-path pagination with 3 pages of 5 products each.
func TestBasicDownload(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 3}
	writer := &testWriter{nmCount: 15}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		RateLimit:          3,
		Burst:              3,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PagesLoaded != 3 {
		t.Errorf("expected 3 pages, got %d", result.PagesLoaded)
	}
	if result.ProductsLoaded != 15 {
		t.Errorf("expected 15 products, got %d", result.ProductsLoaded)
	}
	if writer.saved != 15 {
		t.Errorf("writer saved: expected 15, got %d", writer.saved)
	}
}

// TestDryRun verifies that DryRun mode counts products but does not save.
func TestDryRun(t *testing.T) {
	src := &MockSource{ProductCount: 3, TotalPages: 2}
	writer := &testWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           3,
		DryRun:             true,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PagesLoaded != 2 {
		t.Errorf("expected 2 pages, got %d", result.PagesLoaded)
	}
	if result.ProductsLoaded != 6 {
		t.Errorf("expected 6 products loaded, got %d", result.ProductsLoaded)
	}
	if writer.saved != 0 {
		t.Errorf("dry-run should not save, got %d", writer.saved)
	}
}

// TestSinglePage verifies correct handling when all data fits in one page.
func TestSinglePage(t *testing.T) {
	src := &MockSource{ProductCount: 7, TotalPages: 1}
	writer := &testWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           10, // larger than data
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PagesLoaded != 1 {
		t.Errorf("expected 1 page, got %d", result.PagesLoaded)
	}
	if result.ProductsLoaded != 7 {
		t.Errorf("expected 7 products, got %d", result.ProductsLoaded)
	}
}

// TestEmptyResponse verifies handling when API returns no data on first page.
func TestEmptyResponse(t *testing.T) {
	src := &emptySource{} // always returns empty
	writer := &testWriter{}

	var progressMsgs []string
	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           10,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
		OnProgress:         func(_, _ int, msg string) { progressMsgs = append(progressMsgs, msg) },
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PagesLoaded != 0 {
		t.Errorf("expected 0 pages, got %d", result.PagesLoaded)
	}

	found := false
	for _, msg := range progressMsgs {
		if msg == "ℹ️  No data for the specified period" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no data' message in progress, got: %v", progressMsgs)
	}
}

// TestContextCancel verifies that Run respects context cancellation.
func TestContextCancel(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 100} // many pages
	writer := &testWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected context canceled error, got nil")
	}
}

// TestWithPastPeriod verifies that past period is included in API requests.
func TestWithPastPeriod(t *testing.T) {
	src := &MockSource{ProductCount: 3, TotalPages: 1}

	// Capture the request sent to source
	var capturedReq wb.FunnelAggregatedRequest
	decoratedSrc := &capturingSource{
		inner:  src,
		onCall: func(req wb.FunnelAggregatedRequest) { capturedReq = req },
	}

	writer := &testWriter{}
	dl := NewDownloader(decoratedSrc, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PastStart:          "2025-12-25",
		PastEnd:            "2025-12-31",
		PageSize:           10,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.PastPeriod == nil {
		t.Fatal("expected PastPeriod in request, got nil")
	}
	if capturedReq.PastPeriod.Start != "2025-12-25" {
		t.Errorf("past start: expected 2025-12-25, got %s", capturedReq.PastPeriod.Start)
	}
	if capturedReq.PastPeriod.End != "2025-12-31" {
		t.Errorf("past end: expected 2025-12-31, got %s", capturedReq.PastPeriod.End)
	}
}

// TestResumeFromOffset verifies offset resume skips already-downloaded data.
func TestResumeFromOffset(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 4} // 20 total
	writer := &testWriter{
		count:   10, // 10 already in DB
		nmCount: 20,
	}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resume from offset=max(0, 10-5)=5, so pages 2,3,4 → 15 products
	if result.ProductsLoaded < 10 {
		t.Errorf("expected at least 10 products after resume, got %d", result.ProductsLoaded)
	}
}

// TestSaveErrorStops verifies that save errors stop the download loop.
func TestSaveErrorStops(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 3}
	writer := &testWriter{
		saveErr: errors.New("disk full"),
	}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err == nil {
		t.Fatal("Run should return error on save failure")
	}
	if result.PagesLoaded != 0 {
		t.Errorf("expected 0 pages on save error, got %d", result.PagesLoaded)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}

// TestAPIErrorExhausted verifies that all retries exhausted is handled gracefully.
func TestAPIErrorExhausted(t *testing.T) {
	src := &errorSource{}
	writer := &testWriter{}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		MaxPageRetries:     2,
		PageRetryBaseSleep: 10 * time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err == nil {
		t.Fatal("Run should return error when API retries exhausted")
	}
	if result.PagesLoaded != 0 {
		t.Errorf("expected 0 pages on API error, got %d", result.PagesLoaded)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}

// capturingSource wraps a Source and captures the last request.
type capturingSource struct {
	inner  Source
	onCall func(req wb.FunnelAggregatedRequest)
}

func (s *capturingSource) LoadAggregatedPage(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	s.onCall(req)
	return s.inner.LoadAggregatedPage(ctx, req)
}

// TestProgressPassesPageAndTotal verifies OnProgress receives page number
// and estimated pages total ([N/M % ETA] префикс dllog).
func TestProgressPassesPageAndTotal(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 3}
	writer := &testWriter{nmCount: 10} // оценка: 10 товаров / 5 на страницу = 2 страницы

	type call struct{ cur, total int }
	var calls []call
	var pageMsgs int
	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:      "2026-01-01",
		SelectedEnd:        "2026-01-07",
		PageSize:           5,
		MaxPageRetries:     1,
		PageRetryBaseSleep: 10 * time.Millisecond,
		OnProgress: func(cur, total int, msg string) {
			calls = append(calls, call{cur, total})
			if strings.HasPrefix(msg, "📄") {
				pageMsgs++
			}
		},
	})

	if _, err := dl.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// «📄 Page N» печатается ПЕРЕД запросом: 3 страницы данных + 1 холостая
	// (за ней «✅ All data loaded») = 4 строки.
	if pageMsgs != 4 {
		t.Fatalf("page messages = %d, want 4 (3 данных + 1 холостая)", pageMsgs)
	}
	// Оценка страниц: ceil(10/5) = 2 (реальных 3 — оценка лишь для ETA).
	// Проверяем только строки страниц; инфо-строки (период) идут cur=0, total=0.
	for _, c := range calls {
		if c.cur > 0 && c.total != 2 {
			t.Errorf("page call total = %d, want 2 (estimate ceil(10/5))", c.total)
		}
	}
	// Инфо-строки (период) идут с cur=0.
	if len(calls) == 0 || calls[0].cur != 0 {
		t.Errorf("первое сообщение должно быть инфо-строкой (cur=0): %+v", calls[0])
	}
}

// TestProgressEstimate_HistoryWinsOverSales: оценка тотала [N/M] берётся из
// собственной истории воронки (последние прогонЫ той же длины окна), а не из
// distinct sales — состав отчёта шире orders/sales (переходы, корзина), sales
// занижал в 1,8–2,5 раза (инцидент 16.10: [15/9 166%], ночь — 40% под видом 98,4%).
func TestProgressEstimate_HistoryWinsOverSales(t *testing.T) {
	src := &emptySource{}
	writer := &testWriter{history: 20000, nmCount: 8130} // история важнее sales

	var pageTotal int
	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart: "2026-10-10",
		SelectedEnd:   "2026-10-16",
		PageSize:      1000,
		OnProgress: func(cur, total int, msg string) {
			if cur > 0 && strings.HasPrefix(msg, "📄") && pageTotal == 0 {
				pageTotal = total
			}
		},
	})
	if _, err := dl.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := (20000 + 1000 - 1) / 1000; pageTotal != want {
		t.Errorf("page total = %d, want %d (история 20000, а не sales 8130)", pageTotal, want)
	}
}

// TestPeriodWindowDays: длина окна для сверки с историей; мусорные даты → false.
func TestPeriodWindowDays(t *testing.T) {
	cases := []struct {
		start, end string
		want       int
		ok         bool
	}{
		{"2026-10-09", "2026-10-15", 6, true},  // прод days=7
		{"2026-10-15", "2026-10-15", 0, true},  // тест days=1 (истории 0-дневных нет → fallback)
		{"2026-10-15", "2026-10-14", 0, false}, // end < start
		{"", "2026-10-15", 0, false},           // пустая дата
		{"15.10.2026", "16.10.2026", 0, false}, // не YYYY-MM-DD
	}
	for _, c := range cases {
		got, ok := periodWindowDays(c.start, c.end)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("periodWindowDays(%q, %q) = (%d, %v), want (%d, %v)",
				c.start, c.end, got, ok, c.want, c.ok)
		}
	}
}

// TestBudgetModeExhausted verifies budget-mode: страница воюет циклами попыток
// до исхода бюджета, после чего — честная ошибка «page budget exhausted»
// (offset-resume продолжит со сохранённой страницы при следующем запуске).
func TestBudgetModeExhausted(t *testing.T) {
	oldPause := pageRetryCyclePause
	pageRetryCyclePause = 1 * time.Millisecond
	defer func() { pageRetryCyclePause = oldPause }()

	src := &errorSource{}
	writer := &testWriter{}

	var progressMsgs []string
	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:   "2026-01-01",
		SelectedEnd:     "2026-01-07",
		PageSize:        5,
		PageRetryBudget: 30 * time.Millisecond,
		OnProgress:      func(_, _ int, msg string) { progressMsgs = append(progressMsgs, msg) },
	})

	start := time.Now()
	result, err := dl.Run(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run should return error when page budget exhausted")
	}
	if !strings.Contains(err.Error(), "page budget") {
		t.Errorf("error = %v, want 'page budget' exhausted", err)
	}
	if result.PagesLoaded != 0 {
		t.Errorf("expected 0 pages on budget exhaustion, got %d", result.PagesLoaded)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
	if elapsed < 30*time.Millisecond {
		t.Errorf("elapsed = %v, fought less than the budget", elapsed)
	}
	cycles := 0
	for _, msg := range progressMsgs {
		if strings.Contains(msg, "page-budget") {
			cycles++
		}
	}
	if cycles == 0 {
		t.Errorf("expected page-budget progress messages, got: %v", progressMsgs)
	}
}

// TestBudgetModeSuccess verifies that budget-mode passes the happy path
// (первая попытка успешна — бюджет не тратится).
func TestBudgetModeSuccess(t *testing.T) {
	src := &MockSource{ProductCount: 5, TotalPages: 2}
	writer := &testWriter{nmCount: 10}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:   "2026-01-01",
		SelectedEnd:     "2026-01-07",
		PageSize:        5,
		PageRetryBudget: 10 * time.Second,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PagesLoaded != 2 || result.ProductsLoaded != 10 {
		t.Errorf("pages/products = %d/%d, want 2/10", result.PagesLoaded, result.ProductsLoaded)
	}
}
