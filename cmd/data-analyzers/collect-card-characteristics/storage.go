package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// buildXLSX creates an in-memory XLSX for the given subjects.
//
// Two-pass approach:
//  1. Create per-subject sheets (collect sheet names)
//  2. Fill summary sheet with hyperlinks to those sheets
//
// Returns *excelize.File — caller is responsible for saving.
func buildXLSX(data []SubjectData) *excelize.File {
	f := excelize.NewFile()

	// --- Styles ---
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	wrapStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	linkStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "1265BE", Underline: "single"},
	})

	// --- Summary sheet (created first as "Sheet1", filled after subject sheets) ---
	summarySheet := "Сводка"
	f.SetSheetName("Sheet1", summarySheet)

	summaryHeaders := []string{"Subject ID", "Предмет", "Карточек", "Характеристик", "Всего значений"}
	for i, h := range summaryHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summarySheet, cell, h)
		f.SetCellStyle(summarySheet, cell, cell, headerStyle)
	}
	f.SetColWidth(summarySheet, "A", "A", 12)
	f.SetColWidth(summarySheet, "B", "B", 30)
	f.SetColWidth(summarySheet, "C", "E", 16)

	// --- Pass 1: per-subject sheets (collect names for hyperlinks) ---
	usedNames := map[string]int{summarySheet: 1}
	sheetNameMap := make(map[int]string, len(data)) // data index → sheet name

	for i, sd := range data {
		sheetName := makeSheetName(sd.SubjectName, usedNames)
		f.NewSheet(sheetName)
		sheetNameMap[i] = sheetName

		// Header row
		charHeaders := []string{"Характеристика", "Char ID", "Все значения", "Уникальных", "Карточек"}
		for j, h := range charHeaders {
			cell, _ := excelize.CoordinatesToCellName(j+1, 1)
			f.SetCellValue(sheetName, cell, h)
			f.SetCellStyle(sheetName, cell, cell, headerStyle)
		}

		// Column widths
		f.SetColWidth(sheetName, "A", "A", 30) // characteristic name
		f.SetColWidth(sheetName, "B", "B", 12) // char ID
		f.SetColWidth(sheetName, "C", "C", 80) // values (wide, wrapped)
		f.SetColWidth(sheetName, "D", "D", 14) // unique count
		f.SetColWidth(sheetName, "E", "E", 14) // card count

		// Data rows (already sorted by CardCount DESC from query)
		for j, ch := range sd.Characteristics {
			row := j + 2
			xSet(f, sheetName, row, 1, ch.Name)
			xSet(f, sheetName, row, 2, ch.CharID)

			valuesCell, _ := excelize.CoordinatesToCellName(3, row)
			f.SetCellValue(sheetName, valuesCell, strings.Join(ch.Values, ", "))
			f.SetCellStyle(sheetName, valuesCell, valuesCell, wrapStyle)

			xSet(f, sheetName, row, 4, len(ch.Values))
			xSet(f, sheetName, row, 5, ch.CardCount)
		}
	}

	// --- Pass 2: summary data rows with hyperlinks ---
	for i, sd := range data {
		row := i + 2
		tv := totalValues(sd)
		xSet(f, summarySheet, row, 1, sd.SubjectID)

		// Subject name with hyperlink to its sheet
		subjectCell, _ := excelize.CoordinatesToCellName(2, row)
		f.SetCellValue(summarySheet, subjectCell, sd.SubjectName)
		linkTarget := "'" + sheetNameMap[i] + "'!A1"
		_ = f.SetCellHyperLink(summarySheet, subjectCell, linkTarget, "Location")
		f.SetCellStyle(summarySheet, subjectCell, subjectCell, linkStyle)

		xSet(f, summarySheet, row, 3, sd.CardCount)
		xSet(f, summarySheet, row, 4, len(sd.Characteristics))
		xSet(f, summarySheet, row, 5, tv)
	}

	return f
}

// ExportXLSX creates a single Excel file with one sheet per WB subject.
// Delegates to buildXLSX for file construction.
func ExportXLSX(data []SubjectData, outputPath string) error {
	return buildXLSX(data).SaveAs(outputPath)
}

// ExportXLSXBatch splits subjects into chunks and exports each to a separate XLSX file.
// File naming: if outputPath is "report.xlsx", files become "report_part_01.xlsx", etc.
// Returns the list of created file paths.
func ExportXLSXBatch(data []SubjectData, outputPath string, itemsPerFile int) ([]string, error) {
	if itemsPerFile <= 0 {
		itemsPerFile = 30
	}

	ext := filepath.Ext(outputPath)
	base := strings.TrimSuffix(outputPath, ext)

	created := make([]string, 0, (len(data)+itemsPerFile-1)/itemsPerFile)

	for i := 0; i < len(data); i += itemsPerFile {
		end := min(i+itemsPerFile, len(data))
		chunk := data[i:end]

		fileIdx := i/itemsPerFile + 1
		partPath := fmt.Sprintf("%s_part_%02d%s", base, fileIdx, ext)

		if err := buildXLSX(chunk).SaveAs(partPath); err != nil {
			return created, fmt.Errorf("save batch file %s: %w", partPath, err)
		}
		created = append(created, partPath)
	}

	return created, nil
}

// makeSheetName truncates subject name to fit Excel's 31-char sheet name limit.
// Uses rune-aware truncation for correct Russian text handling.
// Deduplicates via suffix " (2)", " (3)" etc. if truncated names collide.
func makeSheetName(name string, usedNames map[string]int) string {
	runes := []rune(name)
	if len(runes) <= 31 {
		name = string(runes)
	} else {
		name = string(runes[:28]) + "..."
	}

	// Deduplicate: if this name is already used, append " (N)"
	if _, exists := usedNames[name]; exists {
		base := name
		// Make room for " (N)" suffix within 31 chars
		for n := 2; ; n++ {
			suffix := fmt.Sprintf(" (%d)", n)
			maxBaseLen := 31 - len(suffix)
			truncated := base
			if len([]rune(truncated)) > maxBaseLen {
				truncated = string([]rune(truncated)[:maxBaseLen])
			}
			candidate := truncated + suffix
			if _, exists := usedNames[candidate]; !exists {
				name = candidate
				break
			}
		}
	}

	usedNames[name]++
	return name
}

// xSet writes a value to a cell.
func xSet(f *excelize.File, sheet string, row, col int, value any) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}

// totalValues counts all unique values across all characteristics of a subject.
func totalValues(sd SubjectData) int {
	total := 0
	for _, ch := range sd.Characteristics {
		total += len(ch.Values)
	}
	return total
}
