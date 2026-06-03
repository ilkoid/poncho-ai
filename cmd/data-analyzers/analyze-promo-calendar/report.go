package main

import (
	"fmt"
	"math"

	"github.com/xuri/excelize/v2"
)

// promoMatrix maps vendor_code → date → PromoDay for O(1) cell lookup.
type promoMatrix map[string]map[string]PromoDay

// articleMeta holds product info per vendor_code (same across all dates).
type articleMeta struct {
	title      string
	category1C string
}

// buildMatrix converts a flat slice of PromoDay into a nested map.
func buildMatrix(days []PromoDay) promoMatrix {
	m := make(promoMatrix, 1024)
	for _, d := range days {
		if m[d.VendorCode] == nil {
			m[d.VendorCode] = make(map[string]PromoDay, 64)
		}
		if existing, ok := m[d.VendorCode][d.StatsDate]; ok && existing.TotalSpend >= d.TotalSpend {
			continue
		}
		m[d.VendorCode][d.StatsDate] = d
	}
	return m
}

// orderedArticles returns vendor_codes in insertion order (already sorted by SQL).
func orderedArticles(days []PromoDay) []string {
	seen := make(map[string]bool, 1024)
	result := make([]string, 0, 1024)
	for _, d := range days {
		if !seen[d.VendorCode] {
			seen[d.VendorCode] = true
			result = append(result, d.VendorCode)
		}
	}
	return result
}

// buildMetaMap extracts title and category per vendor_code from PromoDay data.
func buildMetaMap(days []PromoDay) map[string]articleMeta {
	m := make(map[string]articleMeta, 1024)
	for _, d := range days {
		if _, ok := m[d.VendorCode]; !ok {
			m[d.VendorCode] = articleMeta{title: d.Title, category1C: d.Category1C}
		}
	}
	return m
}

// formatDateShort converts "2026-05-01" → "01.05" for column headers.
func formatDateShort(isoDate string) string {
	if len(isoDate) >= 10 {
		return isoDate[8:10] + "." + isoDate[5:7]
	}
	return isoDate
}

// formatSpend formats ruble amount compactly: "1,250₽", "15.3K₽", "0₽".
func formatSpend(v float64) string {
	if v == 0 {
		return "0₽"
	}
	abs := math.Abs(v)
	switch {
	case abs >= 1e6:
		return fmt.Sprintf("%.1fM₽", v/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.1fK₽", v/1e3)
	default:
		return fmt.Sprintf("%.0f₽", v)
	}
}

// formatCell formats the content of a promo cell: "15.3% / 1,250₽".
func formatCell(p PromoDay) string {
	drr := p.DRR()
	switch {
	case p.TotalSpend > 0 && drr < 0:
		return fmt.Sprintf("∞ / %s", formatSpend(p.TotalSpend))
	case p.TotalSpend > 0:
		return fmt.Sprintf("%.1f%% / %s", drr, formatSpend(p.TotalSpend))
	default:
		return "0₽"
	}
}

// Column layout:
//   A = Артикул (vendor_code)
//   B = Название (cards.title)
//   C = Категория 1С (onec_goods.category)
//   D+ = dates
const (
	colArticle  = 1 // A
	colTitle    = 2 // B
	colCategory = 3 // C
	colDateBase = 4 // D+
)

// ExportXLSX creates an Excel file with the promo calendar matrix.
func ExportXLSX(days []PromoDay, dates []string, xlsxPath string) error {
	f := excelize.NewFile()
	sheet := "Календарь"
	f.SetSheetName("Sheet1", sheet)

	// --- Styles ---
	headerStyle := xMustStyle(f, &excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 10},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	articleStyle := xMustStyle(f, &excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	textStyle := xMustStyle(f, &excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	greenStyle := xMustStyle(f, &excelize.Style{
		Font:      &excelize.Font{Color: "006100", Size: 9},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#C6EFCE"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	yellowStyle := xMustStyle(f, &excelize.Style{
		Font:      &excelize.Font{Color: "9C5700", Size: 9},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFEB9C"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	// --- Build lookup structures ---
	matrix := buildMatrix(days)
	articles := orderedArticles(days)
	metaMap := buildMetaMap(days)

	// --- Column widths ---
	f.SetColWidth(sheet, "A", "A", 16) // Артикул
	f.SetColWidth(sheet, "B", "B", 30) // Название
	f.SetColWidth(sheet, "C", "C", 20) // Категория 1С
	for i := range dates {
		colName, _ := excelize.ColumnNumberToName(colDateBase + i)
		f.SetColWidth(sheet, colName, colName, 14)
	}

	// --- Header row ---
	row := 1
	setHeader(f, sheet, row, colArticle, "Артикул", headerStyle)
	setHeader(f, sheet, row, colTitle, "Название", headerStyle)
	setHeader(f, sheet, row, colCategory, "Категория 1С", headerStyle)
	for i, d := range dates {
		setHeader(f, sheet, row, colDateBase+i, formatDateShort(d), headerStyle)
	}

	// --- Data rows ---
	row = 2
	for _, art := range articles {
		meta := metaMap[art]

		// Col A: vendor_code
		xSet(f, sheet, row, colArticle, art)
		setStyle(f, sheet, row, colArticle, articleStyle)

		// Col B: title
		xSet(f, sheet, row, colTitle, meta.title)
		setStyle(f, sheet, row, colTitle, textStyle)

		// Col C: category 1C
		xSet(f, sheet, row, colCategory, meta.category1C)
		setStyle(f, sheet, row, colCategory, textStyle)

		// Col D+: dates
		dateRow := matrix[art]
		for i, d := range dates {
			col := colDateBase + i
			if p, ok := dateRow[d]; ok {
				xSet(f, sheet, row, col, formatCell(p))
				if p.TotalSpend > 0 {
					setStyle(f, sheet, row, col, greenStyle)
				} else {
					setStyle(f, sheet, row, col, yellowStyle)
				}
			}
		}
		row++
	}

	// --- Freeze panes at D2 (article + title + category + header row always visible) ---
	if err := f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      3, // freeze A-C
		YSplit:      1, // freeze header
		TopLeftCell: "D2",
		ActivePane:  "bottomRight",
	}); err != nil {
		return fmt.Errorf("set freeze panes: %w", err)
	}

	return f.SaveAs(xlsxPath)
}

// --- excelize helpers ---

func setHeader(f *excelize.File, sheet string, row, col int, value string, style int) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
	f.SetCellStyle(sheet, cell, cell, style)
}

func xSet(f *excelize.File, sheet string, row, col int, value any) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}

func setStyle(f *excelize.File, sheet string, row, col, styleID int) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellStyle(sheet, cell, cell, styleID)
}

// xMustStyle creates a style and panics on error (excelize style creation rarely fails).
func xMustStyle(f *excelize.File, style *excelize.Style) int {
	id, err := f.NewStyle(style)
	if err != nil {
		panic(fmt.Sprintf("excelize style creation failed: %v", err))
	}
	return id
}
