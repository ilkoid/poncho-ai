package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
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
	fmt.Println("  Скачивание справочника складов...")
	warehouses, err := client.GetWarehouses(ctx, rl.Ref, rl.RefBurst)
	if err != nil {
		return 0, 0, fmt.Errorf("get warehouses: %w", err)
	}
	whSaved, err := repo.SaveWarehouses(ctx, warehouses)
	if err != nil {
		return 0, 0, fmt.Errorf("save warehouses: %w", err)
	}
	fmt.Printf("  ✅ Складов: %d\n", whSaved)

	// 2. Transit tariffs
	fmt.Println("  Скачивание транзитных тарифов...")
	tariffs, err := client.GetTransitTariffs(ctx, rl.Ref, rl.RefBurst)
	if err != nil {
		return whSaved, 0, fmt.Errorf("get transit tariffs: %w", err)
	}
	tSaved, err := repo.SaveTransitTariffs(ctx, tariffs)
	if err != nil {
		return whSaved, 0, fmt.Errorf("save tariffs: %w", err)
	}
	fmt.Printf("  ✅ Тарифов: %d\n", tSaved)

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

		supplies, err := client.GetSupplies(ctx, rl.List, rl.ListBurst, filter, suppliesPageSize, offset)
		requests++
		if err != nil {
			return allSupplies, requests, fmt.Errorf("get supplies (offset=%d): %w", offset, err)
		}

		if len(supplies) == 0 {
			break
		}

		allSupplies = append(allSupplies, supplies...)
		fmt.Printf("  Страница: offset=%d, получено=%d, всего=%d\n", offset, len(supplies), len(allSupplies))

		if len(supplies) < suppliesPageSize {
			break
		}
		offset += suppliesPageSize
	}

	return allSupplies, requests, nil
}

// DownloadSupplyDetails downloads goods and packages for each supply.
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

	for i, pair := range supplyIDs {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return totalGoods, totalPackages, totalRequests, ctx.Err()
		default:
		}

		// Skip unplanned supplies (supply_id=0) — they have no goods/packages
		if pair.SupplyID == 0 {
			continue
		}

		// Download goods
		var allGoods []wb.GoodInSupply
		goodsOffset := 0
		for {
			goods, err := client.GetSupplyGoods(ctx, rl.Goods, rl.GoodsBurst, pair.SupplyID, goodsPageSize, goodsOffset)
			totalRequests++
			if err != nil {
				fmt.Printf("  ❌ Ошибка товары supply_id=%d: %v\n", pair.SupplyID, err)
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
				fmt.Printf("  ❌ Ошибка сохранения товаров supply_id=%d: %v\n", pair.SupplyID, err)
			} else {
				totalGoods += saved
			}
		}

		// Download packages
		boxes, err := client.GetSupplyPackages(ctx, rl.Package, rl.PackageBurst, pair.SupplyID)
		totalRequests++
		if err != nil {
			fmt.Printf("  ❌ Ошибка упаковка supply_id=%d: %v\n", pair.SupplyID, err)
		} else if len(boxes) > 0 {
			saved, err := repo.SaveSupplyPackages(ctx, pair.SupplyID, pair.PreorderID, boxes)
			if err != nil {
				fmt.Printf("  ❌ Ошибка сохранения упаковки supply_id=%d: %v\n", pair.SupplyID, err)
			} else {
				totalPackages += saved
			}
		}

		// Progress every 10 supplies
		if (i+1)%10 == 0 || i+1 == len(supplyIDs) {
			fmt.Printf("  Обработано поставок: %d/%d (товаров: %d, упаковок: %d)\n",
				i+1, len(supplyIDs), totalGoods, totalPackages)
		}
	}

	return totalGoods, totalPackages, totalRequests, nil
}

// SupplyRowFromAPI converts an API Supply to a DB SupplyRow.
func SupplyRowFromAPI(s *wb.Supply, downloadedAt string) sqlite.SupplyRow {
	return sqlite.SupplyFromAPI(s, downloadedAt)
}
