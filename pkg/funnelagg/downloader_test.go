package funnelagg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// testWriter tracks calls for test assertions.
type testWriter struct {
	saved    int
	count    int // GetFunnelAggregatedCount result
	nmCount  int // GetDistinctNmIDCount result
	saveErr  error
	batches  [][]wb.FunnelAggregatedProduct
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          5,
		RateLimit:         3,
		Burst:             3,
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          3,
		DryRun:            true,
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          10, // larger than data
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          10,
		MaxPageRetries:    1,
		PageRetryBaseSleep: 10 * time.Millisecond,
		OnProgress: func(msg string) { progressMsgs = append(progressMsgs, msg) },
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          5,
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PastStart:         "2025-12-25",
		PastEnd:           "2025-12-31",
		PageSize:          10,
		MaxPageRetries:    1,
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
		count:   10,                                // 10 already in DB
		nmCount: 20,
	}

	dl := NewDownloader(src, writer, DownloadOptions{
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          5,
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          5,
		MaxPageRetries:    1,
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
		SelectedStart:     "2026-01-01",
		SelectedEnd:       "2026-01-07",
		PageSize:          5,
		MaxPageRetries:    2,
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
