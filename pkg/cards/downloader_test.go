package cards

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// mockWriter implements CardsWriter in-memory for testing.
type mockWriter struct {
	mu         sync.Mutex
	cards      []wb.ProductCard
	cursorJSON string
	cursorNmID int
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

func (w *mockWriter) GetCardsLastCursor(_ context.Context) (string, int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursorJSON, w.cursorNmID, nil
}

func (w *mockWriter) SaveCardsCursor(_ context.Context, updatedAt string, nmID int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cursorJSON = updatedAt
	w.cursorNmID = nmID
	return nil
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

	// Cursor should NOT be saved (dry-run skips SaveCardsCursor)
	updatedAt, nmID, _ := writer.GetCardsLastCursor(context.Background())
	if updatedAt != "" || nmID != 0 {
		t.Errorf("cursor should be empty (dry-run), got updatedAt=%s nmID=%d", updatedAt, nmID)
	}
}

func TestDownloader_Resume(t *testing.T) {
	source := NewMockCardsSource(250)
	writer := newMockWriter()

	// First run: download all 250 cards
	dl1 := NewDownloader(source, writer, DownloadOptions{})
	result1, err := dl1.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run() error: %v", err)
	}
	if result1.TotalCards != 250 {
		t.Fatalf("first run TotalCards = %d, want 250", result1.TotalCards)
	}

	// Check cursor was saved
	updatedAt, nmID, _ := writer.GetCardsLastCursor(context.Background())
	if updatedAt == "" && nmID == 0 {
		t.Fatal("cursor not saved after first run")
	}

	// Second run with Resume=true: mock source has same data,
	// so it will resume from cursor and download remaining cards.
	// Since the mock doesn't actually filter by cursor in GetCardsPage
	// (it always returns from the cursor position), this tests the wiring.
	writer2 := newMockWriter()
	// Pre-set cursor as if previous run saved it
	writer2.SaveCardsCursor(context.Background(), updatedAt, nmID)

	dl2 := NewDownloader(source, writer2, DownloadOptions{Resume: true})
	_, err = dl2.Run(context.Background())
	if err != nil {
		t.Fatalf("resume Run() error: %v", err)
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

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
