// Package main provides WB Sales Downloader utility.
// This file contains mock data generation for simulation mode (SRP).
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockConfig holds configuration for mock data generation.
type MockConfig struct {
	RowsPerPage    int           // Rows per page (default: ~47000 like real data)
	PagesPerPeriod int           // Pages per period (default: 2)
	RowDelay       time.Duration // Delay per row for realistic timing (0 for instant)
	PageDelay      time.Duration // Delay per page for simulating rate limit (0 for instant)
}

// DefaultMockConfig returns default mock configuration.
func DefaultMockConfig() MockConfig {
	return MockConfig{
		RowsPerPage:    47000, // Based on real data from sales-2026-7days.db
		PagesPerPeriod: 3,     // 3 pages = 141K rows, enough for full day coverage
		RowDelay:       0,
		PageDelay:      100 * time.Millisecond, // Fast simulation (100ms vs 60s real)
	}
}

// MockDownloader simulates download with generated data.
// Use for testing without real API calls.
type MockDownloader struct {
	cfg MockConfig
}

// NewMockDownloader creates a new mock downloader.
func NewMockDownloader(cfg MockConfig) *MockDownloader {
	return &MockDownloader{cfg: cfg}
}

// DownloadSales simulates downloading sales data for all date ranges.
// Generates realistic mock data without API calls.
func (m *MockDownloader) DownloadSales(ctx context.Context, repo interface{}, ranges []DateRange, resume bool) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{
		PeriodsCount: len(ranges),
	}

	for i, dr := range ranges {
		periodResult, err := m.downloadPeriod(ctx, dr, i+1, len(ranges))
		if err != nil {
			return result, fmt.Errorf("period %s: %w", dr.String(), err)
		}
		result.TotalRows += periodResult.Rows
		result.TotalPages += periodResult.Pages
	}

	result.Duration = time.Since(start)
	return result, nil
}

// downloadPeriod simulates downloading a single period with mock data.
func (m *MockDownloader) downloadPeriod(ctx context.Context, dr DateRange, periodNum, totalPeriods int) (*periodResult, error) {
	result := &periodResult{}
	periodStart := time.Now()

	fmt.Printf("\n[%d/%d] Интервал %s (MOCK MODE)\n", periodNum, totalPeriods, dr.String())
	fmt.Printf("  🕐 Начало: %s\n", periodStart.Format("2006-01-02 15:04:05"))
	fmt.Printf("  🧪 Симуляция: %d страниц x ~%d строк\n", m.cfg.PagesPerPeriod, m.cfg.RowsPerPage)

	// Generate mock pages
	for page := 1; page <= m.cfg.PagesPerPeriod; page++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		pageStart := time.Now()

		// Generate mock rows
		rows := m.generateMockRows(dr, page, m.cfg.RowsPerPage)

		// Simulate rate limit delay (much faster than real 60s)
		if page > 1 && m.cfg.PageDelay > 0 {
			time.Sleep(m.cfg.PageDelay)
		}

		// Save rows (note: using real repo for full integration test)
		// We need to convert repo interface to call Save
		// For now, just count the rows
		result.Rows += len(rows)
		result.Pages++

		pageDuration := time.Since(pageStart)

		fmt.Printf("  [%s] Страница %d: %s %d строк (за %s)\n",
			pageStart.Format("15:04:05"),
			page,
			progressBar(len(rows), 100000),
			len(rows),
			formatDuration(pageDuration),
		)
	}

	periodDuration := time.Since(periodStart)
	fmt.Printf("  🕐 Окончание: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("  ⏱️  Длительность: %s\n", periodDuration.Round(time.Millisecond))
	fmt.Printf("  ✅ Готово: %d строк, %d страниц\n", result.Rows, result.Pages)

	return result, nil
}

