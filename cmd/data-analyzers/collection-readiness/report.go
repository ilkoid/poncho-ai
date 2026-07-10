// report.go — экспорт отчёта готовности в XLSX (excelize/v2).
//
// Лист «Отчёт»: авто-метрики по каждому товару коллекции. Аномалии воронки подсвечены:
//   - нет nmID (карточка WB не создана)        — строка окрашена, ячейка nmID красная;
//   - заблокирован в 1С (is_article_blocked)    — маркер «да» в колонке «Заблокирован»;
//   - карточный рейтинг = 10 / складов ≥ 5      — зелёная ячейка (критерий «идеала»).
// Лист «Сводка»: кол-ва ключевых состояний воронки (без скора — только raw counts).
package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// maxDescriptionLen — ограничение длины текста описания WB в ячейке.
// Наблюдённый max cards.description ≈ 2000 (school 1997, global 2000); WB API допускает до 5000.
// Cap 5000 = хард-лимит WB, в 2.5× выше наблюдённого → ни одно реальное описание не обрезается.
const maxDescriptionLen = 5000

// truncateDesc обрезает описание до max рун, добавляя «…» при усечении.
func truncateDesc(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// column описывает одну колонку отчёта.
type column struct {
	header string
	width  float64
	// value возвращает значение ячейки для строки.
	value func(r Row) interface{}
}

// reportColumns — порядок и содержание колонок (только auto-часть из PG).
var reportColumns = []column{
	{"Фото", 14.3, func(r Row) interface{} { return "" }}, // миниатюра встраивается отдельно (AddPictureFromBytes)
	{"№", 5, func(r Row) interface{} { return 0 }},        // № заполняется номером строки при выводе
	{"Возраст", 10, func(r Row) interface{} { return r.AgeSegment }},
	{"Пол", 10, func(r Row) interface{} { return r.Sex }},
	{"Коллекция", 28, func(r Row) interface{} { return r.Collection }},
	{"Артикул", 12, func(r Row) interface{} { return r.Article }},
	{"Артикул (знач.)", 14, func(r Row) interface{} { return r.ArticleNum }},
	{"Год производства", 10, func(r Row) interface{} {
		if r.ProductionYear == 0 {
			return ""
		}
		return r.ProductionYear
	}},
	{"nmID", 14, func(r Row) interface{} { return r.NmIDorEmpty() }},
	{"Наименование WB", 30, func(r Row) interface{} { return r.WBName }},
	{"Описание WB", 60, func(r Row) interface{} { return truncateDesc(r.Description, maxDescriptionLen) }},
	{"Наименование для печати", 32, func(r Row) interface{} { return r.NameIM }},
	{"Категория 1С", 20, func(r Row) interface{} { return r.Category }},
	{"цвет", 16, func(r Row) interface{} { return r.Color }},
	{"Диапазон размеров", 24, func(r Row) interface{} { return r.SizeRange }},
	{"Этап 1С (движение)", 26, func(r Row) interface{} { return r.ModelStatus }},
	{"Заблокирован", 12, func(r Row) interface{} { return boolStr(r.ArticleBlocked || r.ModelCancelled) }},
	{"Описание готово", 12, func(r Row) interface{} { return boolRu(r.HasDescription) }},
	{"Рейтинг карточки 0-10", 12, func(r Row) interface{} { return r.ProductRating }},
	{"Звёзды 0-5", 10, func(r Row) interface{} { return r.FeedbackRating }},
	{"Складов с остатком", 12, func(r Row) interface{} { return r.WHWithStock }},
	{"Заказы", 9, func(r Row) interface{} { return r.OrdersCount }},
	{"Выкупы", 9, func(r Row) interface{} { return r.BuyoutCount }},
	{"Остаток WB", 11, func(r Row) interface{} { return r.WBStock }},
	{"Остаток 1С резерв", 12, func(r Row) interface{} { return r.OneCReserv }},
	{"Остаток 1С своб", 12, func(r Row) interface{} { return r.OneCFree }},
	{"Ссылка на фото", 10, func(r Row) interface{} { return "" }}, // hyperlink ставится отдельно
}

// NmIDorEmpty возвращает nmID строкой или пусто, если карточки нет.
func (r Row) NmIDorEmpty() string {
	if r.NmID == nil {
		return ""
	}
	return fmt.Sprintf("%d", *r.NmID)
}

func boolStr(b bool) string {
	if b {
		return "да"
	}
	return ""
}
func boolRu(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

// exportXLSX строит xlsx-отчёт и сохраняет по path.
//
// photos — карта nmID → JPEG-байты миниатюры (для встраивания в колонку «Фото»).
// embed — встроить ли миниатюры (true) или ограничиться колонкой-ссылкой (false).
func exportXLSX(rows []Row, path string, collections, seasons []string, photos map[int64][]byte, embed bool) error {
	f := excelize.NewFile()
	sheet := "Отчёт"
	f.SetSheetName("Sheet1", sheet)

	// ── Стили ──
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	// Нет карточки WB — серая строка (сигнал воронки).
	noCardStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
		Font: &excelize.Font{Color: "595959"},
	})
	// Красная ячейка nmID для товаров без карточки.
	nmMissingStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006", Bold: true},
	})
	// Заблокирован — красный маркер.
	blockedStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})
	// Критерий «идеала» — зелёная ячейка.
	idealStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"C6EFCE"}},
		Font: &excelize.Font{Color: "006100"},
	})
	// Ссылка на фото — синяя подчёркнутая.
	linkStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "0563C1", Underline: "single"},
	})
	// Описание WB — перенос по словам (вертикально-верх).
	descStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})

	ncol := len(reportColumns)

	// ── Шапка ──
	for i, c := range reportColumns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, c.header)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	// Ширины колонок.
	for i, c := range reportColumns {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, c.width)
	}

	// ── Данные ──
	for i, r := range rows {
		row := i + 2
		for ci, c := range reportColumns {
			val := c.value(r)
			if c.header == "№" {
				val = i + 1 // № п/п
			}
			xSet(f, sheet, row, ci+1, val)
		}

		// Высота строки — под миниатюру 100×75 (только при встраивании).
		if embed {
			f.SetRowHeight(sheet, row, 56.4)
		}

		// Встраивание миниатюры в колонку «Фото» (клик → полноразмерное фото).
		//
		// ОГРАНИЧЕНИЕ: это плавающее изображение (twoCellAnchor) — оно НЕ следует за Sort в Excel
		// по дизайну самого Excel (Sort меняет значения ячеек, а рисунки живут в отдельном слое по
		// координатам). excelize v2.10.1 умеет писать только PlaceOverCells; cell-embedded
		// (Place in Cell / =IMAGE(), которые сортируются) — только чтение (writer'а нет).
		// Поэтому файл выходит уже пресортированным (ORDER BY collection, article) — при ручной
		// пересортировке в Excel миниатюры оторвутся от строк. То же касается автофильтра на шапке
		// (см. ниже): дропдаун делает Sort/Filter заметнее, но корень тот же — плавающие рисунки не
		// привязаны к строкам. В режиме embed_photos=false (--no-photos) фильтр/сортировка работают
		// без ограничений. Известное ограничение, на будущее.
		if embed && r.HasWBCard() && r.NmID != nil {
			if photoBytes, ok := photos[*r.NmID]; ok && len(photoBytes) > 0 {
				photoCell, _ := excelize.CoordinatesToCellName(photoColIndex(), row)
				if err := f.AddPictureFromBytes(sheet, photoCell, &excelize.Picture{
					Extension: ".jpg",
					File:      photoBytes,
					Format: &excelize.GraphicOptions{
						AltText:             fmt.Sprintf("nm_%d", *r.NmID),
						AutoFit:             true,
						AutoFitIgnoreAspect: true,
						Hyperlink:           r.PhotoBig,
						HyperlinkType:       "External",
					},
				}); err != nil {
					fmt.Printf("WARN: embed photo nm_id=%d: %v\n", *r.NmID, err)
				}
			}
		}

		// Колонка «Ссылка на фото» — кликабельная ссылка на полноразмерное фото.
		if r.PhotoBig != "" {
			linkCell, _ := excelize.CoordinatesToCellName(photoLinkColIndex(), row)
			f.SetCellValue(sheet, linkCell, "фото")
			f.SetCellHyperLink(sheet, linkCell, r.PhotoBig, "External")
			f.SetCellStyle(sheet, linkCell, linkCell, linkStyle)
		}

		// Описание WB — перенос по словам (полный текст в ячейке, читается кликом/расширением строки).
		if r.Description != "" {
			descCell, _ := excelize.CoordinatesToCellName(descColIndex(), row)
			f.SetCellStyle(sheet, descCell, descCell, descStyle)
		}

		// Подсветка: строка без карточки WB.
		if !r.HasWBCard() {
			start, _ := excelize.CoordinatesToCellName(1, row)
			end, _ := excelize.CoordinatesToCellName(ncol, row)
			f.SetCellStyle(sheet, start, end, noCardStyle)
			// Ячейка nmID — красная.
			nmCell, _ := excelize.CoordinatesToCellName(nmIDColIndex(), row)
			f.SetCellValue(sheet, nmCell, "НЕТ")
			f.SetCellStyle(sheet, nmCell, nmCell, nmMissingStyle)
		}

		// Подсветка: заблокирован в 1С.
		if r.ArticleBlocked || r.ModelCancelled {
			bCell, _ := excelize.CoordinatesToCellName(blockedColIndex(), row)
			f.SetCellStyle(sheet, bCell, bCell, blockedStyle)
		}

		// Подсветка критериев «идеала»: карточный рейтинг = 10 и складов ≥ 5.
		if r.HasWBCard() && r.ProductRating >= 10 {
			rateCell, _ := excelize.CoordinatesToCellName(ratingColIndex(), row)
			f.SetCellStyle(sheet, rateCell, rateCell, idealStyle)
		}
		if r.HasWBCard() && r.WHWithStock >= 5 {
			whCell, _ := excelize.CoordinatesToCellName(whColIndex(), row)
			f.SetCellStyle(sheet, whCell, whCell, idealStyle)
		}
	}

	// Закрепить шапку (строка 1) и колонку «Фото» (A) — фото всегда на виду при прокрутке.
	f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		XSplit:      1,
		YSplit:      1,
		TopLeftCell: "B2",
		ActivePane:  "bottomRight",
	})

	// Автофильтр на шапке (строка 1): дропдауны для фильтра/сортировки прямо в Excel.
	// Диапазон A1:<последняя колонка><последняя строка> покрывает шапку + все данные; Excel
	// вешает стрелки на первую строку диапазона (шапку). Не ставим на пустой лист (len(rows)==0).
	if len(rows) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(ncol) // 27 → "AA"
		lastRow := len(rows) + 1
		if err := f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, lastRow), nil); err != nil {
			return err
		}
	}

	// Сводный лист.
	addFunnelSummary(f, rows, collections, seasons)

	return f.SaveAs(path)
}

