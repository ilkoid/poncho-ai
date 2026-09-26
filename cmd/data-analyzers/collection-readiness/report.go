// report.go — экспорт отчёта готовности в XLSX (excelize/v2).
//
// Лист «Отчёт»: авто-метрики по каждому товару коллекции. Аномалии воронки подсвечены:
//   - нет nmID (карточка WB не создана)        — строка окрашена, ячейка nmID красная;
//   - заблокирован в 1С (is_article_blocked)    — маркер «да» в колонке «Заблокирован»;
//   - карточный рейтинг = 10 / складов ≥ 5      — зелёная ячейка (критерий «идеала»).
//
// Лист «Сводка»: кол-ва ключевых состояний воронки (без скора — только raw counts).
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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
	{"Характеристики WB", 55, func(r Row) interface{} { return charsCell(r) }},
	{"Сертификат/декларация", 30, func(r Row) interface{} { return r.certCell(time.Now()) }},
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

// charsCell — ячейка «Характеристики WB»: по строке на характеристику
// («Название: значение1, значение2»), последней строкой — размеры карточки WB.
func charsCell(r Row) string {
	var b strings.Builder
	for _, l := range r.CharLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if r.WBSizes != "" {
		b.WriteString("Размеры (WB): " + r.WBSizes)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// certDateLayouts — форматы дат срока сертификата в 1С (как в validate-certificates).
var certDateLayouts = []string{"2006-01-02T15:04:05", "2006-01-02", "02.01.2006"}

// parseCertDate — best-effort разбор даты срока сертификата; ok=false, если не разобралась.
// Даты с годом < 2000 бракуются: у товаров без документа 1С пишет артефакт «0001-01-01».
func parseCertDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range certDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Year() < 2000 {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// certExpired — истёк ли срок: сравнение с полуночью текущего дня, чтобы сертификат
// со сроком «до сегодня» оставался валидным весь последний день.
func certExpired(t, now time.Time) bool {
	return t.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
}

// HasOneCCertDoc — есть ли сертификат/декларация по данным 1С (флаг или тип/номер).
func (r Row) HasOneCCertDoc() bool {
	return r.HasOneCCert || r.CertType != "" || r.CertNumber != ""
}

// certCell — ячейка «Сертификат/декларация»: строка 1 — тип/номер/срок из 1С,
// строка 2 — маркер состояния номера на карточке WB (когда карточка живая и в 1С есть документ),
// последняя строка — ТНВЭД с карточки (ВЭД-домен, не маркетинг: не живёт в «Характеристиках WB»).
func (r Row) certCell(now time.Time) string {
	var lines []string
	switch {
	case r.CertType != "" || r.CertNumber != "":
		s := r.CertType
		if r.CertNumber != "" {
			s += " №" + r.CertNumber
		}
		s = strings.TrimSpace(s)
		if t, ok := parseCertDate(r.CertEnd); ok {
			s += ", до " + t.Format("02.01.2006")
			if certExpired(t, now) {
				s += " (просрочен)"
			}
		}
		lines = append(lines, s)
	case r.HasOneCCert:
		lines = append(lines, "да (без номера)")
	}
	if len(lines) > 0 && r.HasWBCard() {
		lines = append(lines, r.certWBMarker())
	}
	if r.WBTnved != "" {
		lines = append(lines, "ТНВЭД: "+r.WBTnved)
	}
	return strings.Join(lines, "\n")
}

// certWBMarker — маркер состояния номера на карточке относительно 1С (вторая строка
// ячейки). Семантика зеркалит buildReconcileChanges в fix-certificates: точное
// совпадение после trim, тип 1С задаёт ожидаемый слот (Сертификат → 15001136,
// Декларация → 15001135).
func (r Row) certWBMarker() string {
	cert, decl := r.WBCertNum, r.WBDeclNum
	if cert == "" && decl == "" {
		return "на WB: НЕТ"
	}
	var expected, other, otherFilled string
	switch r.CertType {
	case "Декларация":
		expected, other, otherFilled = decl, cert, "заполнен сертификат"
	case "Сертификат":
		expected, other, otherFilled = cert, decl, "заполнена декларация"
	default: // тип 1С неизвестен — берём любой непустой слот
		expected = cert
		if expected == "" {
			expected = decl
		}
	}
	if expected == "" && other != "" {
		return "на WB: не тот тип — " + otherFilled
	}
	if expected != "" && strings.TrimSpace(expected) == strings.TrimSpace(r.CertNumber) {
		return "на WB: да"
	}
	shown := expected
	if shown == "" {
		shown = other
	}
	return "на WB: другой номер — " + shown
}

// CertProblem — красная подсветка сертификата: просрочен по 1С, либо 1С даёт
// документ, карточка WB живая, а номера на ней нет (рассинхрон, зона fix-certificates).
func (r Row) CertProblem(now time.Time) bool {
	if t, ok := parseCertDate(r.CertEnd); ok && certExpired(t, now) {
		return true
	}
	return r.HasOneCCertDoc() && r.HasWBCard() && !r.WBHasCertDecl()
}

// CertStale — янтарная подсветка: номер на карточке отличается от 1С или сидит
// в слоте другого типа, при этом документ 1С валиден — «продление не перенесено».
// Красный (CertProblem) приоритетнее: просрочка 1С гасит янтарную подсветку.
func (r Row) CertStale(now time.Time) bool {
	if !r.HasOneCCertDoc() || !r.HasWBCard() || r.CertProblem(now) {
		return false
	}
	return r.certWBMarker() != "на WB: да"
}

// reportStyles — стили листа «Отчёт». Единый источник цветов: те же ID стилей
// красят свотчи листа «Легенда», поэтому легенда не может разойтись с подсветкой.
type reportStyles struct {
	header    int
	noCard    int
	nmMissing int
	blocked   int
	ideal     int
	link      int
	desc      int
	certBad   int
	certStale int
}

func newReportStyles(f *excelize.File) *reportStyles {
	st := &reportStyles{}
	st.header, _ = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	// Нет карточки WB — серая строка (сигнал воронки).
	st.noCard, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
		Font: &excelize.Font{Color: "595959"},
	})
	// Красная ячейка nmID для товаров без карточки.
	st.nmMissing, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006", Bold: true},
	})
	// Заблокирован — красный маркер.
	st.blocked, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})
	// Критерий «идеала» — зелёная ячейка.
	st.ideal, _ = f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"C6EFCE"}},
		Font: &excelize.Font{Color: "006100"},
	})
	// Ссылка на фото — синяя подчёркнутая.
	st.link, _ = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "0563C1", Underline: "single"},
	})
	// Многострочные текстовые ячейки — перенос по словам (вертикально-верх).
	st.desc, _ = f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	// Сертификат: просрочен или номера нет на карточке WB — красный (как blocked).
	st.certBad, _ = f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font:      &excelize.Font{Color: "9C0006"},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	// Сертификат: номер на WB отличается / тип перепутан при валидном 1С — янтарный.
	st.certStale, _ = f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFEB9C"}},
		Font:      &excelize.Font{Color: "9C6500"},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	return st
}

