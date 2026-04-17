package main

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config wraps shared config types for this downloader.
type Config struct {
	WB     config.WBClientConfig `yaml:"wb"`
	Supply config.SupplyConfig   `yaml:"supply"`
}

// SuppliesClient defines the interface for supply API calls.
// Defined in consumer package following Rule 6 (Go idiom).
type SuppliesClient interface {
	GetWarehouses(ctx context.Context, rateLimit, burst int) ([]wb.Warehouse, error)
	GetTransitTariffs(ctx context.Context, rateLimit, burst int) ([]wb.TransitTariff, error)
	GetSupplies(ctx context.Context, rateLimit, burst int, filter wb.SuppliesFilterRequest, limit, offset int) ([]wb.Supply, error)
	GetSupplyDetails(ctx context.Context, rateLimit, burst int, supplyID int64) (*wb.SupplyDetails, error)
	GetSupplyGoods(ctx context.Context, rateLimit, burst int, supplyID int64, limit, offset int) ([]wb.GoodInSupply, error)
	GetSupplyPackages(ctx context.Context, rateLimit, burst int, supplyID int64) ([]wb.Box, error)
}
