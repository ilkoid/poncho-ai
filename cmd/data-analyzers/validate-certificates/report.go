// report.go — console output, CSV export, and XLSX export for certificate validation.
package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// Verdict constants — single-source-of-truth for result categorization.
const (
	VerdictOK            = "✅ Ок"
	VerdictExpiringSoon  = "⚠️ Скоро истекает"
	VerdictExpired       = "❌ Протух"
	VerdictNotFound      = "❓ Не найден"
	VerdictApproxMatch   = "❓ Неточный"
	VerdictNonRU         = "🔧 KG/KZ"
	VerdictError         = "⛔ Ошибка"
)

// ValidationResult combines local data with FSA lookup result.
type ValidationResult struct {
	Article           string
	GoodName          string
	CertificateType   string
	CertificateNumber string
	LocalEnd          string
	Found             bool
	FSAStatus         string // "Действующий", "Архивный", etc.
	FSAEndDate        string
	DateMatch         string // "✓", "✗", "—"
	DaysRemaining     int    // negative = expired
	Error             string // non-empty if lookup failed
	// New fields for enhanced XLSX.
	OneCType     string // 1C type: Обувь/Одежда/Аксессуары
	OneCCategory string // 1C category level 1 name
	WBSubject    string // WB subject name
	ApproxMatch  bool   // true if findExactOrFirst took first result, not exact match
}

// verdict returns the single-category verdict for a validation result.
func verdict(r ValidationResult) string {
	if r.Error != "" {
		return VerdictError
	}
	if r.FSAStatus == "Пропущен" {
		return VerdictNonRU
	}
	if r.ApproxMatch {
		return VerdictApproxMatch
	}
	if !r.Found {
		return VerdictNotFound
	}
	switch r.FSAStatus {
	case "Действующий", "Возобновлён", "Продлён":
		if r.DaysRemaining > 30 {
			return VerdictOK
		}
		if r.DaysRemaining > 0 {
			return VerdictExpiringSoon
		}
		return VerdictExpired
	case "Архивный", "Прекращён", "Аннулирован", "Окончание срока":
		return VerdictExpired
	default:
		// Unknown active status — treat as needing review.
		return VerdictNotFound
	}
}

// printDryRun shows certificates that would be checked.
func printDryRun(certs []CertRecord) {
	sep := strings.Repeat("─", 80)
	fmt.Println("\n" + sep)
	fmt.Println("  СЕРТИФИКАТЫ ДЛЯ ПРОВЕРКИ (--dry-run)")
	fmt.Println(sep)
	fmt.Printf("  %-12s  %-30s  %-20s  %s\n", "Артикул", "Тип", "Номер", "Действует до")
	fmt.Println(sep)

	seen := make(map[string]bool)
	for _, c := range certs {
		if seen[c.CertificateNumber] {
			continue
		}
		seen[c.CertificateNumber] = true
		endDate := formatLocalDate(c.LocalEnd)
		num := c.CertificateNumber
		if len(num) > 18 {
			num = num[:15] + "..."
		}
		fmt.Printf("  %-12s  %-30s  %-20s  %s\n",
			c.Article, c.CertificateType, num, endDate)
	}
	fmt.Println(sep)
	fmt.Printf("  Уникальных номеров: %d  (всего записей: %d)\n", len(seen), len(certs))
}

