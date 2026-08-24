// report.go — экспорт остатков FBO (склады WB) в XLSX (excelize/v2).
//
// Структура книги:
//
//  1. «Сводка» — топ-таблица по складам: склад / регион / строки / Σ свободный /
//     Σ в пути к клиенту / Σ в пути от клиента / Итого. Сортировка по убыванию
//     свободного остатка. Строка ИТОГО внизу.
//  2..N — по одному листу на каждый склад: Артикул продавца / Бренд / Предмет /
//     Штрихкод / Размер (tech_size) / Размер WB / Свободный / В пути к клиенту /
//     В пути от клиента / Итого. AutoFilter + freeze panes.
//
// Стили повторяют stock-season-report/report.go и collection-readiness/report.go:
// синяя шапка #4472C4 bold white, freeze panes на строке заголовка, autofilter.
// Числа пишутся как int — чтобы суммировались формулами в Excel.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// WarehouseGroup — все строки одного склада (для листа склада).
type WarehouseGroup struct {
	Name   string
	Region string
	Rows   []StockRow
	// Итоги по складу (считаются один раз при группировке).
	OnStock, InWayToClient, InWayFromClient, Total int64
}

// exportXLSX строит xlsx-отчёт и сохраняет по path. date — дата среза (в шапку).
func exportXLSX(groups []WarehouseGroup, date, path string) error {
	f := excelize.NewFile()

	// ── Общие стили (те же, что в stock-season-report/report.go) ──
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	boldStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	subtotalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Italic: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDEBF7"}},
	})

	// Лист 1: «Сводка» (переименовываем дефолтный Sheet1).
	summary := "Сводка"
	f.SetSheetName("Sheet1", summary)
	writeSummary(f, summary, groups, date, headerStyle, titleStyle, boldStyle, subtotalStyle)

	// Листы 2..N: по одному на каждый склад (в порядке убывания свободного остатка —
	// тот же порядок, что в Сводке, чтобы листы шли от крупных к мелким).
	used := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		sheet := uniqueSheetName(f, g.Name, used)
		f.NewSheet(sheet)
		writeWarehouseSheet(f, sheet, g, date, headerStyle, titleStyle, subtotalStyle)
	}

	// Активный лист при открытии — Сводка (нулевой индекс).
	f.SetActiveSheet(0)
	return f.SaveAs(path)
}

// ── Лист «Сводка» ─────────────────────────────────────────

func writeSummary(f *excelize.File, sheet string, groups []WarehouseGroup, date string,
	headerStyle, titleStyle, boldStyle, subtotalStyle int) {

	// Заголовок книги.
	f.SetCellValue(sheet, "A1", fmt.Sprintf("ОСТАТКИ НА СКЛАДАХ WB (FBO) — СРЕЗ %s", date))
	f.SetCellStyle(sheet, "A1", "G1", titleStyle)
	f.MergeCell(sheet, "A1", "G1")

	// Шапка таблицы.
	row := 3
	headers := []string{"Склад", "Регион", "Строк", "Свободный", "В пути к клиенту",
		"В пути от клиента", "Итого"}
	for i, h := range headers {
		xSet(f, sheet, row, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, row), cellName(i+1, row), headerStyle)
	}

	// Данные: уже отсортированы по убыванию OnStock при группировке.
	for _, g := range groups {
		row++
		xSet(f, sheet, row, 1, g.Name)
		xSet(f, sheet, row, 2, g.Region)
		xSet(f, sheet, row, 3, len(g.Rows))
		xSet(f, sheet, row, 4, g.OnStock)
		xSet(f, sheet, row, 5, g.InWayToClient)
		xSet(f, sheet, row, 6, g.InWayFromClient)
		xSet(f, sheet, row, 7, g.Total)
	}

	// Строка ИТОГО.
	row++
	var grandOn, grandTo, grandFrom, grandTotal int64
	var grandRows int
	for _, g := range groups {
		grandOn += g.OnStock
		grandTo += g.InWayToClient
		grandFrom += g.InWayFromClient
		grandTotal += g.Total
		grandRows += len(g.Rows)
	}
	xSet(f, sheet, row, 1, "ИТОГО")
	xSet(f, sheet, row, 2, "")
	xSet(f, sheet, row, 3, grandRows)
	xSet(f, sheet, row, 4, grandOn)
	xSet(f, sheet, row, 5, grandTo)
	xSet(f, sheet, row, 6, grandFrom)
	xSet(f, sheet, row, 7, grandTotal)
	f.SetCellStyle(sheet, "A3", fmt.Sprintf("G%d", row), subtotalStyle)
	// subtotalStyle задаёт заливку, но перетирает выравнивание шапки — вернём шапке её стиль.
	for i := range headers {
		f.SetCellStyle(sheet, cellName(i+1, 3), cellName(i+1, 3), headerStyle)
	}

	// Ширина колонок и freeze.
	widths := []float64{34, 20, 8, 14, 20, 22, 14}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, w)
	}
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 3, TopLeftCell: "A4", ActivePane: "bottomLeft"})

	_ = boldStyle // зарезервирован для будущих пояснений
}

// ── Лист склада ───────────────────────────────────────────

