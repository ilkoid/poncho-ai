package regionsales

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDownloader_Basic tests a full download pipeline with mock data.
func TestDownloader_Basic(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(50, 3) // 50 products × 3 regions = 150 items

	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		Begin:      "2026-05-27",
		End:        "2026-06-02",
		OnProgress: func(msg string) { msgs = append(msgs, msg) },
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 150 {
		t.Errorf("TotalRows = %d, want 150", result.TotalRows)
	}
	if result.Requests != 1 {
		t.Errorf("Requests = %d, want 1", result.Requests)
	}
	if writer.Saved() != 150 {
		t.Errorf("DiscardWriter.Saved() = %d, want 150", writer.Saved())
	}
}

// TestDownloader_DryRun tests that dry-run mode skips writer.
func TestDownloader_DryRun(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(20, 2) // 40 items

	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin:  "2026-05-27",
		End:    "2026-06-02",
		DryRun: true,
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 40 {
		t.Errorf("TotalRows = %d, want 40 (dry-run counts)", result.TotalRows)
	}
	if writer.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 (dry-run should not save)", writer.Saved())
	}
}

// TestDownloader_EmptyData tests download with no data from source.
func TestDownloader_EmptyData(t *testing.T) {
	source := NewMockRegionSalesSource() // no data populated
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin: "2026-05-27",
		End:   "2026-06-02",
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0", result.TotalRows)
	}
	if result.Requests != 1 {
		t.Errorf("Requests = %d, want 1 (request made, empty response)", result.Requests)
	}
}

// TestDownloader_Cancelled tests context cancellation.
func TestDownloader_Cancelled(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(100, 3)

	writer := NewDiscardWriter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := DownloadOptions{
		Begin: "2026-05-27",
		End:   "2026-06-02",
	}
	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("error = %q, want 'context cancelled'", err.Error())
	}
}

// TestDownloader_SingleDate tests --date mode (begin=end, 1 request).
func TestDownloader_SingleDate(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(10, 2) // 20 items

	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		Date:       "2026-06-01",
		OnProgress: func(msg string) { msgs = append(msgs, msg) },
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 20 {
		t.Errorf("TotalRows = %d, want 20", result.TotalRows)
	}
	if result.Requests != 1 {
		t.Errorf("Requests = %d, want 1 (single date = single request)", result.Requests)
	}

	// Verify the period message shows single date
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "2026-06-01 → 2026-06-01") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '2026-06-01 → 2026-06-01' in progress, got: %v", msgs)
	}
}

// TestDownloader_LongRange tests that a 90-day range splits into 3 requests.
func TestDownloader_LongRange(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(5, 2) // 10 items per request

	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		Begin:      "2026-03-04",
		End:        "2026-06-02",
		OnProgress: func(msg string) { msgs = append(msgs, msg) },
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 90 days → 31+31+28 = 3 requests
	if result.Requests != 3 {
		t.Errorf("Requests = %d, want 3 (90-day range split into 3 sub-ranges)", result.Requests)
	}
	// Each request returns 10 items
	if result.TotalRows != 30 {
		t.Errorf("TotalRows = %d, want 30 (3 requests × 10 items)", result.TotalRows)
	}

	// Verify split message
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "3 request(s)") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '3 request(s)' in progress, got: %v", msgs)
	}
}

// TestDownloader_DefaultDays tests that Days defaults to 7.
func TestDownloader_DefaultDays(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(5, 1)
	writer := NewDiscardWriter()

	opts := DownloadOptions{} // No Date, Begin, End, Days → defaults to 7 days
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Requests != 1 {
		t.Errorf("Requests = %d, want 1 (7 days ≤ 31)", result.Requests)
	}
}

// TestSplitDateRange verifies date splitting boundaries.
func TestSplitDateRange(t *testing.T) {
	tests := []struct {
		name     string
		begin    string
		end      string
		maxDays  int
		expected int // number of sub-ranges
	}{
		{"single day", "2026-06-01", "2026-06-01", 31, 1},
		{"exact 31 days", "2026-05-02", "2026-06-01", 31, 1},
		{"32 days → 2 ranges", "2026-05-01", "2026-06-01", 31, 2},
		{"90 days → 3 ranges", "2026-03-04", "2026-06-02", 31, 3},
		{"7 days", "2026-05-27", "2026-06-02", 31, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges := splitDateRange(tt.begin, tt.end, tt.maxDays)
			if len(ranges) != tt.expected {
				t.Errorf("splitDateRange(%s, %s, %d) = %d ranges, want %d",
					tt.begin, tt.end, tt.maxDays, len(ranges), tt.expected)
			}

			// Verify no gaps and no overlaps
			for i := 1; i < len(ranges); {
				prevEnd, _ := time.Parse("2006-01-02", ranges[i-1][1])
				curBegin, _ := time.Parse("2006-01-02", ranges[i][0])
				// curBegin should be exactly 1 day after prevEnd
				expectedBegin := prevEnd.AddDate(0, 0, 1)
				if !curBegin.Equal(expectedBegin) {
					t.Errorf("gap/overlap between ranges %d and %d: prev ends %s, next starts %s (expected %s)",
						i-1, i, ranges[i-1][1], ranges[i][0], expectedBegin.Format("2006-01-02"))
				}
				i++
			}

			// Verify first range starts at begin, last range ends at end
			if ranges[0][0] != tt.begin {
				t.Errorf("first range starts at %s, want %s", ranges[0][0], tt.begin)
			}
			if ranges[len(ranges)-1][1] != tt.end {
				t.Errorf("last range ends at %s, want %s", ranges[len(ranges)-1][1], tt.end)
			}
		})
	}
}

// TestMockSource_Failure tests that mock source can simulate failures.
func TestMockSource_Failure(t *testing.T) {
	source := NewMockRegionSalesSource()
	source.Populate(5, 1)
	source.SetFailCount(2) // first 2 calls fail

	_, err := source.GetRegionSales(context.Background(), "2026-06-01", "2026-06-01")
	if err == nil {
		t.Error("expected error on first call")
	}

	_, err = source.GetRegionSales(context.Background(), "2026-06-01", "2026-06-01")
	if err == nil {
		t.Error("expected error on second call")
	}

	items, err := source.GetRegionSales(context.Background(), "2026-06-01", "2026-06-01")
	if err != nil {
		t.Fatalf("expected success on third call, got: %v", err)
	}
	if len(items) != 5 {
		t.Errorf("got %d items on third call, want 5", len(items))
	}
}
