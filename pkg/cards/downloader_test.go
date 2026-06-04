package cards

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements CardsWriter in-memory for testing.
type mockWriter struct {
	mu    sync.Mutex
	cards []wb.ProductCard
}

func newMockWriter() *mockWriter {
	return &mockWriter{}
}

func (w *mockWriter) SaveCards(_ context.Context, cards []wb.ProductCard) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cards = append(w.cards, cards...)
	return len(cards), nil
}

func (w *mockWriter) CountCards(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.cards), nil
}

func TestDownloader_Basic(t *testing.T) {
	source := NewMockCardsSource(250) // 3 pages (100 + 100 + 50)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalCards != 250 {
		t.Errorf("TotalCards = %d, want 250", result.TotalCards)
	}
	if result.Requests != 3 {
		t.Errorf("Requests = %d, want 3", result.Requests)
	}

	count, _ := writer.CountCards(context.Background())
	if count != 250 {
		t.Errorf("saved count = %d, want 250", count)
	}
}

func TestDownloader_DryRun(t *testing.T) {
	source := NewMockCardsSource(50)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{DryRun: true})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalCards != 50 {
		t.Errorf("TotalCards = %d, want 50", result.TotalCards)
	}

	// Writer should have NO cards (dry-run skips SaveCards)
	count, _ := writer.CountCards(context.Background())
	if count != 0 {
		t.Errorf("saved count = %d, want 0 (dry-run)", count)
	}
}

func TestDownloader_Limit(t *testing.T) {
	source := NewMockCardsSource(250)
	writer := newMockWriter()

	dl := NewDownloader(source, writer, DownloadOptions{Limit: 42})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.TotalCards != 42 {
		t.Errorf("TotalCards = %d, want 42 (limit)", result.TotalCards)
	}

	count, _ := writer.CountCards(context.Background())
	if count != 42 {
		t.Errorf("saved count = %d, want 42", count)
	}
}

func TestDownloader_ContextCancel(t *testing.T) {
	source := NewMockCardsSource(250)
	writer := newMockWriter()

	// Use a context that's already past deadline — guarantees immediate cancellation
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	dl := NewDownloader(source, writer, DownloadOptions{})
	_, err := dl.Run(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}