// printReport outputs validation results as a formatted table.
func printReport(results []ValidationResult) {
	sep := strings.Repeat("═", 120)
	dash := strings.Repeat("─", 120)

	fmt.Println("\n" + sep)
	fmt.Println("  РЕЗУЛЬТАТЫ ВАЛИДАЦИИ СЕРТИФИКАТОВ — ФГИС РОСАККРЕДИТАЦИИ")
	fmt.Println(sep)

	// Table header.
	fmt.Printf("  %-12s  %-18s  %-14s  %-12s  %-10s  %-8s  %-20s  %s\n",
		"Артикул", "Номер", "Статус ФСА", "Дата ФСА", "Совп.", "Дни", "Вердикт", "Тип")
	fmt.Println(dash)

	var active, expired, notFound, errors, skipped, approx int
	for _, r := range results {
		v := verdict(r)

		// Count categories.
		switch v {
		case VerdictOK, VerdictExpiringSoon:
			active++
		case VerdictExpired:
			expired++
		case VerdictNotFound:
			notFound++
		case VerdictNonRU:
			skipped++
		case VerdictError:
			errors++
		case VerdictApproxMatch:
			approx++
		}

		// Days remaining.
		var daysStr string
		switch {
		case r.Error != "":
			daysStr = "—"
		case r.DaysRemaining > 0:
			daysStr = fmt.Sprintf("%d", r.DaysRemaining)
		case r.DaysRemaining == 0:
			daysStr = "сег."
		default:
			daysStr = fmt.Sprintf("%d†", -r.DaysRemaining)
		}

		status := r.FSAStatus
		if r.Error != "" {
			status = "ОШИБКА"
		} else if !r.Found && r.FSAStatus != "Пропущен" {
			status = "Не найден"
		}

		num := r.CertificateNumber
		if utf8.RuneCountInString(num) > 18 {
			num = string([]rune(num)[:15]) + "..."
		}

		certType := "С"
		if isDeclaration(r.CertificateType) {
			certType = "Д"
		}

		fmt.Printf("  %-12s  %-18s  %-14s  %-12s  %-10s  %-8s  %-20s  %s\n",
			r.Article, num, status, r.FSAEndDate, r.DateMatch, daysStr, v, certType)
	}

	fmt.Println(sep)

	// Summary.
	total := len(results)
	fmt.Printf("  Всего: %d  |  Ок: %d  |  Истёкших: %d  |  Не найдено: %d  |  Неточных: %d  |  KG/KZ: %d  |  Ошибок: %d\n",
		total, active, expired, notFound, approx, skipped, errors)
	fmt.Println(sep)

	// Expiring soon warning.
	var expiring []ValidationResult
	for _, r := range results {
		if v := verdict(r); v == VerdictExpiringSoon {
			expiring = append(expiring, r)
		}
	}
	if len(expiring) > 0 {
		fmt.Printf("\n  ⚠  СКОРО ИСТЕКАЮТ (%d шт., ≤30 дней):\n", len(expiring))
		for _, r := range expiring {
			fmt.Printf("     %-12s  %-20s  действует до %s  (осталось %d дн.)\n",
				r.Article, r.CertificateNumber, r.FSAEndDate, r.DaysRemaining)
		}
	}

	// Expired list (top-20 by days overdue).
	var expiredList []ValidationResult
	for _, r := range results {
		if v := verdict(r); v == VerdictExpired {
			expiredList = append(expiredList, r)
		}
	}
	if len(expiredList) > 0 {
		sort.Slice(expiredList, func(i, j int) bool {
			return expiredList[i].DaysRemaining < expiredList[j].DaysRemaining
		})
		limit := len(expiredList)
		if limit > 20 {
			limit = 20
		}
		fmt.Printf("\n  †  ИСТЁКШИЕ (%d шт., топ-%d):\n", len(expiredList), limit)
		for _, r := range expiredList[:limit] {
			fmt.Printf("     %-12s  %-20s  истёк %s  (%d дн. назад)\n",
				r.Article, r.CertificateNumber, r.FSAEndDate, -r.DaysRemaining)
		}
		if len(expiredList) > 20 {
			fmt.Printf("     ... и ещё %d\n", len(expiredList)-20)
		}
	}
}

// exportCSV writes validation results to a CSV file.
func exportCSV(results []ValidationResult, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create CSV: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Header row.
	_ = w.Write([]string{
		"article", "good_name", "onec_type", "onec_category", "wb_subject",
		"cert_type", "cert_number",
		"local_end", "fsa_status", "fsa_end_date",
		"date_match", "days_remaining", "verdict", "approx_match", "error",
	})

	for _, r := range results {
		certType := "С"
		if isDeclaration(r.CertificateType) {
			certType = "Д"
		}
		approxStr := ""
		if r.ApproxMatch {
			approxStr = "да"
		}
		_ = w.Write([]string{
			r.Article,
			r.GoodName,
			r.OneCType,
			r.OneCCategory,
			r.WBSubject,
			certType,
			r.CertificateNumber,
			r.LocalEnd,
			r.FSAStatus,
			r.FSAEndDate,
			r.DateMatch,
			fmt.Sprintf("%d", r.DaysRemaining),
			verdict(r),
			approxStr,
			r.Error,
		})
	}
	return w.Error()
}

