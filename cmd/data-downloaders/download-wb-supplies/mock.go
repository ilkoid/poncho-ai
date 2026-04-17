package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// runMock downloads mock data to verify DB schema and save logic.
func runMock(ctx context.Context, repo *sqlite.SQLiteSalesRepository, supplyCfg config.SupplyConfig) {
	// 1. Mock warehouses
	warehouses := []wb.Warehouse{
		{ID: 507, Name: "Коледино", Address: "Подольск", WorkTime: "24/7", IsActive: true, IsTransitActive: false},
		{ID: 117198, Name: "Казань", Address: "Казань", WorkTime: "24/7", IsActive: true, IsTransitActive: false},
		{ID: 300461, Name: "Гомель 2", Address: "Гомель", WorkTime: "24/7", IsActive: false, IsTransitActive: true},
	}
	whSaved, _ := repo.SaveWarehouses(ctx, warehouses)
	fmt.Printf("  ✅ Складов: %d\n", whSaved)

	// 2. Mock tariffs
	tariffs := []wb.TransitTariff{
		{
			TransitWarehouseName:     "Обухово",
			DestinationWarehouseName: "Краснодар",
			ActiveFrom:               "2024-11-03T21:01:00Z",
			PalletTariff:             7500,
			BoxTariff: []wb.VolumeTariff{
				{From: 0, To: 1500, Value: 5.3},
				{From: 1500, To: 0, Value: 3.9},
			},
		},
	}
	tSaved, _ := repo.SaveTransitTariffs(ctx, tariffs)
	fmt.Printf("  ✅ Тарифов: %d\n", tSaved)

	// 3. Mock supplies
	now := time.Now().Format("2006-01-02 15:04:05")
	supplyID1 := int64(26596368)
	supplyID2 := int64(22677736)
	supplies := []wb.Supply{
		{Phone: "+7 916 *** 33 33", SupplyID: &supplyID1, PreorderID: 34601223, CreateDate: "2024-12-29T16:57:59+03:00", StatusID: 2, BoxTypeID: 5},
		{Phone: "+7 000 *** 36 76", SupplyID: &supplyID2, PreorderID: 27363170, CreateDate: "2024-08-22T18:10:59+03:00", StatusID: 6, BoxTypeID: 2},
	}

	rows := make([]sqlite.SupplyRow, 0, len(supplies))
	for i := range supplies {
		rows = append(rows, SupplyRowFromAPI(&supplies[i], now))
	}
	sSaved, _ := repo.SaveSupplies(ctx, rows)
	fmt.Printf("  ✅ Поставок: %d\n", sSaved)

	// 4. Mock goods
	goods := []wb.GoodInSupply{
		{Barcode: "1234567891234", VendorCode: "wb4sewt0vg", NmID: 987456654, TechSize: "C", NeedKiz: true, Tnved: strPtr("6204430000"), Color: strPtr("красный"), SupplierBoxAmount: intPtr(10), Quantity: 10},
		{Barcode: "9876543210987", VendorCode: "wb7xkqw2a", NmID: 987456655, TechSize: "M", Quantity: 5},
	}
	gSaved, _ := repo.SaveSupplyGoods(ctx, supplyID1, 34601223, goods)
	fmt.Printf("  ✅ Товаров: %d\n", gSaved)

	// 5. Mock packages
	boxes := []wb.Box{
		{PackageCode: "WB_689", Quantity: 1, Barcodes: []wb.GoodInBox{{Barcode: "1234567891234", Quantity: 1}}},
	}
	pSaved, _ := repo.SaveSupplyPackages(ctx, supplyID1, 34601223, boxes)
	fmt.Printf("  ✅ Упаковок: %d\n", pSaved)

	// 6. Verify counts
	supplyCount, _ := repo.CountSupplies(ctx)
	goodsCount, _ := repo.CountSupplyGoods(ctx)
	fmt.Printf("\n  Итого в БД: %d поставок, %d товаров\n", supplyCount, goodsCount)
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
