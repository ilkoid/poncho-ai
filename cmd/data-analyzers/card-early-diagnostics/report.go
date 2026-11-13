// report.go — экспорт вотчлиста в XLSX (excelize/v2), стилевая машина collection-readiness.
//
// Лист «Вотчлист»: только карточки по критериям гендира (нет заказов N полных дней).
// Подсветка: дней без заказов 3-4 янтарь / 5-6 оранж / 7+ красный; сработавшие триггеры
// (наименование, фото, размерный ряд, скрытость) — точечно. Лист «7+ дней» — кандидаты
// на эскалацию. Лист «Сводка» — raw counts. Лист «Легенда» — свотчи теми же стилями.
package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/xuri/excelize/v2"
)

// column описывает одну колонку отчёта.
type column struct {
	header string
	width  float64
	value  func(r *Row) interface{}
}

// reportColumns — порядок и содержание колонок: сначала идентификация (фильтры
// xlsx: категория 1С, предмет WB, артикул, nmID, наименование, сезон), затем
// параметры продаж, затем влияющие факторы. Порядок — в коде (смена = правка здесь).
var reportColumns = []column{
	{"Фото", 14.3, func(r *Row) interface{} { return "" }}, // миниатюра встраивается отдельно
	{"№", 5, func(r *Row) interface{} { return 0 }},        // заполняется номером строки
	{"Категория 1С", 22, func(r *Row) interface{} { return r.Category1C }},
	{"Предмет WB", 16, func(r *Row) interface{} { return r.SubjectWB }},
	{"Артикул", 12, func(r *Row) interface{} { return r.Article1C }},
	{"nmID", 14, func(r *Row) interface{} { return r.NmID }},
	{"Наименование WB", 30, func(r *Row) interface{} { return r.TitleWB }},
	{"Сезон 1С", 14, func(r *Row) interface{} { return r.Season1C }},
	{"Начало продаж", 13, func(r *Row) interface{} { return r.SalesStart }},
	{"Якорь", 15, func(r *Row) interface{} { return r.AnchorSourceRu() }},
	{"Дней без заказов", 10, func(r *Row) interface{} { return r.FullDays }},
	{"Сигнал", 20, func(r *Row) interface{} { return r.SignalRu() }},
	{"Последний заказ", 13, func(r *Row) interface{} { return r.LastOrder }},
	{"Заказов всего", 9, func(r *Row) interface{} { return r.OrdersTotal }},
	{"Открытия 7д", 9, func(r *Row) interface{} { return r.Opens7d }},
	{"Корзины 7д", 9, func(r *Row) interface{} { return r.Carts7d }},
	{"Скрыта", 8, func(r *Row) interface{} { return shadowCell(r) }},
	{"Рейтинг 0-10", 9, func(r *Row) interface{} { return ratingCell(r) }},
	{"Длина назв.", 8, func(r *Row) interface{} { return r.TitleLen }},
	{"Длина опис.", 8, func(r *Row) interface{} { return r.DescLen }},
	{"Наименование 1С", 30, func(r *Row) interface{} { return r.NameIM1C }},
	{"Фото шт.", 7, func(r *Row) interface{} { return r.PhotoCount }},
	{"Видео", 7, func(r *Row) interface{} { return videoCell(r) }},
	{"Характ. шт.", 8, func(r *Row) interface{} { return r.CharCount }},
	{"Остаток WB", 9, func(r *Row) interface{} { return int64Cell(r.WBStockTotal) }},
	{"Размеров WB", 9, func(r *Row) interface{} { return intCell(r.SizesWB) }},
	{"Размеров 1С", 9, func(r *Row) interface{} { return intCell(r.Sizes1C) }},
	{"Склад 1С", 12, func(r *Row) interface{} { return r.FboStorage }},
	{"Ряд WB/1С", 8, func(r *Row) interface{} { return ratioCell(r.SizesRatio) }},
	{"Доступность", 18, func(r *Row) interface{} { return availabilityRu(r.StockAvail) }},
	{"Кампаний", 8, func(r *Row) interface{} { return r.Campaigns }},
	{"Главная гипотеза", 44, func(r *Row) interface{} { return r.TopHypothesis }},
	{"Ссылка на фото", 10, func(r *Row) interface{} { return "" }}, // hyperlink ставится отдельно
}

