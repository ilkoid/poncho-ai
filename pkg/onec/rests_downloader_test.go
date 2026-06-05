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

type trackingRestsWriter struct {
	mu sync.Mutex

	batches      [][]RestsRow
	cleanCalled  bool
	purgeCalled  bool
	purgeDays    int
	countValue   int
	saveErr      error // inject error on SaveRests
}

func (tw *trackingRestsWriter) SaveRests(_ context.Context, rows []RestsRow, _ string) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.saveErr != nil {
		return 0, tw.saveErr
	}
	// Copy to prevent mutation
	batch := make([]RestsRow, len(rows))
	copy(batch, rows)
	tw.batches = append(tw.batches, batch)
	return len(rows), nil
}

func (tw *trackingRestsWriter) CountRests(_ context.Context) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.countValue, nil
}

func (tw *trackingRestsWriter) CleanRests(_ context.Context) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.cleanCalled = true
	return nil
}

func (tw *trackingRestsWriter) PurgeOldRestsSnapshots(_ context.Context, days int) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.purgeCalled = true
	tw.purgeDays = days
	return 0, nil
}

func (tw *trackingRestsWriter) totalSaved() int {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	total := 0
	for _, b := range tw.batches {
		total += len(b)
	}
	return total
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRestsBasicDownload(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{countValue: 60}

	var progress []string
	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
		SnapshotDate: "2026-06-05",
		OnProgress: func(msg string) {
			progress = append(progress, msg)
		},
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 10 goods × 2 SKUs × 3 storages = 60 rows
	if result.GoodsCount != 10 {
		t.Errorf("goods count: got %d, want 10", result.GoodsCount)
	}
	if result.TotalSaved != 60 {
		t.Errorf("total saved: got %d, want 60", result.TotalSaved)
	}
	if result.FilteredOut != 0 {
		t.Errorf("filtered out: got %d, want 0", result.FilteredOut)
	}
	if result.TotalInDB != 60 {
		t.Errorf("total in DB: got %d, want 60", result.TotalInDB)
	}
	if result.Duration == 0 {
		t.Error("duration should be > 0")
	}

	// Verify writer received data
	if tw.totalSaved() != 60 {
		t.Errorf("writer total: got %d, want 60", tw.totalSaved())
	}
	if tw.cleanCalled {
		t.Error("CleanRests should NOT have been called")
	}
	if tw.purgeCalled {
		t.Error("PurgeOldRestsSnapshots should NOT have been called (no retention)")
	}

	// Verify progress messages
	if len(progress) == 0 {
		t.Fatal("expected progress messages")
	}
	if !strings.Contains(progress[0], "60 rows") {
		t.Errorf("progress[0]: %q should contain row count", progress[0])
	}
}

func TestRestsDryRun(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{countValue: 0}

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
		SnapshotDate: "2026-06-05",
		DryRun:       true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Real writer should NOT have received data (DryRun swaps to DiscardWriter)
	if tw.totalSaved() != 0 {
		t.Errorf("dry-run should not write to real writer, got %d rows", tw.totalSaved())
	}

	// But result should still show counts from source
	if result.GoodsCount != 10 {
		t.Errorf("goods count: got %d, want 10", result.GoodsCount)
	}
	if result.TotalSaved != 60 {
		t.Errorf("total saved: got %d, want 60 (counted by DiscardWriter)", result.TotalSaved)
	}
}

func TestRestsCleanFlag(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{countValue: 60}

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
		SnapshotDate: "2026-06-05",
		Clean:        true,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !tw.cleanCalled {
		t.Error("CleanRests should have been called with Clean=true")
	}

	// Purge should NOT be called after clean (table is already empty)
	if tw.purgeCalled {
		t.Error("PurgeOldRestsSnapshots should NOT be called when Clean=true")
	}
}

func TestRestsCleanFlagDryRun(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{}

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
		SnapshotDate: "2026-06-05",
		Clean:        true,
		DryRun:       true,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tw.cleanCalled {
		t.Error("CleanRests should NOT have been called when DryRun=true")
	}
}

func TestRestsRetention(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{countValue: 100}

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:       "http://test/rests/",
		SnapshotDate:  "2026-06-05",
		RetentionDays: 7,
	})

	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !tw.purgeCalled {
		t.Error("PurgeOldRestsSnapshots should have been called")
	}
	if tw.purgeDays != 7 {
		t.Errorf("purge days: got %d, want 7", tw.purgeDays)
	}
}

