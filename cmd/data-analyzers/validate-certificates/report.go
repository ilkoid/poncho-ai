// report.go — console output and CSV export for certificate validation.
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
	sep := strings.Repeat("═", 105)
	dash := strings.Repeat("─", 105)

	fmt.Println("\n" + sep)
	fmt.Println("  РЕЗУЛЬТАТЫ ВАЛИДАЦИИ СЕРТИФИКАТОВ — ФГИС РОСАККРЕДИТАЦИИ")
	fmt.Println(sep)

	// Table header.
	fmt.Printf("  %-12s  %-18s  %-12s  %-12s  %-8s  %-8s  %s\n",
		"Артикул", "Номер", "Статус ФСА", "Дата ФСА", "Совп.", "Дни", "Тип")
	fmt.Println(dash)

	var active, expired, notFound, errors, skipped int
	for _, r := range results {
		// Days remaining.
		var daysStr string
		switch {
		case r.Error != "":
			daysStr = "—"
			errors++
		case r.DaysRemaining > 0:
			daysStr = fmt.Sprintf("%d", r.DaysRemaining)
		case r.DaysRemaining == 0:
			daysStr = "сег."
		default:
			daysStr = fmt.Sprintf("%d†", -r.DaysRemaining)
		}

		// Status.
		status := r.FSAStatus
		if r.Error != "" {
			status = "ОШИБКА"
		} else if r.FSAStatus == "Пропущен" {
			skipped++
		} else if !r.Found {
			status = "Не найден"
			notFound++
		} else {
			switch r.FSAStatus {
			case "Действующий", "Возобновлён":
				active++
			case "Архивный":
				expired++
			}
		}

		num := r.CertificateNumber
		if utf8.RuneCountInString(num) > 18 {
			num = string([]rune(num)[:15]) + "..."
		}

		certType := "С"
		if isDeclaration(r.CertificateType) {
			certType = "Д"
		}

		fmt.Printf("  %-12s  %-18s  %-12s  %-12s  %-8s  %-8s  %s\n",
			r.Article, num, status, r.FSAEndDate, r.DateMatch, daysStr, certType)
	}

	fmt.Println(sep)

	// Summary.
	total := len(results)
	fmt.Printf("  Всего: %d  |  Действующих: %d  |  Истёкших: %d  |  Не найдено: %d  |  Пропущено: %d  |  Ошибок: %d\n",
		total, active, expired, notFound, skipped, errors)
	fmt.Println(sep)

	// Expiring soon warning.
	var expiring []ValidationResult
	for _, r := range results {
		if r.Found && r.DaysRemaining > 0 && r.DaysRemaining <= 30 {
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
		if r.Found && r.DaysRemaining < 0 {
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
		"article", "good_name", "cert_type", "cert_number",
		"local_end", "fsa_status", "fsa_end_date",
		"date_match", "days_remaining", "error",
	})

	for _, r := range results {
		_ = w.Write([]string{
			r.Article,
			r.GoodName,
			r.CertificateType,
			r.CertificateNumber,
			r.LocalEnd,
			r.FSAStatus,
			r.FSAEndDate,
			r.DateMatch,
			fmt.Sprintf("%d", r.DaysRemaining),
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

	// Styles.
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	activeStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"C6EFCE"}},
		Font: &excelize.Font{Color: "006100"},
	})
	expiredStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})
	notFoundStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFEB9C"}},
		Font: &excelize.Font{Color: "9C5700"},
	})
	errorStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Font: &excelize.Font{Color: "595959"},
	})

	// Headers.
	headers := []string{"Артикул", "Название", "Тип", "Номер", "Дата 1С",
		"Статус ФСА", "Дата ФСА", "Совп.", "Дни", "Ошибка"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}

	// Column widths.
	widths := map[string]float64{"A": 14, "B": 35, "C": 14, "D": 28, "E": 14,
		"F": 16, "G": 14, "H": 10, "I": 10, "J": 30}
	for col, w := range widths {
		f.SetColWidth(sheet, col, col, w)
	}

	// Data rows.
	for i, r := range results {
		row := i + 2
		xSet(f, sheet, row, 1, r.Article)
		xSet(f, sheet, row, 2, r.GoodName)

		certType := "С"
		if isDeclaration(r.CertificateType) {
			certType = "Д"
		}
		xSet(f, sheet, row, 3, certType)
		xSet(f, sheet, row, 4, r.CertificateNumber)
		xSet(f, sheet, row, 5, formatLocalDate(r.LocalEnd))
		xSet(f, sheet, row, 6, r.FSAStatus)
		xSet(f, sheet, row, 7, r.FSAEndDate)
		xSet(f, sheet, row, 8, r.DateMatch)
		xSet(f, sheet, row, 9, r.DaysRemaining)
		xSet(f, sheet, row, 10, r.Error)

		// Conditional formatting: style the status column.
		var style int
		switch {
		case r.Error != "":
			style = errorStyle
		case !r.Found:
			style = notFoundStyle
		case r.FSAStatus == "Действующий" || r.FSAStatus == "Возобновлён":
			style = activeStyle
		case r.FSAStatus == "Архивный" || r.FSAStatus == "Прекращён" || r.FSAStatus == "Аннулирован":
			style = expiredStyle
		default:
			style = notFoundStyle
		}
		statusCell, _ := excelize.CoordinatesToCellName(6, row)
		f.SetCellStyle(sheet, statusCell, statusCell, style)
	}

	return f.SaveAs(path)
}

// xSet is a helper for setting cell values in excelize.
func xSet(f *excelize.File, sheet string, row, col int, value interface{}) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
