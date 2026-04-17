package main

import (
	"time"

	"github.com/ilkoid/poncho-ai/pkg/analytics"
)

// MARow represents one row in the flat ma_daily table.
// All product identifiers and attributes are denormalized into this single struct.
type MARow struct {
	SnapshotDate string
	NmID         int

	// Product identifiers
	Article    string
	Identifier string
	VendorCode string

	// Product attributes (from onec_goods/pim_goods)
	Name           string
	NameIM         string
	Brand          string
	Type           string
	Category       string
	CategoryLevel1 string
	CategoryLevel2 string
	Sex            string
	Season         string
	Color          string
	Collection     string

	// Actual sales
	Sold     int
	SoldPrev int

	// Moving averages
	MA3  *float64
	MA7  *float64
	MA14 *float64
	MA28 *float64

	Delta1D int
}

// ComputeMASnapshots calculates moving averages for all products in the daily sales map.
//
// refDate is the snapshot date (YYYY-MM-DD). MAs are computed from the N complete days
// BEFORE refDate, not including refDate itself. Zero-sales days are included in MA calculation.
func ComputeMASnapshots(daily map[int]map[string]int, refDate string, windows []int, minDays int) []MARow {
	ref, err := time.Parse("2006-01-02", refDate)
	if err != nil {
		return nil
	}

	var result []MARow
	for nmID, dayMap := range daily {
		sold := dayMap[refDate]
		prevDate := ref.AddDate(0, 0, -1).Format("2006-01-02")
		soldPrev := dayMap[prevDate]

		// Skip products with no sales in the entire window
		if sold == 0 && soldPrev == 0 && len(dayMap) == 0 {
			continue
		}

		row := MARow{
			SnapshotDate: refDate,
			NmID:         nmID,
			Sold:         sold,
			SoldPrev:     soldPrev,
			Delta1D:      sold - soldPrev,
		}

		// Compute MAs for each window
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

		result = append(result, row)
	}

	return result
}

