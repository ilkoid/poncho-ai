package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/ilkoid/poncho-ai/pkg/filter"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// compareRow holds computed deltas for a single card.
type compareRow struct {
	NmID        int
	VendorCode  string
	WB_L, WB_W, WB_H, WB_Wt float64
	OC_L, OC_W, OC_H, OC_Wt float64
	DeltaVol    float64
	DeltaWeight float64
	VolFlag     bool
	WtFlag      bool
	IsNew       bool // all WB dims are zero
}

func runCompare(ctx context.Context, db *sql.DB, f *filter.Filter, cfg CompareConfig) error {
	fmt.Println("=== fix-card-dimensions: compare (WB vs 1C) ===")
	fmt.Printf("tolerance: volume ≥ %.0f cm³, weight ≥ %.3f kg\n\n", cfg.ToleranceCm3, cfg.ToleranceKg)

	aggRows, err := getAllAggregatedDimensions(ctx, db)
	if err != nil {
		return fmt.Errorf("get dimensions: %w", err)
	}
	if len(aggRows) == 0 {
		fmt.Println("no cards with 1C dimension data found")
		return nil
	}

	filtered := applyFilters(aggRows, f)
	fmt.Printf("in-memory filter: %d → %d cards\n", len(aggRows), len(filtered))

	filtered, err = applySQLFilters(ctx, db, filtered, f)
	if err != nil {
		return fmt.Errorf("apply sql filters: %w", err)
	}
	fmt.Printf("sql filter: → %d cards\n", len(filtered))

	if len(filtered) == 0 {
		fmt.Println("no cards passed filters")
		return nil
	}

	rows := computeDeltas(filtered, cfg)
	printCompareReport(os.Stdout, rows)

	return nil
}

func computeDeltas(aggRows []sqlite.DimensionAggRow, cfg CompareConfig) []compareRow {
	var result []compareRow

	for _, r := range aggRows {
		volWB := r.OldLength * r.OldWidth * r.OldHeight
		volOC := r.NewLength * r.NewWidth * r.NewHeight
		deltaVol := math.Abs(volWB - volOC)
		deltaWt := math.Abs(r.OldWeight - r.NewWeight)

		volFlag := deltaVol > cfg.ToleranceCm3
		wtFlag := deltaWt > cfg.ToleranceKg
		isNew := r.OldLength == 0 && r.OldWidth == 0 && r.OldHeight == 0 && r.OldWeight == 0

		// Show card if volume OR weight exceeds threshold.
		if !volFlag && !wtFlag {
			continue
		}

		result = append(result, compareRow{
			NmID:       r.NmID,
			VendorCode: r.VendorCode,
			WB_L:       r.OldLength,
			WB_W:       r.OldWidth,
			WB_H:       r.OldHeight,
			WB_Wt:      r.OldWeight,
			OC_L:       r.NewLength,
			OC_W:       r.NewWidth,
			OC_H:       r.NewHeight,
			OC_Wt:      r.NewWeight,
			DeltaVol:    deltaVol,
			DeltaWeight: deltaWt,
			VolFlag:     volFlag,
			WtFlag:      wtFlag,
			IsNew:       isNew,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].DeltaVol > result[j].DeltaVol
	})

	return result
}

func printCompareReport(w io.Writer, rows []compareRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "\nno discrepancies found — all cards match 1C within tolerance")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "nm_id\tvendor_code\tWB (L×W×H)\t1C (L×W×H)\tΔVol cm³\tWB W kg\t1C W kg\tΔWt kg\tFlags")
	fmt.Fprintln(tw, "─────\t───────────\t──────────\t──────────\t────────\t───────\t───────\t───────\t─────")

	var volCount, wtCount, newCount int

	for _, r := range rows {
		wbDims := fmt.Sprintf("%.0f×%.0f×%.0f", r.WB_L, r.WB_W, r.WB_H)
		ocDims := fmt.Sprintf("%.0f×%.0f×%.0f", r.OC_L, r.OC_W, r.OC_H)
		deltaWt := r.OC_Wt - r.WB_Wt

		var flags string
		if r.VolFlag {
			flags += "Vol! "
			volCount++
		}
		if r.WtFlag {
			flags += "Wt! "
			wtCount++
		}
		if r.IsNew {
			flags += "[NEW]"
			newCount++
		}

		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%.0f\t%.3f\t%.3f\t%+.3f\t%s\n",
			r.NmID, r.VendorCode, wbDims, ocDims, r.DeltaVol,
			r.WB_Wt, r.OC_Wt, deltaWt, flags)
	}

	tw.Flush()

	fmt.Fprintf(w, "\nSummary: %d cards with discrepancies", len(rows))
	fmt.Fprintf(w, " (volume=%d, weight=%d, new=%d)\n", volCount, wtCount, newCount)
}
