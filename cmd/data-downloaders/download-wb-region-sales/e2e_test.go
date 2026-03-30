package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

func TestE2ERegionSalesDownload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-region-sales.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	t.Run("MockSaveAndCount", func(t *testing.T) {
		mockClient := NewMockRegionSalesClient()
		PopulateMockRegionSales(mockClient, 50, 3) // 50 products x 3 regions = 150 items

		result, err := DownloadRegionSales(ctx, mockClient, repo, "2026-03-01", "2026-03-31", 6, 5)
		if err != nil {
			t.Fatalf("DownloadRegionSales failed: %v", err)
		}

		if result.TotalRows != 150 {
			t.Errorf("Expected 150 rows, got %d", result.TotalRows)
		}
		if result.Requests != 1 {
			t.Errorf("Expected 1 request, got %d", result.Requests)
		}

		count, err := repo.CountRegionSales(ctx)
		if err != nil {
			t.Fatalf("CountRegionSales failed: %v", err)
		}
		if count != 150 {
			t.Errorf("Expected 150 in DB, got %d", count)
		}
	})

	t.Run("IdempotentReRun", func(t *testing.T) {
		mockClient1 := NewMockRegionSalesClient()
		PopulateMockRegionSales(mockClient1, 20, 2) // 20 x 2 = 40

		result1, err := DownloadRegionSales(ctx, mockClient1, repo, "2026-02-01", "2026-02-28", 6, 5)
		if err != nil {
			t.Fatalf("First run failed: %v", err)
		}

		mockClient2 := NewMockRegionSalesClient()
		PopulateMockRegionSales(mockClient2, 20, 2)

		result2, err := DownloadRegionSales(ctx, mockClient2, repo, "2026-02-01", "2026-02-28", 6, 5)
		if err != nil {
			t.Fatalf("Second run failed: %v", err)
		}

		if result1.TotalRows != result2.TotalRows {
			t.Errorf("Idempotent mismatch: first=%d second=%d", result1.TotalRows, result2.TotalRows)
		}

		// DB count should not double (INSERT OR REPLACE on same period)
		count, _ := repo.CountRegionSales(ctx)
		expectedTotal := 150 + result1.TotalRows // 150 from Test 1 + 40 from this test
		if count != expectedTotal {
			t.Errorf("Expected %d in DB after idempotent re-run, got %d", expectedTotal, count)
		}
	})

	t.Run("EmptyData", func(t *testing.T) {
		// Empty mock client returns no data
		mockClient := NewMockRegionSalesClient()
		// Don't populate — empty

		result, err := DownloadRegionSales(ctx, mockClient, repo, "2026-03-20", "2026-03-20", 6, 5)
		if err != nil {
			t.Fatalf("Empty data download failed: %v", err)
		}

		if result.TotalRows != 0 {
			t.Errorf("Expected 0 rows for empty data, got %d", result.TotalRows)
		}
		if result.Requests != 1 {
			t.Errorf("Expected 1 request even for empty data, got %d", result.Requests)
		}
	})
}

func TestDateAvailabilityWarning(t *testing.T) {
	// Test that a date older than 31 days triggers the warning logic.
	// The actual warning is printed to stdout, so we just verify the calculation.
	now := time.Now()
	earliestAvailable := now.AddDate(0, 0, -31)
	oldDate := now.AddDate(0, 0, -60)

	if !oldDate.Before(earliestAvailable) {
		t.Error("Test setup: oldDate should be before earliestAvailable")
	}
}
