// report.go — экспорт отчёта по остаткам WB в XLSX (excelize/v2).
//
// 5 листов:
//  1. «Сводка»         — итоги по компании: сезон × 3 типа остатков + строка ИТОГО.
//  2. «Склад × Сезон»  — главный рабочий лист: регион/склад/сезон + 3 типа, autofilter + freeze.
//  3. «По складам»      — склады с subtotal по регионам и строкой ИТОГО.
//  4. «Матрица»         — pivot: строки = склады, колонки сгруппированы по сезонам (по 3 типа).
//  5. «Детали»          — drill-down: артикул/бренд/предмет × склад × сезон + 3 типа.
//
// Стили повторяют collection-readiness/report.go: синяя шапка #4472C4, freeze panes,
// autofilter. Числа пишутся как int — чтобы суммировались формулами в Excel.
package main

import (
	"fmt"
	"sort"

	"github.com/xuri/excelize/v2"
)

// seasonOrder — бизнес-порядок сезонов (не алфавитный). Хронология коллекций одежды:
// зима → весна → лето → «жаркое лето» → осень → школа → новый год → без сезона.
// Сезоны не из списка сортируются в хвост алфавитно (на случай новых значений в 1С).
var seasonOrder = map[string]int{
	"Зима":        1,
	"Весна":       2,
	"Лето":        3,
	"Жаркое лето": 4,
	"Осень":       5,
	"Школа":       6,
	"Новый год":   7,
	noSeason:      99,
}

// seasonRank — ранг сезона для сортировки (хвост = 1000 + алфавитный индекс).
func seasonRank(s string) int {
	if r, ok := seasonOrder[s]; ok {
		return r
	}
	return 1000
}

// sortSeasonsInPlace сортирует слайс сезонов в бизнес-порядке.
func sortSeasonsInPlace(s []string) {
	sort.SliceStable(s, func(i, j int) bool {
		ri, rj := seasonRank(s[i]), seasonRank(s[j])
		if ri != rj {
			return ri < rj
		}
		return s[i] < s[j] // детерминизм внутри неизвестных значений
	})
}

// allSeasons возвращает уникальный отсортированный список сезонов из агрегата.
func allSeasons(rows []AggRow) []string {
	seen := make(map[string]struct{}, 16)
	out := make([]string, 0, 16)
	for _, r := range rows {
		if _, ok := seen[r.Season]; !ok {
			seen[r.Season] = struct{}{}
			out = append(out, r.Season)
		}
	}
	sortSeasonsInPlace(out)
	return out
}

// seasonTotals — итоги по сезону по всей компании.
type seasonTotals struct {
	OnStock, InWayToClient, InWayFromClient, Total int64
}

// totalsBySeason агрегирует строки по сезону (сумма по всем складам).
func totalsBySeason(rows []AggRow) map[string]seasonTotals {
	m := make(map[string]seasonTotals, 16)
	for _, r := range rows {
		t := m[r.Season]
		t.OnStock += r.OnStock
		t.InWayToClient += r.InWayToClient
		t.InWayFromClient += r.InWayFromClient
		t.Total += r.Total()
		m[r.Season] = t
	}
	return m
}

// exportXLSX строит xlsx-отчёт и сохраняет по path. date — дата среза (в шапку).
func exportXLSX(agg []AggRow, details []DetailRow, date, path string) error {
	f := excelize.NewFile()

	// ── Общие стили ──
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	boldStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	subtotalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Italic: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDEBF7"}},
	})

	// Лист 1: Сводка (переименовываем дефолтный Sheet1).
	summary := "Сводка"
	f.SetSheetName("Sheet1", summary)
	writeSummary(f, summary, agg, date, headerStyle, titleStyle, boldStyle)

	// Лист 2: Склад × Сезон (главный).
	main := "Склад × Сезон"
	f.NewSheet(main)
	writeMain(f, main, agg, headerStyle)

	// Лист 3: По складам.
	byWH := "По складам"
	f.NewSheet(byWH)
	writeByWarehouse(f, byWH, agg, headerStyle, subtotalStyle)

	// Лист 4: Матрица.
	matrix := "Матрица"
	f.NewSheet(matrix)
	writeMatrix(f, matrix, agg, headerStyle)

	// Лист 5: Детали.
	det := "Детали"
	f.NewSheet(det)
	writeDetails(f, det, details, headerStyle)

	// Активный лист при открытии — главный.
	idx, _ := f.GetSheetIndex(main)
	f.SetActiveSheet(idx)
	return f.SaveAs(path)
}