// generateMockRows creates realistic mock sales data.
func (m *MockDownloader) generateMockRows(dr DateRange, page int, count int) []wb.RealizationReportRow {
	rows := make([]wb.RealizationReportRow, count)

	// Base date from period
	baseDate := dr.From

	// Mock delivery methods (based on real API values)
	giBoxTypes := []string{"", "Монопаллета", "Короб СТ", "Микс", "Без коробов"}

	// Mock subjects
	subjects := []string{"Футболки", "Джинсы", "Платья", "Куртки", "Обувь"}
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}

	for i := 0; i < count; i++ {
		rrdID := (page-1)*100000 + i + 1
		isFBW := i%3 == 0 // ~33% FBW like real data

		rows[i] = wb.RealizationReportRow{
			RrdID:             rrdID,
			DocTypeName:       "Продажа",
			SaleID:            fmt.Sprintf("SALE_%d", rrdID),
			DateFrom:          baseDate.Format("2006-01-02"),
			DateTo:            baseDate.Format("2006-01-02"),
			SupplierArticle:   fmt.Sprintf("ART_%d", i%1000),
			SubjectName:       subjects[i%len(subjects)],
			NmID:              100000 + i%50000,
			BrandName:         brands[i%len(brands)],
			TechSize:          fmt.Sprintf("%d", (i%50)+40),
			Barcode:           fmt.Sprintf("%013d", 2000000000000+int64(i)),
			Quantity:          1,
			IsCancel:          false,
			DeliveryMethod:    map[bool]string{true: "ФБВ", false: "FBS, (МГТ)"}[isFBW],
			GiBoxTypeName:     giBoxTypes[i%len(giBoxTypes)],
			OfficeName:        fmt.Sprintf("Склад-%d", i%10),
			PPVzForPay:        float64(1000 + i%9000),
			RetailPrice:       float64(2000 + i%8000),
			RetailAmount:      float64(2000 + i%8000),
			SalePercent:       15.5,
			CommissionPercent: 7.5,
			DeliveryRub:       float64(i % 100),
			OrderDT:           baseDate.Add(-time.Duration(i%5) * 24 * time.Hour).Format(time.RFC3339),
			SaleDT:            baseDate.Format(time.RFC3339),
			RRDT:              baseDate.Add(24 * time.Hour).Format(time.RFC3339),
		}
	}

	return rows
}

// DownloadSalesWithMock runs download in mock mode with generated data.
// Saves to real database for full integration testing.
func DownloadSalesWithMock(ctx context.Context, repo SalesRepositorySaver, ranges []DateRange) (*DownloadResult, error) {
	mockCfg := DefaultMockConfig()

	start := time.Now()
	result := &DownloadResult{
		PeriodsCount: len(ranges),
	}

	for i, dr := range ranges {
		periodResult, err := downloadPeriodWithMock(ctx, repo, dr, i+1, len(ranges), mockCfg)
		if err != nil {
			return result, fmt.Errorf("period %s: %w", dr.String(), err)
		}
		result.TotalRows += periodResult.Rows
		result.TotalPages += periodResult.Pages
	}

	result.Duration = time.Since(start)
	return result, nil
}

// downloadPeriodWithMock downloads a single period with mock data to real database.
func downloadPeriodWithMock(ctx context.Context, repo SalesRepositorySaver, dr DateRange, periodNum, totalPeriods int, mockCfg MockConfig) (*periodResult, error) {
	result := &periodResult{}
	periodStart := time.Now()

	fmt.Printf("\n[%d/%d] Интервал %s (MOCK MODE)\n", periodNum, totalPeriods, dr.String())
	fmt.Printf("  🕐 Начало: %s\n", periodStart.Format("2006-01-02 15:04:05"))
	fmt.Printf("  🧪 Симуляция: %d страниц x ~%d строк\n", mockCfg.PagesPerPeriod, mockCfg.RowsPerPage)

	for page := 1; page <= mockCfg.PagesPerPeriod; page++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		pageStart := time.Now()

		// Generate mock rows
		rows := generateMockRowsForDB(dr, page, mockCfg.RowsPerPage)

		// Simulate rate limit delay (much faster than real 60s)
		if page > 1 && mockCfg.PageDelay > 0 {
			time.Sleep(mockCfg.PageDelay)
		}

		// Save to real database
		if err := repo.Save(ctx, rows); err != nil {
			return result, fmt.Errorf("save page %d: %w", page, err)
		}

		result.Rows += len(rows)
		result.Pages++

		pageDuration := time.Since(pageStart)

		fmt.Printf("  [%s] Страница %d: %s %d строк (за %s)\n",
			pageStart.Format("15:04:05"),
			page,
			progressBar(len(rows), 100000),
			len(rows),
			formatDuration(pageDuration),
		)
	}

	periodDuration := time.Since(periodStart)
	fmt.Printf("  🕐 Окончание: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("  ⏱️  Длительность: %s\n", periodDuration.Round(time.Millisecond))
	fmt.Printf("  ✅ Готово: %d строк, %d страниц\n", result.Rows, result.Pages)

	return result, nil
}

