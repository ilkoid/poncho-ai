package wb

import "time"

// DefaultMaxDaysPerPeriod is the default maximum period length for WB Statistics API.
// WB API limits reportDetailByPeriod requests to 30 days.
const DefaultMaxDaysPerPeriod = 30

// MaxDaysPerPeriod can be overridden if needed (for testing or special cases).
var MaxDaysPerPeriod = DefaultMaxDaysPerPeriod

// DateRange represents a date interval for API requests.
type DateRange struct {
	From time.Time
	To   time.Time
}

// String returns a human-readable representation.
func (dr DateRange) String() string {
	if dr.HasTime() {
		return dr.From.Format("2006-01-02T15:04:05") + " → " + dr.To.Format("2006-01-02T15:04:05")
	}
	return dr.From.Format("20060102") + " → " + dr.To.Format("20060102")
}

// HasTime returns true if the range includes specific time components.
func (dr DateRange) HasTime() bool {
	if dr.From.IsZero() || dr.To.IsZero() {
		return false
	}
	return dr.From.Hour() != 0 || dr.From.Minute() != 0 || dr.From.Second() != 0 ||
		dr.To.Hour() != 0 || dr.To.Minute() != 0 || dr.To.Second() != 0
}

// FromRFC3339 returns the from date in RFC3339 format.
func (dr DateRange) FromRFC3339() string {
	return dr.From.Format(time.RFC3339)
}

// ToRFC3339 returns the to date in RFC3339 format.
func (dr DateRange) ToRFC3339() string {
	return dr.To.Format(time.RFC3339)
}

// FromInt returns dateFrom as integer YYYYMMDD.
func (dr DateRange) FromInt() int {
	s := dr.From.Format("20060102")
	result := 0
	for _, c := range s {
		result = result*10 + int(c-'0')
	}
	return result
}

// ToInt returns dateTo as integer YYYYMMDD.
func (dr DateRange) ToInt() int {
	s := dr.To.Format("20060102")
	result := 0
	for _, c := range s {
		result = result*10 + int(c-'0')
	}
	return result
}

// SplitPeriod splits a date range into intervals of MaxDaysPerPeriod days.
// This is the canonical implementation used by all WB data downloaders.
func SplitPeriod(from, to time.Time) []DateRange {
	var result []DateRange

	isTimeRange := from.Hour() != 0 || from.Minute() != 0 || from.Second() != 0 ||
		to.Hour() != 0 || to.Minute() != 0 || to.Second() != 0

	days := int(to.Sub(from).Hours()/24) + 1
	if days <= MaxDaysPerPeriod {
		result = append(result, DateRange{From: from, To: to})
		return result
	}

	current := from

	for current.Before(to) || current.Equal(to) {
		end := current.AddDate(0, 0, MaxDaysPerPeriod-1)

		if isTimeRange && !end.Equal(to) {
			end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, end.Location())
		}

		if end.After(to) {
			end = to
		}

		result = append(result, DateRange{From: current, To: end})

		nextStart := end.AddDate(0, 0, 1)
		if isTimeRange {
			nextStart = time.Date(nextStart.Year(), nextStart.Month(), nextStart.Day(), 0, 0, 0, 0, nextStart.Location())
		}

		if !nextStart.Before(to) {
			break
		}
		current = nextStart
	}

	return result
}

// ParseDate parses YYYY-MM-DD.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// ParseDateTime parses various datetime formats used in configs.
func ParseDateTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// AdjustPeriodForResume implements smart resume logic.
// Conservative safe approach: only skip when 100% certain.
func AdjustPeriodForResume(from, to, firstDT, lastDT time.Time) (newFrom time.Time, shouldSkip bool, skippedUntil time.Time) {
	if lastDT.IsZero() {
		return from, false, time.Time{}
	}

	if to.Before(firstDT) {
		return from, false, time.Time{}
	}

	if from.After(lastDT) {
		return from, false, time.Time{}
	}

	if (lastDT.After(from) || lastDT.Equal(from)) && lastDT.Before(to) {
		resumeFrom := lastDT.Add(time.Second)
		return resumeFrom, false, lastDT
	}

	if from.Equal(to) && (from.Equal(lastDT) || from.Before(lastDT)) {
		return to, true, lastDT
	}

	return from, false, time.Time{}
}
