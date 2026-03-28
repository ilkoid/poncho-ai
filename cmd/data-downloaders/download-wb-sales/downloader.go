// Package main provides WB Sales Downloader utility.
// This file contains download orchestration logic (SRP).
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SalesIterator is the interface for downloading sales data.
// Allows mocking in tests.
type SalesIterator interface {
	ReportDetailByPeriodIteratorWithTime(
		ctx context.Context,
		baseURL string,
		rateLimit int,
		burst int,
		dateFrom string,
		dateTo string,
		callback func([]wb.RealizationReportRow) error,
	) (int, error)
	ReportDetailByPeriodIterator(
		ctx context.Context,
		baseURL string,
		rateLimit int,
		burst int,
		dateFrom int,
		dateTo int,
		callback func([]wb.RealizationReportRow) error,
	) (int, error)
}

// DownloadConfig holds configuration for download process.
type DownloadConfig struct {
	Client    SalesIterator
	Repo      *sqlite.SQLiteSalesRepository
	RateLimit int
	Burst     int
}

// DownloadResult holds statistics after download completion.
type DownloadResult struct {
	TotalRows    int
	TotalPages   int
	Duration     time.Duration
	PeriodsCount int
}

// DownloadSales downloads sales data for all date ranges.
// Smart resume: adjusts periods based on last record in database.
func DownloadSales(ctx context.Context, cfg DownloadConfig, ranges []DateRange, resume bool) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{
		PeriodsCount: len(ranges),
	}

	// Smart resume: get last and first records from database
	var lastDT, firstDT time.Time
	if resume {
		var err error
		lastDT, err = cfg.Repo.GetLastSaleDT(ctx)
		if err != nil {
			return result, fmt.Errorf("get last sale_dt: %w", err)
		}
		firstDT, err = cfg.Repo.GetFirstSaleDT(ctx)
		if err != nil {
			return result, fmt.Errorf("get first sale_dt: %w", err)
		}
		if !lastDT.IsZero() {
			fmt.Printf("📊 Найдена последняя запись: %s\n",
				lastDT.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("📥 База пуста - загружаем с начала")
		}
	}

	// Filter and adjust periods
	var adjustedRanges []DateRange
	skippedCount := 0

	for _, dr := range ranges {
		if resume {
			newFrom, shouldSkip, skippedUntil := AdjustPeriodForResume(dr.From, dr.To, firstDT, lastDT)

			if shouldSkip {
				skippedCount++
				fmt.Printf("  ⏭️  Пропущен: %s\n", dr.String())
				// Update lastDT for next period
				lastDT = skippedUntil
				continue
			}

			if newFrom.After(dr.From) {
				adjustedRange := DateRange{From: newFrom, To: dr.To}
				fmt.Printf("  🔄 %s → %s\n",
					dr.String(), adjustedRange.String())
				dr = adjustedRange
			}
		}
		adjustedRanges = append(adjustedRanges, dr)
	}

	result.PeriodsCount = len(adjustedRanges)
	if skippedCount > 0 {
		fmt.Printf("⏭️  Пропущено периодов: %d\n", skippedCount)
	}

	if len(adjustedRanges) == 0 {
		fmt.Println("✅ Все периоды уже загружены!")
		return result, nil
	}

	// Load only adjusted periods
	for i, dr := range adjustedRanges {
		// resume=false because filtering is no longer needed!
		periodResult, err := downloadPeriod(ctx, cfg, dr, i+1, len(adjustedRanges), false)
		if err != nil {
			return result, fmt.Errorf("period %s: %w", dr.String(), err)
		}
		result.TotalRows += periodResult.Rows
		result.TotalPages += periodResult.Pages
	}

	result.Duration = time.Since(start)
	return result, nil
}

// periodResult holds statistics for a single period download.
type periodResult struct {
	Rows  int
	Pages int

	// Timing metrics
	TotalDuration   time.Duration
	APIWaitTime     time.Duration // Time waiting for API responses
	DBWriteTime     time.Duration // Time writing to database
	ProcessingTime  time.Duration // Time processing/splitting data
}

