package onec

import (
	"context"
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// MockRestsSource — deterministic fake data for --mock mode and tests
// ---------------------------------------------------------------------------

// MockRestsSource implements RestsSource with configurable fake data.
type MockRestsSource struct {
	mu sync.Mutex

	// FailOn controls whether FetchRests should return an error.
	FailOn bool

	// NumGoods/NumSKUs/NumStorages control generated data volume.
	NumGoods    int
	NumSKUs     int
	NumStorages int
}

// NewMockRestsSource creates a MockRestsSource with default data (10 goods × 2 SKUs × 3 storages = 60 rows).
func NewMockRestsSource() *MockRestsSource {
	return &MockRestsSource{
		NumGoods:    10,
		NumSKUs:     2,
		NumStorages: 3,
	}
}

// FetchRests generates deterministic fake rests data and writes via the writer.
func (m *MockRestsSource) FetchRests(ctx context.Context, _ string, filter RestsStorageFilter,
	writer RestsWriter, snapshotDate string) (int, int, int, error) {

	m.mu.Lock()
	failOn := m.FailOn
	nGoods := m.NumGoods
	nSKUs := m.NumSKUs
	nStorages := m.NumStorages
	m.mu.Unlock()

	if failOn {
		return 0, 0, 0, fmt.Errorf("mock: rests fetch failed")
	}

	storageNames := []string{"Склад Москва", "Склад Вологда", "Склад СПб"}
	storageGUIDs := []string{"storage-guid-msk", "storage-guid-vlg", "storage-guid-spb"}

	batch := make([]RestsRow, 0, 500)
	totalSaved := 0
	filteredOut := 0

	for i := range nGoods {
		guid := fmt.Sprintf("good-guid-%03d", i)

		for j := range nSKUs {
			skuGUID := fmt.Sprintf("sku-guid-%03d-%d", i, j)

			for k := range nStorages {
				stName := storageNames[k%len(storageNames)]
				stGUID := storageGUIDs[k%len(storageGUIDs)]

				if !filter.Matches(stGUID, stName) {
					filteredOut++
					continue
				}

				batch = append(batch, RestsRow{
					GoodGUID:    guid,
					SKUGUID:     skuGUID,
					StorageGUID: stGUID,
					StorageName: stName,
					Stock:       10 + i,
					Reserv:      i % 3,
					Free:        10 + i - (i % 3),
					FirstStage:  k == 0,
				})
			}
		}

		// Flush batch every 500 rows
		if len(batch) >= 500 {
			n, err := writer.SaveRests(ctx, batch, snapshotDate)
			if err != nil {
				return i + 1, totalSaved, filteredOut, err
			}
			totalSaved += n
			batch = batch[:0]
		}
	}

	// Flush remaining
	if len(batch) > 0 {
		n, err := writer.SaveRests(ctx, batch, snapshotDate)
		if err != nil {
			return nGoods, totalSaved, filteredOut, err
		}
		totalSaved += n
	}

	return nGoods, totalSaved, filteredOut, nil
}

// ---------------------------------------------------------------------------
// RestsDiscardWriter — no-op writer for --mock mode (NEVER touches DB)
// ---------------------------------------------------------------------------

// RestsDiscardWriter implements RestsWriter with thread-safe counting.
// All writes are discarded — used for --mock mode, DryRun, and testing.
type RestsDiscardWriter struct {
	mu sync.Mutex

	savedCount   int
	countValue   int // returned by CountRests
	cleanCalled  bool
	purgeCalled  bool
}

// NewRestsDiscardWriter creates a RestsDiscardWriter.
func NewRestsDiscardWriter() *RestsDiscardWriter {
	return &RestsDiscardWriter{}
}

// SaveRests counts and discards.
func (w *RestsDiscardWriter) SaveRests(_ context.Context, rows []RestsRow, _ string) (int, error) {
	w.mu.Lock()
	w.savedCount += len(rows)
	w.mu.Unlock()
	return len(rows), nil
}

// CountRests returns the configured count value (0 by default).
func (w *RestsDiscardWriter) CountRests(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.countValue, nil
}

// CleanRests is a no-op.
func (w *RestsDiscardWriter) CleanRests(_ context.Context) error {
	w.mu.Lock()
	w.cleanCalled = true
	w.mu.Unlock()
	return nil
}

// PurgeOldRestsSnapshots is a no-op.
func (w *RestsDiscardWriter) PurgeOldRestsSnapshots(_ context.Context, _ int) (int, error) {
	w.mu.Lock()
	w.purgeCalled = true
	w.mu.Unlock()
	return 0, nil
}

// Saved returns the total number of rows passed to SaveRests (thread-safe).
func (w *RestsDiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.savedCount
}

// CleanWasCalled returns whether CleanRests was called.
func (w *RestsDiscardWriter) CleanWasCalled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanCalled
}

// SetCount configures the value returned by CountRests.
func (w *RestsDiscardWriter) SetCount(n int) {
	w.mu.Lock()
	w.countValue = n
	w.mu.Unlock()
}
