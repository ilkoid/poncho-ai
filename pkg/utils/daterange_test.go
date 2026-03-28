package utils

import (
	"testing"
	"time"
)

func TestDateRangeString(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected string
	}{
		{"date only", "2025-01-01", "2025-01-31", "20250101 → 20250131"},
		{"with time", "2025-01-01T00:00:00", "2025-01-31T23:59:59", "2025-01-01T00:00:00 → 2025-01-31T23:59:59"},
		{"same day", "2025-01-15", "2025-01-15", "20250115 → 20250115"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, _ := ParseDateTime(tt.from)
			to, _ := ParseDateTime(tt.to)
			dr := DateRange{From: from, To: to}
			if dr.String() != tt.expected {
				t.Errorf("String() = %q, want %q", dr.String(), tt.expected)
			}
		})
	}
}

func TestDateRangeHasTime(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected bool
	}{
		{"date only", "2025-01-01", "2025-01-31", false},
		{"from has time", "2025-01-01T10:30:00", "2025-01-31", true},
		{"to has time", "2025-01-01", "2025-01-31T23:59:59", true},
		{"both have time", "2025-01-01T00:00:00", "2025-01-31T23:59:59", true},
		{"zero dates", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, _ := ParseDateTime(tt.from)
			to, _ := ParseDateTime(tt.to)
			dr := DateRange{From: from, To: to}
			if dr.HasTime() != tt.expected {
				t.Errorf("HasTime() = %v, want %v", dr.HasTime(), tt.expected)
			}
		})
	}
}

func TestDateRangeFromToInt(t *testing.T) {
	from, _ := ParseDate("2025-01-15")
	to, _ := ParseDate("2025-01-31")
	dr := DateRange{From: from, To: to}

	if dr.FromInt() != 20250115 {
		t.Errorf("FromInt() = %d, want %d", dr.FromInt(), 20250115)
	}
	if dr.ToInt() != 20250131 {
		t.Errorf("ToInt() = %d, want %d", dr.ToInt(), 20250131)
	}
}

func TestDateRangeSplit(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		to        string
		maxDays   int
		wantCount int
	}{
		{"single day", "2025-01-15", "2025-01-15", 30, 1},
		{"short range", "2025-01-01", "2025-01-10", 30, 1},
		{"exact limit", "2025-01-01", "2025-01-30", 30, 1},
		{"over limit", "2025-01-01", "2025-01-31", 30, 2},
		{"long range", "2025-01-01", "2025-03-31", 30, 3},
		{"custom limit", "2025-01-01", "2025-01-07", 5, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, _ := ParseDate(tt.from)
			to, _ := ParseDate(tt.to)
			dr := DateRange{From: from, To: to}
			ranges := dr.Split(tt.maxDays)
			if len(ranges) != tt.wantCount {
				t.Errorf("Split(%d) = %d ranges, want %d", tt.maxDays, len(ranges), tt.wantCount)
			}
		})
	}
}

func TestDateRangeSplitContinuity(t *testing.T) {
	from, _ := ParseDate("2025-01-01")
	to, _ := ParseDate("2025-01-31")
	dr := DateRange{From: from, To: to}
	ranges := dr.Split(10)

	// Verify continuity (no gaps, no overlaps)
	for i := 0; i < len(ranges)-1; i++ {
		currentEnd := ranges[i].To
		nextStart := ranges[i+1].From
		expectedNext := currentEnd.AddDate(0, 0, 1)

		if !nextStart.Equal(expectedNext) {
			t.Errorf("Gap or overlap at index %d: current.End=%v, next.Start=%v, expected=%v",
				i, currentEnd, nextStart, expectedNext)
		}
	}
}

func TestDateRangeSplitTimeRange(t *testing.T) {
	from, _ := time.Parse("2006-01-02T15:04:05", "2025-01-01T10:00:00")
	to, _ := time.Parse("2006-01-02T15:04:05", "2025-01-01T18:00:00")
	dr := DateRange{From: from, To: to}

	ranges := dr.Split(30)
	if len(ranges) != 1 {
		t.Fatalf("Split() = %d ranges, want 1", len(ranges))
	}

	// Time components should be preserved
	if !ranges[0].From.Equal(from) || !ranges[0].To.Equal(to) {
		t.Errorf("Time components not preserved: got %v → %v, want %v → %v",
			ranges[0].From, ranges[0].To, from, to)
	}
}

