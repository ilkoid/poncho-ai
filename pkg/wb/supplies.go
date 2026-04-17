// Package wb provides FBW Supplies API methods and types.
//
// Supplies API (supplies-api.wildberries.ru) provides:
//   - Reference data: warehouses, transit tariffs
//   - Supply management: list, details, goods, packages
//
// Rate limits: 30 req/min for supply endpoints, 6 req/min for reference data.
package wb

import (
	"context"
	"fmt"
	"net/url"
)

const suppliesBaseURL = "https://supplies-api.wildberries.ru"

// ============================================================================
// Supply types — mapped from Swagger: 07-orders-fbw.yaml
// ============================================================================

// Supply represents a supply from POST /api/v1/supplies list endpoint.
type Supply struct {
	Phone        string  `json:"phone"`         // Телефон создавшего
	SupplyID     *int64  `json:"supplyID"`      // ID поставки (null для незапланированных)
	PreorderID   int64   `json:"preorderID"`    // ID заказа
	CreateDate   string  `json:"createDate"`    // Дата создания
	SupplyDate   *string `json:"supplyDate"`    // Плановая дата (nullable)
	FactDate     *string `json:"factDate"`      // Фактическая дата (nullable)
	UpdatedDate  *string `json:"updatedDate"`   // Дата изменения (nullable)
	StatusID     int     `json:"statusID"`      // 1-6: статус поставки
	BoxTypeID    int     `json:"boxTypeID"`     // 0=виртуальная, 1/2=короба, 5=монопаллеты, 6=суперсейф
	IsBoxOnPallet *bool  `json:"isBoxOnPallet"` // Поштучная палета (только при boxTypeID=2)
}

// SupplyDetails represents detailed supply info from GET /api/v1/supplies/{ID}.
type SupplyDetails struct {
	Phone                   string  `json:"phone"`
	StatusID                int     `json:"statusID"`
	VirtualTypeID           *int    `json:"virtualTypeID"`
	BoxTypeID               int     `json:"boxTypeID"`
	CreateDate              string  `json:"createDate"`
	SupplyDate              *string `json:"supplyDate"`
	FactDate                *string `json:"factDate"`
	UpdatedDate             *string `json:"updatedDate"`
	WarehouseID             int     `json:"warehouseID"`
	WarehouseName           string  `json:"warehouseName"`
	ActualWarehouseID       *int    `json:"actualWarehouseID"`
	ActualWarehouseName     *string `json:"actualWarehouseName"`
	TransitWarehouseID      *int    `json:"transitWarehouseID"`
	TransitWarehouseName    *string `json:"transitWarehouseName"`
	AcceptanceCost          *float64 `json:"acceptanceCost"`
	PaidAcceptanceCoefficient *float64 `json:"paidAcceptanceCoefficient"`
	RejectReason            *string `json:"rejectReason"`
	SupplierAssignName      string  `json:"supplierAssignName"`
	StorageCoef             *string `json:"storageCoef"`
	DeliveryCoef            *string `json:"deliveryCoef"`
	Quantity                int     `json:"quantity"`
	ReadyForSaleQuantity    int     `json:"readyForSaleQuantity"`
	AcceptedQuantity        int     `json:"acceptedQuantity"`
	UnloadingQuantity       int     `json:"unloadingQuantity"`
	DepersonalizedQuantity  *int    `json:"depersonalizedQuantity"`
	IsBoxOnPallet           *bool   `json:"isBoxOnPallet"`
}

// GoodInSupply represents a product in a supply from GET /api/v1/supplies/{ID}/goods.
type GoodInSupply struct {
	Barcode              string  `json:"barcode"`
	VendorCode           string  `json:"vendorCode"`
	NmID                 int64   `json:"nmID"`
	NeedKiz              bool    `json:"needKiz"`
	Tnved                *string `json:"tnved"`
	TechSize             string  `json:"techSize"`
	Color                *string `json:"color"`
	SupplierBoxAmount    *int    `json:"supplierBoxAmount"`
	Quantity             int     `json:"quantity"`
	ReadyForSaleQuantity *int    `json:"readyForSaleQuantity"`
	AcceptedQuantity     *int    `json:"acceptedQuantity"`
	UnloadingQuantity    *int    `json:"unloadingQuantity"`
}

// Box represents a package in a supply from GET /api/v1/supplies/{ID}/package.
type Box struct {
	PackageCode string       `json:"packageCode"`
	Quantity    int          `json:"quantity"`
	Barcodes    []GoodInBox  `json:"barcodes"`
}

// GoodInBox represents a product inside a box.
type GoodInBox struct {
	Barcode  string `json:"barcode"`
	Quantity int    `json:"quantity"`
}

// Warehouse represents a WB warehouse from GET /api/v1/warehouses.
type Warehouse struct {
	ID              int    `json:"ID"`
	Name            string `json:"name"`
	Address         string `json:"address"`
	WorkTime        string `json:"workTime"`
	IsActive        bool   `json:"isActive"`
	IsTransitActive bool   `json:"isTransitActive"`
}

