package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
	"github.com/xuri/excelize/v2"
)

// timeParse — обёртка time.Parse для короткой записи в dtStr.
func timeParse(layout, value string) (time.Time, error) {
	return time.Parse(layout, value)
}

// ============================================================================
// Генерация XLSX по макету фин-отчёта ВБ («Прототип Отчета ВБ.xlsx»):
// 56 колонок A..BD, шапка высотой 150, серые AM:AP (тип платежа / эквайринг /
// вознаграждение ВВ / его НДС). Соответствие заголовков макета полям API
// сверено с SalesReportsDetailedRes (docs/wb_api_swagger/13-finances.yaml,
// строки 1227-1575).
//
// Отклонения от макета:
//   - freeze первой строки и автофильтр добавлены для удобства сверки;
//   - колонки без источника в новом API (V «К перечислению за товар, НДС»,
//     AH «Договор», AS «Номер партнера») остаются пустыми и помечены в
//     листе «Легенда».
// ============================================================================

const dataSheet = "Отчет ВБ"

const (
	stylePlain = iota
	styleMoney
	stylePct
)

// colSpec описывает одну колонку макета.
type colSpec struct {
	title  string // точный текст из макета
	gray   bool   // серая колонка AM:AP
	style  int    // стиль ячеек данных (plain/money/pct)
	value  func(r *wb.RealizationReportRow) any
	wire   string // имя поля в ответе finance API (для легенды)
	status string // примечание для легенды; "" = заполняется штатно
	width  float64
}