func TestDateRangeDays(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected int
	}{
		{"single day", "2025-01-15", "2025-01-15", 1},
		{"one week", "2025-01-01", "2025-01-07", 7},
		{"january", "2025-01-01", "2025-01-31", 31},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, _ := ParseDate(tt.from)
			to, _ := ParseDate(tt.to)
			dr := DateRange{From: from, To: to}
			if dr.Days() != tt.expected {
				t.Errorf("Days() = %d, want %d", dr.Days(), tt.expected)
			}
		})
	}
}

func TestDateRangeContains(t *testing.T) {
	from, _ := ParseDate("2025-01-10")
	to, _ := ParseDate("2025-01-20")
	dr := DateRange{From: from, To: to}

	tests := []struct {
		date     string
		expected bool
	}{
		{"2025-01-09", false}, // before
		{"2025-01-10", true},  // start
		{"2025-01-15", true},  // middle
		{"2025-01-20", true},  // end
		{"2025-01-21", false}, // after
	}

	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			date, _ := ParseDate(tt.date)
			if dr.Contains(date) != tt.expected {
				t.Errorf("Contains(%v) = %v, want %v", date, dr.Contains(date), tt.expected)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		input    string
		wantErr  bool
		expected string // YYYY-MM-DD format
	}{
		{"2025-01-15", false, "2025-01-15"},
		{"invalid", true, ""},
		{"", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDate(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result.Format("2006-01-02") != tt.expected {
				t.Errorf("ParseDate(%q) = %v, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseDateTime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
		verify   func(time.Time) bool
	}{
		{
			"RFC3339 with Z",
			"2025-01-15T10:30:00Z",
			false,
			func(t time.Time) bool { return t.Year() == 2025 && t.Month() == 1 && t.Day() == 15 },
		},
		{
			"ISO without Z",
			"2025-01-15T10:30:00",
			false,
			func(t time.Time) bool { return t.Hour() == 10 && t.Minute() == 30 },
		},
		{
			"date only",
			"2025-01-15",
			false,
			func(t time.Time) bool { return t.Hour() == 0 && t.Minute() == 0 },
		},
		{
			"invalid",
			"invalid-date",
			true,
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDateTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDateTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.verify != nil && !tt.verify(result) {
				t.Errorf("ParseDateTime(%q) = %v, verification failed", tt.input, result)
			}
		})
	}
}

func TestNewDateRangeFromStrings(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"valid", "2025-01-01", "2025-01-31", false},
		{"invalid from", "invalid", "2025-01-31", true},
		{"invalid to", "2025-01-01", "invalid", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dr, err := NewDateRangeFromStrings(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDateRangeFromStrings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				from, _ := ParseDate(tt.from)
				to, _ := ParseDate(tt.to)
				if dr.From != from || dr.To != to {
					t.Errorf("NewDateRangeFromStrings() = {%v, %v}, want {%v, %v}", dr.From, dr.To, from, to)
				}
			}
		})
	}
}

func TestTodayRange(t *testing.T) {
	dr := TodayRange()
	now := time.Now()

	expectedStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	expectedEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())

	if !dr.From.Equal(expectedStart) {
		t.Errorf("TodayRange().From = %v, want %v", dr.From, expectedStart)
	}
	if !dr.To.Equal(expectedEnd) {
		t.Errorf("TodayRange().To = %v, want %v", dr.To, expectedEnd)
	}
}

func TestYesterdayRange(t *testing.T) {
	dr := YesterdayRange()
	now := time.Now()

	expectedStart := now.AddDate(0, 0, -1)
	expectedStart = time.Date(expectedStart.Year(), expectedStart.Month(), expectedStart.Day(), 0, 0, 0, 0, now.Location())

	if !dr.From.Equal(expectedStart) {
		t.Errorf("YesterdayRange().From = %v, want %v", dr.From, expectedStart)
	}
}

func TestLastNDaysRange(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		wantDays int
	}{
		{"1 day", 1, 1},
		{"7 days", 7, 7},
		{"30 days", 30, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dr := LastNDaysRange(tt.n)
			if dr.Days() != tt.wantDays {
				t.Errorf("LastNDaysRange(%d).Days() = %d, want %d", tt.n, dr.Days(), tt.wantDays)
			}
		})
	}
}

func TestDateRangeRFC3339(t *testing.T) {
	from, _ := time.Parse(time.RFC3339, "2025-01-01T10:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-01-31T18:30:00Z")
	dr := DateRange{From: from, To: to}

	if dr.FromRFC3339() != "2025-01-01T10:00:00Z" {
		t.Errorf("FromRFC3339() = %s, want 2025-01-01T10:00:00Z", dr.FromRFC3339())
	}
	if dr.ToRFC3339() != "2025-01-31T18:30:00Z" {
		t.Errorf("ToRFC3339() = %s, want 2025-01-31T18:30:00Z", dr.ToRFC3339())
	}
}