// SalesRepositorySaver is a minimal interface for saving rows.
type SalesRepositorySaver interface {
	Save(ctx context.Context, rows []wb.RealizationReportRow) error
}

// generateMockRowsForDB creates realistic mock sales data for database insertion.
func generateMockRowsForDB(dr DateRange, page int, count int) []wb.RealizationReportRow {
	rows := make([]wb.RealizationReportRow, count)

	// Calculate period duration for realistic date distribution
	periodDuration := dr.To.Sub(dr.From)
	hoursInPeriod := int(periodDuration.Hours())
	if hoursInPeriod < 1 {
		hoursInPeriod = 24 // Minimum 1 day
	}

	// Mock delivery methods (based on real API values)
	giBoxTypes := []string{"", "Монопаллета", "Короб СТ", "Микс", "Без коробов"}

	// Mock subjects
	subjects := []string{"Футболки", "Джинсы", "Платья", "Куртки", "Обувь"}
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}

	for i := 0; i < count; i++ {
		rrdID := (page-1)*100000 + i + 1
		isFBW := i%3 == 0 // ~33% FBW like real data

		// Distribute rr_dt evenly across the entire period (realistic!)
		// Each row gets a different rr_dt within the requested range
		// Use (count-1) to ensure last record reaches the end of period
		// Use minutes for finer granularity (not just hours)
		var offsetMinutes int
		if count > 1 {
			// Convert period duration to minutes for better precision
			periodMinutes := int(periodDuration.Minutes())
			offsetMinutes = periodMinutes * i / (count - 1)
		}
		rrDT := dr.From.Add(time.Duration(offsetMinutes) * time.Minute)

		// order_dt is BEFORE rr_dt (orders placed before sale, realistic!)
		// Orders can be 0-30 days before rr_dt
		daysBeforeRR := 1 + (i % 30) // 1-30 days before
		orderDT := rrDT.Add(-time.Duration(daysBeforeRR) * 24 * time.Hour)

		// sale_dt is close to rr_dt (same day usually)
		saleDT := rrDT.Add(-time.Duration(i%3) * time.Hour) // 0-2 hours before rr_dt

		// Pick values based on index for consistency
		giBoxType := ""
		deliveryMethod := ""
		if isFBW {
			giBoxType = giBoxTypes[1+(i%3)] // Skip empty, use "Монопаллета", "Короб СТ", "Микс"
			deliveryMethod = "ФБВ"
		} else {
			deliveryMethod = "FBS, (МГТ)"
		}

		retailPrice := float64(2000 + i%8000)
		retailAmount := float64(2000 + i%8000)

		rows[i] = wb.RealizationReportRow{
			RrdID:             rrdID,
			RealizationReportID: 10000 + page,
			DocTypeName:       "Продажа",
			SaleID:            fmt.Sprintf("SALE_%d", rrdID),
			DateFrom:          dr.From.Format("2006-01-02"),
			DateTo:            dr.To.Format("2006-01-02"),
			SupplierArticle:   fmt.Sprintf("ART_%d", i%1000),
			SubjectName:       subjects[i%len(subjects)],
			NmID:              100000 + i%50000,
			BrandName:         brands[i%len(brands)],
			TechSize:          fmt.Sprintf("%d", (i%50)+40),
			Barcode:           fmt.Sprintf("%013d", 2000000000000+int64(i)),
			Quantity:          1,
			IsCancel:          false,
			DeliveryMethod:    deliveryMethod,
			GiBoxTypeName:     giBoxType,
			OfficeName:        fmt.Sprintf("Склад-%d", i%10),
			PPVzForPay:        float64(1000 + i%9000),
			RetailPrice:       retailPrice,
			RetailAmount:      retailAmount,
			SalePercent:       15.5,
			CommissionPercent: 7.5,
			DeliveryRub:       float64(i % 100),
			OrderDT:           orderDT.Format(time.RFC3339),
			SaleDT:            saleDT.Format(time.RFC3339),
			RRDT:              rrDT.Format(time.RFC3339),
			// New financial fields (realistic mock values)
			RetailPriceWithDiscRub: retailPrice * 0.85,
			PPVzSppPrc:            25.0,
			PPVzKvwPrc:            42.0,
			PPVzKvwPrcBase:        45.0,
			PPVzSalesCommission:   retailAmount * 0.25,
			AcquiringFee:          retailAmount * 0.015,
			AcquiringPercent:      1.5,
			GiID:                  100000 + i%1000,
		}
	}

	return rows
}
