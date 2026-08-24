package fbsorders

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// Тестовые двойники
// ============================================================================

// fakeSource исполняет сценарий без сети.
type fakeSource struct {
	orders         []wb.FBSOrder
	statusesByID   map[int64]wb.FBSOrderStatus
	feed           []wb.OrderFeedItem
	statusBatchMax int // максимум ID в одном запрошенном батче
	statusCalls    int
	feedErr        error
}

func (f *fakeSource) FBSOrdersIterator(_ context.Context, _, _ time.Time, cb func([]wb.FBSOrder) error) (int, error) {
	if len(f.orders) == 0 {
		return 0, nil
	}
	if err := cb(f.orders); err != nil {
		return 0, err
	}
	return len(f.orders), nil
}

func (f *fakeSource) GetFBSOrdersStatus(_ context.Context, ids []int64) ([]wb.FBSOrderStatus, error) {
	f.statusCalls++
	if len(ids) > f.statusBatchMax {
		f.statusBatchMax = len(ids)
	}
	out := make([]wb.FBSOrderStatus, 0, len(ids))
	for _, id := range ids {
		if st, ok := f.statusesByID[id]; ok {
			out = append(out, st)
		}
	}
	return out, nil
}

func (f *fakeSource) OrderFeedIterator(_ context.Context, _ time.Time, cb func([]wb.OrderFeedItem) error) (int, error) {
	if f.feedErr != nil {
		return 0, f.feedErr
	}
	if len(f.feed) == 0 {
		return 0, nil
	}
	if err := cb(f.feed); err != nil {
		return 0, err
	}
	return len(f.feed), nil
}

// errSource ломается на указанном вызове статусов (эмуляция сбоя батча).
type errSource struct {
	fake     *fakeSource
	failOn   int // номер вызова GetFBSOrdersStatus (1-based), 0 = не ломаться
	failedAt int
}

func (e *errSource) FBSOrdersIterator(ctx context.Context, from, to time.Time, cb func([]wb.FBSOrder) error) (int, error) {
	return e.fake.FBSOrdersIterator(ctx, from, to, cb)
}

func (e *errSource) GetFBSOrdersStatus(ctx context.Context, ids []int64) ([]wb.FBSOrderStatus, error) {
	e.failedAt++
	if e.failOn > 0 && e.failedAt == e.failOn {
		return nil, fmt.Errorf("simulated status batch failure")
	}
	return e.fake.GetFBSOrdersStatus(ctx, ids)
}

func (e *errSource) OrderFeedIterator(ctx context.Context, from time.Time, cb func([]wb.OrderFeedItem) error) (int, error) {
	return e.fake.OrderFeedIterator(ctx, from, cb)
}

// memWriter — in-memory Writer с записью всех вызовов.
type memWriter struct {
	savedOrders   []wb.FBSOrder
	savedStatuses []wb.FBSOrderStatus
	savedFeed     []wb.OrderFeedItem
	candidateIDs  []int64
	called        bool
}

func (w *memWriter) SaveOrders(_ context.Context, orders []wb.FBSOrder) (int, error) {
	w.called = true
	w.savedOrders = append(w.savedOrders, orders...)
	return len(orders), nil
}

func (w *memWriter) SaveStatuses(_ context.Context, statuses []wb.FBSOrderStatus) (int, error) {
	w.savedStatuses = append(w.savedStatuses, statuses...)
	return len(statuses), nil
}

func (w *memWriter) SaveOrderFeed(_ context.Context, items []wb.OrderFeedItem) (int, error) {
	w.savedFeed = append(w.savedFeed, items...)
	return len(items), nil
}

func (w *memWriter) LoadStatusCandidateIDs(_ context.Context, _ time.Time) ([]int64, error) {
	out := make([]int64, len(w.candidateIDs))
	copy(out, w.candidateIDs)
	return out, nil
}

// makeOrders строит n заданий со статусами.
func makeOrders(n int) ([]wb.FBSOrder, map[int64]wb.FBSOrderStatus) {
	orders := make([]wb.FBSOrder, n)
	byID := make(map[int64]wb.FBSOrderStatus, n)
	base := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < n; i++ {
		id := int64(1000 + i)
		orders[i] = wb.FBSOrder{ID: id, RID: fmt.Sprintf("rid-%d", id), CreatedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)}
		byID[id] = wb.FBSOrderStatus{ID: id, SupplierStatus: "new", WbStatus: "waiting"}
	}
	return orders, byID
}

