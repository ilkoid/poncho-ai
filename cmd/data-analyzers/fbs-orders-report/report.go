// report.go — экспорт отчёта по FBS-заданиям в XLSX (excelize/v2).
//
// Структура книги:
//
//  1. «Сводка»       — итоги по origin + ключевые числа (активные, старые, покрытие);
//  2. «Динамика»     — создания заданий по дням: native / migrated / no_stats_row;
//  3. «Возраст»      — активные (new/confirm) по бакетам возраста + гистограмма
//                      лагов мигрированных;
//  4. «Статусы»      — крест origin × supplier_status × wb_status;
//  5. «Детализация»  — старые активные и мигрированные задания (для разбора);
//  6. «Методика»     — источники, пороги, часовые пояса, ограничения.
//
// Стили повторяют stock-warehouse-report/report.go: синяя шапка #4472C4 bold white,
// freeze panes, autofilter.
package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"
)

// originLabel — человекочитаемые названия классов происхождения.
func originLabel(origin string) string {
	switch origin {
	case "native":
		return "Новый FBS"
	case "migrated":
		return "Мигрирован FBO→FBS"
	case "no_stats_row":
		return "Нет строки в статистике"
	}
	return origin
}

// exportXLSX строит книгу и сохраняет по path.
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
		Font:      &excelize.Font{Bold: true, Italic: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDEBF7"}},
	})

	wrap := func(sheet string, headers []string, row int) {
		for i, h := range headers {
			xSet(f, sheet, row, i+1, h)
			f.SetCellStyle(sheet, cellName(i+1, row), cellName(i+1, row), headerStyle)
		}
	}

	// ── 1. Сводка ──
	s := "Сводка"
	f.SetSheetName("Sheet1", s)
	f.SetCellValue(s, "A1", "СБОРОЧНЫЕ ЗАДАНИЯ FBS — ОБЪЁМ, СВЕЖЕСТЬ, ПРОИСХОЖДЕНИЕ")
	f.SetCellStyle(s, "A1", "F1", titleStyle)
	f.MergeCell(s, "A1", "F1")
	f.SetCellValue(s, "A2", fmt.Sprintf("Снимок: %s … %s | пороги: migrated = лаг > %d ч, «старое» = > %d дн | сгенерировано %s",
		data.Coverage.MinDate, data.Coverage.MaxDate, cfg.LagHours, cfg.OldDays, nowMoscow().Format("02.01.2006 15:04")))
	r := 4
	wrap(s, []string{"Происхождение", "Заданий", "Активных (new/confirm)", "Выкуплено", "Лаг, ч (медиана)", "Доля, %"}, r)
	var totalTasks int64
	for _, o := range data.Origins {
		totalTasks += o.Tasks
	}
	for _, o := range data.Origins {
		r++
		xSet(f, s, r, 1, originLabel(o.Origin))
		xSet(f, s, r, 2, o.Tasks)
		xSet(f, s, r, 3, o.Active)
		xSet(f, s, r, 4, o.Sold)
		lag := "—"
		if o.MedLagH >= 0 {
			lag = fmt.Sprintf("%.1f", o.MedLagH)
		}
		xSet(f, s, r, 5, lag)
		pct := 0.0
		if totalTasks > 0 {
			pct = float64(o.Tasks) / float64(totalTasks) * 100
		}
		xSet(f, s, r, 6, fmt.Sprintf("%.1f", pct))
	}
	r += 2
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "Ключевые числа")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("F%d", r), boldStyle)
	for _, kv := range [][2]interface{}{
		{"Активных заданий (new/confirm)", data.ActiveCount},
		{fmt.Sprintf("Активных старше %d дней", cfg.OldDays), data.OldActiveCount},
		{"Заданий найдено в Statistics API, %", fmt.Sprintf("%.1f", data.Coverage.MatchPct())},
		{"Свежесть таблицы orders", data.Coverage.OrdersFresh},
	} {
		r++
		xSet(f, s, r, 1, kv[0])
		xSet(f, s, r, 2, kv[1])
	}

	// ── 2. Динамика ──
	s = "Динамика"
	f.NewSheet(s)
	r = 1
	wrap(s, []string{"Дата создания (МСК)", "Новые FBS", "Мигрированные", "Нет в статистике", "Всего"}, r)
	var sumN, sumM, sumNo int64
	for _, d := range data.Daily {
		r++
		xSet(f, s, r, 1, d.Day)
		xSet(f, s, r, 2, d.Native)
		xSet(f, s, r, 3, d.Migrated)
		xSet(f, s, r, 4, d.NoStatsRow)
		xSet(f, s, r, 5, d.Total())
		sumN += d.Native
		sumM += d.Migrated
		sumNo += d.NoStatsRow
	}
	r++
	xSet(f, s, r, 1, "ИТОГО")
	xSet(f, s, r, 2, sumN)
	xSet(f, s, r, 3, sumM)
	xSet(f, s, r, 4, sumNo)
	xSet(f, s, r, 5, sumN+sumM+sumNo)
	f.SetCellStyle(s, fmt.Sprintf("A%d", 2), fmt.Sprintf("E%d", r), totalStyle)
	f.SetCellStyle(s, "A1", "E1", headerStyle)

	// ── 3. Возраст ──
	s = "Возраст"
	f.NewSheet(s)
	f.SetCellValue(s, "A1", "ВОЗРАСТ АКТИВНЫХ ЗАДАНИЙ (new / confirm)")
	f.SetCellStyle(s, "A1", "F1", titleStyle)
	f.MergeCell(s, "A1", "F1")
	r = 3
	wrap(s, []string{"Статус продавца", "< 1 дн", "1–3 дн", "3–7 дн", "> 7 дн", "Всего"}, r)
	for _, a := range data.AgeByStatus {
		r++
		xSet(f, s, r, 1, a.SupplierStatus)
		xSet(f, s, r, 2, a.Lt1d)
		xSet(f, s, r, 3, a.D1to3d)
		xSet(f, s, r, 4, a.D3to7d)
		xSet(f, s, r, 5, a.Gt7d)
		xSet(f, s, r, 6, a.Total)
	}
	// Гистограмма лагов мигрированных (бакеты в часах).
	r += 2
	f.SetCellValue(s, fmt.Sprintf("A%d", r), "ЛАГ «СОЗДАНИЕ ЗАДАНИЯ − ЗАКАЗ» ДЛЯ МИГРИРОВАННЫХ (Ч)")
	f.SetCellStyle(s, fmt.Sprintf("A%d", r), fmt.Sprintf("F%d", r), boldStyle)
	r++
	lagBuckets := bucketize(data.MigratedLags, []float64{48, 96, 240, 480, 720})
	wrap(s, []string{"Лаг", "Заданий"}, r)
	for _, b := range lagBuckets {
		r++
		xSet(f, s, r, 1, b.Label)
		xSet(f, s, r, 2, b.Count)
	}

	// ── 4. Статусы ──
	s = "Статусы"
	f.NewSheet(s)
	r = 1
	wrap(s, []string{"Происхождение", "Статус продавца", "Статус WB", "Заданий"}, r)
	for _, st := range data.Statuses {
		r++
		xSet(f, s, r, 1, originLabel(st.Origin))
		xSet(f, s, r, 2, st.SupplierStatus)
		xSet(f, s, r, 3, st.WBStatus)
		xSet(f, s, r, 4, st.N)
	}
	f.SetCellStyle(s, "A1", "D1", headerStyle)

	// ── 5. Детализация ──
	s = "Детализация"
	f.NewSheet(s)
	r = 1
	wrap(s, []string{"rid", "Создано (МСК)", "Возраст, дн", "Статус продавца", "Статус WB",
		"Происхождение", "Лаг, ч", "Схема в статистике", "Артикул продавца", "Код 1С",
		"nm_id", "Предмет", "Поставка", "Можно отменить", "Цена, руб"}, r)
	for _, d := range data.Detail {
		r++
		xSet(f, s, r, 1, d.Rid)
		xSet(f, s, r, 2, d.CreatedMSK)
		xSet(f, s, r, 3, round1(d.AgeDays))
		xSet(f, s, r, 4, d.SupplierStatus)
		xSet(f, s, r, 5, d.WBStatus)
		xSet(f, s, r, 6, originLabel(d.Origin))
		if d.LagH != nil {
			xSet(f, s, r, 7, round1(*d.LagH))
		} else {
			xSet(f, s, r, 7, "—")
		}
		xSet(f, s, r, 8, d.StatsWHType)
		xSet(f, s, r, 9, d.VendorCode)
		xSet(f, s, r, 10, d.Article)
		xSet(f, s, r, 11, d.NmID)
		xSet(f, s, r, 12, d.SubjectName)
		xSet(f, s, r, 13, d.SupplyID)
		xSet(f, s, r, 14, d.IsCancellable)
		xSet(f, s, r, 15, d.PriceRub)
	}
	f.SetCellStyle(s, "A1", "O1", headerStyle)
	if len(data.Detail) > 0 {
		f.SetPanes(s, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 1,
			TopLeftCell: "A2", ActivePane: "bottomLeft"})
		f.AutoFilter(s, fmt.Sprintf("A1:O%d", len(data.Detail)+1), nil)
	}

	// ── 6. Методика ──
	s = "Методика"
	f.NewSheet(s)
	lines := []string{
		"МЕТОДИКА И ОГРАНИЧЕНИЯ",
		"",
		fmt.Sprintf("Источники: public.fbs_orders + fbs_orders_status — разовый снимок GET /api/v3/orders +"),
		("POST /api/v3/orders/status (fetch-fbs-orders.sh, 16.08.2026); public.orders — Statistics API"),
		("«Заказы» (/api/v1/supplier/orders); public.operational_sales — выкуп; public.cards — справочник."),
		"",
		fmt.Sprintf("origin: native — лаг created_at−order_date ≤ %d ч (заказ сразу размещён как FBS);", cfg.LagHours),
		fmt.Sprintf("migrated — лаг > %d ч: сборочное задание создано намного позже заказа;", cfg.LagHours),
		("в Statistics API такие заказы числятся за «Склад WB» (FBO). no_stats_row — строки в Statistics API нет."),
		"",
		("Часовые пояса: created_at (API v3) — UTC RFC3339; order_date (Statistics) — МСК без смещения."),
		("Все даты в отчёте приведены к МСК. Возраст считается на момент генерации отчёта."),
		"",
		("Ограничения:"),
		("• API v3 не отдаёт дату «поступления в работу» (processedAt нет) — свежесть оценивается"),
		("  статусами supplier_status (new → confirm → complete) и возрастом created_at."),
		("• no_stats_row (~20% заданий): целые корзины отсутствуют в Statistics API при полном"),
		("  жизненном цикле заданий — природа неясна, класс выделен отдельно."),
		("• Statistics API хранит заказы 90 дней; таблица orders пополняется ежедневным загрузчиком."),
		("• Таблицы fbs_orders* — одноразовые, после анализа можно DROP (import-fbs-snapshot.sh)."),
	}
	for i, ln := range lines {
		xSet(f, s, i+1, 1, ln)
	}

	f.SetActiveSheet(0)
	return f.SaveAs(path)
}

