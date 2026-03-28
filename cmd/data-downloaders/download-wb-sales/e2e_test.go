// Package main provides E2E tests for WB Sales Downloader.
// Tests the full pipeline: config loading → download → SQLite storage.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestE2EDownloadWithMocks tests the full download pipeline with mock data.
func TestE2EDownloadWithMocks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-sales.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	mockClient := wb.NewMockClient()

	// Test 1: Basic download (single day, 100 rows)
	t.Run("BasicDownload", func(t *testing.T) {
		mockClient.Clear()
		mockClient.AddMockSales(generateMockSales(100, time.Now())...)

		downloadCfg := DownloadConfig{
			Client:    mockClient,
			Repo:      repo,
			RateLimit: 100,
			Burst:     10,
		}

		now := time.Now()
		ranges := []DateRange{{From: now, To: now}}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := DownloadSales(ctx, downloadCfg, ranges, false)
		if err != nil {
			t.Fatalf("Download failed: %v", err)
		}

		if result.TotalRows != 100 {
			t.Errorf("Expected 100 rows, got %d", result.TotalRows)
		}

		t.Logf("✅ Downloaded %d rows", result.TotalRows)
	})

	// Test 2: Service record splitting
	t.Run("ServiceRecordSplitting", func(t *testing.T) {
		// Create fresh repo for this test
		serviceDBPath := filepath.Join(tmpDir, "test-service.db")
		serviceRepo, err := sqlite.NewSQLiteSalesRepository(serviceDBPath)
		if err != nil {
			t.Fatalf("Failed to create service repo: %v", err)
		}
		defer serviceRepo.Close()

		mockClient.Clear()

		// Add 50 sales + 30 service records
		var rows []wb.RealizationReportRow
		for i := 0; i < 50; i++ {
			rows = append(rows, wb.RealizationReportRow{
				RrdID:         i + 1,
				NmID:          1000000 + i,
				DocTypeName:   "Продажа",
				SubjectName:   "Test Product",
				BrandName:     "Test Brand",
				Quantity:      1,
				RetailPrice:   1000.0,
				GiBoxTypeName: "Микс",
				SaleDT:        time.Now().Format(time.RFC3339),
			})
		}
		for i := 0; i < 30; i++ {
			rows = append(rows, wb.RealizationReportRow{
				RrdID:            100 + i,
				NmID:             0,
				DocTypeName:      "",
				SupplierOperName: "Возмещение издержек",
			})
		}
		mockClient.AddMockSales(rows...)

		downloadCfg := DownloadConfig{
			Client:    mockClient,
			Repo:      serviceRepo,
			RateLimit: 100,
			Burst:     10,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := DownloadSales(ctx, downloadCfg, []DateRange{{From: time.Now(), To: time.Now()}}, false)
		if err != nil {
			t.Fatalf("Download failed: %v", err)
		}

		if result.TotalRows != 50 {
			t.Errorf("Expected 50 sales rows, got %d", result.TotalRows)
		}

		serviceCount, _ := serviceRepo.CountServiceRecords(ctx)
		if serviceCount != 30 {
			t.Errorf("Expected 30 service records, got %d", serviceCount)
		}

		t.Logf("✅ Split: %d sales + %d service records", result.TotalRows, serviceCount)
	})

	// Test 3: Resume mode - data deduplication
	t.Run("ResumeMode", func(t *testing.T) {
		resumeDBPath := filepath.Join(tmpDir, "test-resume.db")
		resumeRepo, err := sqlite.NewSQLiteSalesRepository(resumeDBPath)
		if err != nil {
			t.Fatalf("Failed to create resume repo: %v", err)
		}
		defer resumeRepo.Close()

		mockClient.Clear()
		mockClient.AddMockSales(generateMockSalesWithRrdID(50, 1)...)

		downloadCfg := DownloadConfig{
			Client:    mockClient,
			Repo:      resumeRepo,
			RateLimit: 100,
			Burst:     10,
		}

		ctx := context.Background()

		// First download
		result1, err := DownloadSales(ctx, downloadCfg, []DateRange{{From: time.Now(), To: time.Now()}}, false)
		if err != nil {
			t.Fatalf("First download failed: %v", err)
		}

		// Verify database count after first download
		count1, _ := resumeRepo.Count(ctx)

		// Second download with resume - should use rrd_id deduplication
		// The resume mode checks if rrd_id already exists in DB
		_, err = DownloadSales(ctx, downloadCfg, []DateRange{{From: time.Now(), To: time.Now()}}, true)
		if err != nil {
			t.Fatalf("Resume download failed: %v", err)
		}

		// Verify database count is the same (no duplicates)
		count2, _ := resumeRepo.Count(ctx)

		if result1.TotalRows != 50 {
			t.Errorf("First download: expected 50, got %d", result1.TotalRows)
		}

		if count1 != count2 {
			t.Errorf("Resume should not add duplicates: before=%d, after=%d", count1, count2)
		}

		t.Logf("✅ Resume: downloaded=%d, DB count stable at %d (no duplicates)", result1.TotalRows, count2)
	})

	// Test 4: Period splitting (time-based)
	t.Run("TimeBasedPeriodSplitting", func(t *testing.T) {
		from := time.Date(2026, 2, 19, 0, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		to := time.Date(2026, 2, 19, 23, 59, 59, 0, time.FixedZone("MSK", 3*3600))

		ranges := SplitPeriod(from, to)

		if len(ranges) != 1 {
			t.Errorf("Expected 1 period, got %d", len(ranges))
		}

		if !ranges[0].HasTime() {
			t.Error("Expected time-based range")
		}

		t.Logf("✅ Time-based range: %s", ranges[0].String())
	})

	// Test 5: Database verification
	t.Run("DatabaseVerification", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Check the first repo (has 100 rows from BasicDownload)
		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Count failed: %v", err)
		}

		if count != 100 {
			t.Errorf("Expected 100 rows in DB, got %d", count)
		}

		t.Logf("✅ Database contains %d rows", count)
	})
}

