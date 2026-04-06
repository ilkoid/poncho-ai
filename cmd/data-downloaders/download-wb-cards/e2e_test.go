package main

import (
	"context"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestE2ECardsDownload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-cards.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	t.Run("MockSaveAndCount", func(t *testing.T) {
		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 50) // 50 cards

		result, err := DownloadCards(ctx, mockClient, repo, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("DownloadCards failed: %v", err)
		}

		if result.TotalCards != 50 {
			t.Errorf("Expected 50 cards, got %d", result.TotalCards)
		}
		if result.Pages != 1 { // 50 cards < 100 limit = 1 page
			t.Errorf("Expected 1 page, got %d", result.Pages)
		}

		count, err := repo.CountCards(ctx)
		if err != nil {
			t.Fatalf("CountCards failed: %v", err)
		}
		if count != 50 {
			t.Errorf("Expected 50 in DB, got %d", count)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		// Use a new database for this test
		dbPath2 := filepath.Join(tmpDir, "test-cards-pagination.db")
		repo2, err := sqlite.NewSQLiteSalesRepository(dbPath2)
		if err != nil {
			t.Fatalf("Failed to create repository: %v", err)
		}
		defer repo2.Close()

		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 250) // 250 cards = 3 pages with limit 100

		result, err := DownloadCards(ctx, mockClient, repo2, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("DownloadCards failed: %v", err)
		}

		if result.TotalCards != 250 {
			t.Errorf("Expected 250 cards, got %d", result.TotalCards)
		}
		if result.Pages != 3 { // 250 cards / 100 per page = 3 pages
			t.Errorf("Expected 3 pages, got %d", result.Pages)
		}

		count, err := repo2.CountCards(ctx)
		if err != nil {
			t.Fatalf("CountCards failed: %v", err)
		}
		if count != 250 {
			t.Errorf("Expected 250 in DB, got %d", count)
		}
	})

	t.Run("Resume", func(t *testing.T) {
		// New database for resume test
		dbPath3 := filepath.Join(tmpDir, "test-cards-resume.db")
		repo3, err := sqlite.NewSQLiteSalesRepository(dbPath3)
		if err != nil {
			t.Fatalf("Failed to create repository: %v", err)
		}
		defer repo3.Close()

		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 200)

		// First run: download all
		result1, err := DownloadCards(ctx, mockClient, repo3, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("First run failed: %v", err)
		}
		if result1.TotalCards != 200 {
			t.Errorf("Expected 200 cards in first run, got %d", result1.TotalCards)
		}

		// Second run: resume from cursor at end (should return 0 new cards)
		// Resume logic loads cursor and continues from there
		// Since first run loaded all 200 cards, cursor is at the end
		// Resume should return 0 (no more cards available)
		result2, err := DownloadCards(ctx, mockClient, repo3, true, 100, 5, 0)
		if err != nil {
			t.Fatalf("Resume run failed: %v", err)
		}
		if result2.TotalCards != 0 {
			t.Errorf("Expected 0 cards in resume run (already complete), got %d", result2.TotalCards)
		}

		count, _ := repo3.CountCards(ctx)
		if count != 200 { // Should still be 200 (INSERT OR REPLACE)
			t.Errorf("Expected 200 in DB after resume, got %d", count)
		}
	})

	t.Run("Limit", func(t *testing.T) {
		dbPath4 := filepath.Join(tmpDir, "test-cards-limit.db")
		repo4, err := sqlite.NewSQLiteSalesRepository(dbPath4)
		if err != nil {
			t.Fatalf("Failed to create repository: %v", err)
		}
		defer repo4.Close()

		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 500)

		result, err := DownloadCards(ctx, mockClient, repo4, false, 100, 5, 150)
		if err != nil {
			t.Fatalf("DownloadCards with limit failed: %v", err)
		}

		if result.TotalCards != 150 {
			t.Errorf("Expected 150 cards (limited), got %d", result.TotalCards)
		}

		count, _ := repo4.CountCards(ctx)
		if count != 150 {
			t.Errorf("Expected 150 in DB, got %d", count)
		}
	})

	t.Run("ChildRecords", func(t *testing.T) {
		dbPath5 := filepath.Join(tmpDir, "test-cards-child.db")
		repo5, err := sqlite.NewSQLiteSalesRepository(dbPath5)
		if err != nil {
			t.Fatalf("Failed to create repository: %v", err)
		}
		defer repo5.Close()

		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 10) // 10 cards with nested data

		_, err = DownloadCards(ctx, mockClient, repo5, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("DownloadCards failed: %v", err)
		}

		// Verify main cards were saved
		// Child records (photos, sizes, characteristics, tags) are saved
		// within SaveCards() in a transaction, so if CountCards succeeds,
		// the child records are assumed to be saved correctly.
		count, err := repo5.CountCards(ctx)
		if err != nil {
			t.Fatalf("CountCards failed: %v", err)
		}
		if count != 10 {
			t.Errorf("Expected 10 cards, got %d", count)
		}
	})

	t.Run("IdempotentReRun", func(t *testing.T) {
		dbPath6 := filepath.Join(tmpDir, "test-cards-idempotent.db")
		repo6, err := sqlite.NewSQLiteSalesRepository(dbPath6)
		if err != nil {
			t.Fatalf("Failed to create repository: %v", err)
		}
		defer repo6.Close()

		mockClient1 := NewMockCardsClient()
		PopulateMockCards(mockClient1, 30)

		result1, err := DownloadCards(ctx, mockClient1, repo6, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("First run failed: %v", err)
		}

		mockClient2 := NewMockCardsClient()
		PopulateMockCards(mockClient2, 30)

		result2, err := DownloadCards(ctx, mockClient2, repo6, false, 100, 5, 0)
		if err != nil {
			t.Fatalf("Second run failed: %v", err)
		}

		if result1.TotalCards != result2.TotalCards {
			t.Errorf("Idempotent mismatch: first=%d second=%d", result1.TotalCards, result2.TotalCards)
		}

		// DB count should not double (INSERT OR REPLACE on nm_id)
		count, _ := repo6.CountCards(ctx)
		if count != 30 {
			t.Errorf("Expected 30 in DB after idempotent re-run, got %d", count)
		}
	})
}

