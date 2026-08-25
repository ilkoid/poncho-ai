// report.go — экспорт воронки FBS в XLSX (excelize/v2).
//
// Структура книги:
//
//  1. «Сводка»          — итоговая воронка + деньги (главное: реальная выручка
//     и % выкупа) + покрытие источников;
//  2. «Глоссарий»       — определения: когорта vs динамика, статусы, метрики,
//     деньги, источники;
//  3. «Динамика»        — события по дням (день = дата перехода) + выручка;
//  4. «Когорты»         — по дню создания (лента) с пометкой зрелости;
//  5. «Когорты v3»      — те же когорты из v3-снимка статусов;
//  6. «Причины отмен»   — день × cancel_type;
//  7. «Скорость ЖЦ»     — медиана/p90 цикла заказ→выкуп, по когортам;
//  8. «География»       — города/районы: заказы, % выкупа, выручка;
//  9. «Топ номенклатур» — воронка по артикулам WB, сортировка по упущенному;
//
// 10. «Кросс-проверка»  — покрытие ленты относительно v3 по rid=srid;
// 11. «Методика»        — источники, семантика, оговорки.
//
// Стили повторяют stock-warehouse-report/report.go: синяя шапка #4472C4 bold
// white, freeze panes, autofilter; деньги — формат #,##0.
package main

import (
	"fmt"
	"time"

	"github.com/xuri/excelize/v2"
)

