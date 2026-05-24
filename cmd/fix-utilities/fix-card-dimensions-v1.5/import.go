package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"

	"github.com/xuri/excelize/v2"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// importXLSFile reads the 1C WMS dimension XLS and saves rows into onec_dimensions.
// XLS columns: Номенклатура, НоменклатураИдентификатор, Характеристика,
// ХарактеристикаИдентификатор, Длина, Ширина, Высота, Вес, Объём
func importXLSFile(ctx context.Context, dbPath, xlsPath string) (int, error) {
	dllog.PrintHeader("fix-card-dimensions: import XLS",
		dllog.HeaderField{Key: "DB", Value: dbPath},
		dllog.HeaderField{Key: "XLS", Value: xlsPath},
	)

	f, err := excelize.OpenFile(xlsPath)
	if err != nil {
		return 0, fmt.Errorf("open xls: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return 0, fmt.Errorf("read sheet %q: %w", sheet, err)
	}

	if len(rows) < 2 {
		return 0, fmt.Errorf("xlsx has no data rows (only %d rows total)", len(rows))
	}

	// Skip header row
	dataRows := rows[1:]
	dllog.Log("parsed %d data rows from sheet %q", len(dataRows), sheet)

	var dimRows []sqlite.OneCDimensionRow
	for i, row := range dataRows {
		if len(row) < 8 {
			dllog.Error("row %d: only %d columns (need 8), skipping", i+2, len(row))
			continue
		}

		goodName := row[0]
		goodGUID := row[1]
		sizeName := row[2]
		skuGUID := row[3]

		lengthDM := parseFloat(row[4])
		widthDM := parseFloat(row[5])
		heightDM := parseFloat(row[6])
		weightKG := parseFloat(row[7])

		var volumeCM3 float64
		if len(row) >= 9 {
			volumeCM3 = parseFloat(row[8])
		} else {
			volumeCM3 = math.Ceil(lengthDM*10) * math.Ceil(widthDM*10) * math.Ceil(heightDM*10)
		}

		if goodGUID == "" || skuGUID == "" {
			dllog.Error("row %d: empty GUID (good=%q sku=%q), skipping", i+2, goodGUID, skuGUID)
			continue
		}

		dimRows = append(dimRows, sqlite.OneCDimensionRow{
			GoodGUID:  goodGUID,
			SKUGUID:   skuGUID,
			GoodName:  goodName,
			SizeName:  sizeName,
			LengthDM:  lengthDM,
			WidthDM:   widthDM,
			HeightDM:  heightDM,
			WeightKG:  weightKG,
			VolumeCM3: volumeCM3,
			Source:    "xls",
		})
	}

	if len(dimRows) == 0 {
		return 0, fmt.Errorf("no valid dimension rows found in XLS")
	}

	repo, err := openDB(dbPath)
	if err != nil {
		return 0, err
	}
	defer repo.Close()

	count, err := repo.ImportDimensions(ctx, dimRows)
	if err != nil {
		return 0, fmt.Errorf("save dimensions: %w", err)
	}

	dbCount, _ := repo.CountDimensions(ctx)
	dllog.Log("onec_dimensions table now has %d rows (imported %d)", dbCount, count)

	_ = filepath.Base(xlsPath)
	return count, nil
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}
