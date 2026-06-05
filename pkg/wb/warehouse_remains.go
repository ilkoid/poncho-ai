// Package wb provides Warehouse Remains async report API methods.
//
// Three-step async flow (all GET):
//  1. Create report:  GET /api/v1/warehouse_remains
//  2. Poll status:    GET /api/v1/warehouse_remains/tasks/{id}/status
//  3. Download data:  GET /api/v1/warehouse_remains/tasks/{id}/download
//
// Base URL: https://seller-analytics-api.wildberries.ru
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	whRemainsBaseURL      = "https://seller-analytics-api.wildberries.ru"
	whRemainsCreatePath   = "/api/v1/warehouse_remains"
	whRemainsStatusPath   = "/api/v1/warehouse_remains/tasks/%s/status"
	whRemainsDownloadPath = "/api/v1/warehouse_remains/tasks/%s/download"
)

// CreateWarehouseRemainsReport initiates a warehouse remains report generation.
// GET /api/v1/warehouse_remains?groupByNm=true&groupBySize=true
//
// Rate limit: 1/min, burst 5 (swagger).
// Returns task ID for subsequent polling.
func (c *Client) CreateWarehouseRemainsReport(ctx context.Context, groupByNm, groupBySize bool, rateLimit, burst int) (string, error) {
	params := url.Values{}
	if groupByNm {
		params.Set("groupByNm", "true")
	}
	if groupBySize {
		params.Set("groupBySize", "true")
	}

	var resp WarehouseRemainsCreateResponse
	if err := c.Get(ctx, "wh_remains_create", whRemainsBaseURL, rateLimit, burst, whRemainsCreatePath, params, &resp); err != nil {
		return "", fmt.Errorf("create warehouse remains report: %w", err)
	}

	return resp.Data.TaskID, nil
}

// GetWarehouseRemainsStatus polls the status of a warehouse remains report task.
// GET /api/v1/warehouse_remains/tasks/{id}/status
//
// Rate limit: 1/5sec = 12/min, burst 5 (swagger).
// Statuses: new, processing, done, purged, canceled.
func (c *Client) GetWarehouseRemainsStatus(ctx context.Context, taskID string, rateLimit, burst int) (string, error) {
	path := fmt.Sprintf(whRemainsStatusPath, taskID)

	var resp WarehouseRemainsStatusResponse
	if err := c.Get(ctx, "wh_remains_status", whRemainsBaseURL, rateLimit, burst, path, nil, &resp); err != nil {
		return "", fmt.Errorf("warehouse remains status %s: %w", taskID, err)
	}

	return resp.Data.Status, nil
}

// DownloadWarehouseRemains downloads the generated warehouse remains report data.
// GET /api/v1/warehouse_remains/tasks/{id}/download
//
// Rate limit: 1/min, burst 1 (swagger).
// Returns bare JSON array (no wrapper): [{...}, {...}].
// Uses GetRaw() + manual json.Unmarshal because response is not wrapped in {"data":...}.
func (c *Client) DownloadWarehouseRemains(ctx context.Context, taskID string, rateLimit, burst int) ([]WarehouseRemainsItem, error) {
	path := fmt.Sprintf(whRemainsDownloadPath, taskID)

	raw, err := c.GetRaw(ctx, "wh_remains_download", whRemainsBaseURL, rateLimit, burst, path, nil)
	if err != nil {
		return nil, fmt.Errorf("download warehouse remains %s: %w", taskID, err)
	}

	var items []WarehouseRemainsItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unmarshal warehouse remains: %w", err)
	}

	return items, nil
}
