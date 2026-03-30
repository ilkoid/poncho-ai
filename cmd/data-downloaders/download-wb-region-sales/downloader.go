package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	// maxRegionSaleDataAvailability is the WB data horizon:
	// the API only stores data for the last 31 days from today.
	maxRegionSaleDataAvailability = 31
)

// RegionSalesClient is the interface for Region Sale API operations.
// Defined in cmd/ per Rule 6 (consumer's interface).
// *wb.Client satisfies RegionSalesClient directly (no adapter needed).
type RegionSalesClient interface {
	GetRegionSales(ctx context.Context, dateFrom, dateTo string, rateLimit, burst int) ([]wb.RegionSaleItem, error)
}

// RegionSalesResult holds download results.
type RegionSalesResult struct {
	TotalRows int
	Requests  int
	Duration  time.Duration
}

// DownloadRegionSales downloads region sales data for the given date range.
// Makes a single API request (no pagination, no chunking).
// Uses INSERT OR REPLACE for idempotency (No Resume strategy).
func DownloadRegionSales(
	ctx context.Context,
	client RegionSalesClient,
	repo *sqlite.SQLiteSalesRepository,
	begin, end string,
	rateLimit, burst int,
) (*RegionSalesResult, error) {
	start := time.Now()
	result := &RegionSalesResult{}

	// Warn if begin date exceeds data availability horizon
	from, _ := time.Parse("2006-01-02", begin)
	earliestAvailable := time.Now().AddDate(0, 0, -maxRegionSaleDataAvailability)
	if from.Before(earliestAvailable) {
		fmt.Printf("⚠️  Warning: begin date %s is older than %d days — WB API may return no data for this period\n",
			begin, maxRegionSaleDataAvailability)
	}

	// Check cancellation
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
	default:
	}

	fmt.Printf("Requesting %s to %s\n", begin, end)

	items, err := client.GetRegionSales(ctx, begin, end, rateLimit, burst)
	if err != nil {
		return nil, fmt.Errorf("region sale %s to %s: %w", begin, end, err)
	}

	if len(items) == 0 {
		fmt.Println("No data")
		result.Requests = 1
		result.Duration = time.Since(start)
		return result, nil
	}

	n, err := repo.SaveRegionSales(ctx, begin, end, items)
	if err != nil {
		return nil, fmt.Errorf("save region sales: %w", err)
	}

	result.TotalRows = n
	result.Requests = 1
	result.Duration = time.Since(start)
	fmt.Printf("✅ Saved %d rows\n", n)
	return result, nil
}