// exportXLSX строит xlsx-отчёт и сохраняет по path.
//
// photos — карта nmID → JPEG-байты миниатюры (для встраивания в колонку «Фото»).
// embed — встроить ли миниатюры (true) или ограничиться колонкой-ссылкой (false).
func exportXLSX(rows []Row, path string, collections, seasons []string, allowedYears []int, photos map[int64][]byte, embed bool) error {
	f := excelize.NewFile()
	sheet := "Отчёт"
	f.SetSheetName("Sheet1", sheet)

	// ── Стили ──
	st := newReportStyles(f)

	now := time.Now()

	ncol := len(reportColumns)

	// ── Шапка ──
	for i, c := range reportColumns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, c.header)
		f.SetCellStyle(sheet, cell, cell, st.header)
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
			f.SetCellStyle(sheet, linkCell, linkCell, st.link)
		}

		// Описание WB — перенос по словам (полный текст в ячейке, читается кликом/расширением строки).
		if r.Description != "" {
			descCell, _ := excelize.CoordinatesToCellName(descColIndex(), row)
			f.SetCellStyle(sheet, descCell, descCell, st.desc)
		}

		// Характеристики WB — перенос по словам (многострочная ячейка, как описание).
		if chars := charsCell(r); chars != "" {
			chCell, _ := excelize.CoordinatesToCellName(charsColIndex(), row)
			f.SetCellStyle(sheet, chCell, chCell, st.desc)
		}

		// Сертификат/декларация — перенос + подсветка: красный (просрочка/номера нет)
		// приоритетнее янтарного (номер отличается/тип перепутан при валидном 1С).
		if cert := r.certCell(now); cert != "" {
			cCell, _ := excelize.CoordinatesToCellName(certColIndex(), row)
			style := st.desc
			switch {
			case r.CertProblem(now):
				style = st.certBad
			case r.CertStale(now):
				style = st.certStale
			}
			f.SetCellStyle(sheet, cCell, cCell, style)
		}

		// Подсветка: строка без карточки WB.
		if !r.HasWBCard() {
			start, _ := excelize.CoordinatesToCellName(1, row)
			end, _ := excelize.CoordinatesToCellName(ncol, row)
			f.SetCellStyle(sheet, start, end, st.noCard)
			// Ячейка nmID — красная.
			nmCell, _ := excelize.CoordinatesToCellName(nmIDColIndex(), row)
			f.SetCellValue(sheet, nmCell, "НЕТ")
			f.SetCellStyle(sheet, nmCell, nmCell, st.nmMissing)
		}

		// Подсветка: заблокирован в 1С.
		if r.ArticleBlocked || r.ModelCancelled {
			bCell, _ := excelize.CoordinatesToCellName(blockedColIndex(), row)
			f.SetCellStyle(sheet, bCell, bCell, st.blocked)
		}

		// Подсветка критериев «идеала»: карточный рейтинг = 10 и складов ≥ 5.
		if r.HasWBCard() && r.ProductRating >= 10 {
			rateCell, _ := excelize.CoordinatesToCellName(ratingColIndex(), row)
			f.SetCellStyle(sheet, rateCell, rateCell, st.ideal)
		}
		if r.HasWBCard() && r.WHWithStock >= 5 {
			whCell, _ := excelize.CoordinatesToCellName(whColIndex(), row)
			f.SetCellStyle(sheet, whCell, whCell, st.ideal)
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
		lastCol, _ := excelize.ColumnNumberToName(ncol) // 29 → "AC"
		lastRow := len(rows) + 1
		if err := f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, lastRow), nil); err != nil {
			return err
		}
	}

	// Сводный лист.
	addFunnelSummary(f, rows, collections, seasons, allowedYears)

	// Легенда: расшифровка подсветки и маркеров (свотчи — теми же стилями, что и отчёт).
	addLegendSheet(f, st)

	return f.SaveAs(path)
}

