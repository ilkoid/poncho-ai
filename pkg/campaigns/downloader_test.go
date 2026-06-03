package campaigns

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBasicDownload verifies the full 3-phase download flow:
// campaigns → details → fullstats, checking all counters.
func TestBasicDownload(t *testing.T) {
	src := NewMockCampaignsSource(5)
	writer := NewDiscardWriter()

	now := time.Now()
	opts := DownloadOptions{
		Begin:          now.AddDate(0, 0, -7).Format("2006-01-02"),
		End:            now.AddDate(0, 0, -1).Format("2006-01-02"),
		FullstatsRate:  3,
		FullstatsBurst: 1,
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5 campaigns across 3 groups (active, paused, finished)
	if result.CampaignsTotal != 5 {
		t.Errorf("CampaignsTotal: got %d, want 5", result.CampaignsTotal)
	}
	if result.CampaignsForStats != 5 {
		t.Errorf("CampaignsForStats: got %d, want 5", result.CampaignsForStats)
	}

	// Details: all 5 campaigns
	if result.DetailsLoaded != 5 {
		t.Errorf("DetailsLoaded: got %d, want 5", result.DetailsLoaded)
	}

	// Stats: at least 1 daily row per campaign
	if result.DailyRows < 5 {
		t.Errorf("DailyRows: got %d, want >= 5", result.DailyRows)
	}
	if result.AppRows < 5 {
		t.Errorf("AppRows: got %d, want >= 5", result.AppRows)
	}

	// Post-run: PopulateCampaignProducts should have been called
	if !writer.Populated() {
		t.Error("PopulateCampaignProducts was not called")
	}

	// Writer counts should match result
	if writer.SavedCampaigns() != 5 {
		t.Errorf("SavedCampaigns: got %d, want 5", writer.SavedCampaigns())
	}
	if writer.SavedDetails() != 5 {
		t.Errorf("SavedDetails: got %d, want 5", writer.SavedDetails())
	}
}

// TestDryRun verifies that DryRun=true skips all DB writes
// but still counts correctly in the result.
func TestDryRun(t *testing.T) {
	src := NewMockCampaignsSource(3)
	writer := NewDiscardWriter()

	now := time.Now()
	opts := DownloadOptions{
		Begin:          now.AddDate(0, 0, -7).Format("2006-01-02"),
		End:            now.AddDate(0, 0, -1).Format("2006-01-02"),
		DryRun:         true,
		FullstatsRate:  3,
		FullstatsBurst: 1,
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should still have campaign counts from API
	if result.CampaignsTotal != 3 {
		t.Errorf("CampaignsTotal: got %d, want 3", result.CampaignsTotal)
	}

	// But writer should have zero saves (DryRun skips all writes)
	if writer.SavedCampaigns() != 0 {
		t.Errorf("SavedCampaigns: got %d, want 0 (dry-run)", writer.SavedCampaigns())
	}
	if writer.SavedDetails() != 0 {
		t.Errorf("SavedDetails: got %d, want 0 (dry-run)", writer.SavedDetails())
	}
	if writer.SavedDaily() != 0 {
		t.Errorf("SavedDaily: got %d, want 0 (dry-run)", writer.SavedDaily())
	}
	if writer.Populated() {
		t.Error("PopulateCampaignProducts should not be called in dry-run")
	}

	// Stats rows should still be counted in result (from API response flattening)
	if result.DailyRows < 3 {
		t.Errorf("DailyRows: got %d, want >= 3", result.DailyRows)
	}
}

// TestResumeMode verifies that resume mode adjusts the date range
// based on previously loaded data.
func TestResumeMode(t *testing.T) {
	src := NewMockCampaignsSource(2)

	// Create a writer that reports existing data for resume
	writer := &resumeTestWriter{
		lastDates: map[int]time.Time{
			10001: time.Now().AddDate(0, 0, -2), // Campaign 10001 loaded up to 2 days ago
			10002: time.Now().AddDate(0, 0, -1), // Campaign 10002 loaded up to yesterday
		},
	}

	now := time.Now()
	begin := now.AddDate(0, 0, -7).Format("2006-01-02")
	end := now.AddDate(0, 0, -1).Format("2006-01-02")

	opts := DownloadOptions{
		Begin:          begin,
		End:            end,
		Resume:         true,
		FullstatsRate:  3,
		FullstatsBurst: 1,
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Resume mode should still download stats (date range adjusted)
	if result.DailyRows == 0 {
		t.Error("expected stats rows in resume mode")
	}
}

// TestContextCancel verifies that context cancellation stops the download.
func TestContextCancel(t *testing.T) {
	src := NewMockCampaignsSource(10)
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	now := time.Now()
	opts := DownloadOptions{
		Begin:          now.AddDate(0, 0, -7).Format("2006-01-02"),
		End:            now.AddDate(0, 0, -1).Format("2006-01-02"),
		FullstatsRate:  3,
		FullstatsBurst: 1,
	}

	dl := NewDownloader(src, writer, opts)
	_, err := dl.Run(ctx)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// resumeTestWriter wraps DiscardWriter and returns preset last dates for resume testing.
type resumeTestWriter struct {
	DiscardWriter
	lastDates map[int]time.Time
}

func (w *resumeTestWriter) GetLastCampaignStatsDateAll(_ context.Context) (map[int]time.Time, error) {
	return w.lastDates, nil
}