// ============================================================================
// Тесты
// ============================================================================

// Полнота статусов: все кандидаты (включая древнее незакрытое задание за
// пределами окна) опрашиваются, батчи ровно ≤ 1000, статусы сохраняются.
func TestRun_StatusCompletenessAndBatching(t *testing.T) {
	orders, byID := makeOrders(2500)
	// Древнее незакрытое задание (год назад) — должно попасть в кандидаты.
	const ancientID = int64(42)
	byID[ancientID] = wb.FBSOrderStatus{ID: ancientID, SupplierStatus: "confirm", WbStatus: "waiting"}

	src := &fakeSource{orders: orders, statusesByID: byID}
	w := &memWriter{}
	for i := range orders {
		w.candidateIDs = append(w.candidateIDs, orders[i].ID)
	}
	w.candidateIDs = append(w.candidateIDs, ancientID)

	dl := NewDownloader(src, w, DownloadOptions{StatusWindowDays: 7})
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.StatusCandidates != 2501 {
		t.Errorf("StatusCandidates = %d, want 2501", res.StatusCandidates)
	}
	if src.statusBatchMax > statusBatchSize {
		t.Errorf("batch max = %d, want ≤ %d", src.statusBatchMax, statusBatchSize)
	}
	if res.StatusBatches != 3 { // 1000 + 1000 + 501
		t.Errorf("StatusBatches = %d, want 3", res.StatusBatches)
	}
	if res.TotalStatuses != 2501 {
		t.Errorf("TotalStatuses = %d, want 2501", res.TotalStatuses)
	}
	// Древний ID реально опрошен и сохранён.
	found := false
	for _, st := range w.savedStatuses {
		if st.ID == ancientID {
			found = true
		}
	}
	if !found {
		t.Error("ancient non-terminal order status not saved")
	}
	if res.TotalOrders != 2500 {
		t.Errorf("TotalOrders = %d, want 2500", res.TotalOrders)
	}
}

// Ошибка батча статусов фатальна: прогон возвращает ошибку.
func TestRun_StatusBatchErrorIsFatal(t *testing.T) {
	orders, byID := makeOrders(1500)
	src := &fakeSource{orders: orders, statusesByID: byID}
	w := &memWriter{}
	for i := range orders {
		w.candidateIDs = append(w.candidateIDs, orders[i].ID)
	}

	failing := &errSource{fake: src, failOn: 2}
	dl := NewDownloader(failing, w, DownloadOptions{})
	res, err := dl.Run(context.Background())
	if err == nil {
		t.Fatal("expected fatal error on status batch failure")
	}
	if res == nil || res.TotalStatuses != 1000 {
		t.Errorf("first batch should be saved before failure, got %d", res.TotalStatuses)
	}
	if !strings.Contains(err.Error(), "прогон прерван") {
		t.Errorf("error should mention interrupted run: %v", err)
	}
}

// Сбой ленты заказов нефатален: прогон успешен, ошибка в FeedErr.
func TestRun_FeedFailureIsNonFatal(t *testing.T) {
	orders, byID := makeOrders(10)
	src := &fakeSource{orders: orders, statusesByID: byID, feedErr: fmt.Errorf("429 too many requests")}
	w := &memWriter{}
	for i := range orders {
		w.candidateIDs = append(w.candidateIDs, orders[i].ID)
	}

	dl := NewDownloader(src, w, DownloadOptions{})
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("feed failure must be non-fatal: %v", err)
	}
	if res.FeedErr == "" {
		t.Error("FeedErr should record the feed error")
	}
	if len(w.savedFeed) != 0 {
		t.Errorf("feed rows saved = %d, want 0", len(w.savedFeed))
	}
	if res.TotalStatuses != 10 {
		t.Errorf("statuses = %d, want 10 (feed failure must not affect statuses)", res.TotalStatuses)
	}
}