// Индексы (1-based) колонок, к которым применяется точечная подсветка.
func nmIDColIndex() int      { return colIndexByHeader("nmID") }
func blockedColIndex() int   { return colIndexByHeader("Заблокирован") }
func ratingColIndex() int    { return colIndexByHeader("Рейтинг карточки 0-10") }
func whColIndex() int        { return colIndexByHeader("Складов с остатком") }
func photoColIndex() int     { return colIndexByHeader("Фото") }
func photoLinkColIndex() int { return colIndexByHeader("Ссылка на фото") }
func descColIndex() int      { return colIndexByHeader("Описание WB") }
func charsColIndex() int     { return colIndexByHeader("Характеристики WB") }
func certColIndex() int      { return colIndexByHeader("Сертификат/декларация") }

func colIndexByHeader(header string) int {
	for i, c := range reportColumns {
		if c.header == header {
			return i + 1
		}
	}
	return 1
}

// addFunnelSummary — лист «Сводка» с raw-подсчётами состояний воронки (без скора).
func addFunnelSummary(f *excelize.File, rows []Row, collections, seasons []string, allowedYears []int) {
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
	if len(allowedYears) > 0 {
		if filterDesc != "" {
			filterDesc += "  "
		}
		filterDesc += "Годы: " + joinYears(allowedYears)
	}
	if filterDesc == "" {
		filterDesc = "(фильтр не задан)"
	}

	// Подсчёты.
	var withCard, noCard, blocked, blockedWithCard, rating10, whGe5 int
	var cert1C, certAbsent, certStaleNum, certStaleType, expiredCnt int
	now := time.Now()
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
		rowExpired := false
		if t, ok := parseCertDate(r.CertEnd); ok && certExpired(t, now) {
			expiredCnt++
			rowExpired = true
		}
		if r.HasOneCCertDoc() {
			cert1C++
		}
		// Бакеты маркера взаимоисключающие; actionable (янтарные) — только при валидном 1С.
		if r.HasOneCCertDoc() && r.HasWBCard() {
			switch m := r.certWBMarker(); {
			case m == "на WB: НЕТ":
				certAbsent++
			case rowExpired:
				// просроченный 1С — красный, не actionable
			case strings.HasPrefix(m, "на WB: другой номер"):
				certStaleNum++
			case strings.HasPrefix(m, "на WB: не тот тип"):
				certStaleType++
			}
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
		{"Сертификат/декларация в 1С", cert1C},
		{"  └ номера нет на карточке WB (рассинхрон)", certAbsent},
		{"  └ номер на WB отличается, 1С валиден (устарел)", certStaleNum},
		{"  └ тип перепутан (сертификат↔декларация), 1С валиден", certStaleType},
		{"Сертификат просрочен (срок 1С)", expiredCnt},
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

// addLegendSheet — третий лист «Легенда»: расшифровка подсветки и маркеров.
// Свотчи в колонке A красятся теми же ID стилей (reportStyles), что и ячейки
// отчёта, — легенда физически не может разойтись с реальной подсветкой.
func addLegendSheet(f *excelize.File, st *reportStyles) {
	sheet := "Легенда"
	f.NewSheet(sheet)

	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	blockStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 12}})
	wrapStyle, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	f.SetCellValue(sheet, "A1", "ЛЕГЕНДА ОТЧЁТА")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)

	row := 3
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "ПОДСВЕТКА ЯЧЕЕК")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), blockStyle)
	row++
	for _, h := range []struct {
		style int
		label string
		text  string
	}{
		{st.certBad, "красный",
			"«Сертификат/декларация»: номера нет на карточке WB («на WB: НЕТ») либо срок 1С истёк («(просрочен)»)."},
		{st.certStale, "янтарный",
			"«Сертификат/декларация»: номер на WB отличается от 1С или тип перепутан, документ 1С валиден — продление не перенесено (чинится fix-certificates --reconcile)."},
		{st.blocked, "красный",
			"«Заблокирован»: товар заблокирован в 1С / модель снята."},
		{st.nmMissing, "красный",
			"«nmID»: ячейка «НЕТ» — карточка на WB не создана."},
		{st.ideal, "зелёный",
			"Критерий «идеала»: карточный рейтинг = 10 / складов с остатком ≥ 5."},
		{st.noCard, "серый (строка)",
			"Строка целиком — карточка на WB не создана (нет nmID)."},
	} {
		cell := fmt.Sprintf("A%d", row)
		f.SetCellValue(sheet, cell, h.label)
		f.SetCellStyle(sheet, cell, cell, h.style)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), h.text)
		f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), wrapStyle)
		f.SetRowHeight(sheet, row, 28)
		row++
	}
	row++

	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "МАРКЕРЫ КОЛОНКИ «СЕРТИФИКАТ/ДЕКЛАРАЦИЯ»")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), blockStyle)
	row++
	for _, m := range [][2]string{
		{"на WB: да", "номер на карточке совпадает с 1С"},
		{"на WB: НЕТ", "номера на карточке нет"},
		{"на WB: другой номер — …", "на карточке иной (обычно старый) номер — показан после тире"},
		{"на WB: не тот тип — заполнен сертификат|декларация", "номер в слоте другого типа (сертификат↔декларация)"},
		{"(просрочен)", "срок действия документа по 1С истёк (даты 0001-01-01 игнорируются)"},
		{"да (без номера)", "1С знает о документе, но номера у неё нет"},
		{"ТНВЭД: 6109902000", "код ТН ВЭД с карточки WB — отдельной строкой (ВЭД-домен, не маркетинг)"},
	} {
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), m[0])
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), m[1])
		row++
	}
	row++

	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "КОЛОНКА «ХАРАКТЕРИСТИКИ WB»")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), blockStyle)
	row++
	for _, m := range [][2]string{
		{"Название: значение1, значение2", "по строке на маркетинговую характеристику карточки; мультизначения через запятую"},
		{"Размеры (WB): 62, 68, 74", "последняя строка ячейки — размеры карточки WB (0 = один размер)"},
		{"(без ВЭД-полей)", "разрешительная документация (сертификат/декларация, даты) и ТНВЭД сюда не попадают — см. колонку «Сертификат/декларация»"},
	} {
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), m[0])
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), m[1])
		row++
	}

	f.SetColWidth(sheet, "A", "A", 46)
	f.SetColWidth(sheet, "B", "B", 92)
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

// joinYears — компактное перечисление годов производства (нормализуются к 20XX
// для читаемости: [24, 25, 26] или [2024, 2026] → "2024, 2025, 2026").
func joinYears(y []int) string {
	parts := make([]string, len(y))
	for i, v := range y {
		if v > 100 {
			v %= 100
		}
		parts[i] = strconv.Itoa(2000 + v)
	}
	return strings.Join(parts, ", ")
}

// xSet — хелпер установки значения ячейки (как в analyze-promo-calendar/report.go).
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