// TestE2ERetryLogic tests retry with simulated failures.
func TestE2ERetryLogic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-retry.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Test: Retry on simulated failures
	t.Run("RetryOnFailure", func(t *testing.T) {
		mockClient := wb.NewMockClient()
		mockClient.SetFailCount(2) // Fail first 2, succeed on 3rd
		mockClient.AddMockSales(generateMockSalesWithRrdID(50, 1)...)

		downloadCfg := DownloadConfig{
			Client:    mockClient,
			Repo:      repo,
			RateLimit: 100,
			Burst:     10,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := DownloadSales(ctx, downloadCfg, []DateRange{{From: time.Now(), To: time.Now()}}, false)
		if err != nil {
			t.Fatalf("Download failed after retries: %v", err)
		}

		if result.TotalRows != 50 {
			t.Errorf("Expected 50 rows after retry, got %d", result.TotalRows)
		}

		t.Logf("✅ Retry succeeded after 2 failures, downloaded %d rows", result.TotalRows)
	})
}

// generateMockSales creates mock sales data for a specific date.
func generateMockSales(count int, date time.Time) []wb.RealizationReportRow {
	var rows []wb.RealizationReportRow

	for i := 0; i < count; i++ {
		row := wb.RealizationReportRow{
			RrdID:           i + 1,
			NmID:            1000000 + i,
			SupplierArticle: fmt.Sprintf("ART-%03d", i),
			SubjectName:     "Test Subject",
			BrandName:       "Test Brand",
			DocTypeName:     "Продажа",
			SaleDT:          date.Format(time.RFC3339),
			Quantity:        1,
			RetailPrice:     1000.0 + float64(i),
			SalePercent:     15.0,
			GiBoxTypeName:   "Микс",
		}
		rows = append(rows, row)
	}

	return rows
}

// generateMockSalesWithRrdID creates mock sales with unique RrdID.
func generateMockSalesWithRrdID(count, startRrdID int) []wb.RealizationReportRow {
	var rows []wb.RealizationReportRow
	now := time.Now()

	for i := 0; i < count; i++ {
		row := wb.RealizationReportRow{
			RrdID:           startRrdID + i,
			NmID:            1000000 + i,
			SupplierArticle: fmt.Sprintf("ART-%03d", i),
			SubjectName:     "Test Subject",
			BrandName:       "Test Brand",
			DocTypeName:     "Продажа",
			SaleDT:          now.Format(time.RFC3339),
			RRDT:            now.Format(time.RFC3339), // Required for resume mode
			Quantity:        1,
			RetailPrice:     1000.0 + float64(i),
			SalePercent:     15.0,
			GiBoxTypeName:   "Микс",
		}
		rows = append(rows, row)
	}

	return rows
}