// formatLocalDate converts ISO date "2023-05-25T00:00:00" to "25.05.2023".
func formatLocalDate(iso string) string {
	if iso == "" {
		return "—"
	}
	if t, err := time.Parse("2006-01-02T15:04:05", iso); err == nil {
		return t.Format("02.01.2006")
	}
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("02.01.2006")
	}
	return iso
}

// normalizeDate extracts date portion from various formats to YYYY-MM-DD.
func normalizeDate(s string) string {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02", "02.01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// daysUntilExpiry returns days until the given date (negative if expired).
func daysUntilExpiry(dateStr string) int {
	for _, layout := range []string{"2006-01-02", "02.01.2006", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return int(time.Until(t).Hours() / 24)
		}
	}
	return 0
}

// compareDates checks if local and FSA dates match.
func compareDates(localEnd, fsaEnd string) string {
	if localEnd == "" || fsaEnd == "" {
		return "—"
	}
	if normalizeDate(localEnd) == normalizeDate(fsaEnd) {
		return "✓"
	}
	return "✗"
}

// exportXLSX writes validation results to an Excel file with conditional formatting.
func exportXLSX(results []ValidationResult, path string) error {
	f := excelize.NewFile()
	sheet := "Сертификаты"
	f.SetSheetName("Sheet1", sheet)

	// Styles — for full-row coloring by verdict.
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	activeStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"C6EFCE"}},
		Font: &excelize.Font{Color: "006100"},
	})
	expiringStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFEB9C"}},
		Font: &excelize.Font{Color: "9C5700"},
	})
	expiredStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})
	notFoundStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F4B084"}},
		Font: &excelize.Font{Color: "7F4700"},
	})
	nonRUStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Font: &excelize.Font{Color: "595959"},
	})
	errorStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D5A6E6"}},
		Font: &excelize.Font{Color: "4A235A"},
	})

	// verdictStyle maps verdict to style.
	verdictStyle := map[string]int{
		VerdictOK:           activeStyle,
		VerdictExpiringSoon: expiringStyle,
		VerdictExpired:      expiredStyle,
		VerdictNotFound:     notFoundStyle,
		VerdictApproxMatch:  expiringStyle,
		VerdictNonRU:        nonRUStyle,
		VerdictError:        errorStyle,
	}

	// Headers (14 columns).
	headers := []string{
		"Артикул", "Название", "Тип 1С", "Категория 1С", "Предмет WB",
		"Тип док-та", "Номер", "Дата 1С",
		"Статус ФСА", "Дата ФСА", "Совп. дат", "Дней до конца",
		"Вердикт", "Ошибка",
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}

	// Column widths.
	widths := map[string]float64{
		"A": 14, "B": 35, "C": 14, "D": 22, "E": 22,
		"F": 12, "G": 30, "H": 14,
		"I": 16, "J": 14, "K": 10, "L": 14,
		"M": 20, "N": 30,
	}
	for col, w := range widths {
		f.SetColWidth(sheet, col, col, w)
	}

	// Data rows.
	for i, r := range results {
		row := i + 2
		v := verdict(r)
		certType := "С"
		if isDeclaration(r.CertificateType) {
			certType = "Д"
		}

		xSet(f, sheet, row, 1, r.Article)
		xSet(f, sheet, row, 2, r.GoodName)
		xSet(f, sheet, row, 3, r.OneCType)
		xSet(f, sheet, row, 4, r.OneCCategory)
		xSet(f, sheet, row, 5, r.WBSubject)
		xSet(f, sheet, row, 6, certType)
		xSet(f, sheet, row, 7, r.CertificateNumber)
		xSet(f, sheet, row, 8, formatLocalDate(r.LocalEnd))
		xSet(f, sheet, row, 9, r.FSAStatus)
		xSet(f, sheet, row, 10, r.FSAEndDate)
		xSet(f, sheet, row, 11, r.DateMatch)
		xSet(f, sheet, row, 12, r.DaysRemaining)
		xSet(f, sheet, row, 13, v)
		xSet(f, sheet, row, 14, r.Error)

		// Color entire row (columns A–N) by verdict.
		style, ok := verdictStyle[v]
		if !ok {
			style = notFoundStyle
		}
		startCell, _ := excelize.CoordinatesToCellName(1, row)
		endCell, _ := excelize.CoordinatesToCellName(14, row)
		f.SetCellStyle(sheet, startCell, endCell, style)
	}

	// Add Summary sheet.
	addSummarySheet(f, results)

	return f.SaveAs(path)
}

