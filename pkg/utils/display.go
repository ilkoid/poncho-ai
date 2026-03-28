// Package utils provides reusable utility functions for CLI applications.
// This file contains display and formatting helpers.
package utils

import (
	"fmt"
	"strings"
	"time"
)

// Repeat returns a string consisting of s repeated n times.
// Optimized with strings.Builder for performance.
func Repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) * n)
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}

// MaskAPIKey hides most of the API key for security.
// Shows first 4 and last 4 characters with "..." in between.
// Returns "***" for keys with 8 or fewer characters.
func MaskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// FormatDuration returns a human-readable duration string.
// Formats as "Xh Ym", "Ym Zs", or "Zs" depending on magnitude.
func FormatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// ProgressBar returns a visual progress bar string.
// The bar has the specified width (default 30 if <= 0).
// Uses filled block (█) and light shade (░) characters.
// Returns empty string if max <= 0.
func ProgressBar(current, total, width int) string {
	if width <= 0 {
		width = 30
	}
	if total <= 0 {
		return Repeat("░", width)
	}

	// Calculate filled portion
	filled := int(float64(current) / float64(total) * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}

	return Repeat("█", filled) + Repeat("░", width-filled)
}
