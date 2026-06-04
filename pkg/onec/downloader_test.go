package onec

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Tracking writer — records all calls for test assertions
// ---------------------------------------------------------------------------

type trackingWriter struct {
	mu sync.Mutex

	goods     [][]Good
	skus      [][]SKU
	dims      [][]DimensionRow
	prices    [][]PriceRow
	pimGoods  [][]PIMGoods
	cleanCalled bool
	saveErr   error // inject error on any Save call
}

func (tw *trackingWriter) SaveGoods(_ context.Context, goods []Good) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	tw.goods = append(tw.goods, goods)
	return len(goods), nil
}

func (tw *trackingWriter) SaveSKUs(_ context.Context, skus []SKU) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	tw.skus = append(tw.skus, skus)
	return len(skus), nil
}

func (tw *trackingWriter) SaveDimensions(_ context.Context, dims []DimensionRow) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	tw.dims = append(tw.dims, dims)
	return len(dims), nil
}

func (tw *trackingWriter) SaveOneCPrices(_ context.Context, prices []PriceRow, _ string) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	tw.prices = append(tw.prices, prices)
	return len(prices), nil
}

func (tw *trackingWriter) SavePIMGoods(_ context.Context, items []PIMGoods) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	tw.pimGoods = append(tw.pimGoods, items)
	return len(items), nil
}

func (tw *trackingWriter) CleanAll(_ context.Context) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.cleanCalled = true
	return nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAllStepsSuccess(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	var progress []string
	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
		OnProgress: func(msg string) {
			progress = append(progress, msg)
		},
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify counts
	if result.GoodsCount != 10 {
		t.Errorf("goods: got %d, want 10", result.GoodsCount)
	}
	if result.SKUCount != 30 { // 10 goods × 3 SKUs
		t.Errorf("SKUs: got %d, want 30", result.SKUCount)
	}
	if result.DimensionCount != 30 {
		t.Errorf("dimensions: got %d, want 30", result.DimensionCount)
	}
	if result.PriceCount != 50 { // 10 goods × 5 price types
		t.Errorf("prices: got %d, want 50", result.PriceCount)
	}
	if result.PIMCount != 10 {
		t.Errorf("PIM goods: got %d, want 10", result.PIMCount)
	}

	// Verify writer received data
	if len(tw.goods) == 0 {
		t.Error("writer received no goods")
	}
	if len(tw.skus) == 0 {
		t.Error("writer received no SKUs")
	}
	if len(tw.prices) == 0 {
		t.Error("writer received no prices")
	}
	if len(tw.pimGoods) == 0 {
		t.Error("writer received no PIM goods")
	}

	// Verify progress messages
	if len(progress) != 3 {
		t.Fatalf("progress messages: got %d, want 3", len(progress))
	}
	if !strings.Contains(progress[0], "Step 1/3") {
		t.Errorf("progress[0]: %q should contain 'Step 1/3'", progress[0])
	}
	if !strings.Contains(progress[1], "Step 2/3") {
		t.Errorf("progress[1]: %q should contain 'Step 2/3'", progress[1])
	}
	if !strings.Contains(progress[2], "Step 3/3") {
		t.Errorf("progress[2]: %q should contain 'Step 3/3'", progress[2])
	}
}

func TestDryRun(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
		DryRun:       true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Counts should reflect source data even in dry-run
	if result.GoodsCount != 10 {
		t.Errorf("goods count: got %d, want 10", result.GoodsCount)
	}
	if result.PriceCount != 50 {
		t.Errorf("price count: got %d, want 50", result.PriceCount)
	}

	// Writer should NOT have been called
	if len(tw.goods) != 0 {
		t.Errorf("dry-run should not write goods, got %d calls", len(tw.goods))
	}
	if len(tw.skus) != 0 {
		t.Errorf("dry-run should not write SKUs, got %d calls", len(tw.skus))
	}
	if len(tw.prices) != 0 {
		t.Errorf("dry-run should not write prices, got %d calls", len(tw.prices))
	}
	if len(tw.pimGoods) != 0 {
		t.Errorf("dry-run should not write PIM goods, got %d calls", len(tw.pimGoods))
	}
}

