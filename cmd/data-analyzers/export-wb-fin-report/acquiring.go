package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
	"github.com/xuri/excelize/v2"
)

// ============================================================================
// Режим --acquiring: отдельный «Отчёт об издержках на приём платежей» (эквайринг)
// за тот же период, что и основной отчёт. Отвечает на вопрос фин-аналитика
// «может теперь сам эквайринг где-то в другом месте отражается?» — да:
// POST /api/finance/v1/acquiring/{list,detailed} (13-finances.yaml:267-449).
//
// Выход — небольшой отдельный XLSX (основной файл на 600k строк аналитику
// повторно не нужен):
//   лист «Эквайринг» — детализация по строкам (банк-эквайер, комиссия в т.ч.
//   НДС, НДС, счёт-фактура);
//   лист «Сверка»    — итоги из /acquiring/list + Σ по файлу + разбивки по
//   банкам/типам документов + контроль против Σ acquiringFee из детализации
//   продаж (передаётся флагом --sales-acquiring-sum).
// ============================================================================

// runAcquiring — сценарий --acquiring: fetch list+detailed, XLSX, сводка.
func runAcquiring(ctx context.Context, client *wb.Client, cfg finReportConfig) {
	start := time.Now()

	// Итоги по отчётам (1 запрос) + детализация по строкам (1+ запросов).
	fmt.Println("  GET список отчётов (acquiring/list)…")
	reports, err := client.AcquiringReportList(ctx, wb.FinanceReportsBaseURL,
		cfg.rateLimit, cfg.burst, cfg.fromStr, cfg.toStr)
	if err != nil {
		fmt.Printf("  ⚠️  acquiring/list: %v (продолжаем без итогов отчётов)\n", err)
	}
	for _, r := range reports {
		fmt.Printf("    отчёт %d: %s…%s, издержки %.2f ₽ (в т.ч. НДС %.2f ₽)\n",
			r.ReportID, r.DateFrom, r.DateTo, r.AcquiringFeeSum, r.AcquiringFeeVatSum)
	}
	if cfg.listOnly {
		fmt.Printf("\nвсего отчётов за %s…%s: %d (--list-only, детализация не запрашивалась)\n",
			cfg.begin, cfg.end, len(reports))
		return
	}

	var rows []wb.AcquiringDetailedRow
	pages := 0
	total, err := client.AcquiringReportDetailedIterator(ctx, wb.FinanceReportsBaseURL,
		cfg.rateLimit, cfg.burst, cfg.fromStr, cfg.toStr,
		func(batch []wb.AcquiringDetailedRow) error {
			rows = append(rows, batch...)
			pages++
			fmt.Printf("  страница %d: +%d строк (итого %d)\n", pages, len(batch), len(rows))
			return nil
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "выгрузка acquiring: %v\n", err)
		os.Exit(1)
	}
	if total == 0 {
		fmt.Printf("⚠️  acquiring/detailed вернул 0 строк за %s…%s — проверьте период\n", cfg.begin, cfg.end)
	}

	outAbs, _ := filepath.Abs(cfg.outPath)
	meta := ReportMeta{
		Period:      fmt.Sprintf("%s…%s (MSK)", cfg.begin, cfg.end),
		Method:      "POST " + wb.FinanceReportsBaseURL + acquiringDetailMethodName,
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05 MSK"),
		Sheet:       filepath.Base(cfg.outPath),
	}
	if err := BuildAcquiringXLSX(outAbs, meta, rows, reports, cfg.salesAcquiringSum); err != nil {
		fmt.Fprintf(os.Stderr, "xlsx: %v\n", err)
		os.Exit(1)
	}

	printAcquiringSummary(outAbs, rows, cfg.salesAcquiringSum, start)
}

const acquiringDetailMethodName = "/api/finance/v1/acquiring/detailed"

// acqSpec — колонка листа «Эквайринг» (аналог colSpec основного отчёта).
type acqSpec struct {
	title string
	style int // stylePlain | styleMoney
	value func(r *wb.AcquiringDetailedRow) any
	width float64
}

