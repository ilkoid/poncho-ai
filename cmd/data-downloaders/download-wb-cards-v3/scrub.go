package main

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/cards"
	"github.com/ilkoid/poncho-ai/pkg/scrub"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: scrubCardsWriter satisfies cards.CardsWriter.
// This keeps scrubbing isolated inside the v3 utility — pkg/cards is not modified.
var _ cards.CardsWriter = (*scrubCardsWriter)(nil)

// scrubCardsWriter decorates a cards.CardsWriter: it rewrites sensitive
// substrings (per the scrub.Replacer) in card fields BEFORE delegating the save
// to the underlying writer. It is the v3 load-time scrubbing hook.
//
// Why a decorator (and not an edit to pkg/cards): keeping pkg/cards and
// download-wb-cards-v2 frozen. The Downloader accepts any cards.CardsWriter, so
// wrapping is transparent to it. Note scrubbing happens in SaveCards, so it does
// NOT run in --dry-run (the Downloader skips saves); dry-run prints only counts,
// so this is not observable.
type scrubCardsWriter struct {
	inner cards.CardsWriter
	r     *scrub.Replacer
}

// newScrubCardsWriter wraps inner with substring scrubbing driven by r.
func newScrubCardsWriter(inner cards.CardsWriter, r *scrub.Replacer) *scrubCardsWriter {
	return &scrubCardsWriter{inner: inner, r: r}
}

// SaveCards scrubs card fields in place, then delegates to the underlying writer.
func (w *scrubCardsWriter) SaveCards(ctx context.Context, cs []wb.ProductCard) (int, error) {
	w.r.ApplySlice(cs)
	return w.inner.SaveCards(ctx, cs)
}

// CountCards delegates unchanged — scrubbing does not affect counts.
func (w *scrubCardsWriter) CountCards(ctx context.Context) (int, error) {
	return w.inner.CountCards(ctx)
}
