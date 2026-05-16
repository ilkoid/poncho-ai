package main

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/analytics"
)

// ArticleRow represents one row in the ma_article_daily table.
// Aggregated from SKU-level data: one row per (nm_id, region_name).
type ArticleRow struct {
	SnapshotDate string
	NmID         int
	RegionName   string

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

	// Ratings (from products table)
	ProductRating  *float64 // stars on product card
	FeedbackRating *float64 // rating from reviews
	FeedbackCount  int      // number of reviews

	// Prices (from product_prices)
	Price           *float64 // base price
	DiscountedPrice *float64 // price with discount
	Discount        int      // discount %

	// Funnel (from funnel_metrics_daily)
	OpenCount       int
	CartCount       int
	OrderCount      int
	BuyoutCount     int
	ConversionBuyout *float64

	// Visibility (aggregated from search_queries_daily)
	AvgPosition *float64
	Visibility  *float64

	// Stock (aggregated across all sizes)
	StockQty       int64
	SupplyIncoming int64
	TotalSizes     int
	SizesInStock   int
	FillPct        float64

	// MA (computed from article-level sales, NOT averaged from SKU MA)
	MA3  *float64
	MA7  *float64
	MA14 *float64
	MA28 *float64

	// Derived metrics
	SDRDays  *float64 // stock_qty / MA-7
	TrendPct *float64 // (MA-3 - MA-7) / MA-7 * 100

	// Flags
	Risk       bool
	Critical   bool
	OutOfStock bool
	BrokenGrid bool
}

// articleGroupKey is the grouping key for article aggregation.
type articleGroupKey struct {
	NmID       int
	RegionName string
}

// ComputeArticleSnapshots builds article-level rows from SKU-level data.
//
// Parameters:
//   - skuRows:       already computed SKU rows (ma_sku_daily)
//   - articleSales:  nm_id → region → date → SUM(sold across all chrt_ids)
//   - refDate:       snapshot date YYYY-MM-DD
//   - windows:       MA windows (e.g. [3, 7, 14, 28])
//   - minDays:       minimum days with data for MA computation
//   - alerts:        alert thresholds
func ComputeArticleSnapshots(
	skuRows []SKURow,
	articleSales map[int]map[string]map[string]int, // nm_id → region → date → sold
	refDate string,
	windows []int,
	minDays int,
	alerts AlertsParams,
) []ArticleRow {
	ref, err := time.Parse("2006-01-02", refDate)
	if err != nil {
		fmt.Printf("WARN: parse refDate %q: %v\n", refDate, err)
		return nil
	}

	// Group SKU rows by (nm_id, region_name)
	groups := make(map[articleGroupKey][]SKURow)
	for i := range skuRows {
		key := articleGroupKey{NmID: skuRows[i].NmID, RegionName: skuRows[i].RegionName}
		groups[key] = append(groups[key], skuRows[i])
	}

	var result []ArticleRow
	for key, skus := range groups {
		row := ArticleRow{
			SnapshotDate: refDate,
			NmID:         key.NmID,
			RegionName:   key.RegionName,
		}

		// Copy attributes from first SKU row (all same nm_id)
		first := skus[0]
		row.Article = first.Article
		row.Identifier = first.Identifier
		row.VendorCode = first.VendorCode
		row.Name = first.Name
		row.Brand = first.Brand
		row.Type = first.Type
		row.Category = first.Category
		row.CategoryLevel1 = first.CategoryLevel1
		row.CategoryLevel2 = first.CategoryLevel2
		row.Sex = first.Sex
		row.Season = first.Season
		row.Color = first.Color
		row.Collection = first.Collection

		// Aggregate stock across all sizes
		row.TotalSizes = first.TotalSizes // same for all sizes of same nm_id
		for _, s := range skus {
			row.StockQty += s.StockQty
			row.SupplyIncoming += s.SupplyIncoming
		}

		// Sizes in stock: count distinct chrt_ids with stock > 0 in this group
		inStockSizes := make(map[int64]bool)
		for _, s := range skus {
			if s.StockQty > 0 {
				inStockSizes[s.ChrtID] = true
			}
		}
		row.SizesInStock = len(inStockSizes)

		// Fill percentage
		if row.TotalSizes > 0 {
			row.FillPct = float64(row.SizesInStock) / float64(row.TotalSizes) * 100
		}

		// MA from article-level sales (SUM across all chrt_ids per day)
		if regionMap, ok := articleSales[key.NmID]; ok {
			if dayMap, ok2 := regionMap[key.RegionName]; ok2 {
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

		// Derived metrics
		row.SDRDays = computeSDR(row.StockQty, row.MA7)
		row.TrendPct = computeTrend(row.MA3, row.MA7)

		// Risk flags
		computeArticleFlags(&row, alerts)

		result = append(result, row)
	}

	return result
}

// computeArticleFlags sets risk flags for article-level rows.
func computeArticleFlags(row *ArticleRow, alerts AlertsParams) {
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