// addSummarySheet adds a "Сводка" sheet with verdict counts and examples.
func addSummarySheet(f *excelize.File, results []ValidationResult) {
	sheet := "Сводка"
	f.NewSheet(sheet)

	// Styles for summary.
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})

	// Title.
	f.SetCellValue(sheet, "A1", "СВОДКА ВАЛИДАЦИИ СЕРТИФИКАТОВ")
	f.SetCellStyle(sheet, "A1", "A1", titleStyle)

	// Count by verdict.
	counts := make(map[string]int)
	var order []string
	for _, v := range []string{
		VerdictExpired, VerdictExpiringSoon, VerdictOK,
		VerdictNotFound, VerdictApproxMatch, VerdictNonRU, VerdictError,
	} {
		order = append(order, v)
		counts[v] = 0
	}
	for _, r := range results {
		counts[verdict(r)]++
	}

	// Summary table: verdict + count.
	row := 3
	sumHeaders := []string{"Вердикт", "Кол-во"}
	for i, h := range sumHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, row)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	row++

	for _, v := range order {
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), v)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), counts[v])
		row++
	}

	// Total.
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "ВСЕГО")
	f.SetCellValue(sheet, fmt.Sprintf("B%d", row), len(results))
	row += 2

	// Action items: show details for expired and expiring-soon.
	actionVerdicts := []struct {
		title   string
		verdict string
	}{
		{"❌ ПРОТУКШИЕ — ТРЕБУЕТСЯ ЗАМЕНА", VerdictExpired},
		{"⚠️ СКОРО ИСТЕКАЮТ — ПЛАНИРУЙТЕ ЗАМЕНУ", VerdictExpiringSoon},
		{"❓ НЕ НАЙДЕНЫ — ПРОВЕРИТЕ ВРУЧНУЮ", VerdictNotFound},
	}

	for _, action := range actionVerdicts {
		var items []ValidationResult
		for _, r := range results {
			if verdict(r) == action.verdict {
				items = append(items, r)
			}
		}
		if len(items) == 0 {
			continue
		}

		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), action.title)
		f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), titleStyle)
		row++

		// Detail headers.
		detailHeaders := []string{"Артикул", "Название", "Категория 1С", "Предмет WB", "Номер", "Дата ФСА", "Дней"}
		for i, h := range detailHeaders {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			f.SetCellValue(sheet, cell, h)
			f.SetCellStyle(sheet, cell, cell, headerStyle)
		}
		row++

		for _, r := range items {
			f.SetCellValue(sheet, fmt.Sprintf("A%d", row), r.Article)
			f.SetCellValue(sheet, fmt.Sprintf("B%d", row), r.GoodName)
			f.SetCellValue(sheet, fmt.Sprintf("C%d", row), r.OneCCategory)
			f.SetCellValue(sheet, fmt.Sprintf("D%d", row), r.WBSubject)
			f.SetCellValue(sheet, fmt.Sprintf("E%d", row), r.CertificateNumber)
			f.SetCellValue(sheet, fmt.Sprintf("F%d", row), r.FSAEndDate)
			f.SetCellValue(sheet, fmt.Sprintf("G%d", row), r.DaysRemaining)
			row++
		}
		row++ // blank row between sections
	}

	// Column widths for summary.
	f.SetColWidth(sheet, "A", "A", 40)
	f.SetColWidth(sheet, "B", "B", 14)
	f.SetColWidth(sheet, "C", "C", 22)
	f.SetColWidth(sheet, "D", "D", 22)
	f.SetColWidth(sheet, "E", "E", 35)
	f.SetColWidth(sheet, "F", "F", 14)
	f.SetColWidth(sheet, "G", "G", 10)
}

// xSet is a helper for setting cell values in excelize.
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