// downloadPeriod downloads sales data for a single date range.
// Uses wb.Client.ReportDetailByPeriodIterator or ReportDetailByPeriodIteratorWithTime
// depending on whether the range includes time components.
func downloadPeriod(ctx context.Context, cfg DownloadConfig, dr DateRange, periodNum, totalPeriods int, resume bool) (*periodResult, error) {
	result := &periodResult{}
	periodStart := time.Now()

	fmt.Printf("\n[%d/%d] Интервал %s\n", periodNum, totalPeriods, dr.String())
	fmt.Printf("  🕐 Начало: %s\n", periodStart.Format("2006-01-02 15:04:05"))

	// Use iterator from wb.Client - handles pagination automatically
	// Statistics API base URL (endpoint path added by client)
	statsAPIURL := "https://statistics-api.wildberries.ru"

	// Check if we need time-based iterator
	if dr.HasTime() {
		// Use time-based iterator for partial days (e.g., 00:00-12:00)
		fmt.Printf("  🔧 Time-based mode: %s → %s\n", dr.FromRFC3339(), dr.ToRFC3339())
		_, err := cfg.Client.ReportDetailByPeriodIteratorWithTime(
			ctx,
			statsAPIURL,
			cfg.RateLimit,
			cfg.Burst,
			dr.FromRFC3339(),
			dr.ToRFC3339(),
			func(rows []wb.RealizationReportRow) error {
				return saveRows(ctx, cfg.Repo, rows, resume, result)
			},
		)
		if err != nil {
			return result, fmt.Errorf("iterator error: %w", err)
		}
	} else {
		// Use date-based iterator for full days
		fmt.Printf("  🔧 Date-based mode: %d → %d\n", dr.FromInt(), dr.ToInt())
		_, err := cfg.Client.ReportDetailByPeriodIterator(
			ctx,
			statsAPIURL,
			cfg.RateLimit,
			cfg.Burst,
			dr.FromInt(),
			dr.ToInt(),
			func(rows []wb.RealizationReportRow) error {
				return saveRows(ctx, cfg.Repo, rows, resume, result)
			},
		)
		if err != nil {
			return result, fmt.Errorf("iterator error: %w", err)
		}
	}

	periodDuration := time.Since(periodStart)
	result.TotalDuration = periodDuration
	result.APIWaitTime = periodDuration - result.ProcessingTime - result.DBWriteTime

	fmt.Printf("  🕐 Окончание: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("  ⏱️  Длительность: %s\n", periodDuration.Round(time.Millisecond))
	fmt.Printf("  📊 Breakdown: API=%s, Обработка=%s, БД=%s\n",
		result.APIWaitTime.Round(time.Millisecond),
		result.ProcessingTime.Round(time.Millisecond),
		result.DBWriteTime.Round(time.Millisecond))
	fmt.Printf("  ✅ Готово: %d строк, %d страниц\n", result.Rows, result.Pages)

	return result, nil
}

// saveRows saves a batch of rows to database with resume support.
// Splits records into two tables:
//   - sales: real sales/returns (nm_id > 0 AND doc_type_name not empty)
//   - service_records: logistics, deductions, etc. (nm_id = 0 OR empty doc_type_name)
func saveRows(ctx context.Context, repo *sqlite.SQLiteSalesRepository, rows []wb.RealizationReportRow, resume bool, result *periodResult) error {
	processingStart := time.Now()

	if len(rows) == 0 {
		return nil
	}

	// Split rows into sales and service records
	// Sales: nm_id > 0 AND doc_type_name is not empty ("Продажа" or "Возврат")
	// Service: nm_id = 0 OR doc_type_name is empty (logistics, deductions, returns to seller)
	var salesRows []wb.RealizationReportRow
	var serviceRows []wb.RealizationReportRow
	var logisticsWithProduct int // Counter for nm_id > 0 with empty doc_type_name
	for _, row := range rows {
		if row.NmID > 0 && row.DocTypeName != "" {
			// Real sale or return with product info
			salesRows = append(salesRows, row)
		} else {
			// Service record (logistics, deductions, or product-specific logistics)
			serviceRows = append(serviceRows, row)
			if row.NmID > 0 && row.DocTypeName == "" {
				logisticsWithProduct++
			}
		}
	}

	// Log splitting
	if len(serviceRows) > 0 {
		fmt.Printf("  🔧 Разделение: %d строк → %d продаж + %d служебных (из них %d логистика по товарам)\n",
			len(rows), len(salesRows), len(serviceRows), logisticsWithProduct)
	}

	processingDuration := time.Since(processingStart)
	result.ProcessingTime += processingDuration

	// DB write phase
	dbStart := time.Now()

	// Save service records (no resume check needed - just INSERT OR IGNORE)
	if len(serviceRows) > 0 {
		if err := repo.SaveServiceRecords(ctx, serviceRows); err != nil {
			return fmt.Errorf("save %d service records: %w", len(serviceRows), err)
		}
	}

	// For resume mode: filter out existing sales rows
	var toSave []wb.RealizationReportRow
	if resume && len(salesRows) > 0 {
		skippedCount := 0
		for _, row := range salesRows {
			exists, err := repo.Exists(ctx, row.RrdID)
			if err != nil {
				return fmt.Errorf("check exists rrd_id=%d: %w", row.RrdID, err)
			}
			if exists {
				skippedCount++
			} else {
				toSave = append(toSave, row)
			}
		}
		// Log resume filtering
		if skippedCount > 0 {
			fmt.Printf("  🔄 Resume: фильтр %d → %d новых (пропущено %d)\n",
				len(salesRows), len(toSave), skippedCount)
		}
	} else {
		toSave = salesRows
	}

	// Save sales to database
	if len(toSave) > 0 {
		if err := repo.Save(ctx, toSave); err != nil {
			return fmt.Errorf("save %d rows: %w", len(toSave), err)
		}
	}

	dbDuration := time.Since(dbStart)
	result.DBWriteTime += dbDuration

	result.Rows += len(toSave)
	result.Pages++

	// Show progress bar with timing breakdown
	progress := len(toSave) + len(serviceRows)
	fmt.Printf("  [%s] Страница %d: %s %d строк (обработка: %s, БД: %s)\n",
		processingStart.Format("15:04:05"),
		result.Pages,
		progressBar(progress, progress),
		progress,
		processingDuration.Round(time.Millisecond),
		dbDuration.Round(time.Millisecond),
	)

	return nil
}

// formatDuration returns a human-readable duration string.
func formatDuration(d time.Duration) string {
	ms := d.Milliseconds()
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(ms)/60000)
	}
}