func shadowCell(r *Row) string {
	if r.IsShadowed == nil {
		return "н/д"
	}
	if *r.IsShadowed {
		return "ДА"
	}
	return ""
}

func ratingCell(r *Row) interface{} {
	if r.CardRating == nil {
		return "н/д"
	}
	return *r.CardRating
}

func int64Cell(v *int64) interface{} {
	if v == nil {
		return "н/д"
	}
	return *v
}

func intCell(v *int) interface{} {
	if v == nil {
		return "н/д"
	}
	return *v
}

func ratioCell(v *float64) interface{} {
	if v == nil {
		return "н/д"
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// videoCell — наличие видео у карточки («—» = нет).
func videoCell(r *Row) string {
	if r.HasVideo {
		return "да"
	}
	return "—"
}

// availabilityRu — «Доступность товара» (оценка оборачиваемости WB, портал;
// русские ярлыки — сваггер 11-analytics.yaml availabilityFilters) человеческим
// языком для xlsx; в БД остаются сырые коды WB.
func availabilityRu(code string) string {
	switch code {
	case "nonLiquid":
		return "залежался (неликвид)"
	case "balanced":
		return "баланс (норма)"
	case "deficient":
		return "дефицит (мало товара)"
	case "nonActual":
		return "неактуальный (не сезон)"
	case "actual":
		return "актуальный (продаётся)"
	case "invalidData":
		return "не рассчитано"
	default:
		return code
	}
}

// reportStyles — стили листа. Единый источник цветов: те же ID красят свотчи
// «Легенды», поэтому легенда физически не может разойтись с подсветкой.
type reportStyles struct {
	header int
	// Ярусность дней без заказов (письмо гендира: повторная оценка на 5-й, эскалация на 7-й).
	days3 int // янтарь
	days5 int // оранж
	days7 int // красный bold
	bad   int // триггер-красный (точечный)
	warn  int // триггер-янтарный (точечный)
	gray  int // нет данных
	muted int // серая строка (pre_sales)
	link  int
}

func newReportStyles(f *excelize.File) *reportStyles {
	st := &reportStyles{}
	st.header, _ = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	st.days3, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFEB9C"}},
		Font: &excelize.Font{Color: "9C6500", Bold: true},
	})
	st.days5, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFD966"}},
		Font: &excelize.Font{Color: "7F6000", Bold: true},
	})
	st.days7, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006", Bold: true},
	})
	st.bad, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})
	st.warn, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFEB9C"}},
		Font: &excelize.Font{Color: "9C6500"},
	})
	st.gray, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
		Font: &excelize.Font{Color: "808080", Italic: true},
	})
	st.muted, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
		Font: &excelize.Font{Color: "595959"},
	})
	st.link, _ = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "0563C1", Underline: "single"},
	})
	return st
}

