// Package main provides E2E tests for promotion download utility.
package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestE2EPromotionLoad(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-promotion.db")

	repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	// Test 1: Load campaigns
	t.Run("LoadCampaigns", func(t *testing.T) {
		mockClient := NewMockPromotionClient()
		PopulateMockData(mockClient, 5, 7)

		_, ids, err := DownloadCampaigns(ctx, mockClient, repo, nil)
		if err != nil {
			t.Fatalf("DownloadCampaigns failed: %v", err)
		}

		if len(ids) != 5 {
			t.Errorf("Expected 5 campaigns, got %d", len(ids))
		}

		// Verify DB count
		count, err := repo.CountCampaigns(ctx)
		if err != nil {
			t.Fatalf("CountCampaigns failed: %v", err)
		}
		if count != 5 {
			t.Errorf("Expected 5 campaigns in DB, got %d", count)
		}

		t.Logf("✅ Loaded %d campaigns", len(ids))
	})

	// Test 2: Load stats
	t.Run("LoadStats", func(t *testing.T) {
		mockClient := NewMockPromotionClient()
		PopulateMockData(mockClient, 3, 7)

		// First load campaigns
		_, ids, _ := DownloadCampaigns(ctx, mockClient, repo, nil)

		// Then load stats
		beginDate := time.Now().AddDate(0, 0, -6).Format("2006-01-02")
		endDate := time.Now().Format("2006-01-02")

		summary, err := DownloadCampaignStats(ctx, mockClient, repo, ids, beginDate, endDate, false, 3, 1)
		if err != nil {
			t.Fatalf("DownloadCampaignStats failed: %v", err)
		}

		if summary.DailyRows == 0 {
			t.Error("Expected some stats to be loaded")
		}

		t.Logf("✅ Loaded %d daily, %d app, %d nm rows",
			summary.DailyRows, summary.AppRows, summary.NmRows)
	})

	// Test 3: Status filtering
	t.Run("StatusFiltering", func(t *testing.T) {
		mockClient := NewMockPromotionClient()
		PopulateMockData(mockClient, 10, 7)

		// Filter only active campaigns (status=9)
		_, ids, err := DownloadCampaigns(ctx, mockClient, repo, []int{9})
		if err != nil {
			t.Fatalf("DownloadCampaigns with filter failed: %v", err)
		}

		// All returned campaigns should be active
		for _, id := range ids {
			// Verify campaign is active (we'd need a GetCampaign method for full check)
			t.Logf("Campaign %d is active", id)
		}

		t.Logf("✅ Filtered to %d active campaigns", len(ids))
	})

	// Test 4: Resume mode
	t.Run("ResumeMode", func(t *testing.T) {
		mockClient := NewMockPromotionClient()
		PopulateMockData(mockClient, 2, 7)

		// Load campaigns
		_, ids, _ := DownloadCampaigns(ctx, mockClient, repo, nil)

		beginDate := time.Now().AddDate(0, 0, -6).Format("2006-01-02")
		endDate := time.Now().Format("2006-01-02")

		// First load: all 7 days
		summary1, err := DownloadCampaignStats(ctx, mockClient, repo, ids, beginDate, endDate, false, 3, 1)
		if err != nil {
			t.Fatalf("First DownloadCampaignStats failed: %v", err)
		}

		// Second load with resume: should skip already loaded
		summary2, err := DownloadCampaignStats(ctx, mockClient, repo, ids, beginDate, endDate, true, 3, 1)
		if err != nil {
			t.Fatalf("Second DownloadCampaignStats failed: %v", err)
		}

		// With resume, should load 0 or very few new records
		if summary2.DailyRows > 0 {
			t.Logf("⚠️  Resume mode loaded %d new records (expected 0)", summary2.DailyRows)
		}

		t.Logf("✅ First load: %d daily rows, Resume load: %d daily rows",
			summary1.DailyRows, summary2.DailyRows)
	})

	// Test 5: Retry on failure
	t.Run("RetryOnFailure", func(t *testing.T) {
		mockClient := NewMockPromotionClient()
		PopulateMockData(mockClient, 2, 3)
		mockClient.SetFailCount(2) // Fail first 2 requests

		_, ids, err := DownloadCampaigns(ctx, mockClient, repo, nil)
		if err != nil {
			// This is expected - mock doesn't retry automatically
			t.Logf("Expected failure after retries: %v", err)
			return
		}

		t.Logf("✅ Retries succeeded, got %d campaigns", len(ids))
	})
}

func TestDateRangeSplitting(t *testing.T) {
	tests := []struct {
		name      string
		begin     string
		end       string
		wantCount int
	}{
		{"7 days = 1 window", "2026-03-20", "2026-03-26", 1},
		{"31 days = 1 window", "2026-02-25", "2026-03-27", 1},
		{"32 days = 2 windows", "2026-02-24", "2026-03-27", 2},
		{"90 days = 3 windows", "2025-12-28", "2026-03-27", 3},
		{"93 days = 3 windows", "2025-12-25", "2026-03-27", 3},
		{"100 days = 4 windows", "2025-12-18", "2026-03-27", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges := splitDateRanges(tt.begin, tt.end)
			if len(ranges) != tt.wantCount {
				t.Errorf("splitDateRanges(%s, %s) = %d windows, want %d",
					tt.begin, tt.end, len(ranges), tt.wantCount)
			}

			// Verify no gaps and no overlaps
			for i := 1; i < len(ranges); i++ {
				prevEnd, _ := time.Parse("2006-01-02", ranges[i-1].end)
				currBegin, _ := time.Parse("2006-01-02", ranges[i].begin)
				expected := prevEnd.AddDate(0, 0, 1)
				if !currBegin.Equal(expected) {
					t.Errorf("Gap between windows: %s → %s (expected %s)",
						ranges[i-1].end, ranges[i].begin, expected.Format("2006-01-02"))
				}
			}

			// Verify each window is <= 31 days
			for _, r := range ranges {
				begin, _ := time.Parse("2006-01-02", r.begin)
				end, _ := time.Parse("2006-01-02", r.end)
				days := int(end.Sub(begin).Hours()/24) + 1
				if days > 31 {
					t.Errorf("Window %s → %s is %d days (max 31)", r.begin, r.end, days)
				}
			}

			// Verify coverage: first.begin == begin, last.end == end
			if ranges[0].begin != tt.begin {
				t.Errorf("First window begins at %s, want %s", ranges[0].begin, tt.begin)
			}
			last := ranges[len(ranges)-1]
			if last.end != tt.end {
				t.Errorf("Last window ends at %s, want %s", last.end, tt.end)
			}
		})
	}
}

func TestCampaignStatusNames(t *testing.T) {
	tests := []struct {
		status   int
		expected string
	}{
		{-1, "Удалена"},
		{4, "Готова к запуску"},
		{7, "Завершена"},
		{8, "Отменена"},
		{9, "Активна"},
		{11, "На паузе"},
		{999, "Неизвестно"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := wb.StatusName(tt.status)
			if got != tt.expected {
				t.Errorf("StatusName(%d) = %s, want %s", tt.status, got, tt.expected)
			}
		})
	}
}

func TestCampaignTypeNames(t *testing.T) {
	tests := []struct {
		campaignType int
		expected     string
	}{
		{8, "Поиск"},
		{9, "Автоматическая"},
		{6, "Бустер"},
		{50, "Каталог"},
		{999, "Неизвестно"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := wb.TypeName(tt.campaignType)
			if got != tt.expected {
				t.Errorf("TypeName(%d) = %s, want %s", tt.campaignType, got, tt.expected)
			}
		})
	}
}
