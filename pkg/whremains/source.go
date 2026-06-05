package whremains

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource adapts *wb.Client to the WhRemainsSource interface.
// Stores rate limit params for each of the 3 async endpoints separately,
// since each has different swagger-documented limits.
type WBSource struct {
	client *wb.Client

	createRL, createBurst       int
	statusRL, statusBurst       int
	downloadRL, downloadBurst   int
}

// NewWBSource creates a WhRemainsSource backed by the real WB API.
func NewWBSource(
	client *wb.Client,
	createRL, createBurst int,
	statusRL, statusBurst int,
	downloadRL, downloadBurst int,
) *WBSource {
	return &WBSource{
		client:        client,
		createRL:      createRL,
		createBurst:   createBurst,
		statusRL:      statusRL,
		statusBurst:   statusBurst,
		downloadRL:    downloadRL,
		downloadBurst: downloadBurst,
	}
}

// CreateReport initiates warehouse remains report generation.
// Delegates to wb.Client.CreateWarehouseRemainsReport with grouping params.
func (s *WBSource) CreateReport(ctx context.Context, params WHRemainsParams) (string, error) {
	return s.client.CreateWarehouseRemainsReport(ctx, params.GroupByNm, params.GroupBySize, s.createRL, s.createBurst)
}

// PollStatus checks current task status.
// Delegates to wb.Client.GetWarehouseRemainsStatus with status-specific rate limits.
func (s *WBSource) PollStatus(ctx context.Context, taskID string) (string, error) {
	return s.client.GetWarehouseRemainsStatus(ctx, taskID, s.statusRL, s.statusBurst)
}

// DownloadReport downloads completed report data.
// Delegates to wb.Client.DownloadWarehouseRemains with download-specific rate limits.
func (s *WBSource) DownloadReport(ctx context.Context, taskID string) ([]wb.WarehouseRemainsItem, error) {
	return s.client.DownloadWarehouseRemains(ctx, taskID, s.downloadRL, s.downloadBurst)
}