// exportXLSX строит xlsx-отчёт и сохраняет по path. rows должны быть отсортированы
// заранее (плавающие миниатюры не следуют за Sort в Excel — файл выходит пресортированным).
func exportXLSX(rows []Row, path string, cfg *Config, photos map[int64][]byte, embed bool, snapshotDate, revision string) error {
	f := excelize.NewFile()
	sheet := "Вотчлист"
	f.SetSheetName("Sheet1", sheet)

	st := newReportStyles(f)
	ncol := len(reportColumns)

	// ── Шапка и ширины ──
	for i, c := range reportColumns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, c.header)
		f.SetCellStyle(sheet, cell, cell, st.header)
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, c.width)
	}

	// ── Данные ──
	for i := range rows {
		r := &rows[i]
		row := i + 2
		for ci, c := range reportColumns {
			val := c.value(r)
			if c.header == "№" {
				val = i + 1
			}
			xSet(f, sheet, row, ci+1, val)
		}

		if embed {
			f.SetRowHeight(sheet, row, 56.4)
		}

		// Миниатюра в колонке «Фото» (клик → полноразмерное фото). Плавающее
		// изображение не следует за Sort (ограничение Excel/excelize, см. readiness).
		if embed {
			if photoBytes, ok := photos[r.NmID]; ok && len(photoBytes) > 0 {
				photoCell, _ := excelize.CoordinatesToCellName(colIndexByHeader("Фото"), row)
				if err := f.AddPictureFromBytes(sheet, photoCell, &excelize.Picture{
					Extension: ".jpg",
					File:      photoBytes,
					Format: &excelize.GraphicOptions{
						AltText:             fmt.Sprintf("nm_%d", r.NmID),
						AutoFit:             true,
						AutoFitIgnoreAspect: true,
						Hyperlink:           r.PhotoBig,
						HyperlinkType:       "External",
					},
				}); err != nil {
					fmt.Printf("WARN: embed photo nm_id=%d: %v\n", r.NmID, err)
				}
			}
		}

		// Ссылка на полноразмерное фото.
		if r.PhotoBig != "" {
			linkCell, _ := excelize.CoordinatesToCellName(colIndexByHeader("Ссылка на фото"), row)
			f.SetCellValue(sheet, linkCell, "фото")
			f.SetCellHyperLink(sheet, linkCell, r.PhotoBig, "External")
			f.SetCellStyle(sheet, linkCell, linkCell, st.link)
		}

		// pre_sales — серая строка целиком (info-категория, вне процесса 3/5/7).
		if r.Signal == SignalPreSales {
			start, _ := excelize.CoordinatesToCellName(1, row)
			end, _ := excelize.CoordinatesToCellName(ncol, row)
			f.SetCellStyle(sheet, start, end, st.muted)
		}

		// Подсветка: дни без заказов (3-4 / 5-6 / 7+).
		if r.FullDays >= 3 {
			dCell, _ := excelize.CoordinatesToCellName(colIndexByHeader("Дней без заказов"), row)
			style := st.days3
			if r.FullDays >= 7 {
				style = st.days7
			} else if r.FullDays >= 5 {
				style = st.days5
			}
			f.SetCellStyle(sheet, dCell, dCell, style)
		}

		// Триггеры факторов.
		highlightCell(f, sheet, row, "Наименование WB", TitleBad(r.TitleWB, r.SubjectWB, &cfg.Thresholds),
			TitleShort(r.TitleWB, &cfg.Thresholds), st)
		highlightCell(f, sheet, row, "Фото шт.", false, r.PhotoCount < cfg.Thresholds.PhotoMin, st)
		if r.SizesRatio != nil && *r.SizesRatio < cfg.Thresholds.SizesRatioMin {
			highlightCell(f, sheet, row, "Ряд WB/1С", false, true, st)
		}
		if r.IsShadowed != nil && *r.IsShadowed {
			highlightCell(f, sheet, row, "Скрыта", true, false, st)
		}
		if r.StockAvail == "nonLiquid" {
			highlightCell(f, sheet, row, "Доступность", false, true, st)
		}
		if r.Opens7d == 0 && r.Campaigns == 0 && r.Signal != SignalPreSales {
			highlightCell(f, sheet, row, "Кампаний", false, true, st)
		}
	}

	// Закрепить шапку (строка 1) и колонку «Фото» (A).
	f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		XSplit:      1,
		YSplit:      1,
		TopLeftCell: "B2",
		ActivePane:  "bottomRight",
	})

	if len(rows) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(ncol)
		if err := f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, len(rows)+1), nil); err != nil {
			return err
		}
	}

	// NB: ревизию НЕ пишем в docProps (SetDocProps) — панели Spreadsheets
	// не переваривает сгенерированный excelize пустой узел lastModifiedBy;
	// штамп живёт на листе «Сводка» (A3) и в консоли.
	addEscalationSheet(f, rows, st, snapshotDate)
	addSummarySheet(f, rows, snapshotDate, revision)
	addLegendSheet(f, st)
	addGlossarySheet(f)
	addAvailabilitySheet(f)
	return f.SaveAs(path)
}

// highlightCell — точечная подсветка ячейки колонки: красная (bad) или янтарная (warn).
func highlightCell(f *excelize.File, sheet string, row int, header string, bad, warn bool, st *reportStyles) {
	if !bad && !warn {
		return
	}
	cell, _ := excelize.CoordinatesToCellName(colIndexByHeader(header), row)
	if bad {
		f.SetCellStyle(sheet, cell, cell, st.bad)
		return
	}
	f.SetCellStyle(sheet, cell, cell, st.warn)
}