func exportXLSX(data *ReportData, cfg *Config, path string) error {
	f := excelize.NewFile()

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	boldStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	totalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Italic: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDEBF7"}},
	})
	moneyStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("#,##0")})
	pctStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("0.0")})

	// pct — значение или «—» при отсутствии завершившихся (-1).
	pct := func(v float64) interface{} {
		if v < 0 {
			return "—"
		}
		return v
	}

	wrap := func(sheet string, headers []string, row int) {
		for i, h := range headers {
			xSet(f, sheet, row, i+1, h)
			f.SetCellStyle(sheet, cellName(i+1, row), cellName(i+1, row), headerStyle)
		}
	}
	// таблица — шапка + строки + ИТОГО + freeze/autofilter.
	table := func(sheet string, headers []string, rows [][]any, total []any, moneyCols, pctCols []int) {
		// NewSheet на существующем имени вернёт ошибку «already exists» — это норма:
		// без создания листа SetCellValue молча падает и лист теряется.
		_, _ = f.NewSheet(sheet)
		r := 1
		wrap(sheet, headers, r)
		for _, row := range rows {
			r++
			for i, v := range row {
				xSet(f, sheet, r, i+1, v)
			}
		}
		if total != nil {
			r++
			for i, v := range total {
				xSet(f, sheet, r, i+1, v)
			}
			f.SetCellStyle(sheet, cellName(1, r), cellName(len(headers), r), totalStyle)
		}
		f.SetCellStyle(sheet, cellName(1, 1), cellName(len(headers), 1), headerStyle)
		f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 1,
			TopLeftCell: "A2", ActivePane: "bottomLeft"})
		f.AutoFilter(sheet, cellName(1, 1)+":"+cellName(len(headers), r), nil)
		for _, c := range moneyCols {
			f.SetColStyle(sheet, colLetter(c)+":"+colLetter(c), moneyStyle)
		}
		for _, c := range pctCols {
			f.SetColStyle(sheet, colLetter(c)+":"+colLetter(c), pctStyle)
		}
	}

	// ── 1. Сводка ──
	s := "Сводка"
	f.SetSheetName("Sheet1", s)
	f.SetCellValue(s, "A1", "FBS-ВОРОНКА: ЗАКАЗ → ВЫКУП / ОТМЕНА / ВОЗВРАТ")
	f.SetCellStyle(s, "A1", "D1", titleStyle)
	f.MergeCell(s, "A1", "D1")
	scope := "склад продавца (is_mp)"
	if data.AllModels {
		scope = "все модели выполнения (incl. FBW)"
	}
	window := "весь диапазон данных"
	if data.Days > 0 {
		window = fmt.Sprintf("последние %d сут (МСК)", data.Days)
	}
	f.SetCellValue(s, "A2", fmt.Sprintf("Отчёт: %s | лента %s…%s | окно: %s | сгенерировано %s (МСК) | термины — лист «Глоссарий»",
		scope, strOr(data.Coverage.FeedFrom, "—"), strOr(data.Coverage.FeedTo, "—"),
		window, data.GeneratedAt))
	f.MergeCell(s, "A2", "D2")

	r := 4
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "ВОРОНКА ЛЕНТЫ (текущие статусы за окно)")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("D%d", r), boldStyle)
	kv := [][2]any{
		{"Заказов (строк ленты)", data.Totals.Rows},
		{"Выкуплено, шт", data.Totals.Buyout},
		{"Отменено, шт", data.Totals.Cancel},
		{"Возвраты, шт", data.Totals.Returns},
		{"Ещё в пути (created), шт", data.Totals.InFlight},
		{"Выкуп среди завершённых, %", pct(data.Totals.BuyoutPct())},
	}
	for _, row := range kv {
		r++
		xSet(f, s, r, 1, row[0])
		xSet(f, s, r, 2, row[1])
	}

	r += 2
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "ДЕНЬГИ (цена продавца, без комиссий/логистики WB)")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("D%d", r), boldStyle)
	avgCheck := "—"
	if data.Totals.Buyout > 0 {
		avgCheck = fmt.Sprintf("%.0f ₽", data.Totals.BuyoutRub/float64(data.Totals.Buyout))
	}
	kv = [][2]any{
		{"Заказано, ₽", data.Totals.OrderedRub},
		{"ВЫРУЧКА (выкуплено), ₽", data.Totals.BuyoutRub},
		{"Упущено (отмены+возвраты), ₽", data.Totals.LostRub},
		{"Средний чек выкупа", avgCheck},
	}
	for _, row := range kv {
		r++
		xSet(f, s, r, 1, row[0])
		xSet(f, s, r, 2, row[1])
		f.SetCellStyle(s, fmt.Sprintf("B%d", r), fmt.Sprintf("B%d", r), moneyStyle)
	}

	r += 2
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "ЦИКЛ ЗАКАЗ→ВЫКУП И V3-СНИМОК")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("D%d", r), boldStyle)
	med, p90 := "—", "—"
	if data.Lifecycle.MedianH != nil {
		med = fmt.Sprintf("%.1f ч (~%.1f сут)", *data.Lifecycle.MedianH, *data.Lifecycle.MedianH/24)
	}
	if data.Lifecycle.P90H != nil {
		p90 = fmt.Sprintf("%.1f ч (~%.1f сут)", *data.Lifecycle.P90H, *data.Lifecycle.P90H/24)
	}
	kv = [][2]any{
		{"Выкупов в окне", data.Lifecycle.Buyouts},
		{"Медиана цикла", med},
		{"p90 цикла (когорта зреет)", p90},
	}
	if data.V3 != nil {
		kv = append(kv,
			[2]any{"v3: заданий в окне", data.V3.Orders},
			[2]any{"v3: sold / отменено / в работе", fmt.Sprintf("%d / %d / %d", data.V3.Sold, data.V3.Canceled, data.V3.InFlight)})
	}
	for _, row := range kv {
		r++
		xSet(f, s, r, 1, row[0])
		xSet(f, s, r, 2, row[1])
	}

	r += 2
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "ПОКРЫТИЕ ИСТОЧНИКОВ")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("D%d", r), boldStyle)
	kv = [][2]any{
		{"fbs_orders (v3)", fmt.Sprintf("%d строк, %s…%s", data.Coverage.FbsOrders,
			strOr(data.Coverage.FbsOrdersFrom, "—"), strOr(data.Coverage.FbsOrdersTo, "—"))},
		{"fbs_orders_status", fmt.Sprintf("%d строк", data.Coverage.FbsStatuses)},
		{"order_feed", fmt.Sprintf("%d строк (is_mp: %d), переходы %s…%s", data.Coverage.FeedAll,
			data.Coverage.FeedMp, strOr(data.Coverage.FeedFrom, "—"), strOr(data.Coverage.FeedTo, "—"))},
	}
	for _, row := range kv {
		r++
		xSet(f, s, r, 1, row[0])
		xSet(f, s, r, 2, row[1])
	}

	// ── 2. Глоссарий ──
	s = "Глоссарий"
	f.NewSheet(s)
	f.SetCellValue(s, "A1", "ГЛОССАРИЙ: ЧТО ЗНАЧАТ ПОНЯТИЯ ОТЧЁТА")
	f.SetCellStyle(s, "A1", "B1", titleStyle)
	f.MergeCell(s, "A1", "B1")
	termStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Vertical: "top"},
	})
	defStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	r = 3
	for _, sec := range glossarySections() {
		f.SetCellValue(s, fmt.Sprintf("A%d", r), sec.Title)
		f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("B%d", r), boldStyle)
		r++
		for _, e := range sec.Entries {
			f.SetCellValue(s, fmt.Sprintf("A%d", r), e.Term)
			f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("A%d", r), termStyle)
			f.SetCellValue(s, fmt.Sprintf("B%d", r), e.Def)
			f.SetCellStyle(s, fmt.Sprintf("B%d", r), fmt.Sprintf("B%d", r), defStyle)
			r++
		}
		r++
	}
	f.SetColWidth(s, "A", "A", 30)
	f.SetColWidth(s, "B", "B", 110)

	// ── 3. Динамика ──
	rows := make([][]any, 0, len(data.Daily))
	var tB, tC, tR, tN, tT int64
	var tBR, tLR float64
	for _, d := range data.Daily {
		rows = append(rows, []any{d.Day, d.Buyout, d.Cancel, d.Returns, d.StillNew, d.Total,
			pct(d.BuyoutPct()), d.BuyoutRub, d.LostRub})
		tB += d.Buyout
		tC += d.Cancel
		tR += d.Returns
		tN += d.StillNew
		tT += d.Total
		tBR += d.BuyoutRub
		tLR += d.LostRub
	}
	table("Динамика",
		[]string{"День перехода (МСК)", "Выкупы", "Отмены", "Возвраты", "Создан, без переходов",
			"Всего строк", "Выкуп среди завершённых, %", "Выручка, ₽", "Упущено, ₽"},
		rows,
		[]any{"ИТОГО", tB, tC, tR, tN, tT, pct(data.Totals.BuyoutPct()), tBR, tLR},
		[]int{8, 9}, []int{7})

	// ── 4. Когорты (лента) ──
	rows = rows[:0]
	for _, c := range data.Cohorts {
		mature := "нет"
		if c.Mature {
			mature = "да"
		}
		rows = append(rows, []any{c.Cohort, c.Orders, c.Buyout, c.Cancel, c.Returns, c.InFlight,
			pct(c.BuyoutPct()), c.BuyoutRub, mature})
	}
	table("Когорты",
		[]string{"Когорта создания (МСК)", "Заказы", "Выкуп", "Отмена", "Возврат", "В пути",
			"Выкуп среди завершённых, %", "Выручка, ₽", "Зрелая когорта"},
		rows, nil, []int{8}, []int{7})

	// ── 5. Когорты v3 ──
	rows = rows[:0]
	for _, c := range data.CohortsV3 {
		rows = append(rows, []any{c.Cohort, c.Orders, c.Sold, c.Canceled, c.Defect, c.InFlight})
	}
	table("Когорты v3",
		[]string{"Когорта создания (МСК)", "Заданий", "Sold (выкуп)", "Отменено (3 статуса)", "Брак", "В работе"},
		rows, nil, nil, nil)

	// ── 6. Причины отмен ──
	rows = rows[:0]
	var tRc, tA, tE, tO, tU int64
	for _, cr := range data.CancelReasons {
		rows = append(rows, []any{cr.Day, cr.Receipt, cr.App, cr.Expire, cr.Other, cr.Unknown, cr.Total()})
		tRc += cr.Receipt
		tA += cr.App
		tE += cr.Expire
		tO += cr.Other
		tU += cr.Unknown
	}
	table("Причины отмен",
		[]string{"День отмены (МСК)", "Отказ при получении (receipt)", "Отказ до получения (app)",
			"Срок истёк (expire)", "Техническая (other)", "Не указана", "Итого"},
		rows, []any{"ИТОГО", tRc, tA, tE, tO, tU, tRc + tA + tE + tO + tU},
		nil, nil)

	// ── 7. Скорость ЖЦ ──
	s = "Скорость ЖЦ"
	f.NewSheet(s)
	f.SetCellValue(s, "A1", "ЦИКЛ ЗАКАЗ→ВЫКУП (updated_at − created_at, только выкупы)")
	f.SetCellStyle(s, "A1", "D1", titleStyle)
	f.MergeCell(s, "A1", "D1")
	r = 3
	wrap(s, []string{"Метрика", "Значение"}, r)
	for _, row := range [][2]any{
		{"Выкупов в окне", data.Lifecycle.Buyouts},
		{"Медиана, ч", hoursOr(data.Lifecycle.MedianH)},
		{"p90, ч", hoursOr(data.Lifecycle.P90H)},
	} {
		r++
		xSet(f, s, r, 1, row[0])
		xSet(f, s, r, 2, row[1])
	}
	r += 2
	rows = rows[:0]
	for _, lc := range data.LifecycleByCohort {
		rows = append(rows, []any{lc.Cohort, lc.Buyouts, hoursOr(lc.MedianH), hoursOr(lc.P90H)})
	}
	tableAt(f, s, r, []string{"Когорта создания", "Выкупов", "Медиана, ч", "p90, ч"}, rows)

	// ── 8. География ──
	rows = rows[:0]
	for _, g := range data.Geo {
		rows = append(rows, []any{g.City, g.District, g.Orders, g.Buyout, g.Cancel, g.InFlight,
			pct(g.BuyoutPct()), g.BuyoutRub})
	}
	table("География",
		[]string{"Город", "Район", "Заказы", "Выкуп", "Отмена", "В пути",
			"Выкуп среди завершённых, %", "Выручка, ₽"},
		rows, nil, []int{8}, []int{7})

	// ── 9. Топ номенклатур ──
	rows = rows[:0]
	for _, n := range data.TopNm {
		rows = append(rows, []any{n.NmID, n.VendorCode, n.SubjectName, n.Orders, n.Buyout,
			pct(n.BuyoutPct()), n.Cancel, n.Returns, n.InFlight,
			n.OrderedRub, n.BuyoutRub, n.LostRub})
	}
	table("Топ номенклатур",
		[]string{"nm_id", "Артикул продавца", "Предмет", "Заказы", "Выкуп", "Выкуп среди завершённых, %",
			"Отмена", "Возврат", "В пути", "Заказано, ₽", "Выручка, ₽", "Упущено, ₽"},
		rows, nil, []int{10, 11, 12}, []int{6})

	// ── 10. Кросс-проверка ──
	rows = rows[:0]
	for _, c := range data.Cross {
		rows = append(rows, []any{c.Cohort, c.V3Orders, c.InFeed, c.NotInFee, pct(c.MatchPct())})
	}
	table("Кросс-проверка",
		[]string{"Когорта создания", "v3 заданий", "Есть в ленте", "Нет в ленте", "Покрытие, %"},
		rows, nil, nil, []int{5})

	// ── 11. Методика ──
	s = "Методика"
	f.NewSheet(s)
	lines := methodologyLines(data)
	for i, ln := range lines {
		xSet(f, s, i+1, 1, ln)
	}

	f.SetActiveSheet(0)
	return f.SaveAs(path)
}

