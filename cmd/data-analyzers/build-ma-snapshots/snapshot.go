package main

import "time"

// MARow represents one row in the ma_daily table.
type MARow struct {
	SnapshotDate string
	NmID         int
	Article      string
	Identifier   string
	VendorCode   string

	Sold     int
	SoldPrev int

	MA3  *float64
	MA7  *float64
	MA14 *float64
	MA28 *float64

	Delta1D    int
	Delta1DPct *float64

	DeltaMA3     *float64
	DeltaMA3Pct  *float64
	DeltaMA7     *float64
	DeltaMA7Pct  *float64
	DeltaMA14    *float64
	DeltaMA14Pct *float64
	DeltaMA28    *float64
	DeltaMA28Pct *float64
}

// ProductAttrs holds product dimension data for PowerBI filtering.
type ProductAttrs struct {
	NmID           int
	Article        string
	Identifier     string
	VendorCode     string
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

		// Delta vs previous day
		if soldPrev > 0 {
			pct := float64(sold-soldPrev) / float64(soldPrev) * 100
			row.Delta1DPct = &pct
		}

		// Compute MAs for each window
		for _, w := range windows {
			ma := computeMA(dayMap, ref, w, minDays)
			if ma == nil {
				continue
			}

			delta := float64(sold) - *ma
			pct := delta / *ma * 100

			switch w {
			case 3:
				row.MA3 = ma
				row.DeltaMA3 = &delta
				row.DeltaMA3Pct = &pct
			case 7:
				row.MA7 = ma
				row.DeltaMA7 = &delta
				row.DeltaMA7Pct = &pct
			case 14:
				row.MA14 = ma
				row.DeltaMA14 = &delta
				row.DeltaMA14Pct = &pct
			case 28:
				row.MA28 = ma
				row.DeltaMA28 = &delta
				row.DeltaMA28Pct = &pct
			}
		}

		result = append(result, row)
	}

	return result
}

// computeMA calculates the average of daily sales over the N days before refDate.
// Zero-sales days (days without entries in dayMap) are counted as 0.
// Returns nil if fewer than minDays have any sales data in the window.
func computeMA(dayMap map[string]int, ref time.Time, window, minDays int) *float64 {
	var sum float64
	var daysWithData int

	for i := 1; i <= window; i++ {
		d := ref.AddDate(0, 0, -i).Format("2006-01-02")
		v := dayMap[d] // returns 0 if key missing — zero-sales day
		sum += float64(v)
		if v > 0 {
			daysWithData++
		}
	}

	if daysWithData < minDays {
		return nil
	}

	avg := sum / float64(window)
	return &avg
}
