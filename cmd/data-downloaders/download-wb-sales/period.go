// Package main provides WB Sales Downloader utility.
// This file contains period splitting logic (SRP).
package main

import (
	"time"
)

// defaultMaxDaysPerPeriod is the default maximum period length for WB API reportDetailByPeriod.
// WB API limits requests to 30 days maximum.
const defaultMaxDaysPerPeriod = 30

// maxDaysPerPeriod can be overridden via config for smaller intervals (e.g., 1 day at a time).
// This helps with timeout issues when data volume is high.
var maxDaysPerPeriod = defaultMaxDaysPerPeriod

// DateRange represents a date interval for API requests.
type DateRange struct {
	From time.Time
	To   time.Time
}

// String returns YYYYMMDD or RFC3339 format depending on whether time is specified.
func (dr DateRange) String() string {
	// Use HasTime() method to determine format (single source of truth)
	if dr.HasTime() {
		return dr.From.Format("2006-01-02T15:04:05") + " → " + dr.To.Format("2006-01-02T15:04:05")
	}
	// Иначе показываем только даты
	return dr.From.Format("20060102") + " → " + dr.To.Format("20060102")
}

// HasTime returns true if the date range includes time components (not just dates).
// Matches the logic in SplitPeriod() for consistency.
func (dr DateRange) HasTime() bool {
	if dr.From.IsZero() || dr.To.IsZero() {
		return false
	}
	// Time-based if either from or to has non-zero time components
	// This matches SplitPeriod's isTimeRange logic
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

// SplitPeriod splits a date range into intervals of maxDaysPerPeriod days.
// WB API reportDetailByPeriod has a 30-day maximum per request.
// For date-only ranges (midnight to midnight), splits by calendar days.
// For time ranges, preserves the original time components.
//
// Example:
//   01.01.2025 → 31.01.2025 becomes:
//   - 01.01.2025 → 30.01.2025 (30 days)
//   - 31.01.2025 → 31.01.2025 (1 day)
//
//   2025-01-01T00:00:00 → 2025-01-01T12:00:00 becomes:
//   - 2025-01-01T00:00:00 → 2025-01-01T12:00:00 (half-day, preserved)
func SplitPeriod(from, to time.Time) []DateRange {
	var result []DateRange

	// Check if this is a time-based range (has non-midnight times)
	isTimeRange := from.Hour() != 0 || from.Minute() != 0 || from.Second() != 0 ||
		to.Hour() != 0 || to.Minute() != 0 || to.Second() != 0

	// For short ranges (<= maxDaysPerPeriod), return as-is
	days := int(to.Sub(from).Hours()/24) + 1
	if days <= maxDaysPerPeriod {
		result = append(result, DateRange{From: from, To: to})
		return result
	}

	// For longer ranges, split by maxDaysPerPeriod
	// Preserve time components for first and last intervals
	current := from
	intervalCount := 0

	for current.Before(to) || current.Equal(to) {
		// Calculate end of this interval
		end := current.AddDate(0, 0, maxDaysPerPeriod-1)

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
		intervalCount++

		// Move to start of next day
		nextStart := end.AddDate(0, 0, 1)
		if isTimeRange {
			nextStart = time.Date(nextStart.Year(), nextStart.Month(), nextStart.Day(), 0, 0, 0, 0, nextStart.Location())
		}

		// Stop if we've reached or passed 'to'
		if !nextStart.Before(to) {
			break
		}
		current = nextStart
	}

	return result
}

// ParseDate parses a date string in YYYY-MM-DD format.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// ParseDateTime parses a datetime string.
// Supports multiple formats:
//   - "2006-01-02T15:04:05Z" (RFC3339 with Z)
//   - "2006-01-02T15:04:05" (ISO format without Z)
//   - "2006-01-02" (date only)
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

// AdjustPeriodForResume adjusts the start of a period if there is data in the database.
//
// IMPORTANT CONSERVATIVE APPROACH:
// We only know firstDT and lastDT, NOT all dates in between.
// We CANNOT reliably detect if a period is fully loaded.
// Strategy: ALWAYS LOAD when uncertain, let INSERT OR IGNORE skip duplicates.
//
// Returns:
//   - newFrom: adjusted start time
//   - shouldSkip: true if period is fully loaded (ONLY when 100% certain!)
//   - skippedUntil: timestamp of last data (for tracking)
func AdjustPeriodForResume(from, to, firstDT, lastDT time.Time) (newFrom time.Time, shouldSkip bool, skippedUntil time.Time) {
	// Empty database - load everything
	if lastDT.IsZero() {
		return from, false, time.Time{}
	}

	// Case 1: Requested period is BEFORE any existing data (gap at beginning)
	// Example: DB has Feb-Mar data, requesting Jan
	if to.Before(firstDT) {
		return from, false, time.Time{}  // Load entire period
	}

	// Case 2: Requested period is AFTER all existing data (gap at end)
	// Example: DB has Jan-Feb data, requesting Mar
	if from.After(lastDT) {
		return from, false, time.Time{}  // Load entire period
	}

	// Case 3: Last record is INSIDE requested period - resume from it
	// Example: DB has Jan 1-15, requesting Jan 10-20
	//          Resume from Jan 16
	if (lastDT.After(from) || lastDT.Equal(from)) && lastDT.Before(to) {
		resumeFrom := lastDT.Add(time.Second)
		return resumeFrom, false, lastDT  // Resume from last record
	}

	// Case 4: Uncertain - period might be partially loaded or empty
	// Example: DB has Jan 1-7 and Mar 1-18, requesting Jan 8-15
	//          We DON'T KNOW if Jan 8-15 exists!
	//          Solution: LOAD it, INSERT OR IGNORE will skip duplicates
	//
	// Example: DB has Mar 1-18, requesting Mar 8-15 (fully loaded)
	//          lastDT (Mar 18) > to (Mar 15)
	//          But we CAN'T BE SURE without checking all dates!
	//          Solution: LOAD it (wasteful but SAFE)
	//
	// Only skip if we're 100% certain: single-day period at/before lastDT
	if from.Equal(to) && (from.Equal(lastDT) || from.Before(lastDT)) {
		return to, true, lastDT  // Skip single-day period already loaded
	}

	// Default: LOAD (conservative - better to load twice than miss data)
	return from, false, time.Time{}
}