// ── Лист 1: «Сводка» ──────────────────────────────────────

func writeSummary(f *excelize.File, sheet string, rows []AggRow, date string,
	headerStyle, titleStyle, boldStyle int) {

	f.SetCellValue(sheet, "A1", "ОСТАТКИ WB В РАЗРЕЗЕ СЕЗОНОВ")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)
	f.SetCellValue(sheet, "A2", fmt.Sprintf("Дата среза: %s   |   Складов: %d   |   Источник: stocks_daily_warehouses → onec_goods.season", date, countWarehouses(rows)))

	headers := []string{"Сезон", "На складе", "В пути к клиенту", "В пути от клиента", "Всего"}
	row := 4
	for i, h := range headers {
		xSet(f, sheet, row, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, row), cellName(i+1, row), headerStyle)
	}

	totals := totalsBySeason(rows)
	seasons := allSeasons(rows)
	var grand seasonTotals
	row++
	for _, s := range seasons {
		t := totals[s]
		xSet(f, sheet, row, 1, s)
		xSet(f, sheet, row, 2, t.OnStock)
		xSet(f, sheet, row, 3, t.InWayToClient)
		xSet(f, sheet, row, 4, t.InWayFromClient)
		xSet(f, sheet, row, 5, t.Total)
		grand.OnStock += t.OnStock
		grand.InWayToClient += t.InWayToClient
		grand.InWayFromClient += t.InWayFromClient
		grand.Total += t.Total
		row++
	}
	// Строка ИТОГО.
	xSet(f, sheet, row, 1, "ИТОГО")
	xSet(f, sheet, row, 2, grand.OnStock)
	xSet(f, sheet, row, 3, grand.InWayToClient)
	xSet(f, sheet, row, 4, grand.InWayFromClient)
	xSet(f, sheet, row, 5, grand.Total)
	for i := 1; i <= 5; i++ {
		f.SetCellStyle(sheet, cellName(i, row), cellName(i, row), boldStyle)
	}

	f.SetColWidth(sheet, "A", "A", 18)
	for _, c := range []string{"B", "C", "D", "E"} {
		f.SetColWidth(sheet, c, c, 18)
	}
}

// ── Лист 2: «Склад × Сезон» (главный, с autofilter + freeze) ──

func writeMain(f *excelize.File, sheet string, rows []AggRow, headerStyle int) {
	headers := []string{"Регион", "Склад", "Сезон", "На складе", "В пути к клиенту", "В пути от клиента", "Всего"}
	for i, h := range headers {
		xSet(f, sheet, 1, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, 1), cellName(i+1, 1), headerStyle)
	}

	// Сортировка в Go: регион → склад → сезон (бизнес-порядок). SQL уже отдаёт
	// region/warehouse по алфавиту, но порядок сезонов нужно выставить здесь.
	sorted := make([]AggRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].RegionName != sorted[j].RegionName {
			return sorted[i].RegionName < sorted[j].RegionName
		}
		if sorted[i].WarehouseName != sorted[j].WarehouseName {
			return sorted[i].WarehouseName < sorted[j].WarehouseName
		}
		return seasonRank(sorted[i].Season) < seasonRank(sorted[j].Season)
	})

	for i, r := range sorted {
		row := i + 2
		xSet(f, sheet, row, 1, r.RegionName)
		xSet(f, sheet, row, 2, r.WarehouseName)
		xSet(f, sheet, row, 3, r.Season)
		xSet(f, sheet, row, 4, r.OnStock)
		xSet(f, sheet, row, 5, r.InWayToClient)
		xSet(f, sheet, row, 6, r.InWayFromClient)
		xSet(f, sheet, row, 7, r.Total())
	}

	widths := []float64{28, 34, 16, 12, 18, 20, 12}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, w)
	}

	// Freeze panes: фиксируем шапку (строка 1) + первые 3 текстовые колонки.
	f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, XSplit: 3, YSplit: 1, TopLeftCell: "D2", ActivePane: "bottomRight",
	})

	// Autofilter на шапке.
	if len(sorted) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(len(headers))
		lastRow := len(sorted) + 1
		f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, lastRow), nil)
	}
}

// ── Лист 3: «По складам» (subtotal по регионам + ИТОГО) ──

