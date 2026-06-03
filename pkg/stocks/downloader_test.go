package stocks

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDownloader_Run_Basic tests a full download pipeline with mock data.
func TestDownloader_Run_Basic(t *testing.T) {
	source := NewMockStocksSource()
	source.PopulateStocks(50, 3) // 50 products × 3 warehouses = 150 items

	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		SnapshotDate: "2026-06-03",
		DryRun:       false,
		RateLimit:    3,
		Burst:        1,
		OnProgress:   func(msg string) { msgs = append(msgs, msg) },
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 150 {
		t.Errorf("TotalRows = %d, want 150", result.TotalRows)
	}
	if result.Pages != 1 {
		t.Errorf("Pages = %d, want 1", result.Pages)
	}
	if writer.Saved() != 150 {
		t.Errorf("DiscardWriter.Saved() = %d, want 150", writer.Saved())
	}
}

// TestDownloader_Run_DryRun tests that dry-run mode skips writer.
func TestDownloader_Run_DryRun(t *testing.T) {
	source := NewMockStocksSource()
	source.PopulateStocks(20, 2) // 40 items

	writer := NewDiscardWriter()

	opts := DownloadOptions{
		SnapshotDate: "2026-06-03",
		DryRun:       true,
		RateLimit:    3,
		Burst:        1,
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 40 {
		t.Errorf("TotalRows = %d, want 40 (dry-run counts)", result.TotalRows)
	}
	if writer.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 (dry-run should not save)", writer.Saved())
	}
}

// TestDownloader_Run_Cancelled tests context cancellation.
func TestDownloader_Run_Cancelled(t *testing.T) {
	source := NewMockStocksSource()
	source.PopulateStocks(100, 3)

	writer := NewDiscardWriter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := DownloadOptions{
		SnapshotDate: "2026-06-03",
		RateLimit:    3,
		Burst:        1,
	}
	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("error = %q, want 'context cancelled'", err.Error())
	}
}

// TestDownloader_Run_EmptySource tests download with no data.
func TestDownloader_Run_EmptySource(t *testing.T) {
	source := NewMockStocksSource() // no data
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		SnapshotDate: "2026-06-03",
		RateLimit:    3,
		Burst:        1,
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0", result.TotalRows)
	}
	if result.Pages != 0 {
		t.Errorf("Pages = %d, want 0", result.Pages)
	}
}

// TestDownloader_Run_DefaultDate tests that snapshot date defaults to today.
func TestDownloader_Run_DefaultDate(t *testing.T) {
	source := NewMockStocksSource()
	source.PopulateStocks(5, 1)
	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		// SnapshotDate intentionally empty — should default to today
		RateLimit:  3,
		Burst:      1,
		OnProgress: func(msg string) { msgs = append(msgs, msg) },
	}
	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	found := false
	for _, m := range msgs {
		if strings.Contains(m, today) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected today's date %s in progress messages, got: %v", today, msgs)
	}
}

// TestMockSource_Pagination tests that mock source paginates correctly.
func TestMockSource_Pagination(t *testing.T) {
	source := NewMockStocksSource()
	source.PopulateStocks(100, 3) // 300 items

	// Page 1: items 0-49
	items, err := source.GetStockWarehouses(context.Background(), 50, 0, 3, 1)
	if err != nil {
		t.Fatalf("GetStockWarehouses failed: %v", err)
	}
	if len(items) != 50 {
		t.Errorf("page 1: got %d items, want 50", len(items))
	}

	// Page 2: items 50-99
	items, err = source.GetStockWarehouses(context.Background(), 50, 50, 3, 1)
	if err != nil {
		t.Fatalf("GetStockWarehouses failed: %v", err)
	}
	if len(items) != 50 {
		t.Errorf("page 2: got %d items, want 50", len(items))
	}

	// Beyond end: empty
	items, err = source.GetStockWarehouses(context.Background(), 50, 300, 3, 1)
	if err != nil {
		t.Fatalf("GetStockWarehouses failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("beyond end: got %d items, want 0", len(items))
	}
}

// TestDiscardWriter_SetsDates tests that DiscardWriter returns mock dates.
func TestDiscardWriter_SetsDates(t *testing.T) {
	w := NewDiscardWriter()
	dates, err := w.GetDistinctSnapshotDates(context.Background())
	if err != nil {
		t.Fatalf("GetDistinctSnapshotDates failed: %v", err)
	}
	if len(dates) == 0 {
		t.Error("expected non-empty mock dates")
	}
}