// progressBar returns a visual progress bar string.
func progressBar(progress, max int) string {
	width := 30
	filled := int(float64(progress) / float64(max) * float64(width))
	if filled > width {
		filled = width
	}

	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return bar
}

// PrintSummary prints download summary with FBW analysis and service records.
func PrintSummary(result *DownloadResult, repo *sqlite.SQLiteSalesRepository) {
	fmt.Println("\n" + repeat("=", 71))
	fmt.Printf("🕐 Окончание загрузки: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("✅ Загружено: %d продаж за %d периодов\n", result.TotalRows, result.PeriodsCount)
	fmt.Printf("⏱️  Общее время: %s\n", result.Duration.Round(time.Second))
	fmt.Printf("📄 Страниц: %d\n", result.TotalPages)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Show service records statistics
	fmt.Println("\n📊 СЛУЖЕБНЫЕ ЗАПИСИ (логистика, ПВЗ, удержания):")
	serviceStats, err := repo.GetServiceRecordStats(ctx)
	if err != nil {
		log.Printf("⚠️  Не удалось получить статистику служебных записей: %v", err)
	} else if serviceStats.Total == 0 {
		fmt.Println("  ⚠️  Служебных записей нет")
	} else {
		fmt.Printf("  Всего: %d записей\n", serviceStats.Total)
		fmt.Println("  По типам операций:")
		for oper, count := range serviceStats.ByOperation {
			// Shorten long operation names
			shortName := oper
			if len(oper) > 50 {
				shortName = oper[:47] + "..."
			}
			fmt.Printf("    - %s: %d\n", shortName, count)
		}
	}

	// Analyze gi_box_type_name (proxy for delivery method)
	fmt.Println("\n📊 АНАЛИЗ GI_BOX_TYPE_NAME (тип короба):")

	methods, err := repo.GetDeliveryMethods(ctx)
	if err != nil {
		log.Printf("⚠️  Не удалось получить gi_box_type_name: %v", err)
	} else if len(methods) == 0 {
		fmt.Println("  ⚠️  Данные не загружены")
	} else {
		for _, method := range methods {
			fmt.Printf("  - %s\n", method)
		}
	}

	// Show FBW vs FBS breakdown
	fmt.Println("\n📦 FBW vs FBS:")
	var fbwCount int
	fbwRows, err := repo.GetFBWOnly(ctx)
	if err != nil {
		log.Printf("⚠️  Не удалось получить FBW данные: %v", err)
	} else {
		fbwCount = len(fbwRows)
		fbsCount := result.TotalRows - fbwCount
		if result.TotalRows > 0 {
			fmt.Printf("  FBW (склады WB):  %d (%.1f%%)\n", fbwCount, float64(fbwCount)*100/float64(result.TotalRows))
			fmt.Printf("  FBS (склад продавца или другое): %d (%.1f%%)\n", fbsCount, float64(fbsCount)*100/float64(result.TotalRows))
		}
	}

	fmt.Println("\n💡 FBW фильтрация:")
	fmt.Println("  - gi_box_type_name = 'Микс', 'Без коробов', 'Моно' → FBW")
	fmt.Println("  - gi_box_type_name = '(пусто)' → возможно FBS")
	fmt.Println(repeat("=", 71))
	fmt.Println("🎉 Утилита завершена успешно!")
}
