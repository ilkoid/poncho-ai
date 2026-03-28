// Package progress provides progress tracking interfaces and implementations
// for long-running operations with ETA calculation and visual feedback.
package progress

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ProgressTracker defines the interface for tracking progress of long-running operations.
// Implementations can be silent (for tests), CLI (for terminals), or custom (for TUI).
type ProgressTracker interface {
	// Update increments the progress by the specified number of items.
	Update(items int) error

	// Done marks the tracker as complete and cleans up resources.
	Done()

	// ETA returns the estimated time remaining as a human-readable string.
	// Returns empty string if ETA cannot be calculated yet.
	ETA() string

	// String returns the current progress representation (e.g., progress bar).
	String() string

	// Current returns the current number of processed items.
	Current() int

	// Total returns the total number of items to process.
	Total() int

	// Elapsed returns the time elapsed since the tracker started.
	Elapsed() time.Duration
}

// CLITracker provides a thread-safe CLI progress tracker with visual feedback.
// It displays a progress bar, calculates ETA, and updates at reasonable intervals.
type CLITracker struct {
	total       int
	current     int
	start       time.Time
	lastPrint   time.Time
	lastPrintMu sync.Mutex
	mu          sync.RWMutex
	prefix      string       // Optional prefix for output
	width       int          // Progress bar width (default: 30)
	printInterval time.Duration // Minimum time between prints (default: 500ms)
	done        bool
}

// CLITrackerConfig holds configuration for CLITracker.
type CLITrackerConfig struct {
	Total         int           // Total number of items to process
	Prefix        string        // Optional prefix for output (e.g., "Downloading:")
	Width         int           // Progress bar width (0 for default 30)
	PrintInterval time.Duration // Minimum time between prints (0 for default 500ms)
}

// NewCLITracker creates a new CLI progress tracker with the specified total.
func NewCLITracker(total int) *CLITracker {
	return NewCLITrackerWithConfig(CLITrackerConfig{
		Total: total,
	})
}

// NewCLITrackerWithConfig creates a new CLI progress tracker with custom configuration.
func NewCLITrackerWithConfig(cfg CLITrackerConfig) *CLITracker {
	width := cfg.Width
	if width <= 0 {
		width = 30
	}

	printInterval := cfg.PrintInterval
	if printInterval <= 0 {
		printInterval = 500 * time.Millisecond
	}

	return &CLITracker{
		total:         cfg.Total,
		start:         time.Now(),
		prefix:        cfg.Prefix,
		width:         width,
		printInterval: printInterval,
	}
}

// Update increments the progress by the specified number of items.
// Thread-safe. Prints progress if enough time has elapsed since last print.
func (t *CLITracker) Update(items int) error {
	if t.done {
		return nil
	}

	t.mu.Lock()
	t.current += items
	current := t.current
	total := t.total
	t.mu.Unlock()

	t.maybePrint(current, total)
	return nil
}

// Add is an alias for Update for convenience.
func (t *CLITracker) Add(items int) error {
	return t.Update(items)
}

// Increment adds 1 to the current progress.
func (t *CLITracker) Increment() error {
	return t.Update(1)
}

// Done marks the tracker as complete and prints the final progress.
// Should be called via defer to ensure final output is shown.
func (t *CLITracker) Done() {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	t.mu.Unlock()

	// Always print final state
	t.print(t.current, t.total)
}

// ETA returns the estimated time remaining as a human-readable string.
// Returns empty string if no progress has been made yet.
func (t *CLITracker) ETA() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.current == 0 {
		return ""
	}

	elapsed := time.Since(t.start)
	avgPerItem := elapsed / time.Duration(t.current)
	remaining := time.Duration(t.total-t.current) * avgPerItem

	return "~" + utils.FormatDuration(remaining)
}

// String returns the current progress bar representation.
func (t *CLITracker) String() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.formatProgress(t.current, t.total)
}

// Current returns the current number of processed items.
func (t *CLITracker) Current() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current
}

// Total returns the total number of items to process.
func (t *CLITracker) Total() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.total
}

// Elapsed returns the time elapsed since the tracker started.
func (t *CLITracker) Elapsed() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return time.Since(t.start).Truncate(time.Second)
}

// ProgressPercent returns the completion percentage (0-100).
func (t *CLITracker) ProgressPercent() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.total == 0 {
		return 0
	}
	return float64(t.current) / float64(t.total) * 100
}

// maybePrint prints progress if enough time has elapsed since last print.
func (t *CLITracker) maybePrint(current, total int) {
	t.lastPrintMu.Lock()
	lastPrint := t.lastPrint
	t.lastPrintMu.Unlock()

	if time.Since(lastPrint) >= t.printInterval {
		t.print(current, total)
		t.lastPrintMu.Lock()
		t.lastPrint = time.Now()
		t.lastPrintMu.Unlock()
	}
}

// print prints the current progress to stdout.
func (t *CLITracker) print(current, total int) {
	line := t.formatProgress(current, total)
	fmt.Print("\r" + line) // Use \r to overwrite previous line
}