// glossarySection — раздел листа «Глоссарий».
type glossarySection struct {
	Title   string
	Entries []glossaryEntry
}

// glossaryEntry — термин и его определение.
type glossaryEntry struct {
	Term string
	Def  string
}

// glossarySections — содержание листа «Глоссарий»: те же определения, что
// устоявлись при ручной аналитике; правятся здесь, а не в чате.
func glossarySections() []glossarySection {
	return []glossarySection{
		{
			Title: "ГЛАВНОЕ: КОГОРТА vs ДИНАМИКА",
			Entries: []glossaryEntry{
				{"Когорта", "Все заказы, оформленные покупателями в один календарный день (по МСК). «Когорта 18.08» = все FBS-заказы, размещённые 18 августа. Дальше группа «зреет» ~8–12 суток: часть выкупается, часть отменяется."},
				{"Разрез «Когорты»", "День = день СОЗДАНИЯ заказа. Отвечает на вопрос «что стало с заказами, оформленными в день X?» — единственный честный способ мерить % выкупа: делим на свою же когорту."},
				{"Разрез «Динамика»", "День = день ПЕРЕХОДА в текущий статус (updated_at). Отвечает на вопрос «сколько выкупов/отмен случилось в день X?» — суммирует события когорт любых дат."},
				{"Пример", "Заказ оформлен 18.08, выкуплен 26.08 → он в когорте 18.08 (разрез «Когорты»), а факт выкупа — событие 26.08 (разрез «Динамика»). Рост выкупов в «Динамике» при плоском % = дозревание больших когорт, а не улучшение конверсии."},
			},
		},
		{
			Title: "СОСТОЯНИЯ ЗАКАЗА",
			Entries: []glossaryEntry{
				{"Выкуп", "Покупатель забрал и оплатил заказ в ПВЗ (buyout в ленте, sold в v3). Единственное состояние, дающее продавцу деньги."},
				{"Отмена", "Заказ не дошёл до выкупа. Причины (cancel_type): receipt — отказ ПРИ получении (не забрал в ПВЗ, основная доля отмен); app — отказ ДО получения; expire — истёк срок хранения; other — техническая отмена."},
				{"Возврат", "Выкуплен, но возвращён позже (return; returnDefective — по причине брака)."},
				{"Завершённые", "Выкуп + отмена + возврат — заказы с известной судьбой. «В пути» в знаменатель не входят."},
				{"В пути", "Ещё доставляется: в ленте — created (переходов не было), в v3 — цепочка доставки (waiting, sorted, ready_for_pickup, …)."},
			},
		},
		{
			Title: "МЕТРИКИ",
			Entries: []glossaryEntry{
				{"Выкуп среди завершённых, %", "Выкуп ÷ завершённые. Главная метрика качества воронки; корректна для зрелых когорт."},
				{"Зрелая когорта", "Возраст когорты ≥ p90 цикла (раздел «Скорость ЖЦ», ~12 сут): почти все заказы завершились, проценту можно верить. У незрелых успели завершиться только быстрые → % смещён ВВЕРХ."},
				{"Цикл заказ→выкуп", "Промежуток обновление − создание для выкупов; медиана и p90 — на листе «Скорость ЖЦ». Когорта зреет ≈ p90."},
				{"Средний чек выкупа", "Выручка ÷ число выкупов."},
			},
		},
		{
			Title: "ДЕНЬГИ (цена продавца seller_price, без комиссий/логистики WB)",
			Entries: []glossaryEntry{
				{"Выручка", "Сумма seller_price по ВЫКУПАМ — реальные деньги продавца за период. Финансовая выручка к выплате (после комиссий WB) — отдельный домен finance API, здесь не учитывается."},
				{"Упущено", "Сумма по отменам и возвратам — что было бы выручкой при 100% выкупе завершённых."},
				{"Заказано", "Сумма по всем заказам — денежный потенциал периода (включая ещё в пути)."},
			},
		},
		{
			Title: "ИСТОЧНИКИ И РАЗРЕЗЫ",
			Entries: []glossaryEntry{
				{"Лента (order_feed)", "WB-эндпоинт analytics/v1/order-feed: текущий статус заказа + дата этого статуса (updated_at). Источник событий и денег отчёта."},
				{"v3-снимок", "fbs_orders + fbs_orders_status: состояния на момент последнего прогона загрузчика, без дат переходов. Канон полноты (статусы качаются без пропусков)."},
				{"Кросс-проверка", "Доля v3-заказов, найденных в ленте по rid=srid (~96–99%). Расхождение 1–5% — дрейф пагинации ленты за время живого прогона + край окна."},
				{"rid / srid", "Идентификатор единицы заказа: rid в v3 = srid в статистике/ленте. Ключ сшивки источников."},
				{"Склад продавца (is_mp)", "FBS/DBS — отправления со склада продавца. FBW (склады WB) по умолчанию исключены; флаг --all-models включает."},
				{"День (МСК)", "Границы суток по Europe/Moscow — независимо от таймзоны сервера БД."},
			},
		},
	}
}

