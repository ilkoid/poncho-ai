package searchvis

import (
	"context"
	"testing"
	"time"
)

// ============================================================================
// Test 1: Basic download — MockSource → DiscardWriter
// ============================================================================

func TestBasicDownload(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	nmIDs := []int{101, 102, 201, 301, 401}
	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:        nmIDs,
		BeginDate:    "2026-05-28",
		EndDate:      "2026-06-04",
		SnapshotDate: "2026-06-04",
		QueryLimit:   30,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5 nmIDs → 5 position rows (one per nmID)
	if result.PositionRows != 5 {
		t.Errorf("expected 5 position rows, got %d", result.PositionRows)
	}

	// 5 nmIDs × 3 queries each = 15 query rows
	if result.QueryRows != 15 {
		t.Errorf("expected 15 query rows, got %d", result.QueryRows)
	}

	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
}

// ============================================================================
// Test 2: DryRun — rows counted but DiscardWriter not written
// ============================================================================

func TestDryRun(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:        []int{101, 102},
		BeginDate:    "2026-05-28",
		EndDate:      "2026-06-04",
		SnapshotDate: "2026-06-04",
		QueryLimit:   30,
		DryRun:       true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// In DryRun mode, result counts reflect parsed rows (not DB saves)
	if result.PositionRows != 2 {
		t.Errorf("expected 2 position rows in dry-run, got %d", result.PositionRows)
	}
	if result.QueryRows != 6 {
		t.Errorf("expected 6 query rows in dry-run (2 nmIDs × 3), got %d", result.QueryRows)
	}
}

// ============================================================================
// Test 3: Skip phases
// ============================================================================

func TestSkipPositions(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:        []int{101},
		BeginDate:    "2026-05-28",
		EndDate:      "2026-06-04",
		SnapshotDate: "2026-06-04",
		QueryLimit:   30,
		SkipPositions: true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PositionRows != 0 {
		t.Errorf("expected 0 position rows (skipped), got %d", result.PositionRows)
	}
	if result.QueryRows != 3 {
		t.Errorf("expected 3 query rows, got %d", result.QueryRows)
	}
}

func TestSkipQueries(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:        []int{101},
		BeginDate:    "2026-05-28",
		EndDate:      "2026-06-04",
		SnapshotDate: "2026-06-04",
		QueryLimit:   30,
		SkipQueries:  true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PositionRows != 1 {
		t.Errorf("expected 1 position row, got %d", result.PositionRows)
	}
	if result.QueryRows != 0 {
		t.Errorf("expected 0 query rows (skipped), got %d", result.QueryRows)
	}
}

func TestSkipAll(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:         []int{101},
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-04",
		SnapshotDate:  "2026-06-04",
		SkipPositions: true,
		SkipQueries:   true,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PositionRows != 0 || result.QueryRows != 0 {
		t.Errorf("expected 0/0 rows when all phases skipped, got %d/%d", result.PositionRows, result.QueryRows)
	}
}

// ============================================================================
// Test 4: Context cancellation
// ============================================================================

type slowSource struct {
	MockSource
}

// FetchPositions blocks until context is cancelled, then returns error.
func (s *slowSource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestContextCancellation(t *testing.T) {
	src := &slowSource{}
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:     []int{101},
		BeginDate: "2026-05-28",
		EndDate:   "2026-06-04",
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if ctx.Err() != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", ctx.Err())
	}
}

// ============================================================================
// Test 5: MockReader
// ============================================================================

func TestMockReader(t *testing.T) {
	reader := NewMockReader()

	nmIDs, err := reader.GetDistinctNmIDs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nmIDs) != 4 {
		t.Errorf("expected 4 nmIDs, got %d", len(nmIDs))
	}

	articles, err := reader.GetSupplierArticlesByNmIDs(context.Background(), nmIDs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(articles) != 4 {
		t.Errorf("expected 4 articles, got %d", len(articles))
	}
	if articles[101] != "1240001" {
		t.Errorf("expected article '1240001' for nmID 101, got %q", articles[101])
	}

	active, err := reader.FilterActiveNmIDs(context.Background(), nmIDs, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(active) != 3 {
		t.Errorf("expected 3 active nmIDs, got %d", len(active))
	}

	// activeDays=0 → no filtering
	all, err := reader.FilterActiveNmIDs(context.Background(), nmIDs, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 nmIDs (no filter), got %d", len(all))
	}
}

// ============================================================================
// Test 6: Empty nmIDs
// ============================================================================

func TestEmptyNmIDs(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:     []int{},
		BeginDate: "2026-05-28",
		EndDate:   "2026-06-04",
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PositionRows != 0 || result.QueryRows != 0 {
		t.Errorf("expected 0 rows with empty nmIDs, got %d/%d", result.PositionRows, result.QueryRows)
	}
}

// ============================================================================
// Test 7: Large batch — verify batching works correctly
// ============================================================================

func TestLargeBatch(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	// 250 nmIDs → 3 position batches (100+100+50) + 5 query batches (50×5)
	nmIDs := make([]int, 250)
	for i := range nmIDs {
		nmIDs[i] = 100 + i
	}

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:        nmIDs,
		BeginDate:    "2026-05-28",
		EndDate:      "2026-06-04",
		SnapshotDate: "2026-06-04",
		QueryLimit:   30,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 250 nmIDs → 250 position rows (1 per nmID)
	if result.PositionRows != 250 {
		t.Errorf("expected 250 position rows, got %d", result.PositionRows)
	}

	// 250 nmIDs × 3 queries = 750 query rows
	if result.QueryRows != 750 {
		t.Errorf("expected 750 query rows, got %d", result.QueryRows)
	}
}

// ============================================================================
// Test 8: Duration is set
// ============================================================================

func TestDurationSet(t *testing.T) {
	src := NewMockSource()
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:     []int{101},
		BeginDate: "2026-05-28",
		EndDate:   "2026-06-04",
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}
	if result.Duration > time.Second {
		t.Errorf("mock download should be fast, took %v", result.Duration)
	}
}
