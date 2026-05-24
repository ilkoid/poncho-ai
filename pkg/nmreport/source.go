package nmreport

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the NmReportSource interface.
type WBSource struct {
	client    *wb.Client
	rateLimit int
	burst     int
}

// NewWBSource creates an NmReportSource backed by the real WB API.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

func (s *WBSource) CreateReport(ctx context.Context, req wb.NmReportFunnelRequest) (string, error) {
	return s.client.CreateNmFunnelReport(ctx, req, s.rateLimit, s.burst)
}

func (s *WBSource) PollStatus(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	return s.client.GetNmFunnelReportStatus(ctx, downloadID, s.rateLimit, s.burst)
}

func (s *WBSource) DownloadFile(ctx context.Context, downloadID string) ([]byte, error) {
	return s.client.DownloadNmFunnelReport(ctx, downloadID, s.rateLimit, s.burst)
}
