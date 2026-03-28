package utils

import (
	"testing"
	"time"
	"unicode/utf8"
)

func TestRepeat(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		n        int
		expected string
	}{
		{"basic", "x", 5, "xxxxx"},
		{"empty string", "", 3, ""},
		{"zero count", "a", 0, ""},
		{"negative count", "b", -1, ""},
		{"single char", "█", 10, "██████████"},
		{"multi char", "ab", 3, "ababab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Repeat(tt.s, tt.n)
			if result != tt.expected {
				t.Errorf("Repeat(%q, %d) = %q, want %q", tt.s, tt.n, result, tt.expected)
			}
		})
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{"normal key", "abcd1234efgh5678", "abcd...5678"},
		{"exactly 8 chars", "12345678", "***"},
		{"less than 8", "short", "***"},
		{"empty", "", "***"},
		{"9 chars", "123456789", "1234...6789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskAPIKey(tt.key)
			if result != tt.expected {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"hours", 2*time.Hour + 30*time.Minute, "2h 30m"},
		{"minutes", 45*time.Minute + 30*time.Second, "45m 30s"},
		{"seconds", 30*time.Second, "30s"},
		{"zero", 0, "0s"},
		{"mixed", 1*time.Hour + 5*time.Minute + 6*time.Second, "1h 5m"},
		{"subseconds", 1500*time.Millisecond, "1s"}, // truncated to seconds
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatDuration(tt.d)
			if result != tt.expected {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, result, tt.expected)
			}
		})
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name     string
		current  int
		total    int
		width    int
		wantLen  int
	}{
		{"zero progress", 0, 100, 30, 30},
		{"half progress", 50, 100, 30, 30},
		{"full progress", 100, 100, 30, 30},
		{"over progress", 150, 100, 30, 30},
		{"default width", 5, 10, 0, 30},
		{"custom width", 50, 100, 50, 50},
		{"zero total", 10, 0, 30, 30},
		{"zero current", 0, 10, 20, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProgressBar(tt.current, tt.total, tt.width)
			resultLen := utf8.RuneCountInString(result)
			if resultLen != tt.wantLen {
				t.Errorf("ProgressBar(%d, %d, %d) len = %d, want %d (got: %q)",
					tt.current, tt.total, tt.width, resultLen, tt.wantLen, result)
			}
			// Verify characters are only █ or ░
			for _, r := range result {
				if r != '█' && r != '░' {
					t.Errorf("ProgressBar contains invalid character: %c", r)
				}
			}
		})
	}
}