// addEscalationSheet — лист «7+ дней»: кандидаты на эскалацию руководителю ОЭК
// (письмо гендира, п.5). Вычисляется на лету из счётчика дней — состояния нет.
func addEscalationSheet(f *excelize.File, rows []Row, st *reportStyles, snapshotDate string) {
	sheet := "7+ дней"
	f.NewSheet(sheet)

	esc := make([]Row, 0)
	for _, r := range rows {
		if r.FullDays >= 7 {
			esc = append(esc, r)
		}
	}

	title, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	f.SetCellValue(sheet, "A1", "ЭСКАЛАЦИЯ: 7+ ДНЕЙ БЕЗ ЗАКАЗОВ")
	f.SetCellStyle(sheet, "A1", "A1", title)
	f.SetCellValue(sheet, "A2", fmt.Sprintf("Карточек: %d — передаются руководителю ОЭК (процесс гендира, п.5). Дата среза: %s",
		len(esc), snapshotDate))

	// Те же колонки, что в «Вотчлисте» (кроме фото), начиная со строки 4.
	// Шапка и данные идут по одному плотному счётчику: пропущенные колонки
	// (Фото) НЕ оставляют дыр — иначе значения уезжают от своих заголовков
	// (находка аудита 09.11, P1).
	ci := 0
	for _, c := range reportColumns {
		if c.header == "Фото" || c.header == "Ссылка на фото" {
			continue
		}
		ci++
		cell, _ := excelize.CoordinatesToCellName(ci, 4)
		f.SetCellValue(sheet, cell, c.header)
		f.SetCellStyle(sheet, cell, cell, st.header)
		col, _ := excelize.ColumnNumberToName(ci)
		f.SetColWidth(sheet, col, col, c.width)
	}
	for i := range esc {
		r := &esc[i]
		row := i + 5
		cj := 0
		for _, c := range reportColumns {
			if c.header == "Фото" || c.header == "Ссылка на фото" {
				continue
			}
			cj++
			val := c.value(r)
			if c.header == "№" {
				val = i + 1
			}
			xSet(f, sheet, row, cj, val)
		}
	}
}

// addSummarySheet — лист «Сводка»: raw counts без скора.
func addSummarySheet(f *excelize.File, rows []Row, snapshotDate, revision string) {
	sheet := "Сводка"
	f.NewSheet(sheet)

	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	labelStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})

	var slow, stall, pre, d34, d56, d7, shadowed, noVis int
	hypo := map[string]int{}
	for _, r := range rows {
		switch r.Signal {
		case SignalSlowStart:
			slow++
		case SignalStall:
			stall++
		case SignalPreSales:
			pre++
		}
		switch {
		case r.FullDays >= 7:
			d7++
		case r.FullDays >= 5:
			d56++
		case r.FullDays >= 3:
			d34++
		}
		if r.IsShadowed != nil && *r.IsShadowed {
			shadowed++
		}
		if r.Opens7d == 0 {
			noVis++
		}
		hypo[r.HypoCode]++
	}

	f.SetCellValue(sheet, "A1", "РАННЯЯ ДИАГНОСТИКА: КАРТОЧКИ БЕЗ ЗАКАЗОВ")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)
	f.SetCellValue(sheet, "A2", "Срез "+snapshotDate+" — только карточки по критериям гендира (нет заказов N полных дней с начала продаж)")
	f.SetCellValue(sheet, "A3", "Собрано утилитой card-early-diagnostics, ревизия кода: "+revision)

	stats := []struct {
		label string
		value int
	}{
		{"Всего в вотчлисте", len(rows)},
		{"Медленный старт: не разогналась, заказов ≤2 за жизнь", slow},
		{"Остановка продаж: разгонялась, тишина ≥3 дней (stall)", stall},
		{"Продажи не начались (pre_sales, info)", pre},
		{"Дней без заказов 3–4", d34},
		{"Дней без заказов 5–6 (повторная оценка)", d56},
		{"Дней без заказов 7+ (эскалация)", d7},
		{"Скрыты из каталога WB (isShadowed)", shadowed},
		{"Без открытий за 7 дней (не видны)", noVis},
	}
	row := 4
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "Показатель")
	f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "Кол-во")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("B%d", row), labelStyle)
	row++
	for _, s := range stats {
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), s.label)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), s.value)
		row++
	}

	// Распределение гипотез (отсортировано по частоте).
	row++
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "ГЛАВНЫЕ ГИПОТЕЗЫ")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), labelStyle)
	row++
	type hc struct {
		code string
		n    int
	}
	var list []hc
	for code, n := range hypo {
		list = append(list, hc{code, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	for _, h := range list {
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), h.code)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), h.n)
		row++
	}

	f.SetColWidth(sheet, "A", "A", 48)
	f.SetColWidth(sheet, "B", "B", 12)
}

