// Package main provides download logic for WB Stocks Warehouse downloader.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	// maxStocksPageSize is max items per page from the stocks warehouse API.
	maxStocksPageSize = 250000
)

// StocksClient is the interface for Stocks Warehouse API operations.
// Defined in cmd/ per Rule 6 (consumer's interface).
// *wb.Client satisfies StocksClient directly (no adapter needed).
type StocksClient interface {
	GetStockWarehouses(ctx context.Context, limit, offset, rateLimit, burst int) ([]wb.StockWarehouseItem, error)
}

// StockDownloadResult holds counts of rows saved.
type StockDownloadResult struct {
	TotalRows int
	Pages     int
}

// DownloadStockSnapshot downloads all pages from the stocks warehouse API
// and saves to the database. Uses offset-based pagination with 250K rows per page.
func DownloadStockSnapshot(
	ctx context.Context,
	client StocksClient,
	repo *sqlite.SQLiteSalesRepository,
	snapshotDate string,
	rateLimit, burst int,
) (*StockDownloadResult, error) {
	result := &StockDownloadResult{}
	total := 0
	offset := 0

	for {
		// Check cancellation
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		items, err := client.GetStockWarehouses(ctx, maxStocksPageSize, offset, rateLimit, burst)
		if err != nil {
			return result, fmt.Errorf("download page offset=%d: %w", offset, err)
		}

		if len(items) == 0 {
			break
		}

		n, err := repo.SaveStocks(ctx, snapshotDate, items)
		if err != nil {
			return result, fmt.Errorf("save stocks offset=%d: %w", offset, err)
		}
		total += n
		result.Pages++

		fmt.Printf("    offset=%d: %d items (saved %d)\n", offset, len(items), n)

		// Less than full page → last page
		if len(items) < maxStocksPageSize {
			break
		}
		offset += maxStocksPageSize
	}

	result.TotalRows = total
	return result, nil
}

// DetectGaps checks for missing snapshot dates between firstDate and yesterday.
// Returns list of missing dates. Purely informational — does NOT backfill.
func DetectGaps(ctx context.Context, repo *sqlite.SQLiteSalesRepository, firstDate string) ([]string, error) {
	if firstDate == "" {
		return nil, nil
	}

	existingDates, err := repo.GetDistinctSnapshotDates(ctx)
	if err != nil {
		return nil, fmt.Errorf("get snapshot dates: %w", err)
	}

	if len(existingDates) == 0 {
		return nil, nil
	}

	// Build set of existing dates
	existSet := make(map[string]struct{}, len(existingDates))
	for _, d := range existingDates {
		existSet[d] = struct{}{}
	}

	// Generate expected dates from firstDate to yesterday
	start, err := time.Parse("2006-01-02", firstDate)
	if err != nil {
		return nil, fmt.Errorf("parse first_date %q: %w", firstDate, err)
	}
	yesterday := time.Now().AddDate(0, 0, -1)

	var gaps []string
	for d := start; !d.After(yesterday); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		if _, ok := existSet[ds]; !ok {
			gaps = append(gaps, ds)
		}
	}

	return gaps, nil
}

// PrintGapReport prints a formatted report of detected gaps.
func PrintGapReport(gaps []string) {
	if len(gaps) == 0 {
		fmt.Println("  No gaps detected")
		return
	}

	fmt.Printf("  WARNING: %d missing snapshot dates detected:\n", len(gaps))

	// Show first 10 and last 5 if too many
	switch {
	case len(gaps) <= 15:
		fmt.Printf("    %s\n", strings.Join(gaps, ", "))
	default:
		fmt.Printf("    %s\n", strings.Join(gaps[:10], ", "))
		fmt.Printf("    ... (%d more) ...\n", len(gaps)-15)
		fmt.Printf("    %s\n", strings.Join(gaps[len(gaps)-5:], ", "))
	}
}