func TestStepFailure(t *testing.T) {
	src := &MockSource{FailOn: "prices"}
	src.Populate(5, 2, 3)
	tw := &trackingWriter{}

	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
	})

	result, err := dl.Run(context.Background())
	// Should return error from first failed step
	if err == nil {
		t.Fatal("expected error from failed prices step")
	}
	if !strings.Contains(err.Error(), "prices step failed") {
		t.Errorf("error should mention prices: %v", err)
	}

	// Goods and PIM should have succeeded despite prices failure
	if result.GoodsCount != 5 {
		t.Errorf("goods: got %d, want 5", result.GoodsCount)
	}
	if result.PIMCount != 5 {
		t.Errorf("PIM: got %d, want 5", result.PIMCount)
	}

	// Price count should be 0 (fetch failed)
	if result.PriceCount != 0 {
		t.Errorf("prices: got %d, want 0", result.PriceCount)
	}

	// Step errors should contain the prices failure
	if len(result.StepErrors) != 1 {
		t.Fatalf("step errors: got %d, want 1", len(result.StepErrors))
	}
	if result.StepErrors[0].Step != "prices" {
		t.Errorf("failed step: got %q, want 'prices'", result.StepErrors[0].Step)
	}
}

func TestContextCancel(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately — step 1 should complete (already in-flight),
	// but Run should return ctx error after step 1.
	cancel()

	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestCleanFlag(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
		Clean:        true,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !tw.cleanCalled {
		t.Error("CleanAll should have been called with Clean=true")
	}
}

func TestCleanFlagDryRun(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	// Clean should be skipped when DryRun=true
	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
		Clean:        true,
		DryRun:       true,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tw.cleanCalled {
		t.Error("CleanAll should NOT have been called when DryRun=true")
	}
}

func TestDiscardWriter(t *testing.T) {
	w := NewDiscardWriter()

	n, err := w.SaveGoods(context.Background(), make([]Good, 10))
	if err != nil || n != 10 {
		t.Fatalf("SaveGoods: n=%d, err=%v", n, err)
	}

	n, err = w.SaveSKUs(context.Background(), make([]SKU, 30))
	if err != nil || n != 30 {
		t.Fatalf("SaveSKUs: n=%d, err=%v", n, err)
	}

	n, err = w.SaveDimensions(context.Background(), make([]DimensionRow, 15))
	if err != nil || n != 15 {
		t.Fatalf("SaveDimensions: n=%d, err=%v", n, err)
	}

	n, err = w.SaveOneCPrices(context.Background(), make([]PriceRow, 50), "2026-06-05")
	if err != nil || n != 50 {
		t.Fatalf("SavePrices: n=%d, err=%v", n, err)
	}

	n, err = w.SavePIMGoods(context.Background(), make([]PIMGoods, 10))
	if err != nil || n != 10 {
		t.Fatalf("SavePIMGoods: n=%d, err=%v", n, err)
	}

	err = w.CleanAll(context.Background())
	if err != nil {
		t.Fatalf("CleanAll: err=%v", err)
	}

	goods, skus, dims, prices, pim := w.Counts()
	if goods != 10 || skus != 30 || dims != 15 || prices != 50 || pim != 10 {
		t.Errorf("Counts: goods=%d, skus=%d, dims=%d, prices=%d, pim=%d",
			goods, skus, dims, prices, pim)
	}

	if !w.CleanWasCalled() {
		t.Error("CleanWasCalled should be true")
	}
}

func TestMockSourcePopulate(t *testing.T) {
	src := &MockSource{}
	src.Populate(5, 4, 3)

	if len(src.Goods) != 5 {
		t.Errorf("goods: got %d, want 5", len(src.Goods))
	}
	if len(src.SKUs) != 20 { // 5 × 4
		t.Errorf("SKUs: got %d, want 20", len(src.SKUs))
	}
	if len(src.Dimensions) != 20 {
		t.Errorf("dimensions: got %d, want 20", len(src.Dimensions))
	}
	if len(src.Prices) != 15 { // 5 × 3
		t.Errorf("prices: got %d, want 15", len(src.Prices))
	}
	if len(src.PIMGoods) != 5 {
		t.Errorf("PIM goods: got %d, want 5", len(src.PIMGoods))
	}

	// Verify dimension conversion: first good has dimensions > 0
	if src.Dimensions[0].LengthDM <= 0 {
		t.Error("dimension LengthDM should be > 0")
	}
	if src.Dimensions[0].WeightKG <= 0 {
		t.Error("dimension WeightKG should be > 0")
	}
	if src.Dimensions[0].Source != "api" {
		t.Errorf("dimension Source: got %q, want 'api'", src.Dimensions[0].Source)
	}
}

func TestDownloadResultDuration(t *testing.T) {
	src := NewMockSource()
	tw := &trackingWriter{}

	dl := NewDownloader(src, tw, DownloadOptions{
		GoodsURL:     "http://test/goods/",
		PricesURL:    "http://test/prices/",
		PIMURL:       "http://test/pim/",
		SnapshotDate: "2026-06-05",
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Duration == 0 {
		t.Error("duration should be > 0")
	}
	if result.Duration > 5*time.Second {
		t.Error("mock download should be fast, took", result.Duration)
	}
}