func writeByWarehouse(f *excelize.File, sheet string, rows []AggRow, headerStyle, subtotalStyle int) {
	f.SetCellValue(sheet, "A1", "ОСТАТКИ ПО СКЛАДАМ (суммарно по сезонам)")

	headers := []string{"Регион", "Склад", "На складе", "В пути к клиенту", "В пути от клиента", "Всего"}
	headerRow := 3
	for i, h := range headers {
		xSet(f, sheet, headerRow, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, headerRow), cellName(i+1, headerRow), headerStyle)
	}

	// Группируем по региону → склад, суммируя все сезоны внутри склада.
	type key struct{ region, wh string }
	type acc struct{ on, toC, fromC int64 }
	// Сохраняем порядок первого появления для стабильного вывода.
	order := make([]key, 0, 128)
	m := make(map[key]*acc, 128)
	for _, r := range rows {
		k := key{r.RegionName, r.WarehouseName}
		a, ok := m[k]
		if !ok {
			a = &acc{}
			m[k] = a
			order = append(order, k)
		}
		a.on += r.OnStock
		a.toC += r.InWayToClient
		a.fromC += r.InWayFromClient
	}

	// Сортируем по региону → склад.
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].region != order[j].region {
			return order[i].region < order[j].region
		}
		return order[i].wh < order[j].wh
	})

	row := headerRow + 1
	var prevRegion string
	var regionSub acc
	var grand acc
	flushRegion := func() {
		if prevRegion == "" {
			return
		}
		xSet(f, sheet, row, 1, "Итого по «"+prevRegion+"»")
		xSet(f, sheet, row, 2, "")
		xSet(f, sheet, row, 3, regionSub.on)
		xSet(f, sheet, row, 4, regionSub.toC)
		xSet(f, sheet, row, 5, regionSub.fromC)
		xSet(f, sheet, row, 6, regionSub.on+regionSub.toC+regionSub.fromC)
		for i := 1; i <= 6; i++ {
			f.SetCellStyle(sheet, cellName(i, row), cellName(i, row), subtotalStyle)
		}
		row++
		regionSub = acc{}
	}

	for _, k := range order {
		if k.region != prevRegion {
			flushRegion()
			prevRegion = k.region
		}
		a := m[k]
		xSet(f, sheet, row, 1, k.region)
		xSet(f, sheet, row, 2, k.wh)
		xSet(f, sheet, row, 3, a.on)
		xSet(f, sheet, row, 4, a.toC)
		xSet(f, sheet, row, 5, a.fromC)
		xSet(f, sheet, row, 6, a.on+a.toC+a.fromC)
		regionSub.on += a.on
		regionSub.toC += a.toC
		regionSub.fromC += a.fromC
		grand.on += a.on
		grand.toC += a.toC
		grand.fromC += a.fromC
		row++
	}
	flushRegion()

	// Строка ИТОГО по всем регионам.
	xSet(f, sheet, row, 1, "ИТОГО")
	xSet(f, sheet, row, 2, "")
	xSet(f, sheet, row, 3, grand.on)
	xSet(f, sheet, row, 4, grand.toC)
	xSet(f, sheet, row, 5, grand.fromC)
	xSet(f, sheet, row, 6, grand.on+grand.toC+grand.fromC)
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	for i := 1; i <= 6; i++ {
		f.SetCellStyle(sheet, cellName(i, row), cellName(i, row), bold)
	}

	widths := []float64{30, 36, 14, 20, 22, 14}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, w)
	}
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: headerRow, TopLeftCell: "A4", ActivePane: "bottomLeft"})
}

// ── Лист 4: «Матрица» (pivot: склад × сезон, 3 типа) ──

