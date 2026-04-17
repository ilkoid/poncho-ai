// Package analytics предоставляет вычислительные функции для аналитических утилит.
//
// Moving Average (MA) — скользящее среднее за N дней перед указанной датой.
// Чистые функции без зависимостей от БД или API (Rule 6: pkg/ = library).
package analytics

import "time"

// ComputeMA calculates the average of daily values over the N days before refDate.
// Zero-value days (missing keys in dayMap) are counted as 0.
// Returns nil if fewer than minDays have non-zero data in the window.
func ComputeMA(dayMap map[string]int, refDate time.Time, window, minDays int) *float64 {
	var sum float64
	var daysWithData int

	for i := 1; i <= window; i++ {
		d := refDate.AddDate(0, 0, -i).Format("2006-01-02")
		v := dayMap[d] // returns 0 if key missing — zero-sales day
		sum += float64(v)
		if v > 0 {
			daysWithData++
		}
	}

	if daysWithData < minDays {
		return nil
	}

	avg := sum / float64(window)
	return &avg
}