func colSpecs() []colSpec {
	money := styleMoney
	pct := stylePct
	return []colSpec{
		{"Артикул", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.NmID }, "nmId", "", 12},
		{"Размер", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.TechSize }, "techSize", "", 10},
		{"Бренд", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.BrandName }, "brandName", "", 16},
		{"Номер поставки", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.GiID }, "giId", "", 13},
		{"Предмет", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.SubjectName }, "subjectName", "", 20},
		{"Артикул поставщика", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.SupplierArticle }, "vendorCode", "", 18},
		{"Баркод", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.Barcode }, "sku", "", 15},
		{"Тип документа", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.DocTypeName }, "docTypeName", "", 13},
		{"Кол-во", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.Quantity }, "quantity", "", 8},
		{"Цена розничная", false, money, func(r *wb.RealizationReportRow) any { return r.RetailPrice }, "retailPrice", "", 13},
		{"Сумма продаж", false, money, func(r *wb.RealizationReportRow) any { return r.RetailAmount }, "retailAmount", "", 13},
		{"Сумма комиссии продаж", false, money, func(r *wb.RealizationReportRow) any { return r.PPVzSalesCommission },
			"ppvzSalesCommission", "см. примечание 2 (дублирует AK)", 15},
		{"Согласованная скидка", false, pct, func(r *wb.RealizationReportRow) any { return r.ProductDiscountForReport },
			"productDiscountForReport", "в API описан как «Итоговая согласованная скидка, %», yaml:1344-1347", 13},
		{"Процент комиссии", false, pct, func(r *wb.RealizationReportRow) any { return r.CommissionPercent }, "commissionPercent", "", 11},
		{"Склад", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.OfficeName }, "officeName", "", 14},
		{"Обоснование для оплаты", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.SupplierOperName },
			"sellerOperName", "yaml:1301-1304", 22},
		{"Дата заказа", false, stylePlain, func(r *wb.RealizationReportRow) any { return dtStr(r.OrderDT) }, "orderDt", "", 17},
		{"Дата продажи", false, stylePlain, func(r *wb.RealizationReportRow) any { return dtStr(r.SaleDT) }, "saleDt", "", 17},
		{"ШК", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.ShkID }, "shkId", "", 13},
		{"Цена розничная с учетом согласованной скидки", false, money, func(r *wb.RealizationReportRow) any { return r.RetailPriceWithDiscRub },
			"retailPriceWithDisc", "", 18},
		{"К перечислению за товар", false, money, func(r *wb.RealizationReportRow) any { return r.PPVzForPay }, "forPay", "", 15},
		{"К перечислению за товар, НДС", false, money, func(*wb.RealizationReportRow) any { return nil },
			"—", "нет поля в новом API; agencyVat (yaml:1546-1549) — только для продавцов Кыргызстана", 15},
		{"Кол-во доставок", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.DeliveryAmount }, "deliveryAmount", "", 10},
		{"Кол-во возвратов", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.ReturnAmount }, "returnAmount", "", 10},
		{"Стоимость логистики", false, money, func(r *wb.RealizationReportRow) any { return r.DeliveryRub }, "deliveryService", "", 13},
		{"«Тип коробов »", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.GiBoxTypeName }, "giBoxTypeName", "", 14},
		{"Согласованный продуктовый дисконт", false, pct, func(r *wb.RealizationReportRow) any { return r.SalePercent },
			"salePercent", "в API описан как «Согласованный продуктовый дисконт, %», yaml:1289-1292", 15},
		{"Промокод", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.UUIDPromocode },
			"uuidPromocode", "ID промокода (yaml:1530-1533)", 16},
		{"Скидка постоянного покупателя", false, pct, func(r *wb.RealizationReportRow) any { return r.PPVzSppPrc },
			"spp", "в API переименован в «Платформенные скидки, %», yaml:1352-1355", 14},
		{"rid", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.Srid },
			"srid", "= rid по документации WB (yaml:1571-1575)", 40},
		{"Дата операции", false, stylePlain, func(r *wb.RealizationReportRow) any { return dtStr(r.RRDT) }, "rrDate", "", 14},
		{"Номер строки", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.RrdID }, "rrdId",
			"уникальный номер строки детализации отчёта", 13},
		{"Номер отчета", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.RealizationReportID }, "reportId", "", 13},
		{"Договор", false, stylePlain, func(*wb.RealizationReportRow) any { return nil },
			"—", "нет поля в новом API", 12},
		{"Размер кВВ без НДС, % Базовый", false, pct, func(r *wb.RealizationReportRow) any { return r.PPVzKvwPrcBase }, "kvwBase", "", 14},
		{"Итоговый кВВ без НДС, %", false, pct, func(r *wb.RealizationReportRow) any { return r.PPVzKvwPrc }, "kvw", "", 13},
		{"Вознаграждение с продаж до вычета услуг поверенного, без НДС", false, money, func(r *wb.RealizationReportRow) any {
			return r.PPVzSalesCommission
		}, "ppvzSalesCommission", "см. примечание 2 (дублирует L)", 20},
		{"Возмещение за выдачу и возврат товаров на ПВЗ", false, money, func(r *wb.RealizationReportRow) any { return r.PPVzReward },
			"ppvzReward", "yaml:1378-1381", 18},
		{"Тип платежа", true, stylePlain, func(r *wb.RealizationReportRow) any { return r.PaymentProcessing },
			"paymentProcessing", "yaml:1390-1393", 34},
		{"Эквайринг/Комиссия за организацию платежей", true, money, func(r *wb.RealizationReportRow) any { return r.AcquiringFee },
			"acquiringFee", "yaml:1382-1385", 18},
		{"Вознаграждение Вайлдберриз (ВВ), без НДС", true, money, func(r *wb.RealizationReportRow) any { return r.PPVzVw },
			"vw", "yaml:1398-1401", 16},
		{"НДС с Вознаграждения Вайлдберриз", true, money, func(r *wb.RealizationReportRow) any { return r.PPVzVwNds },
			"vwNds", "yaml:1402-1405", 15},
		{"Номер офиса", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.PPVzOfficeID }, "ppvzOfficeId", "", 11},
		{"Наименование офиса доставки", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.PPVzOfficeName },
			"ppvzOfficeName", "", 28},
		{"Номер партнера", false, stylePlain, func(*wb.RealizationReportRow) any { return nil },
			"—", "нет поля в новом API (есть только Партнёр и ИНН партнера)", 13},
		{"Партнер", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.PPVzSupplierName }, "ppvzSupplierName", "", 24},
		{"ИНН партнера", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.PPVzSupplierInn }, "ppvzSupplierInn", "", 15},
		{"Номер таможенной декларации", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.DeclarationNumber },
			"declarationNumber", "", 26},
		{"Обоснование штрафов и доплат", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.BonusTypeName },
			"bonusTypeName", "yaml:1426-1429", 34},
		{"sticker_id", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.StickerID }, "stickerId", "", 13},
		{"Страна продажи", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.Country }, "country", "", 13},
		{"Размер снижения кВВ из-за рейтинга, %", false, pct, func(r *wb.RealizationReportRow) any { return r.SupRatingPrcUp },
			"supRatingUp", "", 15},
		{"Размер снижения кВВ из-за акции, %", false, pct, func(r *wb.RealizationReportRow) any { return r.IsKgvpV2 },
			"isKgvpV2", "", 14},
		{"Возмещение издержек по перевозке", false, money, func(r *wb.RealizationReportRow) any { return r.RebillLogisticCost },
			"rebillLogisticCost", "", 15},
		{"Организатор перевозки", false, stylePlain, func(r *wb.RealizationReportRow) any { return r.RebillLogisticOrg },
			"rebillLogisticOrg", "yaml:1454-1457", 30},
		{"Признак B2B продажи", false, stylePlain, func(r *wb.RealizationReportRow) any { return b2bFlag(r.IsLegalEntity) },
			"isB2b", "", 10},
	}
}

