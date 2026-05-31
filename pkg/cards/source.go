package cards

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const ToolID = "get_cards_list"

// WBSource adapts *wb.Client to CardsSource interface.
// Same pattern as funnel.WBSource — isolates rate limiting details from the downloader.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a CardsSource backed by the real WB Content API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// GetCardsPage fetches one page of cards from WB Content API.
func (s *WBSource) GetCardsPage(ctx context.Context, settings wb.CardsSettings) ([]wb.ProductCard, *wb.CardsCursorResponse, error) {
	return s.client.GetCardsList(ctx, settings, s.rateLimit, s.burst)
}
