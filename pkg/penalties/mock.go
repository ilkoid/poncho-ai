package penalties

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockPenaltiesSource returns deterministic fake penalty data.
// Implements PenaltiesSource for --mock mode and testing.
type MockPenaltiesSource struct {
	mu        sync.RWMutex
	penalties []wb.MeasurementPenaltyItem
}

// NewMockPenaltiesSource creates a mock source pre-populated with count penalties.
func NewMockPenaltiesSource(count int) *MockPenaltiesSource {
	m := &MockPenaltiesSource{}
	m.populate(count)
	return m
}

// MeasurementPenaltiesIterator iterates over mock penalties, calling callback once with all items.
// Respects context cancellation.
func (m *MockPenaltiesSource) MeasurementPenaltiesIterator(
	ctx context.Context,
	_, _ string,
	_, _ int,
	callback func([]wb.MeasurementPenaltyItem, int) error,
) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.penalties) == 0 {
		return 0, nil
	}

	if err := callback(m.penalties, len(m.penalties)); err != nil {
		return 0, err
	}
	return len(m.penalties), nil
}

// populate fills mock with deterministic penalty data.
func (m *MockPenaltiesSource) populate(count int) {
	subjects := []string{"Кроссовки", "Футболка", "Джинсы", "Куртка", "Шорты"}
	baseTime := time.Now().Add(-24 * time.Hour)

	items := make([]wb.MeasurementPenaltyItem, count)
	for i := 0; i < count; i++ {
		subject := subjects[i%len(subjects)]
		penaltyTime := baseTime.Add(time.Duration(i) * time.Minute)

		items[i] = wb.MeasurementPenaltyItem{
			NmId:        100000 + i,
			SubjectName: subject,
			DimId:       98151000 + i,
			PrcOver:     30.0 + float64(i%100),
			Volume:      5.0 + float64(i%10)*0.5,
			Width:       float64(20 + i%15),
			Length:      float64(25 + i%10),
			Height:      float64(5 + i%8),
			VolumeSup:   4.0 + float64(i%8)*0.4,
			WidthSup:    float64(18 + i%12),
			LengthSup:   float64(22 + i%8),
			HeightSup:   float64(4 + i%6),
			PhotoUrls:   []string{fmt.Sprintf("https://img.wbstatic.net/penalty/%d.jpg", i)},
			DtBonus:     penaltyTime.Format("2006-01-02T15:04:05"),
			IsValid:     i%5 != 0, // 80% confirmed, 20% cancelled
			IsValidDt:   penaltyTime.Add(2 * time.Hour).Format("2006-01-02T15:04:05"),
			ReversalAmount: func() float64 {
				if i%5 == 0 {
					return 100.0 + float64(i%50)*10
				}
				return 0
			}(),
			PenaltyAmount: 200.0 + float64(i%100)*5,
		}
	}

	m.mu.Lock()
	m.penalties = items
	m.mu.Unlock()
}

// DiscardWriter implements PenaltiesWriter with no-op persistence.
// Used in --mock mode to guarantee zero DB interaction.
type DiscardWriter struct {
	mu    sync.Mutex
	saved int
}

// NewDiscardWriter creates a no-op writer for mock mode.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SavePenalties counts items but never writes to any database.
func (w *DiscardWriter) SavePenalties(_ context.Context, items []wb.MeasurementPenaltyItem) (int, error) {
	w.mu.Lock()
	w.saved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// DeletePenaltiesOlderThan is a no-op for mock mode.
func (w *DiscardWriter) DeletePenaltiesOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// Saved returns the total count of penalties "saved" (counted).
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saved
}
