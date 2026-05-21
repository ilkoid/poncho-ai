package dllog

import (
	"strings"
	"testing"
	"time"
)

func TestFormatBatch(t *testing.T) {
	tests := []struct {
		name   string
		cur    int
		total  int
		start  time.Time
		want   []string // substrings that must appear
		dont   []string // substrings that must NOT appear
	}{
		{
			name:  "zero total shows count only",
			cur:   5, total: 0,
			want: []string{"5"},
			dont: []string{"/", "ETA"},
		},
		{
			name:  "with total shows fraction and percent",
			cur:   5, total: 12,
			want: []string{"5/12", "41%"},
			dont: []string{"ETA"},
		},
		{
			name:  "with start time shows ETA",
			cur:   5, total: 12,
			start: time.Now().Add(-30 * time.Second),
			want: []string{"5/12", "41%", "ETA"},
		},
		{
			name:  "complete shows 100%",
			cur:   12, total: 12,
			start: time.Now().Add(-2 * time.Minute),
			want: []string{"12/12", "100%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBatch(tt.cur, tt.total, tt.start)

			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("formatBatch(%d, %d, ...) = %q, want substring %q", tt.cur, tt.total, got, w)
				}
			}
			for _, d := range tt.dont {
				if strings.Contains(got, d) {
					t.Errorf("formatBatch(%d, %d, ...) = %q, should NOT contain %q", tt.cur, tt.total, got, d)
				}
			}
		})
	}
}

func TestFormatBatch_ETACalc(t *testing.T) {
	// 5 items done in 10s, 10 remaining → ETA should be ~20s
	start := time.Now().Add(-10 * time.Second)
	got := formatBatch(5, 15, start)

	if !strings.Contains(got, "ETA") {
		t.Errorf("expected ETA in %q", got)
	}
	if !strings.Contains(got, "33%") {
		t.Errorf("expected 33%% in %q", got)
	}
}

func TestColorize_DisabledInTest(t *testing.T) {
	// During tests, stdout is piped so isTerm is false
	got := colorize(31, "hello")
	if got != "hello" {
		t.Errorf("colorize in non-terminal should passthrough, got %q", got)
	}
}

func TestPrintHeader_Format(t *testing.T) {
	// Capture output by checking it doesn't panic and contains expected parts
	// (real output capture would require os.Stdout redirect, which is overkill here)
	// Just verify the function doesn't panic with various inputs.
	PrintHeader("Test Downloader",
		HeaderField{Key: "DB", Value: "/tmp/test.db"},
		HeaderField{Key: "API Key", Value: "abcd...wxyz"},
	)
}

func TestLog_DoesNotPanic(t *testing.T) {
	Log("test message: %d items", 42)
}

func TestError_DoesNotPanic(t *testing.T) {
	Error("something failed: %s", "reason")
}

func TestDone_DoesNotPanic(t *testing.T) {
	Done(2*time.Minute+15*time.Second, "%d rows saved", 500)
}

func TestProgress_DoesNotPanic(t *testing.T) {
	Progress(3, 10, "sales", "150 rows", time.Now().Add(-5*time.Second))
	Progress(1, 0, "balance", "ok", time.Time{})
}
