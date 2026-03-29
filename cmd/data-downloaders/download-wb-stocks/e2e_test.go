// Package main provides E2E tests for WB Stocks Warehouse downloader.
package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

func TestE2EStocksDownload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-stocks.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	// Test 1: Mock save and count
	t.Run("MockSaveAndCount", func(t *testing.T) {
		mockClient := NewMockStocksClient()
		PopulateMockStocks(mockClient, 50, 3) // 50 products × 3 warehouses = 150 items

		snapshotDate := time.Now().Format("2006-01-02")
		result, err := DownloadStockSnapshot(ctx, mockClient, repo, snapshotDate, 3, 1)
		if err != nil {
			t.Fatalf("DownloadStockSnapshot failed: %v", err)
		}

		if result.TotalRows != 150 {
			t.Errorf("Expected 150 rows, got %d", result.TotalRows)
		}

		count, err := repo.CountStocks(ctx)
		if err != nil {
			t.Fatalf("CountStocks failed: %v", err)
		}
		if count != 150 {
			t.Errorf("Expected 150 in DB, got %d", count)
		}

		t.Logf("Saved %d rows, DB count: %d", result.TotalRows, count)
	})

	// Test 2: Gap detection
	t.Run("GapDetection", func(t *testing.T) {
		gaps, err := DetectGaps(ctx, repo, "2020-01-01")
		if err != nil {
			t.Fatalf("DetectGaps failed: %v", err)
		}

		// Since we only have 1 snapshot date, there should be many gaps
		if len(gaps) == 0 {
			t.Log("No gaps detected")
		} else {
			t.Logf("Detected %d gaps (expected, first_date=2020-01-01)", len(gaps))
		}
	})

	// Test 3: Idempotent re-run
	t.Run("IdempotentReRun", func(t *testing.T) {
		snapshotDate := time.Now().Format("2006-01-02")

		mockClient1 := NewMockStocksClient()
		PopulateMockStocks(mockClient1, 20, 2) // 20 products × 2 warehouses = 40 items

		result1, err := DownloadStockSnapshot(ctx, mockClient1, repo, snapshotDate, 3, 1)
		if err != nil {
			t.Fatalf("First run failed: %v", err)
		}

		mockClient2 := NewMockStocksClient()
		PopulateMockStocks(mockClient2, 20, 2)
		result2, err := DownloadStockSnapshot(ctx, mockClient2, repo, snapshotDate, 3, 1)
		if err != nil {
			t.Fatalf("Second run failed: %v", err)
		}

		if result1.TotalRows != result2.TotalRows {
			t.Errorf("Idempotent mismatch: first=%d second=%d", result1.TotalRows, result2.TotalRows)
		}

		t.Logf("First: %d rows, Second: %d rows (idempotent)", result1.TotalRows, result2.TotalRows)
	})

	// Test 4: Count for specific date
	t.Run("CountForDate", func(t *testing.T) {
		date := time.Now().Format("2006-01-02")
		count, err := repo.CountStocksForDate(ctx, date)
		if err != nil {
			t.Fatalf("CountStocksForDate failed: %v", err)
		}
		if count == 0 {
			t.Error("Expected some rows for today's date")
		}
		t.Logf("Count for %s: %d", date, count)
	})
}
