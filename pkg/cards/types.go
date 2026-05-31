// Package cards provides a reusable card downloader for WB Content API.
//
// Architecture follows the v2 downloader pattern (dev_utils.md):
//   - CardsSource — API abstraction (*wb.Client via WBSource adapter)
//   - CardsWriter — persistence abstraction (SQLite, PostgreSQL adapters)
//   - Downloader — business logic depends only on interfaces
package cards

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CardsSource is the data source interface for card downloads.
// Implemented by WBSource (real API) and MockCardsSource (--mock).
//
// Single method: cursor-based pagination mirrors the WB Content API pattern
// where each page returns cards + cursor for the next page.
type CardsSource interface {
	// GetCardsPage fetches one page of cards from WB Content API.
	// Returns (cards, cursor, error). Cursor is nil on last page.
	GetCardsPage(ctx context.Context, settings wb.CardsSettings) ([]wb.ProductCard, *wb.CardsCursorResponse, error)
}

// CardsWriter is the persistence interface for card data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
//
// ISP: 4 methods — exactly what the Downloader needs.
// No query/analytics methods; those belong in a separate CardsReader interface.
type CardsWriter interface {
	// SaveCards saves a batch of cards with all nested data (photos, sizes, chars, tags).
	// Uses upsert semantics (INSERT OR REPLACE / ON CONFLICT UPDATE).
	// Returns count of saved cards.
	SaveCards(ctx context.Context, cards []wb.ProductCard) (int, error)

	// GetCardsLastCursor retrieves the last saved cursor for resume.
	// Returns ("", 0, nil) if no cursor saved (first run).
	GetCardsLastCursor(ctx context.Context) (updatedAt string, nmID int, err error)

	// SaveCardsCursor persists the cursor position for resume.
	SaveCardsCursor(ctx context.Context, updatedAt string, nmID int) error

	// CountCards returns total card count (for progress reporting).
	CountCards(ctx context.Context) (int, error)
}

// DownloadOptions configures the cards download behavior.
type DownloadOptions struct {
	// PageSize is cards per page (default: 100, API max).
	PageSize int

	// Limit is the max total cards to download (0 = unlimited).
	Limit int

	// Resume from last saved cursor.
	Resume bool

	// DryRun skips all DB writes (SaveCards, SaveCardsCursor).
	DryRun bool

	// OnProgress callback for status messages (nil = silent, ideal for Tools).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a cards download run.
type DownloadResult struct {
	TotalCards int
	Pages      int
	Requests   int
	Duration   time.Duration
}
