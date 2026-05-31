package cards

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const defaultPageSize = 100 // WB Content API max per page

// Downloader is a reusable cards downloader.
// Depends on CardsSource (WB API) and CardsWriter (persistence) — both are interfaces.
//
// Usage:
//
//	dl := cards.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source CardsSource
	writer CardsWriter
	opts   DownloadOptions
}

// NewDownloader creates a cards downloader from a CardsSource and CardsWriter.
func NewDownloader(source CardsSource, writer CardsWriter, opts DownloadOptions) *Downloader {
	if opts.PageSize <= 0 {
		opts.PageSize = defaultPageSize
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run downloads product cards using cursor-based pagination.
//
// Pagination loop:
//  1. If opts.Resume: load saved cursor from writer.GetCardsLastCursor()
//  2. Start with PageSize, ascending sort, withPhoto: -1 (all cards)
//  3. Loop: source.GetCardsPage() → trim by Limit → writer.SaveCards() → writer.SaveCardsCursor()
//  4. Break when len(cards)==0 || cursor==nil || cursor.Total < pageSize
//
// Continue-on-error: individual page failures are logged but don't abort.
// Context cancellation is checked before each page request.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Load cursor for resume mode
	settings := wb.CardsSettings{
		Sort:   &wb.CardsSort{Ascending: true},
		Filter: &wb.CardsFilter{WithPhoto: -1},
		Cursor: wb.CardsCursor{Limit: d.opts.PageSize},
	}

	if d.opts.Resume {
		updatedAt, nmID, err := d.writer.GetCardsLastCursor(ctx)
		if err != nil {
			return result, fmt.Errorf("get last cursor: %w", err)
		}
		if updatedAt != "" || nmID != 0 {
			settings.Cursor.UpdatedAt = updatedAt
			settings.Cursor.NmID = nmID
			d.progress("📍 Resuming from cursor: updatedAt=%s, nmID=%d", updatedAt, nmID)
		} else {
			d.progress("📍 No saved cursor found, starting from beginning")
		}
	}

	var totalCards int

	for {
		// Check cancellation
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		result.Requests++
		pageNum := result.Requests

		d.progress("  [Page %d] Fetching cards...", pageNum)
		tStart := time.Now()

		// Fetch page from API
		cards, cursor, err := d.source.GetCardsPage(ctx, settings)
		if err != nil {
			d.progress("  ❌ Page %d error: %v", pageNum, err)
			if len(cards) == 0 {
				result.Duration = time.Since(start)
				return result, fmt.Errorf("failed to fetch page %d: %w", pageNum, err)
			}
			d.progress("  ⚠️  Continuing with partial data...")
		}

		elapsed := time.Since(tStart).Round(time.Millisecond)

		if len(cards) == 0 {
			d.progress("  ✅ No more cards (%s)", elapsed)
			break
		}

		// Apply user limit: trim page if needed
		cardsToSave := cards
		if d.opts.Limit > 0 && totalCards+len(cards) > d.opts.Limit {
			trim := d.opts.Limit - totalCards
			cardsToSave = cards[:trim]
			d.progress("  ✂️  Trimming page to %d cards (limit=%d)", trim, d.opts.Limit)
		}

		// Save to database (unless dry-run)
		if !d.opts.DryRun {
			n, err := d.writer.SaveCards(ctx, cardsToSave)
			if err != nil {
				result.Duration = time.Since(start)
				return result, fmt.Errorf("save cards page %d: %w", pageNum, err)
			}
			totalCards += n
			d.progress("  ✅ %d cards saved (%s)", n, elapsed)
		} else {
			totalCards += len(cardsToSave)
			d.progress("  🏜️  [DRY-RUN] %d cards skipped (%s)", len(cardsToSave), elapsed)
		}

		// Save cursor for resume
		if !d.opts.DryRun {
			lastCard := cardsToSave[len(cardsToSave)-1]
			if err := d.writer.SaveCardsCursor(ctx, lastCard.UpdatedAt, lastCard.NmID); err != nil {
				d.progress("  ⚠️  Failed to save cursor: %v", err)
			}
		}

		// Check for user limit
		if d.opts.Limit > 0 && totalCards >= d.opts.Limit {
			d.progress("  🎯 Reached user limit of %d cards", d.opts.Limit)
			break
		}

		// Check for pagination end
		if cursor == nil {
			d.progress("  ✅ End of pagination (nil cursor)")
			break
		}
		if cursor.Total < d.opts.PageSize {
			d.progress("  ✅ End of pagination (total=%d < limit=%d)", cursor.Total, d.opts.PageSize)
			break
		}

		// Update cursor for next page
		lastCard := cardsToSave[len(cardsToSave)-1]
		settings.Cursor = wb.CardsCursor{
			Limit:     d.opts.PageSize,
			UpdatedAt: lastCard.UpdatedAt,
			NmID:      lastCard.NmID,
		}
	}

	result.TotalCards = totalCards
	result.Pages = result.Requests
	result.Duration = time.Since(start)

	return result, nil
}

// progress calls the OnProgress callback if set.
// Does nothing when opts.OnProgress is nil (silent mode for Tools).
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
