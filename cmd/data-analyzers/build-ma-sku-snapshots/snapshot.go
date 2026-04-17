package main

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/analytics"
)

// SKURow represents one row in the flat ma_sku_daily table.
// Denormalized: all product identifiers, attributes, stock data, MA, and risk flags.
type SKURow struct {
	SnapshotDate string
	NmID         int
	ChrtID       int64
	RegionName   string
	TechSize     string // from card_sizes (e.g. "104", "50")

	// Product identifiers
	Article    string
	Identifier string
	VendorCode string

	// Product attributes (from onec_goods/pim_goods)
	Name           string
	Brand          string
	Type           string
	Category       string
	CategoryLevel1 string
	CategoryLevel2 string
	Sex            string
	Season         string
	Color          string
	Collection     string

	// Stock
	StockQty       int64
	SupplyIncoming int64 // from active supplies: quantity - ready_for_sale_quantity
	TotalSizes     int
	SizesInStock   int
	FillPct        float64

	// MA (regional per chrt_id, N/A when insufficient data)
	MA3        *float64
	MA7        *float64
	MA14       *float64
	MA28       *float64
	MARegional bool // true = MA computed from this region's sales

	// Derived metrics
	SDRDays  *float64 // stock_qty / MA-7 (days until stockout)
	TrendPct *float64 // (MA-3 - MA-7) / MA-7 * 100

	// Flags
	Risk       bool
	Critical   bool
	OutOfStock bool
	BrokenGrid bool
}

// StockKey identifies a unique (nm_id, chrt_id, region_name) position.
type StockKey struct {
	NmID       int
	ChrtID     int64
	RegionName string
}

// StockInfo holds stock data for a single position.
type StockInfo struct {
	StockQty int64
	TechSize string
}

// SizeInfo holds size row completion data per (nm_id, region).
type SizeInfo struct {
	TotalSizes   int
	SizesInStock int
}

// AlertsParams holds alert threshold parameters.
type AlertsParams struct {
	ZeroStockThreshold int
	ReorderWindow      int
	CriticalDays       int
}

// ComputeSKUSnapshots builds flat SKU rows from stock, MA, and size data.
//
// Parameters:
//   - stocks:  map of StockKey → StockInfo (from stocks_daily_warehouses)
//   - maData:  map of nm_id → chrt_id → region → date → sold (regional sales)
//   - sizes:   map of (nm_id, region) → SizeInfo (size row completion)
//   - refDate: snapshot date YYYY-MM-DD
//   - windows: MA windows (e.g. [3, 7, 14, 28])
//   - minDays: minimum days with data for MA computation
//   - alerts:  alert thresholds
func ComputeSKUSnapshots(
	stocks map[StockKey]StockInfo,
	maData map[int]map[int64]map[string]map[string]int, // nm_id → chrt_id → region → date → sold
	sizes map[SizeRegionKey]SizeInfo,
	refDate string,
	windows []int,
	minDays int,
	alerts AlertsParams,
) []SKURow {
	ref, err := time.Parse("2006-01-02", refDate)
	if err != nil {
		fmt.Printf("WARN: parse refDate %q: %v\n", refDate, err)
		return nil
	}

	var result []SKURow
	for key, info := range stocks {
		row := SKURow{
			SnapshotDate: refDate,
			NmID:         key.NmID,
			ChrtID:       key.ChrtID,
			RegionName:   key.RegionName,
			TechSize:     info.TechSize,
			StockQty:     info.StockQty,
		}

		// Size info for this (nm_id, region)
		sk := SizeRegionKey{NmID: key.NmID, RegionName: key.RegionName}
		if si, ok := sizes[sk]; ok {
			row.TotalSizes = si.TotalSizes
			row.SizesInStock = si.SizesInStock
			if si.TotalSizes > 0 {
				row.FillPct = float64(si.SizesInStock) / float64(si.TotalSizes) * 100
			}
		}

		// MA: lookup regional dayMap for this (nm_id, chrt_id, region_name)
		// No fallback to global MA — prevents overstocking in low-data regions.
		if chrtMap, ok1 := maData[key.NmID]; ok1 {
			if regionMap, ok2 := chrtMap[key.ChrtID]; ok2 {
				if dayMap, ok3 := regionMap[key.RegionName]; ok3 {
					row.MARegional = true
					for _, w := range windows {
						ma := analytics.ComputeMA(dayMap, ref, w, minDays)
						if ma == nil {
							continue
						}
						switch w {
						case 3:
							row.MA3 = ma
						case 7:
							row.MA7 = ma
						case 14:
							row.MA14 = ma
						case 28:
							row.MA28 = ma
						}
					}
				}
			}
		}

		// Derived metrics
		row.SDRDays = computeSDR(row.StockQty, row.MA7)
		row.TrendPct = computeTrend(row.MA3, row.MA7)

		// Risk flags
		computeFlags(&row, alerts)

		result = append(result, row)
	}

	return result
}

// SizeRegionKey identifies (nm_id, region) for size row queries.
type SizeRegionKey struct {
	NmID       int
	RegionName string
}

// computeSDR calculates Stock-to-Demand Ratio: stock_qty / MA-7.
// Returns nil if MA-7 is nil or zero (no demand data or division by zero).
func computeSDR(stockQty int64, ma7 *float64) *float64 {
	if ma7 == nil || *ma7 <= 0 {
		return nil
	}
	sdr := float64(stockQty) / *ma7
	return &sdr
}

// computeTrend calculates demand trend: (MA-3 - MA-7) / MA-7 * 100.
// Returns nil if either MA is nil or MA-7 is zero.
func computeTrend(ma3, ma7 *float64) *float64 {
	if ma3 == nil || ma7 == nil || *ma7 == 0 {
		return nil
	}
	trend := (*ma3 - *ma7) / *ma7 * 100
	return &trend
}

// computeFlags sets risk flags based on stock, SDR, and size row data.
//
// Logic:
//   - zero     = stock_qty <= zero_stock_threshold
//   - critical = !zero AND sdr > 0 AND sdr <= critical_days
//   - risk     = !zero AND sdr > 0 AND sdr <= reorder_window
//   - out_of_stock = zero AND MA-7 > 0 (no stock but there is demand)
//   - broken_grid  = sizes_in_stock < total_sizes
func computeFlags(row *SKURow, alerts AlertsParams) {
	zero := row.StockQty <= int64(alerts.ZeroStockThreshold)

	var sdr float64
	if row.SDRDays != nil {
		sdr = *row.SDRDays
	}

	if !zero && row.SDRDays != nil && sdr > 0 {
		if sdr <= float64(alerts.CriticalDays) {
			row.Critical = true
		}
		if sdr <= float64(alerts.ReorderWindow) {
			row.Risk = true
		}
	}

	if zero && row.MA7 != nil && *row.MA7 > 0 {
		row.OutOfStock = true
	}

	if row.SizesInStock < row.TotalSizes {
		row.BrokenGrid = true
	}
}