// ReportMeta — служебная информация о выгрузке для листа «Легенда».
type ReportMeta struct {
	Period      string
	Method      string
	GeneratedAt string
	Sheet       string // имя выходного файла
}

// BuildXLSX пишет файл и возвращает число строк данных.
func BuildXLSX(path string, meta ReportMeta, rows []wb.RealizationReportRow) error {
	f := excelize.NewFile()
	defer f.Close()

	if err := f.SetSheetName("Sheet1", dataSheet); err != nil {
		return fmt.Errorf("rename sheet: %w", err)
	}

	headerBlue, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{WrapText: true, Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "top", Style: 1, Color: "7F7F7F"},
			{Type: "bottom", Style: 1, Color: "7F7F7F"},
		},
	})
	headerGray, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"BFBFBF"}},
		Alignment: &excelize.Alignment{WrapText: true, Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "top", Style: 1, Color: "7F7F7F"},
			{Type: "bottom", Style: 1, Color: "7F7F7F"},
		},
	})
	moneyStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("#,##0.00")})
	pctStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("0.00")})

	specs := colSpecs()

	// ⚠️ Шапку пишем через StreamWriter строкой 1: после Flush лист
	// полностью переписывается содержимым стрима, поэтому всё записанное
	// обычным API до NewStreamWriter теряется. Ширины/высоты/автофильтр —
	// только ПОСЛЕ Flush (они не касаются ячеек).
	sw, err := f.NewStreamWriter(dataSheet)
	if err != nil {
		return fmt.Errorf("stream writer: %w", err)
	}

	headCells := make([]interface{}, len(specs))
	for i, s := range specs {
		id := headerBlue
		if s.gray {
			id = headerGray
		}
		headCells[i] = excelize.Cell{StyleID: id, Value: s.title}
	}
	if err := sw.SetRow("A1", headCells); err != nil {
		return fmt.Errorf("header row: %w", err)
	}

	for ri, row := range rows {
		cells := make([]interface{}, len(specs))
		for ci, s := range specs {
			v := s.value(&row)
			if s.style == styleMoney && v != nil {
				cells[ci] = excelize.Cell{StyleID: moneyStyle, Value: v}
				continue
			}
			if s.style == stylePct && v != nil {
				cells[ci] = excelize.Cell{StyleID: pctStyle, Value: v}
				continue
			}
			cells[ci] = v
		}
		cell, _ := excelize.CoordinatesToCellName(1, ri+2)
		if err := sw.SetRow(cell, cells); err != nil {
			return fmt.Errorf("row %d: %w", ri+2, err)
		}
	}
	if err := sw.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	// Оформление — только после Flush (см. комментарий выше).
	lastRow := len(rows) + 1
	lastCol, _ := excelize.ColumnNumberToName(len(specs))
	for i, s := range specs {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(dataSheet, col, col, s.width); err != nil {
			return err
		}
	}
	if err := f.SetRowHeight(dataSheet, 1, 150); err != nil {
		return err
	}
	// Восстанавливаем dimension листа: StreamWriter может оставить его
	// неполным, что ломает read-only читалки (openpyxl).
	if err := f.SetSheetDimension(dataSheet, "A1:"+lastCol+strconv.Itoa(lastRow)); err != nil {
		return fmt.Errorf("set dimension: %w", err)
	}
	if err := f.AutoFilter(dataSheet, "A1:"+lastCol+strconv.Itoa(lastRow), nil); err != nil {
		return err
	}
	if err := f.SetPanes(dataSheet, &excelize.Panes{
		Freeze: true, Split: false, XSplit: 0, YSplit: 1,
		TopLeftCell: "A2", ActivePane: "bottomRight",
	}); err != nil {
		return err
	}

	writeLegend(f, meta, specs)

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