func acqSpecs() []acqSpec {
	money := styleMoney
	return []acqSpec{
		{"Номер отчета", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.ReportID }, 13},
		{"Дата операции", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.AcqDate }, 13},
		{"Дата продажи", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.SaleDate }, 13},
		{"Тип документа", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.DocTypeName }, 13},
		{"Артикул WB", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.NmID }, 11},
		{"srid", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.Srid }, 40},
		{"ШК", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.ShkID }, 14},
		{"Сумма продаж", money, func(r *wb.AcquiringDetailedRow) any { return float64(r.RetailAmount) }, 13},
		{"Комиссия за эквайринг, в т.ч. НДС", money, func(r *wb.AcquiringDetailedRow) any { return float64(r.AcquiringFee) }, 16},
		{"НДС", money, func(r *wb.AcquiringDetailedRow) any { return float64(r.AcquiringFeeVat) }, 11},
		{"Банк-эквайер", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.AcquiringBank }, 16},
		{"ИНН", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.Tin }, 14},
		{"КПП", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.Kpp }, 12},
		{"Счет-фактура №", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.InvoiceNumber }, 13},
		{"Дата счета-фактуры", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.InvoiceDate }, 13},
		{"Валюта", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.Currency }, 8},
		{"Номер строки (rrdId)", stylePlain, func(r *wb.AcquiringDetailedRow) any { return r.RrdID }, 13},
	}
}

// acqTotals — агрегаты детализации для листа «Сверка» и сводки в stdout.
type acqTotals struct {
	rows        int
	feeSum      float64
	vatSum      float64
	retailSum   float64
	acqDateMin  string
	acqDateMax  string
	saleDateMin string
	saleDateMax string
	byBank      map[string]acqBucket
	byDocType   map[string]acqBucket
}

type acqBucket struct {
	rows   int
	feeSum float64
	vatSum float64
}

func sumAcquiring(rows []wb.AcquiringDetailedRow) acqTotals {
	t := acqTotals{rows: len(rows), byBank: map[string]acqBucket{}, byDocType: map[string]acqBucket{}}
	for _, r := range rows {
		fee, vat := float64(r.AcquiringFee), float64(r.AcquiringFeeVat)
		t.feeSum += fee
		t.vatSum += vat
		t.retailSum += float64(r.RetailAmount)
		if r.AcqDate != "" && (t.acqDateMin == "" || r.AcqDate < t.acqDateMin) {
			t.acqDateMin = r.AcqDate
		}
		if r.AcqDate > t.acqDateMax {
			t.acqDateMax = r.AcqDate
		}
		if r.SaleDate != "" && (t.saleDateMin == "" || r.SaleDate < t.saleDateMin) {
			t.saleDateMin = r.SaleDate
		}
		if r.SaleDate > t.saleDateMax {
			t.saleDateMax = r.SaleDate
		}
		b := t.byBank[r.AcquiringBank]
		b.rows++
		b.feeSum += fee
		b.vatSum += vat
		t.byBank[r.AcquiringBank] = b
		d := t.byDocType[r.DocTypeName]
		d.rows++
		d.feeSum += fee
		d.vatSum += vat
		t.byDocType[r.DocTypeName] = d
	}
	return t
}