// addLegendSheet — лист «Легенда»: свотчи теми же ID стилей, что и подсветка.
func addLegendSheet(f *excelize.File, st *reportStyles) {
	sheet := "Легенда"
	f.NewSheet(sheet)

	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	blockStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 12}})
	wrapStyle, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	f.SetCellValue(sheet, "A1", "ЛЕГЕНДА ОТЧЁТА")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)

	row := 3
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "ДНИ БЕЗ ЗАКАЗОВ (ПИСЬМО ГЕНДИРА: ОЦЕНКА НА 5-Й, ЭСКАЛАЦИЯ НА 7-Й)")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), blockStyle)
	row++
	for _, h := range []struct {
		style int
		label string
		text  string
	}{
		{st.days3, "3–4 дня", "попадает в ежедневный мониторинг (порог no_orders_days)"},
		{st.days5, "5–6 дней", "повторная оценка (п.4 процесса) — задача должна быть в работе"},
		{st.days7, "7+ дней", "эскалация руководителю ОЭК (п.5); лист «7+ дней»"},
	} {
		cell := fmt.Sprintf("A%d", row)
		f.SetCellValue(sheet, cell, h.label)
		f.SetCellStyle(sheet, cell, cell, h.style)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), h.text)
		f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), wrapStyle)
		row++
	}
	row++

	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "ТРИГГЕРЫ ФАКТОРОВ")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), blockStyle)
	row++
	for _, h := range []struct {
		style int
		label string
		text  string
	}{
		{st.bad, "красный", "«Скрыта»: WB скрыл карточку из каталога (isShadowed) — показов не будет."},
		{st.bad, "красный", "«Наименование WB»: совсем короткое (<20 симв), однословное или совпадает с предметом (кейс «Ботинки»)."},
		{st.warn, "янтарный", "«Наименование WB»: коротковатое (<25 симв)."},
		{st.warn, "янтарный", "«Фото шт.»: меньше 4 фото."},
		{st.warn, "янтарный", "«Ряд WB/1С»: на WB представлено меньше половины размеров 1С-склада."},
		{st.warn, "янтарный", "«Доступность»: nonLiquid — WB сам пометил неликвидом."},
		{st.warn, "янтарный", "«Кампаний»: нет кампаний и нет открытий — карточку не находят."},
		{st.gray, "н/д", "серым: данных нет (item_ratings/остатки не покрыли карточку) — не вердикт."},
		{st.muted, "серая строка", "pre_sales: одобрено в 1С, карточка есть, но остатка не было — вне процесса 3/5/7."},
	} {
		cell := fmt.Sprintf("A%d", row)
		f.SetCellValue(sheet, cell, h.label)
		f.SetCellStyle(sheet, cell, cell, h.style)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), h.text)
		f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), wrapStyle)
		f.SetRowHeight(sheet, row, 26)
		row++
	}
	row++

	// Пояснения колонок живут отдельным листом «Глоссарий» (полный словарь:
	// смысл + сырьё + нюансы); здесь — только подсветка.
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "Расшифровка всех колонок — лист «Глоссарий»")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), wrapStyle)
	f.SetRowHeight(sheet, row, 26)

	f.SetColWidth(sheet, "A", "A", 22)
	f.SetColWidth(sheet, "B", "B", 96)
}

// colIndexByHeader — 1-based индекс колонки по заголовку.
func colIndexByHeader(header string) int {
	for i, c := range reportColumns {
		if c.header == header {
			return i + 1
		}
	}
	return 1
}

// sortRows — предсортированный вывод (плавающие миниатюры не следуют за Sort):
// сначала худшие (больше дней без заказов), внутри — по категории/артикулу.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].FullDays != rows[j].FullDays {
			return rows[i].FullDays > rows[j].FullDays
		}
		if rows[i].Category1C != rows[j].Category1C {
			return rows[i].Category1C < rows[j].Category1C
		}
		return rows[i].Article1C < rows[j].Article1C
	})
}

// xSet — хелпер установки значения ячейки.
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
