// Package utils provides reusable utility functions for CLI applications.
// This file contains date and time utilities for API request handling.
package utils

import (
	"time"
)

// DateRange represents a date interval for API requests.
// Supports both date-only (YYYY-MM-DD) and datetime (RFC3339) ranges.
type DateRange struct {
	From time.Time
	To   time.Time
}

// String returns YYYYMMDD or RFC3339 format depending on whether time is specified.
// For date-only ranges: "20250101 → 20250131"
// For time ranges: "2025-01-01T00:00:00 → 2025-01-31T23:59:59"
func (dr DateRange) String() string {
	if dr.HasTime() {
		return dr.From.Format("2006-01-02T15:04:05") + " → " + dr.To.Format("2006-01-02T15:04:05")
	}
	return dr.From.Format("20060102") + " → " + dr.To.Format("20060102")
}

// HasTime returns true if the date range includes time components (not just dates).
// Returns false if either date is zero or both are at midnight (00:00:00).
func (dr DateRange) HasTime() bool {
	if dr.From.IsZero() || dr.To.IsZero() {
		return false
	}
	return dr.From.Hour() != 0 || dr.From.Minute() != 0 || dr.From.Second() != 0 ||
		dr.To.Hour() != 0 || dr.To.Minute() != 0 || dr.To.Second() != 0
}

// FromRFC3339 returns dateFrom in RFC3339 format for time-based API requests.
func (dr DateRange) FromRFC3339() string {
	return dr.From.Format(time.RFC3339)
}

// ToRFC3339 returns dateTo in RFC3339 format for time-based API requests.
func (dr DateRange) ToRFC3339() string {
	return dr.To.Format(time.RFC3339)
}

// FromInt returns dateFrom as integer YYYYMMDD for WB API.
// Example: 2025-01-15 → 20250115
func (dr DateRange) FromInt() int {
	s := dr.From.Format("20060102")
	result := 0
	for _, c := range s {
		result = result*10 + int(c-'0')
	}
	return result
}

// ToInt returns dateTo as integer YYYYMMDD for WB API.
func (dr DateRange) ToInt() int {
	s := dr.To.Format("20060102")
	result := 0
	for _, c := range s {
		result = result*10 + int(c-'0')
	}
	return result
}

// Split splits the date range into intervals of maxDays days.
// For date-only ranges (midnight to midnight), splits by calendar days.
// For time ranges, preserves the original time components.
//
// Example (maxDays=30):
//   01.01.2025 → 31.01.2025 becomes:
//   - 01.01.2025 → 30.01.2025 (30 days)
//   - 31.01.2025 → 31.01.2025 (1 day)
//
// Example (time range):
//   2025-01-01T00:00:00 → 2025-01-01T12:00:00 becomes:
//   - 2025-01-01T00:00:00 → 2025-01-01T12:00:00 (preserved)
func (dr DateRange) Split(maxDays int) []DateRange {
	var result []DateRange

	from, to := dr.From, dr.To
	if from.IsZero() || to.IsZero() {
		return result
	}

	// Check if this is a time-based range (has non-midnight times)
	isTimeRange := from.Hour() != 0 || from.Minute() != 0 || from.Second() != 0 ||
		to.Hour() != 0 || to.Minute() != 0 || to.Second() != 0

	// For short ranges (<= maxDays), return as-is
	days := int(to.Sub(from).Hours()/24) + 1
	if days <= maxDays {
		result = append(result, DateRange{From: from, To: to})
		return result
	}

	// For longer ranges, split by maxDays
	current := from

	for current.Before(to) || current.Equal(to) {
		// Calculate end of this interval
		end := current.AddDate(0, 0, maxDays-1)

		// For time ranges, set end to end of day (23:59:59)
		if isTimeRange && !end.Equal(to) {
			end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, end.Location())
		}

		if end.After(to) {
			end = to
		}

		result = append(result, DateRange{
			From: current,
			To:   end,
		})

		// Move to start of next day
		nextStart := end.AddDate(0, 0, 1)
		if isTimeRange {
			nextStart = time.Date(nextStart.Year(), nextStart.Month(), nextStart.Day(), 0, 0, 0, 0, nextStart.Location())
		}

		// Stop if we've passed 'to' (allow equal - process last interval)
		if nextStart.After(to) {
			break
		}
		current = nextStart
	}

	return result
}

// Days returns the number of days in the range (inclusive).
func (dr DateRange) Days() int {
	if dr.From.IsZero() || dr.To.IsZero() {
		return 0
	}
	return int(dr.To.Sub(dr.From).Hours()/24) + 1
}

// Contains returns true if the given date is within the range (inclusive).
func (dr DateRange) Contains(date time.Time) bool {
	if dr.From.IsZero() || dr.To.IsZero() {
		return false
	}
	return (date.Equal(dr.From) || date.After(dr.From)) &&
		(date.Equal(dr.To) || date.Before(dr.To))
}

// ParseDate parses a date string in YYYY-MM-DD format.
// Returns zero time and error if parsing fails.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// ParseDateTime parses a datetime string with flexible format support.
// Supports multiple formats (tried in order):
//   - "2006-01-02T15:04:05Z" (RFC3339 with Z)
//   - "2006-01-02T15:04:05" (ISO format without Z)
//   - "2006-01-02" (date only, sets time to 00:00:00)
// Returns zero time and error if all formats fail.
func ParseDateTime(s string) (time.Time, error) {
	// Try RFC3339 format first (with Z suffix)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try ISO format without Z (YYYY-MM-DDTHH:MM:SS)
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	// Fall back to date-only format
	return time.Parse("2006-01-02", s)
}

// NewDateRange creates a DateRange from two dates.
// Returns zero range if either date is zero.
func NewDateRange(from, to time.Time) DateRange {
	return DateRange{From: from, To: to}
}

// NewDateRangeFromStrings creates a DateRange from date strings in YYYY-MM-DD format.
// Returns zero range and error if parsing fails.
func NewDateRangeFromStrings(fromStr, toStr string) (DateRange, error) {
	from, err := ParseDate(fromStr)
	if err != nil {
		return DateRange{}, err
	}
	to, err := ParseDate(toStr)
	if err != nil {
		return DateRange{}, err
	}
	return DateRange{From: from, To: to}, nil
}

// TodayRange returns a DateRange for today (00:00:00 to 23:59:59).
func TodayRange() DateRange {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	return DateRange{From: start, To: end}
}

// YesterdayRange returns a DateRange for yesterday.
func YesterdayRange() DateRange {
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	start := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, now.Location())
	end := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 23, 59, 59, 0, now.Location())
	return DateRange{From: start, To: end}
}

// LastNDaysRange returns a DateRange for the last N days (inclusive).
// Example: N=7 → from 7 days ago to today.
func LastNDaysRange(n int) DateRange {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	start := end.AddDate(0, 0, -n+1)
	return DateRange{From: start, To: end}
}