func TestRestsDiscardWriter(t *testing.T) {
	w := NewRestsDiscardWriter()

	n, err := w.SaveRests(context.Background(), make([]RestsRow, 25), "2026-06-05")
	if err != nil || n != 25 {
		t.Fatalf("SaveRests: n=%d, err=%v", n, err)
	}

	n, err = w.SaveRests(context.Background(), make([]RestsRow, 35), "2026-06-05")
	if err != nil || n != 35 {
		t.Fatalf("SaveRests: n=%d, err=%v", n, err)
	}

	if w.Saved() != 60 {
		t.Errorf("Saved: got %d, want 60", w.Saved())
	}

	err = w.CleanRests(context.Background())
	if err != nil {
		t.Fatalf("CleanRests: err=%v", err)
	}
	if !w.CleanWasCalled() {
		t.Error("CleanWasCalled should be true")
	}

	// CountRests returns 0 by default
	count, err := w.CountRests(context.Background())
	if err != nil || count != 0 {
		t.Errorf("CountRests: count=%d, err=%v", count, err)
	}

	// SetCount changes the return value
	w.SetCount(42)
	count, err = w.CountRests(context.Background())
	if err != nil || count != 42 {
		t.Errorf("CountRests after SetCount: count=%d, err=%v", count, err)
	}
}

func TestRestsStorageFilter(t *testing.T) {
	// Empty filter = accept all
	f := RestsStorageFilter{}
	if !f.Matches("any-guid", "any name") {
		t.Error("empty filter should accept all")
	}

	// GUID match (case-insensitive)
	f = RestsStorageFilter{GUIDs: []string{"ABC-123"}}
	if !f.Matches("abc-123", "anything") {
		t.Error("should match GUID case-insensitively")
	}
	if f.Matches("xyz-789", "anything") {
		t.Error("should not match different GUID")
	}

	// Name pattern match (case-insensitive substring)
	f = RestsStorageFilter{NamePatterns: []string{"Москва"}}
	if !f.Matches("any-guid", "Склад Москва") {
		t.Error("should match name substring")
	}
	if !f.Matches("any-guid", "склад москва центр") {
		t.Error("should match case-insensitively")
	}
	if f.Matches("any-guid", "Склад Вологда") {
		t.Error("should not match different name")
	}

	// Union: GUID OR name
	f = RestsStorageFilter{
		GUIDs:        []string{"guid-A"},
		NamePatterns: []string{"Москва"},
	}
	if !f.Matches("guid-A", "Склад Вологда") {
		t.Error("should match by GUID even if name doesn't match")
	}
	if !f.Matches("guid-B", "Склад Москва") {
		t.Error("should match by name even if GUID doesn't match")
	}
	if f.Matches("guid-B", "Склад Вологда") {
		t.Error("should not match when neither GUID nor name matches")
	}
}

func TestRestsContextCancel(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
		SnapshotDate: "2026-06-05",
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestNormalizeRestsGUID(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"_13209c78_1651_11e4_9401_2c768a56a25b", "13209c78-1651-11e4-9401-2c768a56a25b"},
		{"abc123", "abc123"},           // no underscore prefix, no underscores
		{"_abc_def", "abc-def"},        // underscore prefix + internal underscores
		{"", ""},                        // empty
		{"no-prefix", "no-prefix"},     // no underscore prefix
	}

	for _, tc := range tests {
		got := normalizeRestsGUID(tc.input)
		if got != tc.want {
			t.Errorf("normalizeRestsGUID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRestsMockSourceWithFilter(t *testing.T) {
	src := NewMockRestsSource()
	w := NewRestsDiscardWriter()

	// Filter that only accepts "Москва" storage
	filter := RestsStorageFilter{NamePatterns: []string{"Москва"}}
	goodsCount, saved, filteredOut, err := src.FetchRests(
		context.Background(), "http://test/", filter, w, "2026-06-05")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 10 goods × 2 SKUs × 1 matching storage = 20 rows saved
	if goodsCount != 10 {
		t.Errorf("goods count: got %d, want 10", goodsCount)
	}
	if saved != 20 {
		t.Errorf("saved: got %d, want 20", saved)
	}
	// 10 goods × 2 SKUs × 2 non-matching storages = 40 filtered out
	if filteredOut != 40 {
		t.Errorf("filtered out: got %d, want 40", filteredOut)
	}
}

func TestRestsDownloadDuration(t *testing.T) {
	src := NewMockRestsSource()
	tw := &trackingRestsWriter{}

	dl := NewRestsDownloader(src, tw, RestsDownloadOptions{
		RestURL:      "http://test/rests/",
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
