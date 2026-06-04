package supplies

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID constants for SetRateLimit — must match the ToolIDs used in wb.Client calls.
// CLI must call wbClient.SetRateLimit() for each before using the source.
const (
	ToolIDWarehouses     = "get_warehouses"
	ToolIDTransitTariffs = "get_transit_tariffs"
	ToolIDSupplyOps      = "supply_ops" // Shared limiter for 4 supply endpoints
)

// WBSource adapts *wb.Client to Source interface.
// Stores rate limits internally — Source methods have clean signatures.
// CLI must call ShareRateLimit("supply_ops", ...) on the client before creating WBSource.
type WBSource struct {
	client   *wb.Client
	refRL    int
	refBurst int
	opsRL    int
	opsBurst int
}

// NewWBSource creates a Source backed by the real WB Supplies API.
func NewWBSource(client *wb.Client, refRL, refBurst, opsRL, opsBurst int) *WBSource {
	return &WBSource{
		client:   client,
		refRL:    refRL,
		refBurst: refBurst,
		opsRL:    opsRL,
		opsBurst: opsBurst,
	}
}

// GetWarehouses delegates to wb.Client.GetWarehouses.
func (s *WBSource) GetWarehouses(ctx context.Context) ([]wb.Warehouse, error) {
	return s.client.GetWarehouses(ctx, s.refRL, s.refBurst)
}

// GetTransitTariffs delegates to wb.Client.GetTransitTariffs.
func (s *WBSource) GetTransitTariffs(ctx context.Context) ([]wb.TransitTariff, error) {
	return s.client.GetTransitTariffs(ctx, s.refRL, s.refBurst)
}

// GetSupplies delegates to wb.Client.GetSupplies.
func (s *WBSource) GetSupplies(ctx context.Context, filter wb.SuppliesFilterRequest, limit, offset int) ([]wb.Supply, error) {
	return s.client.GetSupplies(ctx, s.opsRL, s.opsBurst, filter, limit, offset)
}

// GetSupplyDetails delegates to wb.Client.GetSupplyDetails.
func (s *WBSource) GetSupplyDetails(ctx context.Context, supplyID int64) (*wb.SupplyDetails, error) {
	return s.client.GetSupplyDetails(ctx, s.opsRL, s.opsBurst, supplyID)
}

// GetSupplyGoods delegates to wb.Client.GetSupplyGoods.
func (s *WBSource) GetSupplyGoods(ctx context.Context, supplyID int64, limit, offset int) ([]wb.GoodInSupply, error) {
	return s.client.GetSupplyGoods(ctx, s.opsRL, s.opsBurst, supplyID, limit, offset)
}

// GetSupplyPackages delegates to wb.Client.GetSupplyPackages.
func (s *WBSource) GetSupplyPackages(ctx context.Context, supplyID int64) ([]wb.Box, error) {
	return s.client.GetSupplyPackages(ctx, s.opsRL, s.opsBurst, supplyID)
}
