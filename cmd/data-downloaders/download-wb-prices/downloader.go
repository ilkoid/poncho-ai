package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DownloadResult holds the outcome of a price download session.
type DownloadResult struct {
	TotalProducts int
	Pages         int
	Requests      int
	Duration      time.Duration
}

// PricesClient is the interface for fetching product prices (real or mock).
type PricesClient interface {
	GetPrices(ctx context.Context, limit, offset, rateLimit, burst int) ([]wb.ProductPrice, int, error)
}

// DownloadPrices fetches all product prices with offset-based pagination.
// snapshotDate is set by the caller (today's date in YYYY-MM-DD format).
// Each page fetches up to `limit` products (max 1000).
func DownloadPrices(
	ctx context.Context,
	client PricesClient,
	saveFn func(ctx context.Context, prices []wb.ProductPrice, snapshotDate string) (int, error),
	snapshotDate string,
	rl, burst, limit int,
) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}
	offset := 0

	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		prices, count, err := client.GetPrices(ctx, limit, offset, rl, burst)
		if err != nil {
			return result, fmt.Errorf("page %d (offset %d): %w", result.Pages+1, offset, err)
		}

		result.Requests++
		result.Pages++

		if len(prices) == 0 {
			break
		}

		saved, err := saveFn(ctx, prices, snapshotDate)
		if err != nil {
			return result, fmt.Errorf("save page %d: %w", result.Pages, err)
		}

		result.TotalProducts += count
		fmt.Printf("  Page %d: %d products (saved %d, total %d)\n",
			result.Pages, count, saved, result.TotalProducts)

		offset += count

		// If we got fewer items than requested, we've reached the end
		if count < limit {
			break
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