// Dry-run: ни одной записи в writer.
func TestRun_DryRunNeverWrites(t *testing.T) {
	orders, byID := makeOrders(10)
	src := &fakeSource{orders: orders, statusesByID: byID}
	w := &memWriter{}

	dl := NewDownloader(src, w, DownloadOptions{DryRun: true})
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if w.called {
		t.Error("writer must not be called in dry-run")
	}
	if res.TotalOrders != 10 {
		t.Errorf("TotalOrders = %d, want 10 (counted, not saved)", res.TotalOrders)
	}
	if res.StatusCandidates != 0 {
		t.Errorf("StatusCandidates = %d, want 0 (status phase skipped in dry-run)", res.StatusCandidates)
	}
}

// DisableFeed: лента не вызывается.
func TestRun_DisableFeed(t *testing.T) {
	orders, byID := makeOrders(5)
	src := &fakeSource{orders: orders, statusesByID: byID}
	w := &memWriter{}
	for i := range orders {
		w.candidateIDs = append(w.candidateIDs, orders[i].ID)
	}

	dl := NewDownloader(src, w, DownloadOptions{DisableFeed: true})
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.FeedErr != "" || res.FeedRows != 0 {
		t.Errorf("feed disabled, but FeedErr=%q FeedRows=%d", res.FeedErr, res.FeedRows)
	}
}

// FeedMpOnly: по умолчанию сохраняются только строки склада продавца
// (is_mp=true: FBS/DBS); FeedMpOnly=false пишет и FBW.
func TestRun_FeedMpOnlyFilter(t *testing.T) {
	orders, byID := makeOrders(5)
	mixed := []wb.OrderFeedItem{
		{Srid: "fbs-1", IsMp: true},
		{Srid: "fbw-1", IsMp: false},
		{Srid: "fbs-2", IsMp: true},
		{Srid: "fbw-2", IsMp: false},
	}

	run := func(mpOnly *bool) (*DownloadResult, *memWriter) {
		src := &fakeSource{orders: orders, statusesByID: byID, feed: mixed}
		w := &memWriter{}
		res, err := NewDownloader(src, w, DownloadOptions{Days: 1, FeedDays: 7, FeedMpOnly: mpOnly}).Run(context.Background())
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return res, w
	}

	res, w := run(nil) // nil = дефолт FBS-only
	if res.FeedRows != 2 || len(w.savedFeed) != 2 {
		t.Fatalf("default FBS-only: FeedRows=%d saved=%d, want 2", res.FeedRows, len(w.savedFeed))
	}
	for _, it := range w.savedFeed {
		if !it.IsMp {
			t.Errorf("сохранена не-FBS строка: %+v", it)
		}
	}

	res, w = run(new(bool)) // false = все модели
	if res.FeedRows != 4 || len(w.savedFeed) != 4 {
		t.Fatalf("all-models: FeedRows=%d saved=%d, want 4", res.FeedRows, len(w.savedFeed))
	}
}

// resolveRange: From/To и Days, инклюзивный To.
func TestResolveRange(t *testing.T) {
	dl := NewDownloader(&fakeSource{}, &memWriter{}, DownloadOptions{Days: 90})
	from, to, err := dl.resolveRange()
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	if days := to.Sub(from).Hours() / 24; days < 89.9 || days > 90.1 {
		t.Errorf("days = %.1f, want ~90", days)
	}

	dl = NewDownloader(&fakeSource{}, &memWriter{}, DownloadOptions{From: "2026-07-24", To: "2026-08-16"})
	from, to, err = dl.resolveRange()
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	if from.Format("2006-01-02") != "2026-07-24" || to.Format("2006-01-02") != "2026-08-17" {
		t.Errorf("range = %s..%s, want 2026-07-24..2026-08-17 (To inclusive)", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}

	if _, _, err := NewDownloader(&fakeSource{}, &memWriter{}, DownloadOptions{From: "2026-08-16", To: "2026-07-24"}).resolveRange(); err == nil {
		t.Error("inverted range must fail")
	}
}

// Мок источник + DiscardWriter: полный прогон без паники, счётчики согласованы.
func TestMockSourceWithDiscardWriter(t *testing.T) {
	src := NewMockSource(1200)
	w := NewDiscardWriter()
	dl := NewDownloader(src, w, DownloadOptions{})

	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	o, s, f := w.Saved()
	if o == 0 || s == 0 || f == 0 {
		t.Errorf("discard counters = %d/%d/%d, want all > 0", o, s, f)
	}
	if res.TotalOrders != o {
		t.Errorf("TotalOrders %d != saved %d", res.TotalOrders, o)
	}
}
