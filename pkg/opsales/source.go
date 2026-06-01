package opsales

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID identifies this downloader in the WB client rate limiter registry.
const ToolID = "wb_opsales"

// WBSource adapts *wb.Client to OpsalesSource interface.
// Same pattern as orders.WBSource — isolates rate limiting details from the downloader.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates an OpsalesSource backed by the real WB Statistics API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// SalesIterator iterates over all operational sales pages from WB Statistics API.
func (s *WBSource) SalesIterator(
	ctx context.Context,
	baseURL string,
	rateLimit, burst int,
	dateFrom string,
	callback func([]wb.SalesItem) error,
) (int, error) {
	return s.client.SalesIterator(ctx, baseURL, rateLimit, burst, dateFrom, callback)
}