// formatProgress formats the progress as a string with bar and stats.
func (t *CLITracker) formatProgress(current, total int) string {
	if total <= 0 {
		return fmt.Sprintf("%s%s", t.prefix, "???")
	}

	bar := utils.ProgressBar(current, total, t.width)
	percent := float64(current) / float64(total) * 100
	elapsed := time.Since(t.start).Truncate(time.Second)

	var eta string
	if current > 0 {
		avgPerItem := elapsed / time.Duration(current)
		remaining := time.Duration(total-current) * avgPerItem
		eta = utils.FormatDuration(remaining)
	}

	prefix := t.prefix
	if prefix != "" && !strings.HasSuffix(prefix, " ") {
		prefix += " "
	}

	if eta != "" {
		return fmt.Sprintf("%s[%s] %d/%d (%.1f%%) ETA: %s", prefix, bar, current, total, percent, eta)
	}
	return fmt.Sprintf("%s[%s] %d/%d (%.1f%%)", prefix, bar, current, total, percent)
}

// SilentTracker is a no-op ProgressTracker for testing or silent operation.
// It tracks progress internally but produces no output.
type SilentTracker struct {
	total   int
	current int
	start   time.Time
	mu      sync.RWMutex
}

// NewSilentTracker creates a new silent progress tracker.
func NewSilentTracker(total int) *SilentTracker {
	return &SilentTracker{
		total: total,
		start: time.Now(),
	}
}

// Update increments the progress (silent).
func (t *SilentTracker) Update(items int) error {
	t.mu.Lock()
	t.current += items
	t.mu.Unlock()
	return nil
}

// Increment adds 1 to the current progress (silent).
func (t *SilentTracker) Increment() error {
	return t.Update(1)
}

// Done marks the tracker as complete (silent).
func (t *SilentTracker) Done() {}

// ETA returns the estimated time remaining.
func (t *SilentTracker) ETA() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.current == 0 {
		return ""
	}

	elapsed := time.Since(t.start)
	avgPerItem := elapsed / time.Duration(t.current)
	remaining := time.Duration(t.total-t.current) * avgPerItem

	return "~" + utils.FormatDuration(remaining)
}

// String returns empty string (silent).
func (t *SilentTracker) String() string { return "" }

// Current returns the current number of processed items.
func (t *SilentTracker) Current() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current
}

// Total returns the total number of items to process.
func (t *SilentTracker) Total() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.total
}

// Elapsed returns the time elapsed since start.
func (t *SilentTracker) Elapsed() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return time.Since(t.start).Truncate(time.Second)
}

// MultiTracker tracks multiple sub-tasks within a single progress display.
// Useful for batch operations with multiple stages.
type MultiTracker struct {
 trackers map[string]ProgressTracker
	mu       sync.RWMutex
	total    int // Sum of all sub-task totals
}

// NewMultiTracker creates a new multi-tracker.
func NewMultiTracker() *MultiTracker {
	return &MultiTracker{
		trackers: make(map[string]ProgressTracker),
	}
}

// Register adds a named sub-tracker.
func (m *MultiTracker) Register(name string, tracker ProgressTracker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trackers[name] = tracker
	m.total += tracker.Total()
}

// Update updates a specific sub-tracker by name.
func (m *MultiTracker) Update(name string, items int) error {
	m.mu.RLock()
	tracker, ok := m.trackers[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("tracker not found: %s", name)
	}
	return tracker.Update(items)
}

// Done marks all sub-trackers as complete.
func (m *MultiTracker) Done() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tracker := range m.trackers {
		tracker.Done()
	}
}

// ETA returns the overall ETA across all trackers.
func (m *MultiTracker) ETA() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalRemaining int
	for _, tracker := range m.trackers {
		totalRemaining += (tracker.Total() - tracker.Current())
	}

	if totalRemaining == 0 {
		return ""
	}

	// Rough estimate: use average rate across all trackers
	totalCurrent := 0
	for _, tracker := range m.trackers {
		totalCurrent += tracker.Current()
	}

	if totalCurrent == 0 {
		return ""
	}

	// Simplified ETA calculation for multi-tracker
	avgElapsed := m.avgElapsed()
	if avgElapsed == 0 {
		return ""
	}

	avgPerItem := avgElapsed / time.Duration(totalCurrent)
	remaining := time.Duration(totalRemaining) * avgPerItem

	return "~" + utils.FormatDuration(remaining)
}

// String returns a summary of all trackers.
func (m *MultiTracker) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var parts []string
	for name, tracker := range m.trackers {
		parts = append(parts, fmt.Sprintf("%s: %d/%d", name, tracker.Current(), tracker.Total()))
	}

	return strings.Join(parts, ", ")
}

// Current returns the sum of current progress across all trackers.
func (m *MultiTracker) Current() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, tracker := range m.trackers {
		total += tracker.Current()
	}
	return total
}

// Total returns the sum of totals across all trackers.
func (m *MultiTracker) Total() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.total
}

// Elapsed returns the elapsed time since the first tracker started.
func (m *MultiTracker) Elapsed() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	minElapsed := time.Duration(0)
	for _, tracker := range m.trackers {
		if elapsed := tracker.Elapsed(); elapsed > minElapsed {
			minElapsed = elapsed
		}
	}
	return minElapsed
}

// avgElapsed calculates the average elapsed time across trackers.
func (m *MultiTracker) avgElapsed() time.Duration {
	if len(m.trackers) == 0 {
		return 0
	}

	total := time.Duration(0)
	for _, tracker := range m.trackers {
		total += tracker.Elapsed()
	}

	return total / time.Duration(len(m.trackers))
}
