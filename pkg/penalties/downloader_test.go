package penalties

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestBasicDownload(t *testing.T) {
	src := NewMockPenaltiesSource(100)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days:       90,
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 100 {
		t.Errorf("expected 100 penalties, got %d", result.TotalPenalties)
	}
	if result.TotalPages != 1 {
		t.Errorf("expected 1 page, got %d", result.TotalPages)
	}
	if writer.Saved() != 100 {
		t.Errorf("discard writer expected 100 saved, got %d", writer.Saved())
	}
}

func TestDryRun(t *testing.T) {
	src := NewMockPenaltiesSource(50)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days:   90,
		DryRun: true,
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 50 {
		t.Errorf("expected 50 penalties counted, got %d", result.TotalPenalties)
	}
	if writer.Saved() != 0 {
		t.Errorf("dry-run should not save, but got %d", writer.Saved())
	}
}

func TestRewriteMode(t *testing.T) {
	src := NewMockPenaltiesSource(30)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days:    90,
		Rewrite: true,
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 30 {
		t.Errorf("expected 30 penalties, got %d", result.TotalPenalties)
	}
}

func TestContextCancellation(t *testing.T) {
	src := NewMockPenaltiesSource(200)
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dl := NewDownloader(src, writer, DownloadOptions{Days: 90})
	_, err := dl.Run(ctx)

	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestExplicitDateRange(t *testing.T) {
	src := NewMockPenaltiesSource(10)
	writer := NewDiscardWriter()

	var progressMsgs []string
	dl := NewDownloader(src, writer, DownloadOptions{
		From: "2026-01-01",
		To:   "2026-03-01",
		OnProgress: func(msg string) {
			progressMsgs = append(progressMsgs, msg)
		},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 10 {
		t.Errorf("expected 10 penalties, got %d", result.TotalPenalties)
	}
	if len(progressMsgs) == 0 {
		t.Error("expected progress messages")
	}
}

func TestDefaultDays(t *testing.T) {
	src := NewMockPenaltiesSource(5)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{}) // Days=0 → default 90
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 5 {
		t.Errorf("expected 5 penalties, got %d", result.TotalPenalties)
	}
}

func TestDiscardWriterConcurrency(t *testing.T) {
	writer := NewDiscardWriter()

	var done atomic.Int32
	for i := 0; i < 10; i++ {
		go func() {
			items := make([]wb.MeasurementPenaltyItem, 10)
			writer.SavePenalties(context.Background(), items)
			done.Add(1)
		}()
	}

	for done.Load() < 10 {
		time.Sleep(time.Millisecond)
	}

	if writer.Saved() != 100 {
		t.Errorf("expected 100 total, got %d", writer.Saved())
	}
}

// ============================================================================
// Filter tests
// ============================================================================

func TestFilterByNmIds(t *testing.T) {
	src := NewMockPenaltiesSource(100) // nm_id = 100000 + i
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days: 90,
		Filter: config.PenaltiesFilterConfig{
			NmIds: []int{100005, 100010, 100050}, // only 3 of 100
		},
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 3 {
		t.Errorf("expected 3 penalties after nm_ids filter, got %d", result.TotalPenalties)
	}
}

func TestFilterBySubject(t *testing.T) {
	src := NewMockPenaltiesSource(100) // subjects: "Кроссовки", "Футболка", "Джинсы", "Куртка", "Шорты"
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days: 90,
		Filter: config.PenaltiesFilterConfig{
			Subject: "кроссов", // case-insensitive contains
		},
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "Кроссовки" appears at i%5==0 → 20 of 100
	if result.TotalPenalties != 20 {
		t.Errorf("expected 20 penalties after subject filter, got %d", result.TotalPenalties)
	}
}

func TestFilterByIsValid(t *testing.T) {
	src := NewMockPenaltiesSource(50) // IsValid: i%5 != 0 → 80% true, 20% false
	writer := NewDiscardWriter()

	valid := true
	dl := NewDownloader(src, writer, DownloadOptions{
		Days: 90,
		Filter: config.PenaltiesFilterConfig{
			IsValid: &valid,
		},
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 50 items, i%5==0 are invalid (10 items), so 40 are valid
	if result.TotalPenalties != 40 {
		t.Errorf("expected 40 confirmed penalties, got %d", result.TotalPenalties)
	}
}

func TestFilterCombined(t *testing.T) {
	src := NewMockPenaltiesSource(100) // subjects cycle: "Кроссовки"(i%5==0), "Футболка"(i%5==1), ...
	writer := NewDiscardWriter()

	// "Футболка" = i%5==1 → 20 items, IsValid: 1%5!=0 = true → all 20 confirmed
	valid := true
	dl := NewDownloader(src, writer, DownloadOptions{
		Days: 90,
		Filter: config.PenaltiesFilterConfig{
			Subject: "футболк", // case-insensitive contains → "Футболка"
			IsValid: &valid,    // all "Футболка" items have IsValid=true
		},
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 20 {
		t.Errorf("expected 20 confirmed футболка penalties, got %d", result.TotalPenalties)
	}
}

func TestFilterEmptyMatches(t *testing.T) {
	src := NewMockPenaltiesSource(50)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Days: 90,
		Filter: config.PenaltiesFilterConfig{
			NmIds: []int{99999999}, // no match
		},
		OnProgress: func(msg string) {},
	})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPenalties != 0 {
		t.Errorf("expected 0 penalties with no-match filter, got %d", result.TotalPenalties)
	}
}