func writeMatrix(f *excelize.File, sheet string, rows []AggRow, headerStyle int) {
	f.SetCellValue(sheet, "A1", "МАТРИЦА: склад × сезон (на складе / в пути к клиенту / в пути от клиента)")
	seasons := allSeasons(rows)

	// Шапка: две строки. R1 = сезон (объединён на 3 колонки), R2 = тип остатка.
	// Фиксированные колонки: A=Регион, B=Склад. Далее по 3 колонки на сезон.
	headerRow1 := 3
	headerRow2 := 4
	xSet(f, sheet, headerRow1, 1, "Регион")
	xSet(f, sheet, headerRow1, 2, "Склад")
	f.MergeCell(sheet, cellName(1, headerRow1), cellName(1, headerRow2))
	f.MergeCell(sheet, cellName(2, headerRow1), cellName(2, headerRow2))
	f.SetCellStyle(sheet, cellName(1, headerRow1), cellName(1, headerRow2), headerStyle)
	f.SetCellStyle(sheet, cellName(2, headerRow1), cellName(2, headerRow2), headerStyle)

	subHeaders := []string{"на складе", "к клиенту", "от клиента"}
	col := 3
	for _, s := range seasons {
		startCol := col
		endCol := col + 2
		xSet(f, sheet, headerRow1, startCol, s)
		f.MergeCell(sheet, cellName(startCol, headerRow1), cellName(endCol, headerRow1))
		f.SetCellStyle(sheet, cellName(startCol, headerRow1), cellName(endCol, headerRow1), headerStyle)
		for i, sh := range subHeaders {
			xSet(f, sheet, headerRow2, col+i, sh)
			f.SetCellStyle(sheet, cellName(col+i, headerRow2), cellName(col+i, headerRow2), headerStyle)
		}
		col = endCol + 1
	}

	// Группировка: регион → склад → сезон → (3 типа).
	type cellKey struct{ region, wh string }
	type seasonVals struct{ on, toC, fromC int64 }
	warehouseOrder := make([]cellKey, 0, 128)
	data := make(map[cellKey]map[string]seasonVals, 128)
	for _, r := range rows {
		k := cellKey{r.RegionName, r.WarehouseName}
		if _, ok := data[k]; !ok {
			data[k] = make(map[string]seasonVals, len(seasons))
			warehouseOrder = append(warehouseOrder, k)
		}
		v := data[k][r.Season]
		v.on += r.OnStock
		v.toC += r.InWayToClient
		v.fromC += r.InWayFromClient
		data[k][r.Season] = v
	}

	// Сортировка складов: регион → склад.
	sort.SliceStable(warehouseOrder, func(i, j int) bool {
		if warehouseOrder[i].region != warehouseOrder[j].region {
			return warehouseOrder[i].region < warehouseOrder[j].region
		}
		return warehouseOrder[i].wh < warehouseOrder[j].wh
	})

	row := headerRow2 + 1
	for _, k := range warehouseOrder {
		xSet(f, sheet, row, 1, k.region)
		xSet(f, sheet, row, 2, k.wh)
		col := 3
		for _, s := range seasons {
			v := data[k][s]
			xSet(f, sheet, row, col, v.on)
			xSet(f, sheet, row, col+1, v.toC)
			xSet(f, sheet, row, col+2, v.fromC)
			col += 3
		}
		row++
	}

	// Ширины.
	f.SetColWidth(sheet, "A", "A", 26)
	f.SetColWidth(sheet, "B", "B", 32)
	nSeasonCols := len(seasons) * 3
	for i := 0; i < nSeasonCols; i++ {
		colN, _ := excelize.ColumnNumberToName(i + 3)
		f.SetColWidth(sheet, colN, colN, 11)
	}
	// Freeze: фиксируем регион + склад и две строки шапки.
	f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, XSplit: 2, YSplit: headerRow2, TopLeftCell: "C5", ActivePane: "bottomRight",
	})
}

// ── Лист 5: «Детали» (drill-down по артикулам) ──

func writeDetails(f *excelize.File, sheet string, details []DetailRow, headerStyle int) {
	headers := []string{"Сезон", "Артикул", "Бренд", "Предмет", "Регион", "Склад",
		"На складе", "В пути к клиенту", "В пути от клиента", "Всего"}
	for i, h := range headers {
		xSet(f, sheet, 1, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, 1), cellName(i+1, 1), headerStyle)
	}

	for i, r := range details {
		row := i + 2
		xSet(f, sheet, row, 1, r.Season)
		xSet(f, sheet, row, 2, r.VendorCode)
		// nmID показываем в скобках рядом с артикулом только при отличии — нет, отдельной
		// колонки нет по решению «только штуки»; nmID доступен в cards при необходимости.
		xSet(f, sheet, row, 3, r.Brand)
		xSet(f, sheet, row, 4, r.SubjectName)
		xSet(f, sheet, row, 5, r.RegionName)
		xSet(f, sheet, row, 6, r.WarehouseName)
		xSet(f, sheet, row, 7, r.OnStock)
		xSet(f, sheet, row, 8, r.InWayToClient)
		xSet(f, sheet, row, 9, r.InWayFromClient)
		xSet(f, sheet, row, 10, r.OnStock+r.InWayToClient+r.InWayFromClient)
	}

	widths := []float64{14, 14, 18, 30, 26, 32, 12, 18, 20, 12}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, w)
	}
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	if len(details) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(len(headers))
		f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, len(details)+1), nil)
	}
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

// countWarehouses — число уникальных складов в агрегате.
func countWarehouses(rows []AggRow) int {
	seen := make(map[int64]struct{}, 128)
	for _, r := range rows {
		seen[r.WarehouseID] = struct{}{}
	}
	return len(seen)
}