// methodologyLines — текст листа «Методика».
func methodologyLines(data *ReportData) []string {
	scope := "только склад продавца (is_mp=true: FBS/DBS)"
	if data.AllModels {
		scope = "все модели выполнения, включая FBW"
	}
	window := "весь диапазон данных"
	if data.Days > 0 {
		window = fmt.Sprintf("события/когорты за последние %d сут", data.Days)
	}
	return []string{
		"МЕТОДИКА И ОГРАНИЧЕНИЯ",
		"",
		"Источники (read-only SELECT):",
		"• public.order_feed — лента заказов POST /api/analytics/v1/order-feed",
		"  (docs/wb_api_swagger/11-analytics.yaml:1682). Ключевая семантика: updated_at = дата",
		"  ТЕКУЩЕГО статуса (11-analytics.yaml:6414) — для выкупов это момент выкупа, поэтому",
		"  «Динамика» группирует по updated_at::date. Статусы: created/buyout/cancel/return/",
		"  returnDefective; cancel_type: app (отказ до получения) / receipt (отказ при",
		"  получении) / expire / other (11-analytics.yaml:6428-6439).",
		"• public.fbs_orders + fbs_orders_status — GET /api/v3/orders + POST /api/v3/orders/status",
		"  (03-orders-fbs.yaml:463, 548): снимок текущих статусов на момент прогона загрузчика,",
		"  без дат переходов; wbStatus enum — 03-orders-fbs.yaml:664 (sold = выкуп, canceled*",
		"  declined_by_client = отмена).",
		"• public.cards — артикул продавца / предмет (лист «Топ номенклатур»).",
		"",
		"Отбор: " + scope + "; окно: " + window + "; границы дней — Europe/Moscow (timestamptz",
		"приводится AT TIME ZONE).",
		"",
		"Оговорки:",
		"• Лента несёт только ТЕКУЩИЙ статус заказа, не историю переходов. «Динамика» = события",
		"  перехода в текущий статус; история шагов накопится в fbs_orders_status_log с",
		"  повторными прогонами загрузчика.",
		"• Когорта зреет ~p90 цикла (лист «Скорость ЖЦ»): у более молодых когорт доля выкупа",
		"  смещена ВВЕРХ — попадают только быстрые переходы. Колонка «Зрелая когорта».",
		"• Край окна ленты: заказы, чей текущий статус старше feed_days загрузчика, в ленту не",
		"  попадают — когорты до края окна неполны (см. «Кросс-проверка»).",
		"• Расхождение v3↔лента ~1–5% — дрейф offset-пагинации ленты за время живого прогона",
		"  плюс край окна. Канон полноты — фаза статусов v3.",
		"• Деньги: seller_price — цена продавца без комиссий/логистики WB. Финансовая выручка",
		"  (к выплате) — отдельный домен finance API, здесь не учитывается.",
		"• Проценты у номенклатур с малым числом заказов — шум; фильтруйте по колонке «Заказы».",
	}
}