// ── Хелперы ──

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

// mskLoc — часовой пояс МСК (фиксированный UTC+3, без базы tzdata).
func mskLoc() *time.Location {
	return time.FixedZone("MSK", 3*3600)
}

// nowMoscow — текущее время в МСК.
func nowMoscow() time.Time { return time.Now().In(mskLoc()) }

// parseMSK парсит «YYYY-MM-DD HH:MM» как наивное МСК-время.
func parseMSK(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04", s, mskLoc())
}

// round1 округляет до одного знака.
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// Bucket — бакет гистограммы.
type Bucket struct {
	Label string
	Count int64
}

// bucketize раскладывает значения по диапазонам [edges[i-1], edges[i]) с подписями;
// последний бакет — «≥ последняя граница».
func bucketize(values []float64, edges []float64) []Bucket {
	sorted := append([]float64(nil), edges...)
	sort.Float64s(sorted)
	labels := make([]string, len(sorted)+1)
	labels[0] = fmt.Sprintf("< %g ч", sorted[0])
	for i := 1; i < len(sorted); i++ {
		labels[i] = fmt.Sprintf("%g–%g ч", sorted[i-1], sorted[i])
	}
	labels[len(sorted)] = fmt.Sprintf("≥ %g ч", sorted[len(sorted)-1])
	out := make([]Bucket, len(labels))
	for i, l := range labels {
		out[i] = Bucket{Label: l}
	}
	for _, v := range values {
		idx := len(sorted)
		for i, e := range sorted {
			if v < e {
				idx = i
				break
			}
		}
		out[idx].Count++
	}
	return out
}
