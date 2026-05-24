package wb

import (
	"context"
	"fmt"

	"net/url"
)

const (
	nmFunnelReportsBaseURL = "https://seller-analytics-api.wildberries.ru"
	nmFunnelCreatePath     = "/api/v2/nm-report/downloads"
	nmFunnelListPath       = "/api/v2/nm-report/downloads"
	nmFunnelDownloadPath   = "/api/v2/nm-report/downloads/file/%s"
)

// CreateNmFunnelReport creates a task to generate funnel CSV report.
// POST /api/v2/nm-report/downloads
//
// Rate limit: 3 req/min, burst 3 (shared with stock-history endpoints).
// Returns the download ID (from req.ID) for polling status.
func (c *Client) CreateNmFunnelReport(ctx context.Context, req NmReportFunnelRequest, rateLimit, burst int) (string, error) {
	var resp StockHistoryReportCreateResponse
	err := c.Post(ctx, "nm_funnel_create", nmFunnelReportsBaseURL, rateLimit, burst, nmFunnelCreatePath, req, &resp)
	if err != nil {
		return "", fmt.Errorf("create nm funnel report: %w", err)
	}
	return req.ID, nil
}

// GetNmFunnelReportStatus fetches status of a specific funnel report by ID.
// GET /api/v2/nm-report/downloads?filter[downloadIds]=id
//
// Rate limit: 3 req/min, burst 3 (shared).
// Reuses StockHistoryReportItem since the response shape is identical.
func (c *Client) GetNmFunnelReportStatus(ctx context.Context, downloadID string, rateLimit, burst int) (*StockHistoryReportItem, error) {
	var resp StockHistoryReportListResponse
	params := url.Values{"filter[downloadIds]": {downloadID}}
	err := c.Get(ctx, "nm_funnel_status", nmFunnelReportsBaseURL, rateLimit, burst, nmFunnelListPath, params, &resp)
	if err != nil {
		return nil, fmt.Errorf("get nm funnel report status: %w", err)
	}

	for _, r := range resp.Data {
		if r.ID == downloadID {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("report %s not found", downloadID)
}

// DownloadNmFunnelReport downloads the generated funnel ZIP archive.
// GET /api/v2/nm-report/downloads/file/{downloadId}
//
// Rate limit: 3 req/min, burst 3 (shared).
// Returns raw ZIP bytes (caller extracts CSV).
func (c *Client) DownloadNmFunnelReport(ctx context.Context, downloadID string, rateLimit, burst int) ([]byte, error) {
	path := fmt.Sprintf(nmFunnelDownloadPath, downloadID)
	return c.GetRaw(ctx, "nm_funnel_download", nmFunnelReportsBaseURL, rateLimit, burst, path, nil)
}