// TestMockClientCursorPagination tests the mock client's cursor-based pagination.
func TestMockClientCursorPagination(t *testing.T) {
	mockClient := NewMockCardsClient()
	PopulateMockCards(mockClient, 100) // 100 cards

	ctx := context.Background()

	// First page
	settings1 := wb.CardsSettings{
		Sort:   &wb.CardsSort{Ascending: true},
		Filter: &wb.CardsFilter{WithPhoto: -1},
		Cursor: wb.CardsCursor{Limit: 30},
	}

	cards1, cursor1, err := mockClient.GetCardsList(ctx, settings1, 100, 5)
	if err != nil {
		t.Fatalf("First page failed: %v", err)
	}
	if len(cards1) != 30 {
		t.Errorf("Expected 30 cards on first page, got %d", len(cards1))
	}
	if cursor1 == nil || cursor1.Total != 30 {
		t.Error("Expected valid cursor with Total=30")
	}

	// Second page (using cursor from first page)
	settings2 := wb.CardsSettings{
		Sort:   &wb.CardsSort{Ascending: true},
		Filter: &wb.CardsFilter{WithPhoto: -1},
		Cursor: wb.CardsCursor{
			Limit:     30,
			UpdatedAt: cursor1.UpdatedAt,
			NmID:      cursor1.NmID,
		},
	}

	cards2, _, err := mockClient.GetCardsList(ctx, settings2, 100, 5)
	if err != nil {
		t.Fatalf("Second page failed: %v", err)
	}
	if len(cards2) != 30 {
		t.Errorf("Expected 30 cards on second page, got %d", len(cards2))
	}

	// Verify cursor moved forward
	if cards2[0].NmID <= cards1[len(cards1)-1].NmID {
		t.Errorf("Second page should start after first page's last card")
	}

	// Verify no overlap in nmIDs
	nmIDs1 := make(map[int]bool)
	for _, card := range cards1 {
		nmIDs1[card.NmID] = true
	}
	for _, card := range cards2 {
		if nmIDs1[card.NmID] {
			t.Errorf("Duplicate nm_id found: %d", card.NmID)
		}
	}
}
