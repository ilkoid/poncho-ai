package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CardsClient is the interface for Content API cards operations.
// Defined in cmd/ per Rule 6 (consumer's interface).
// *wb.Client satisfies CardsClient directly (no adapter needed).
type CardsClient interface {
	GetCardsList(ctx context.Context, settings wb.CardsSettings, rateLimit, burst int) ([]wb.ProductCard, *wb.CardsCursorResponse, error)
}

// CardsDownloadResult holds download results.
type CardsDownloadResult struct {
	TotalCards int
	Pages      int
	Requests   int
	Duration   time.Duration
}

// DownloadCards downloads all product cards using cursor-based pagination.
//
// Cursor loop:
// 1. If --resume: load saved cursor from cards_download_meta
// 2. Start with cursor.Limit = limit (default 100), ascending sort, withPhoto: -1
// 3. Loop: call GetCardsList → SaveCards → SaveCardsCursor
// 4. Break when cursor.Total < limit or cursor == nil or len(cards) == 0
// 5. Continue-on-error for individual pages (log + continue, don't abort)
//
// Returns summary with total cards, pages, requests, and duration.
func DownloadCards(
	ctx context.Context,
	client CardsClient,
	repo *sqlite.SQLiteSalesRepository,
	resume bool,
	rateLimit, burst int,
	limit int,
) (*CardsDownloadResult, error) {
	start := time.Now()
	result := &CardsDownloadResult{}

	// Load cursor for resume mode
	var updatedAt string
	var nmID int
	if resume {
		var err error
		updatedAt, nmID, err = repo.GetCardsLastCursor(ctx)
		if err != nil {
			return nil, fmt.Errorf("get last cursor: %w", err)
		}
		if updatedAt != "" || nmID != 0 {
			fmt.Printf("📍 Resuming from cursor: updatedAt=%s, nmID=%d\n", updatedAt, nmID)
		} else {
			fmt.Println("📍 No saved cursor found, starting from beginning")
		}
	}

	// Initial cursor settings
	settings := wb.CardsSettings{
		Sort:   &wb.CardsSort{Ascending: true},  // Ascending for full download
		Filter: &wb.CardsFilter{WithPhoto: -1},  // -1 = all cards (with and without photos)
		Cursor: wb.CardsCursor{
			Limit: 100, // API max per page
		},
	}

	// Apply user limit (if specified)
	if limit > 0 && limit < 100 {
		settings.Cursor.Limit = limit
	}

	// Apply resume cursor
	if updatedAt != "" || nmID != 0 {
		settings.Cursor.UpdatedAt = updatedAt
		settings.Cursor.NmID = nmID
	}

	// Progress tracking
	var pageCount int
	var totalCards int

	for {
		// Check cancellation
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		pageCount++
		result.Requests++

		fmt.Printf("  [Page %d] Fetching cards...", pageCount)
		tStart := time.Now()

		// Fetch page from API
		cards, cursor, err := client.GetCardsList(ctx, settings, rateLimit, burst)
		if err != nil {
			fmt.Printf(" ❌ Error: %v\n", err)
			// Continue-on-error: log and continue to next page
			// In production, you might want to break or retry
			if len(cards) == 0 {
				// No cards to save, can't continue
				return result, fmt.Errorf("failed to fetch page %d: %w", pageCount, err)
			}
			fmt.Println("  ⚠️  Continuing with partial data...")
		}

		elapsed := time.Since(tStart).Round(time.Millisecond)
		if len(cards) == 0 {
			fmt.Printf(" ✅ No more cards (%s)\n", elapsed)
			break
		}

		// Apply user limit: trim page if needed
		cardsToSave := cards
		if limit > 0 && totalCards+len(cards) > limit {
			trim := limit - totalCards
			cardsToSave = cards[:trim]
			fmt.Printf("  ✂️  Trimming page to %d cards (limit=%d)\n", trim, limit)
		}

		// Save to database
		n, err := repo.SaveCards(ctx, cardsToSave)
		if err != nil {
			return result, fmt.Errorf("save cards page %d: %w", pageCount, err)
		}

		totalCards += n
		fmt.Printf(" ✅ %d cards (%s)\n", n, elapsed)

		// Save cursor for resume
		lastCard := cardsToSave[len(cardsToSave)-1]
		if err := repo.SaveCardsCursor(ctx, lastCard.UpdatedAt, lastCard.NmID); err != nil {
			fmt.Printf("  ⚠️  Failed to save cursor: %v\n", err)
		}

		// Check for user limit
		if limit > 0 && totalCards >= limit {
			fmt.Printf("  🎯 Reached user limit of %d cards\n", limit)
			break
		}

		// Check for pagination end
		if cursor == nil {
			fmt.Println("  ✅ End of pagination (nil cursor)")
			break
		}

		if cursor.Total < settings.Cursor.Limit {
			fmt.Printf("  ✅ End of pagination (total=%d < limit=%d)\n", cursor.Total, settings.Cursor.Limit)
			break
		}

		// Update cursor for next page
		settings.Cursor = wb.CardsCursor{
			Limit:    settings.Cursor.Limit,
			UpdatedAt: lastCard.UpdatedAt,
			NmID:     lastCard.NmID,
		}
	}

	result.TotalCards = totalCards
	result.Pages = pageCount
	result.Duration = time.Since(start)

	return result, nil
}