// TransitTariff represents a transit tariff from GET /api/v1/transit-tariffs.
type TransitTariff struct {
	TransitWarehouseName     string         `json:"transitWarehouseName"`
	DestinationWarehouseName string         `json:"destinationWarehouseName"`
	ActiveFrom               string         `json:"activeFrom"`
	BoxTariff                []VolumeTariff `json:"boxTariff"`
	PalletTariff             int            `json:"palletTariff"`
}

// VolumeTariff represents a tariff tier by volume.
type VolumeTariff struct {
	From  int     `json:"from"`
	To    int     `json:"to"`
	Value float64 `json:"value"`
}

// SuppliesFilterRequest is the body for POST /api/v1/supplies.
type SuppliesFilterRequest struct {
	Dates     []DateFilter `json:"dates,omitempty"`
	StatusIDs []int        `json:"statusIDs,omitempty"`
}

// DateFilter represents a date range filter.
type DateFilter struct {
	From string `json:"from"`
	Till string `json:"till"`
	Type string `json:"type"` // factDate, createDate, supplyDate, updatedDate
}

// ============================================================================
// Client methods
// ============================================================================

// GetWarehouses fetches the list of WB warehouses.
// GET /api/v1/warehouses — Rate: 6 req/min, burst 6 (swagger).
func (c *Client) GetWarehouses(ctx context.Context, rateLimit, burst int) ([]Warehouse, error) {
	var warehouses []Warehouse
	err := c.Get(ctx, "get_warehouses", suppliesBaseURL, rateLimit, burst, "/api/v1/warehouses", nil, &warehouses)
	if err != nil {
		return nil, fmt.Errorf("get warehouses: %w", err)
	}
	return warehouses, nil
}

// GetTransitTariffs fetches available transit tariffs.
// GET /api/v1/transit-tariffs — Rate: 6 req/min, burst 10 (swagger).
func (c *Client) GetTransitTariffs(ctx context.Context, rateLimit, burst int) ([]TransitTariff, error) {
	var tariffs []TransitTariff
	err := c.Get(ctx, "get_transit_tariffs", suppliesBaseURL, rateLimit, burst, "/api/v1/transit-tariffs", nil, &tariffs)
	if err != nil {
		return nil, fmt.Errorf("get transit tariffs: %w", err)
	}
	return tariffs, nil
}

// GetSupplies fetches the list of supplies for a given period.
// POST /api/v1/supplies — Rate: 30 req/min, burst 10 (swagger).
// Returns paginated results; use limit/offset for pagination (max 1000 per page).
func (c *Client) GetSupplies(ctx context.Context, rateLimit, burst int, filter SuppliesFilterRequest, limit, offset int) ([]Supply, error) {
	params := url.Values{}
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("offset", fmt.Sprintf("%d", offset))

	var supplies []Supply
	err := c.Post(ctx, "get_supplies", suppliesBaseURL, rateLimit, burst,
		"/api/v1/supplies?"+params.Encode(), filter, &supplies)
	if err != nil {
		return nil, fmt.Errorf("get supplies (offset=%d): %w", offset, err)
	}
	return supplies, nil
}

// GetSupplyGoods fetches goods for a specific supply.
// GET /api/v1/supplies/{ID}/goods — Rate: 30 req/min, burst 10 (swagger).
func (c *Client) GetSupplyGoods(ctx context.Context, rateLimit, burst int, supplyID int64, limit, offset int) ([]GoodInSupply, error) {
	path := fmt.Sprintf("/api/v1/supplies/%d/goods?limit=%d&offset=%d", supplyID, limit, offset)
	var goods []GoodInSupply
	err := c.Get(ctx, "get_supply_goods", suppliesBaseURL, rateLimit, burst, path, nil, &goods)
	if err != nil {
		return nil, fmt.Errorf("get supply goods (supplyID=%d): %w", supplyID, err)
	}
	return goods, nil
}

// GetSupplyPackages fetches package info for a specific supply.
// GET /api/v1/supplies/{ID}/package — Rate: 30 req/min, burst 10 (swagger).
func (c *Client) GetSupplyPackages(ctx context.Context, rateLimit, burst int, supplyID int64) ([]Box, error) {
	path := fmt.Sprintf("/api/v1/supplies/%d/package", supplyID)
	var boxes []Box
	err := c.Get(ctx, "get_supply_packages", suppliesBaseURL, rateLimit, burst, path, nil, &boxes)
	if err != nil {
		return nil, fmt.Errorf("get supply packages (supplyID=%d): %w", supplyID, err)
	}
	return boxes, nil
}

// GetSupplyDetails fetches detailed info for a specific supply.
// GET /api/v1/supplies/{ID} — Rate: 30 req/min, burst 10 (swagger).
func (c *Client) GetSupplyDetails(ctx context.Context, rateLimit, burst int, supplyID int64) (*SupplyDetails, error) {
	path := fmt.Sprintf("/api/v1/supplies/%d", supplyID)
	var details SupplyDetails
	err := c.Get(ctx, "get_supply_details", suppliesBaseURL, rateLimit, burst, path, nil, &details)
	if err != nil {
		return nil, fmt.Errorf("get supply details (supplyID=%d): %w", supplyID, err)
	}
	return &details, nil
}
