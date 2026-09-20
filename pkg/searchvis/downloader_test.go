package searchvis

import (
	"context"
	"errors"
	"fmt"
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
		NmIDs:         []int{101},
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-04",
		SnapshotDate:  "2026-06-04",
		QueryLimit:    30,
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
// Test 8: Partial batch failure — successful batches are still saved
// (incremental per-batch saves, regression for the buffer-then-save design
// that lost the whole phase when the run was interrupted mid-way)
// ============================================================================

// flakySource returns rows for the first positions batch, then errors on
// every subsequent FetchPositions call; queries always succeed.
type flakySource struct {
	MockSource
	calls int
	fail  *int // when non-nil, FetchPositions fails from this call on
}

func (s *flakySource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	s.calls++
	if s.fail != nil && s.calls >= *s.fail {
		return nil, context.DeadlineExceeded
	}
	return s.MockSource.FetchPositions(ctx, req)
}

func TestPartialBatchFailureStillSaves(t *testing.T) {
	// 250 nmIDs → 3 position batches of 100/100/50.
	nmIDs := make([]int, 250)
	for i := range nmIDs {
		nmIDs[i] = 100 + i
	}

	failFrom := 2 // batch 2 and 3 fail
	src := &flakySource{MockSource: *NewMockSource(), fail: &failFrom}
	writer := NewDiscardWriter()

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

	// Batch 1 (100 rows) must be saved despite batches 2-3 failing.
	if result.PositionRows != 100 {
		t.Errorf("expected 100 position rows from first batch, got %d", result.PositionRows)
	}
	if writer.SavedPositions() != 100 {
		t.Errorf("expected writer to receive 100 rows, got %d", writer.SavedPositions())
	}
	if result.Errors != 2 {
		t.Errorf("expected 2 batch errors, got %d", result.Errors)
	}

	// Queries phase unaffected: 250 nmIDs × 3 = 750 rows.
	if result.QueryRows != 750 {
		t.Errorf("expected 750 query rows, got %d", result.QueryRows)
	}
}

// ============================================================================
// Test 9: Duration is set
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

// ============================================================================
// Test 10-12: Rescue-долив упавших батчей
// ============================================================================

// rescueSource fails positions batches identified by their first nmID for the
// first failTimes attempts, then succeeds (simulates 429-шторм, кончившийся
// к моменту rescue-прохода).
type rescueSource struct {
	MockSource
	failFirst map[int]int // first nmID of batch → how many leading attempts fail
	calls     map[int]int
}

func (s *rescueSource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	key := req.NmIDs[0]
	s.calls[key]++
	if s.failFirst[key] >= s.calls[key] {
		return nil, fmt.Errorf("rate limit exceeded, retry after the period specified in the X-RateLimit-Retry header")
	}
	return s.MockSource.FetchPositions(ctx, req)
}

func TestRescueRecoversFailedBatches(t *testing.T) {
	// 250 nmIDs → 3 position batches; batches 2-3 падают при первой попытке
	// (основной цикл), при второй (rescue-проход) успешны.
	nmIDs := make([]int, 250)
	for i := range nmIDs {
		nmIDs[i] = 100 + i
	}

	src := &rescueSource{
		MockSource: *NewMockSource(),
		failFirst:  map[int]int{200: 1, 300: 1},
		calls:      map[int]int{},
	}
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:              nmIDs,
		BeginDate:          "2026-05-28",
		EndDate:            "2026-06-04",
		SnapshotDate:       "2026-06-04",
		QueryLimit:         30,
		SkipQueries:        true,
		RescueTimeout:      time.Minute,
		RescueInitialSleep: time.Millisecond,
		RescuePassSleep:    time.Millisecond,
	})

	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Errors != 0 {
		t.Errorf("expected 0 errors after rescue, got %d", result.Errors)
	}
	if result.PositionRows != 250 {
		t.Errorf("expected all 250 position rows recovered, got %d", result.PositionRows)
	}
	if writer.SavedPositions() != 250 {
		t.Errorf("expected writer to receive 250 rows, got %d", writer.SavedPositions())
	}
}

// alwaysFailSource fails every positions fetch instantly (имитация «ядовитого»
// батча / затяжного шторма — ошибка возвращается без ретраев клиента).
type alwaysFailSource struct {
	MockSource
}

func (s *alwaysFailSource) FetchPositions(ctx context.Context, req PositionsRequest) ([]SearchPositionRow, error) {
	return nil, fmt.Errorf("400 bad request")
}

func TestRescueBudgetExhausted(t *testing.T) {
	// 150 nmIDs → 2 position batches, оба падают всегда. Rescue обязан
	// остановиться по бюджету (регрессия на зацикливание) и честно
	// посчитать потери.
	nmIDs := make([]int, 150)
	for i := range nmIDs {
		nmIDs[i] = 100 + i
	}

	src := &alwaysFailSource{MockSource: *NewMockSource()}
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:              nmIDs,
		BeginDate:          "2026-05-28",
		EndDate:            "2026-06-04",
		SnapshotDate:       "2026-06-04",
		QueryLimit:         30,
		SkipQueries:        true,
		RescueTimeout:      30 * time.Millisecond,
		RescueInitialSleep: 5 * time.Millisecond,
		RescuePassSleep:    5 * time.Millisecond,
	})

	start := time.Now()
	result, err := dl.Run(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Errors != 2 {
		t.Errorf("expected 2 lost batches after budget exhausted, got %d", result.Errors)
	}
	if result.PositionRows != 0 {
		t.Errorf("expected 0 rows saved, got %d", result.PositionRows)
	}
	if elapsed > 2*time.Second {
		t.Errorf("rescue must be bounded by its budget, took %v", elapsed)
	}
}

func TestRescueCtxCancelDuringSleep(t *testing.T) {
	// Отмена во время rescue initial sleep должна прервать Run с ctx-ошибкой
	// (как ctx-cancel в основном цикле), а не проигнорироваться.
	src := &alwaysFailSource{MockSource: *NewMockSource()}
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	dl := NewDownloader(src, writer, DownloadOptions{
		NmIDs:              []int{100},
		BeginDate:          "2026-05-28",
		EndDate:            "2026-06-04",
		SnapshotDate:       "2026-06-04",
		QueryLimit:         30,
		SkipQueries:        true,
		RescueTimeout:      time.Minute,
		RescueInitialSleep: 500 * time.Millisecond,
		RescuePassSleep:    time.Millisecond,
	})

	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