// BuildAcquiringXLSX пишет файл с листами «Эквайринг» и «Сверка».
func BuildAcquiringXLSX(
	path string,
	meta ReportMeta,
	rows []wb.AcquiringDetailedRow,
	reports []wb.AcquiringReportSummary,
	salesAcquiringSum float64,
) error {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Эквайринг"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
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
	moneyStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("#,##0.00")})

	specs := acqSpecs()

	// Шапку — через StreamWriter строкой 1 (после Flush лист переписывается
	// содержимым стрима); оформление — только после Flush. См. BuildXLSX.
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return fmt.Errorf("stream writer: %w", err)
	}
	headCells := make([]interface{}, len(specs))
	for i, s := range specs {
		headCells[i] = excelize.Cell{StyleID: headerBlue, Value: s.title}
	}
	if err := sw.SetRow("A1", headCells); err != nil {
		return fmt.Errorf("header row: %w", err)
	}
	for ri, row := range rows {
		cells := make([]interface{}, len(specs))
		for ci, s := range specs {
			v := s.value(&row)
			if s.style == styleMoney {
				cells[ci] = excelize.Cell{StyleID: moneyStyle, Value: v}
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

	lastRow := len(rows) + 1
	lastCol, _ := excelize.ColumnNumberToName(len(specs))
	for i, s := range specs {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(sheet, col, col, s.width); err != nil {
			return err
		}
	}
	if err := f.SetRowHeight(sheet, 1, 60); err != nil {
		return err
	}
	if err := f.SetSheetDimension(sheet, "A1:"+lastCol+fmt.Sprint(lastRow)); err != nil {
		return fmt.Errorf("set dimension: %w", err)
	}
	if err := f.AutoFilter(sheet, "A1:"+lastCol+fmt.Sprint(lastRow), nil); err != nil {
		return err
	}
	if err := f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, Split: false, XSplit: 0, YSplit: 1,
		TopLeftCell: "A2", ActivePane: "bottomRight",
	}); err != nil {
		return err
	}

	writeAcquiringReconciliation(f, meta, sumAcquiring(rows), reports, salesAcquiringSum)

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

// writeAcquiringReconciliation — лист «Сверка»: итоги отчётов, Σ детализации,
// разбивки по банкам/типам документов и контроль против основного отчёта.
func writeAcquiringReconciliation(f *excelize.File, meta ReportMeta, t acqTotals, reports []wb.AcquiringReportSummary, salesAcquiringSum float64) {
	const s = "Сверка"
	if _, err := f.NewSheet(s); err != nil {
		return
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	money, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("#,##0.00")})
	head, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9E1F2"}}})
	wrap, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	r := 1
	set := func(col, row int, v any, style int) {
		cell, _ := excelize.CoordinatesToCellName(col, row)
		_ = f.SetCellValue(s, cell, v)
		if style != 0 {
			_ = f.SetCellStyle(s, cell, cell, style)
		}
	}
	title := func(text string) {
		set(1, r, text, bold)
		r++
	}

	title("Сверка эквайринга — " + meta.Period)
	set(1, r, "Метод:", 0)
	set(2, r, meta.Method, 0)
	r++
	set(1, r, "Отчёты-итоги:", 0)
	set(2, r, "POST "+wb.FinanceReportsBaseURL+"/api/finance/v1/acquiring/list", 0)
	r++
	set(1, r, "Сформировано:", 0)
	set(2, r, meta.GeneratedAt, 0)
	r++
	r++

	title("1. Итоги по отчётам (acquiring/list)")
	for c, h := range []string{"Номер отчёта", "Период", "Сформирован", "Издержки по эквайрингу, ₽", "В т.ч. НДС, ₽"} {
		set(c+1, r, h, head)
	}
	r++
	var listFee, listVat float64
	for _, rep := range reports {
		set(1, r, rep.ReportID, 0)
		set(2, r, rep.DateFrom+"…"+rep.DateTo, 0)
		set(3, r, rep.CreateDate, 0)
		set(4, r, float64(rep.AcquiringFeeSum), money)
		set(5, r, float64(rep.AcquiringFeeVatSum), money)
		listFee += float64(rep.AcquiringFeeSum)
		listVat += float64(rep.AcquiringFeeVatSum)
		r++
	}
	if len(reports) == 0 {
		set(1, r, "— (список недоступен)", 0)
		r++
	}
	set(1, r, "Σ по списку отчётов", bold)
	set(4, r, listFee, money)
	set(5, r, listVat, money)
	r += 2

	title("2. Детализация по строкам (acquiring/detailed)")
	set(1, r, "Строк:", 0)
	set(2, r, t.rows, 0)
	r++
	set(1, r, "Σ комиссия за эквайринг (в т.ч. НДС), ₽:", 0)
	set(2, r, t.feeSum, money)
	r++
	set(1, r, "Σ НДС, ₽:", 0)
	set(2, r, t.vatSum, money)
	r++
	set(1, r, "Σ продажи (retailAmount), ₽:", 0)
	set(2, r, t.retailSum, money)
	r++
	set(1, r, "Операции (acqDate):", 0)
	set(2, r, t.acqDateMin+"…"+t.acqDateMax, 0)
	r++
	set(1, r, "Продажи (saleDate):", 0)
	set(2, r, t.saleDateMin+"…"+t.saleDateMax, 0)
	r++
	r++

	title("3. Разбивка по банкам-эквайерам")
	for c, h := range []string{"Банк", "Строк", "Σ комиссия, ₽", "Σ НДС, ₽"} {
		set(c+1, r, h, head)
	}
	r++
	for _, name := range sortedKeys(t.byBank) {
		b := t.byBank[name]
		set(1, r, name, 0)
		set(2, r, b.rows, 0)
		set(3, r, b.feeSum, money)
		set(4, r, b.vatSum, money)
		r++
	}
	r++

	title("4. Разбивка по типам документов")
	for c, h := range []string{"Тип документа", "Строк", "Σ комиссия, ₽", "Σ НДС, ₽"} {
		set(c+1, r, h, head)
	}
	r++
	for _, name := range sortedKeys(t.byDocType) {
		b := t.byDocType[name]
		set(1, r, name, 0)
		set(2, r, b.rows, 0)
		set(3, r, b.feeSum, money)
		set(4, r, b.vatSum, money)
		r++
	}
	r++

	title("5. Контроль против основного отчёта реализации")
	set(1, r, "Σ acquiringFee из детализации продаж (колонка AN осн. отчёта), ₽:", 0)
	set(2, r, salesAcquiringSum, money)
	r++
	set(1, r, "Σ комиссия по acquiring-отчёту (в т.ч. НДС), ₽:", 0)
	set(2, r, t.feeSum, money)
	r++
	set(1, r, "Дельта (acquiring − продажи), ₽:", 0)
	set(2, r, t.feeSum-salesAcquiringSum, money)
	r++
	set(1, r, "Σ НДС из acquiring-отчёта, ₽:", 0)
	set(2, r, t.vatSum, money)
	r++
	note := "Примечание: в acquiring-отчёте комиссия указана «в том числе НДС» (13-finances.yaml:1777-1784); " +
		"в детализации продаж acquiringFee описан как «Компенсация платёжных услуг» (yaml:1382-1385) — " +
		"семантика НДС двух источников может отличаться, дельта выше показывает расхождение."
	set(1, r, note, wrap)
	_ = f.SetRowHeight(s, r, 45)
	r++

	for col, w := range map[string]float64{"A": 52, "B": 30, "C": 22, "D": 24, "E": 20} {
		_ = f.SetColWidth(s, col, col, w)
	}
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printAcquiringSummary — контрольные суммы в stdout.
func printAcquiringSummary(path string, rows []wb.AcquiringDetailedRow, salesAcquiringSum float64, start time.Time) {
	t := sumAcquiring(rows)
	fmt.Println("\n=== Контрольные суммы эквайринга ===")
	fmt.Printf("  Строк:                                        %d\n", t.rows)
	fmt.Printf("  Σ комиссия за эквайринг (в т.ч. НДС):         %.2f ₽\n", t.feeSum)
	fmt.Printf("  Σ НДС:                                        %.2f ₽\n", t.vatSum)
	for _, bank := range sortedKeys(t.byBank) {
		b := t.byBank[bank]
		fmt.Printf("    %-20s %6d строк, %14.2f ₽ (НДС %.2f ₽)\n", bank, b.rows, b.feeSum, b.vatSum)
	}
	if salesAcquiringSum != 0 {
		fmt.Printf("  Контроль (осн. отчёт, колонка AN):            %.2f ₽ (дельта %+.2f ₽)\n",
			salesAcquiringSum, t.feeSum-salesAcquiringSum)
	}
	fmt.Printf("  Операции: %s…%s, продажи: %s…%s\n", t.acqDateMin, t.acqDateMax, t.saleDateMin, t.saleDateMax)
	fmt.Printf("  Файл: %s\n", path)
	fmt.Printf("  Длительность: %s\n", time.Since(start).Round(time.Second))
}
