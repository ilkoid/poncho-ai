// Package main provides download logic for WB Stock History CSV data.
package main

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DownloadResult represents the result of a download operation.
type DownloadResult struct {
	ReportID  string
	Status    string
	RowsCount int
	Duration  time.Duration
}

// DownloadStockHistory orchestrates the stock history download process.
func DownloadStockHistory(
	ctx context.Context,
	wbClient *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	cfg config.StockHistoryConfig,
	beginDate, endDate string,
) (*DownloadResult, error) {

	start := time.Now()

	// TODO: Implement full flow
	// 1. Check resume mode
	// 2. Create report request
	// 3. Wait for report (poll status)
	// 4. Download ZIP
	// 5. Parse CSV
	// 6. Save to database

	return &DownloadResult{
		ReportID:  "mock-id",
		Status:   "SUCCESS",
		Duration: time.Since(start),
	}, nil
}
