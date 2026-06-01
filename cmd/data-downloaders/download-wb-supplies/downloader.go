package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	suppliesPageSize = 1000
	goodsPageSize    = 1000
)

// DownloadResult holds statistics from the download process.
type DownloadResult struct {
	Warehouses  int
	Tariffs     int
	Supplies    int
	Goods       int
	Packages    int
	Requests    int
	Duration    time.Duration
}

// DownloadReference downloads reference data (warehouses, transit tariffs).
func DownloadReference(
	ctx context.Context,
	client SuppliesClient,
	repo *sqlite.SQLiteSalesRepository,
	rl config.SupplyRateLimits,
) (int, int, error) {
	// 1. Warehouses
	dllog.Log("downloading warehouses reference...")
	warehouses, err := client.GetWarehouses(ctx, rl.Ref, rl.RefBurst)
	if err != nil {
		return 0, 0, fmt.Errorf("get warehouses: %w", err)
	}
	whSaved, err := repo.SaveWarehouses(ctx, warehouses)
	if err != nil {
		return 0, 0, fmt.Errorf("save warehouses: %w", err)
	}
	dllog.Log("warehouses: %d saved", whSaved)

	// 2. Transit tariffs
	dllog.Log("downloading transit tariffs...")
	tariffs, err := client.GetTransitTariffs(ctx, rl.Ref, rl.RefBurst)
	if err != nil {
		return whSaved, 0, fmt.Errorf("get transit tariffs: %w", err)
	}
	tSaved, err := repo.SaveTransitTariffs(ctx, tariffs)
	if err != nil {
		return whSaved, 0, fmt.Errorf("save tariffs: %w", err)
	}
	dllog.Log("tariffs: %d saved", tSaved)

	return whSaved, tSaved, nil
}

// DownloadSupplies downloads supplies list with pagination.
// Returns supply rows ready for DB storage.
func DownloadSupplies(
	ctx context.Context,
	client SuppliesClient,
	rl config.SupplyRateLimits,
	filter wb.SuppliesFilterRequest,
) ([]wb.Supply, int, error) {
	var allSupplies []wb.Supply
	offset := 0
	requests := 0

	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return allSupplies, requests, ctx.Err()
		default:
		}

		supplies, err := client.GetSupplies(ctx, rl.SupplyOps, rl.SupplyOpsBurst, filter, suppliesPageSize, offset)
		requests++
		if err != nil {
			return allSupplies, requests, fmt.Errorf("get supplies (offset=%d): %w", offset, err)
		}

		if len(supplies) == 0 {
			break
		}

		allSupplies = append(allSupplies, supplies...)
		dllog.Log("page: offset=%d received=%d total=%d", offset, len(supplies), len(allSupplies))

		if len(supplies) < suppliesPageSize {
			break
		}
		offset += suppliesPageSize
	}

	return allSupplies, requests, nil
}

// DownloadSupplyDetails downloads details (warehouse info), goods and packages for each supply.
func DownloadSupplyDetails(
	ctx context.Context,
	client SuppliesClient,
	repo *sqlite.SQLiteSalesRepository,
	rl config.SupplyRateLimits,
	supplyIDs []sqlite.SupplyIDPair,
) (int, int, int, error) {
	totalGoods := 0
	totalPackages := 0
	totalRequests := 0
	now := time.Now().Format("2006-01-02 15:04:05")

	// Collect detail rows for batch save after loop
	var detailRows []sqlite.SupplyRow

	for i, pair := range supplyIDs {
		// Check context cancellation
		select {
		case <-ctx.Done():
			// Save any collected detail rows before exiting
			if len(detailRows) > 0 {
				repo.SaveSupplies(ctx, detailRows)
			}
			return totalGoods, totalPackages, totalRequests, ctx.Err()
		default:
		}

		// Skip unplanned supplies (supply_id=0) — they have no goods/packages
		if pair.SupplyID == 0 {
			continue
		}

		// Download details (warehouse info) for this supply
		details, err := client.GetSupplyDetails(ctx, rl.SupplyOps, rl.SupplyOpsBurst, pair.SupplyID)
		totalRequests++
		if err != nil {
			dllog.Error("details supply_id=%d: %v", pair.SupplyID, err)
		} else if details != nil {
			row := sqlite.SupplyFromAPIDetail(details, now)
			row.SupplyID = pair.SupplyID
			row.PreorderID = pair.PreorderID
			detailRows = append(detailRows, row)
		}

		// Download goods
		var allGoods []wb.GoodInSupply
		goodsOffset := 0
		for {
			goods, err := client.GetSupplyGoods(ctx, rl.SupplyOps, rl.SupplyOpsBurst, pair.SupplyID, goodsPageSize, goodsOffset)
			totalRequests++
			if err != nil {
				dllog.Error("goods supply_id=%d: %v", pair.SupplyID, err)
				break
			}
			allGoods = append(allGoods, goods...)
			if len(goods) < goodsPageSize {
				break
			}
			goodsOffset += goodsPageSize
		}

		if len(allGoods) > 0 {
			saved, err := repo.SaveSupplyGoods(ctx, pair.SupplyID, pair.PreorderID, allGoods)
			if err != nil {
				dllog.Error("save goods supply_id=%d: %v", pair.SupplyID, err)
			} else {
				totalGoods += saved
			}
		}

		// Download packages
		boxes, err := client.GetSupplyPackages(ctx, rl.SupplyOps, rl.SupplyOpsBurst, pair.SupplyID)
		totalRequests++
		if err != nil {
			dllog.Error("packages supply_id=%d: %v", pair.SupplyID, err)
		} else if len(boxes) > 0 {
			saved, err := repo.SaveSupplyPackages(ctx, pair.SupplyID, pair.PreorderID, boxes)
			if err != nil {
				dllog.Error("save packages supply_id=%d: %v", pair.SupplyID, err)
			} else {
				totalPackages += saved
			}
		}

		// Progress every 10 supplies
		if (i+1)%10 == 0 || i+1 == len(supplyIDs) {
			dllog.Progress(i+1, len(supplyIDs), "supplies", fmt.Sprintf("goods: %d, packages: %d", totalGoods, totalPackages), time.Time{})
		}
	}

	// Batch save all detail rows (INSERT OR REPLACE updates warehouse fields)
	if len(detailRows) > 0 {
		saved, err := repo.SaveSupplies(ctx, detailRows)
		if err != nil {
			dllog.Error("save supply details: %v", err)
		} else {
			dllog.Log("supply details updated: %d", saved)
		}
	}

	return totalGoods, totalPackages, totalRequests, nil
}

// SupplyRowFromAPI converts an API Supply to a DB SupplyRow.
func SupplyRowFromAPI(s *wb.Supply, downloadedAt string) sqlite.SupplyRow {
	return sqlite.SupplyFromAPI(s, downloadedAt)
}
