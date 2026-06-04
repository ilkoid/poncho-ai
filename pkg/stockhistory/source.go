package stockhistory

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the StockHistorySource interface.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates a StockHistorySource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

func (s *WBSource) CreateReport(ctx context.Context, req wb.StockHistoryReportRequest) (string, error) {
	return s.client.CreateStockHistoryReport(ctx, req, s.rateLimit, s.burst)
}

func (s *WBSource) PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	return s.client.GetStockHistoryReportStatus(ctx, downloadID, s.rateLimit, s.burst)
}

func (s *WBSource) DownloadFile(ctx context.Context, downloadID string) ([]byte, error) {
	return s.client.DownloadStockHistoryReport(ctx, downloadID, s.rateLimit, s.burst)
}