// writeLegend кладёт на второй лист соответствие «колонка макета ↔ поле API»
// и примечания — чтобы фин-аналитик мог сверять математику по названиям.
func writeLegend(f *excelize.File, meta ReportMeta, specs []colSpec) {
	s := "Легенда"
	if _, err := f.NewSheet(s); err != nil {
		return
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	note, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	headers := []string{"Колонка", "Заголовок макета", "Поле finance API", "Примечание"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(s, cell, h)
		f.SetCellStyle(s, cell, cell, bold)
	}
	for i, sp := range specs {
		r := i + 2
		set := func(col int, v string) {
			cell, _ := excelize.CoordinatesToCellName(col, r)
			f.SetCellValue(s, cell, v)
		}
		status := sp.status
		if status == "" {
			status = "заполняется"
		}
		set(1, colLetter(i+1))
		set(2, sp.title)
		set(3, sp.wire)
		set(4, status)
	}

	notesRow := len(specs) + 4
	notes := []string{
		"Примечания:",
		fmt.Sprintf("1. Выгрузка: %s; период %s; строк: см. итоги в файле %s.", meta.Method, meta.Period, meta.Sheet),
		"2. Колонки L «Сумма комиссии продаж» и AK «Вознаграждение с продаж до вычета услуг поверенного, без НДС» заполняются из одного поля ppvzSalesCommission (yaml:1370-1373) — WB экспортирует его дважды.",
		"3. Серые колонки AM:AP — те же 4 показателя, которые макет просит добавить в BI.",
		"4. Метод выгрузки: " + meta.Method + " — замена отключённого WB 15.07.2026 GET /api/v5/supplier/reportDetailByPeriod.",
		"5. Дата формирования: " + meta.GeneratedAt + ".",
	}
	for i, n := range notes {
		cell, _ := excelize.CoordinatesToCellName(1, notesRow+i)
		f.SetCellValue(s, cell, n)
		f.SetCellStyle(s, cell, cell, note)
	}
	f.SetColWidth(s, "A", "A", 10)
	f.SetColWidth(s, "B", "B", 46)
	f.SetColWidth(s, "C", "C", 22)
	f.SetColWidth(s, "D", "D", 70)
}

// colLetter — буквенное имя колонки (1→A … 56→BD).
func colLetter(n int) string {
	name, _ := excelize.ColumnNumberToName(n)
	return name
}

// dtStr приводит ISO-дату API к читаемому виду "YYYY-MM-DD HH:MM".
func dtStr(v string) string {
	const iso = "2006-01-02T15:04:05Z07:00"
	t, err := timeParse(iso, v)
	if err != nil {
		return v
	}
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04")
}

func strPtr(s string) *string { return &s }

func b2bFlag(v bool) string {
	if v {
		return "да"
	}
	return ""
}
