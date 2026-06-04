package promotion

import (
	"context"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestRun_AllPhasesComplete verifies that Run() executes all 14 phases
// when all skip flags are off and V1 campaign data exists.
func TestRun_AllPhasesComplete(t *testing.T) {
	opts := DownloadOptions{
		ProductIDs: []wb.NormqueryItem{
			{AdvertID: 12345, NmID: 100001},
			{AdvertID: 12345, NmID: 100002},
			{AdvertID: 67890, NmID: 200001},
		},
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-03",
		CalendarBegin: "2026-05-04",
		CalendarEnd:   "2026-07-04",
		RateLimits: RateLimits{
			Normquery:           300,
			NormqueryBurst:      10,
			NormqueryStats:      10,
			NormqueryStatsBurst: 10,
			BidRec:              5,
			BidRecBurst:         3,
			Finance:             60,
			FinanceBurst:        5,
			Calendar:            100,
			CalendarBurst:       5,
			MinBids:             20,
			MinBidsBurst:        5,
		},
		// No skip flags — all phases enabled.
	}

	dl := NewDownloader(NewMockSource(), NewDiscardWriter(), NewMockReader(), opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// 14 phases total: bids(1) + normquery(4) + recommendations(1) + finance(3) + calendar(3) + budgets(1) + min_bids(1)
	expectedSteps := 14
	if result.TotalSteps != expectedSteps {
		t.Errorf("TotalSteps = %d, want %d", result.TotalSteps, expectedSteps)
	}
	if result.CompletedSteps != expectedSteps {
		t.Errorf("CompletedSteps = %d, want %d", result.CompletedSteps, expectedSteps)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

// TestRun_AllSkipped verifies that Run() does nothing when all skip flags are on.
func TestRun_AllSkipped(t *testing.T) {
	opts := DownloadOptions{
		ProductIDs: []wb.NormqueryItem{
			{AdvertID: 12345, NmID: 100001},
		},
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-03",
		CalendarBegin: "2026-05-04",
		CalendarEnd:   "2026-07-04",
		SkipBids:            true,
		SkipNormquery:       true,
		SkipRecommendations: true,
		SkipFinance:         true,
		SkipCalendar:        true,
		SkipBudgets:         true,
		SkipMinBids:         true,
	}

	dl := NewDownloader(NewMockSource(), NewDiscardWriter(), NewMockReader(), opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if result.TotalSteps != 0 {
		t.Errorf("TotalSteps = %d, want 0 (all skipped)", result.TotalSteps)
	}
	if result.CompletedSteps != 0 {
		t.Errorf("CompletedSteps = %d, want 0", result.CompletedSteps)
	}
}

// TestRun_NoV1Data verifies that only finance + calendar phases run
// when there are no V1 campaign products (empty ProductIDs).
func TestRun_NoV1Data(t *testing.T) {
	opts := DownloadOptions{
		ProductIDs:    nil, // no V1 data
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-03",
		CalendarBegin: "2026-05-04",
		CalendarEnd:   "2026-07-04",
		RateLimits: RateLimits{
			Finance:       60,
			FinanceBurst:  5,
			Calendar:      100,
			CalendarBurst: 5,
		},
	}

	dl := NewDownloader(NewMockSource(), NewDiscardWriter(), NewMockReader(), opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Without V1 data: only finance(3) + calendar(3) = 6 phases
	expectedSteps := 6
	if result.TotalSteps != expectedSteps {
		t.Errorf("TotalSteps = %d, want %d (finance+calendar only)", result.TotalSteps, expectedSteps)
	}
	if result.CompletedSteps != expectedSteps {
		t.Errorf("CompletedSteps = %d, want %d", result.CompletedSteps, expectedSteps)
	}
}

// TestExtractAdvertIDs verifies deduplication of advert IDs from product pairs.
func TestExtractAdvertIDs(t *testing.T) {
	tests := []struct {
		name  string
		input []wb.NormqueryItem
		want  int // expected unique count
	}{
		{
			name:  "empty",
			input: nil,
			want:  0,
		},
		{
			name:  "single",
			input: []wb.NormqueryItem{{AdvertID: 1, NmID: 100}},
			want:  1,
		},
		{
			name: "all_same_advert",
			input: []wb.NormqueryItem{
				{AdvertID: 1, NmID: 100},
				{AdvertID: 1, NmID: 200},
				{AdvertID: 1, NmID: 300},
			},
			want: 1,
		},
		{
			name: "mixed",
			input: []wb.NormqueryItem{
				{AdvertID: 1, NmID: 100},
				{AdvertID: 2, NmID: 200},
				{AdvertID: 1, NmID: 300},
				{AdvertID: 3, NmID: 400},
				{AdvertID: 2, NmID: 500},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAdvertIDs(tt.input)
			if len(got) != tt.want {
				t.Errorf("extractAdvertIDs returned %d IDs, want %d", len(got), tt.want)
			}
			// Verify no duplicates
			seen := make(map[int]bool)
			for _, id := range got {
				if seen[id] {
					t.Errorf("duplicate advert ID %d in result", id)
				}
				seen[id] = true
			}
		})
	}
}

// TestRun_DryRun verifies that DryRun mode completes without errors
// but doesn't actually write (DiscardWriter counter stays at 0 for
// phases that skip saving when DryRun=true).
func TestRun_DryRun(t *testing.T) {
	opts := DownloadOptions{
		ProductIDs: []wb.NormqueryItem{
			{AdvertID: 12345, NmID: 100001},
		},
		BeginDate:     "2026-05-28",
		EndDate:       "2026-06-03",
		CalendarBegin: "2026-05-04",
		CalendarEnd:   "2026-07-04",
		DryRun: true,
		RateLimits: RateLimits{
			Normquery:           300,
			NormqueryBurst:      10,
			NormqueryStats:      10,
			NormqueryStatsBurst: 10,
			BidRec:              5,
			BidRecBurst:         3,
			Finance:             60,
			FinanceBurst:        5,
			Calendar:            100,
			CalendarBurst:       5,
			MinBids:             20,
			MinBidsBurst:        5,
		},
	}

	dw := NewDiscardWriter()
	dl := NewDownloader(NewMockSource(), dw, NewMockReader(), opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if result.CompletedSteps != 14 {
		t.Errorf("CompletedSteps = %d, want 14", result.CompletedSteps)
	}
	// In DryRun mode, writer is never called (each phase checks !d.opts.DryRun before saving)
	if dw.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 (DryRun should skip all writes)", dw.Saved())
	}
}