// tableAt — таблица с шапкой, начинающаяся с произвольной строки (для «Скорости ЖЦ»).
func tableAt(f *excelize.File, sheet string, startRow int, headers []string, rows [][]any) {
	r := startRow
	for i, h := range headers {
		xSet(f, sheet, r, i+1, h)
		f.SetCellStyle(sheet, cellName(i+1, r), cellName(i+1, r), fMustStyle(f))
	}
	for _, row := range rows {
		r++
		for i, v := range row {
			xSet(f, sheet, r, i+1, v)
		}
	}
}

// fMustStyle — шапочный стиль (создаётся один раз на книгу; для простоты — заново).
func fMustStyle(f *excelize.File) int {
	id, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	return id
}

// ── Хелперы ──

func xSet(f *excelize.File, sheet string, row, col int, value any) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// colLetter — буквенное имя колонки по номеру (1 → A).
func colLetter(col int) string {
	name, _ := excelize.CoordinatesToCellName(col, 1)
	return name[:len(name)-1]
}

func strPtr(s string) *string { return &s }

func strOr(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}

func hoursOr(h *float64) any {
	if h == nil {
		return "—"
	}
	return *h
}

// mskLoc — часовой пояс МСК (фиксированный UTC+3, без базы tzdata).
func mskLoc() *time.Location {
	return time.FixedZone("MSK", 3*3600)
}

// nowMoscow — текущее время в МСК.
func nowMoscow() time.Time { return time.Now().In(mskLoc()) }