// Индексы (1-based) колонок, к которым применяется точечная подсветка.
func nmIDColIndex() int     { return colIndexByHeader("nmID") }
func blockedColIndex() int  { return colIndexByHeader("Заблокирован") }
func ratingColIndex() int   { return colIndexByHeader("Рейтинг карточки 0-10") }
func whColIndex() int       { return colIndexByHeader("Складов с остатком") }
func photoColIndex() int    { return colIndexByHeader("Фото") }
func photoLinkColIndex() int { return colIndexByHeader("Ссылка на фото") }
func descColIndex() int     { return colIndexByHeader("Описание WB") }

func colIndexByHeader(header string) int {
	for i, c := range reportColumns {
		if c.header == header {
			return i + 1
		}
	}
	return 1
}

// addFunnelSummary — лист «Сводка» с raw-подсчётами состояний воронки (без скора).
func addFunnelSummary(f *excelize.File, rows []Row, collections, seasons []string) {
	sheet := "Сводка"
	f.NewSheet(sheet)

	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	labelStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})

	// Описание фильтра: показываем что реально задано (коллекции и/или сезоны).
	filterDesc := ""
	if len(collections) > 0 {
		filterDesc += "Коллекции: " + joinCollections(collections)
	}
	if len(seasons) > 0 {
		if filterDesc != "" {
			filterDesc += "  "
		}
		filterDesc += "Сезоны: " + joinCollections(seasons)
	}
	if filterDesc == "" {
		filterDesc = "(фильтр не задан)"
	}

	// Подсчёты.
	var withCard, noCard, blocked, blockedWithCard, rating10, whGe5 int
	for _, r := range rows {
		if r.HasWBCard() {
			withCard++
		} else {
			noCard++
		}
		if r.ArticleBlocked || r.ModelCancelled {
			blocked++
			if r.HasWBCard() {
				blockedWithCard++
			}
		}
		if r.HasWBCard() && r.ProductRating >= 10 {
			rating10++
		}
		if r.HasWBCard() && r.WHWithStock >= 5 {
			whGe5++
		}
	}

	f.SetCellValue(sheet, "A1", "ВОРОНКА ГОТОВНОСТИ КАРТОЧЕК")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)
	f.SetCellValue(sheet, "A2", filterDesc)

	stats := []struct {
		label string
		value int
	}{
		{"Всего товаров в выборке (1С)", len(rows)},
		{"С nmID на WB (карточка создана)", withCard},
		{"БЕЗ nmID на WB (нет карточки)", noCard},
		{"Заблокировано в 1С", blocked},
		{"  └ из них с живой карточкой WB (рассинхрон)", blockedWithCard},
		{"Карточный рейтинг = 10 (идеал)", rating10},
		{"≥5 складов с остатком (идеал)", whGe5},
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

	f.SetColWidth(sheet, "A", "A", 48)
	f.SetColWidth(sheet, "B", "B", 12)
}

// joinCollections — компактное перечисление коллекций для шапки сводки.
func joinCollections(c []string) string {
	if len(c) == 0 {
		return "(не заданы)"
	}
	out := ""
	for i, s := range c {
		if i > 0 {
			out += ", "
		}
		out += `"` + s + `"`
	}
	return out
}

// xSet — хелпер установки значения ячейки (как в analyze-promo-calendar/report.go).
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