func writeWarehouseSheet(f *excelize.File, sheet string, g WarehouseGroup, date string,
	headerStyle, titleStyle, subtotalStyle int) {

	// Заголовок: имя склада + регион + дата.
	f.SetCellValue(sheet, "A1", fmt.Sprintf("%s (%s) — срез %s", g.Name, g.Region, date))
	f.SetCellStyle(sheet, "A1", "J1", titleStyle)
	f.MergeCell(sheet, "A1", "J1")

	// Шапка таблицы (строка 3 — под заголовком).
	row := 3
	headers := []string{"Артикул продавца", "Бренд", "Предмет", "Штрихкод",
		"Размер", "Размер WB", "Свободный", "В пути к клиенту", "В пути от клиента", "Итого"}
	for i, h := range headers {
		xSet(f, sheet, row, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, row), cellName(i+1, row), headerStyle)
	}

	// Данные: одна строка на SKU (chrt_id). Строки уже отсортированы в SQL по vendor_code, chrt_id.
	for _, r := range g.Rows {
		row++
		f.SetSheetRow(sheet, cellName(1, row), &[]interface{}{
			r.VendorCode, r.Brand, r.SubjectName, r.Barcodes(),
			r.TechSize, r.WBSize,
			r.Quantity, r.InWayToClient, r.InWayFromClient, r.Total(),
		})
	}

	// Строка ИТОГО по складу.
	row++
	xSet(f, sheet, row, 1, "ИТОГО")
	xSet(f, sheet, row, 7, g.OnStock)
	xSet(f, sheet, row, 8, g.InWayToClient)
	xSet(f, sheet, row, 9, g.InWayFromClient)
	xSet(f, sheet, row, 10, g.Total)
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("J%d", row), subtotalStyle)

	// Ширина колонок.
	widths := []float64{18, 18, 28, 18, 12, 14, 12, 16, 20, 12}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, w)
	}

	// Freeze panes: закрепляем шапку + первые колонки (артикул/бренд).
	f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, XSplit: 3, YSplit: 3,
		TopLeftCell: "D4", ActivePane: "bottomRight",
	})

	// AutoFilter по таблице данных (без строки ИТОГО).
	if len(g.Rows) > 0 {
		f.AutoFilter(sheet, fmt.Sprintf("A3:J%d", 3+len(g.Rows)), nil)
	}
}

// ── Группировка строк по складам ──────────────────────────

// groupByWarehouse группирует строки по warehouse_name и считает итоги по каждому складу.
// Возвращает слайс, отсортированный по убыванию свободного остатка — тот же порядок
// используется и для листов в книге (от крупных складов к мелким).
func groupByWarehouse(rows []StockRow) []WarehouseGroup {
	byName := make(map[string]*WarehouseGroup, 64)
	order := make([]string, 0, 64)
	for i := range rows {
		r := rows[i]
		g, ok := byName[r.WarehouseName]
		if !ok {
			g = &WarehouseGroup{Name: r.WarehouseName, Region: r.RegionName}
			byName[r.WarehouseName] = g
			order = append(order, r.WarehouseName)
		}
		g.Rows = append(g.Rows, r)
		g.OnStock += r.Quantity
		g.InWayToClient += r.InWayToClient
		g.InWayFromClient += r.InWayFromClient
		g.Total += r.Total()
	}

	out := make([]WarehouseGroup, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	// Сортировка: по убыванию свободного остатка, при равенстве — по имени (детерминизм).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OnStock != out[j].OnStock {
			return out[i].OnStock > out[j].OnStock
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ── Хелперы ───────────────────────────────────────────────

// xSet — установка значения ячейки по (строка, колонка).
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}

// cellName — имя ячейки по (колонка, строка).
func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// sheetNameMaxLen — лимит Excel на длину имени листа (31 символ).
const sheetNameMaxLen = 31

// sheetNameForbidden — символы, запрещённые в имени листа Excel.
const sheetNameForbidden = "[]:*?/\\"

// sanitizeSheetName приводит имя склада к допустимому имени листа Excel:
// обрезает до 28 символов (оставляем запас для суффикса " #N" при коллизиях) и
// заменяет запрещённые символы на '_'.
func sanitizeSheetName(name string) string {
	if name == "" {
		return "Прочее"
	}
	s := strings.Map(func(r rune) rune {
		if strings.ContainsRune(sheetNameForbidden, r) {
			return '_'
		}
		return r
	}, name)
	// Обрезаем с запасом под возможный суффикс коллизии " #N".
	const maxWithSuffix = 28
	if len(s) > maxWithSuffix {
		s = s[:maxWithSuffix]
	}
	return s
}

// uniqueSheetName возвращает уникальное в пределах книги имя листа для склада.
// При коллизии (после обрезки два склада дали одинаковое имя) добавляет суффикс " #2", " #3"…
func uniqueSheetName(f *excelize.File, name string, used map[string]struct{}) string {
	base := sanitizeSheetName(name)
	candidate := base
	for n := 2; ; n++ {
		if _, taken := used[candidate]; !taken {
			used[candidate] = struct{}{}
			return candidate
		}
		// Перегенерируем базу с суффиксом, гарантированно вписываясь в лимит 31.
		suffix := fmt.Sprintf(" #%d", n)
		cut := sheetNameMaxLen - len(suffix)
		if len(base) < cut {
			cut = len(base)
		}
		candidate = base[:cut] + suffix
		// Если и этот занят — цикл повторится с n+1.
	}
}
