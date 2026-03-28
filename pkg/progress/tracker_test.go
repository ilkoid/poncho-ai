package progress

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCLITracker_Basic(t *testing.T) {
	tracker := NewCLITracker(100)

	if tracker.Total() != 100 {
		t.Errorf("Total() = %d, want 100", tracker.Total())
	}

	if tracker.Current() != 0 {
		t.Errorf("Current() = %d, want 0", tracker.Current())
	}

	if tracker.ProgressPercent() != 0 {
		t.Errorf("ProgressPercent() = %f, want 0", tracker.ProgressPercent())
	}
}

func TestCLITracker_Update(t *testing.T) {
	tracker := NewCLITracker(100)

	err := tracker.Update(25)
	if err != nil {
		t.Fatalf("Update(25) error = %v", err)
	}

	if tracker.Current() != 25 {
		t.Errorf("Current() = %d, want 25", tracker.Current())
	}

	if tracker.ProgressPercent() != 25.0 {
		t.Errorf("ProgressPercent() = %f, want 25.0", tracker.ProgressPercent())
	}
}

func TestCLITracker_Increment(t *testing.T) {
	tracker := NewCLITracker(100)

	for i := 0; i < 10; i++ {
		if err := tracker.Increment(); err != nil {
			t.Fatalf("Increment() error = %v", err)
		}
	}

	if tracker.Current() != 10 {
		t.Errorf("Current() = %d, want 10", tracker.Current())
	}
}

func TestCLITracker_ETA(t *testing.T) {
	tracker := NewCLITracker(100)

	// No progress yet
	eta := tracker.ETA()
	if eta != "" {
		t.Errorf("ETA() = %q, want empty string when no progress", eta)
	}

	// Make some progress
	tracker.Update(50)
	time.Sleep(10 * time.Millisecond) // Small delay for measurable elapsed

	eta = tracker.ETA()
	if eta == "" {
		t.Error("ETA() = empty string, want estimate after progress")
	}
	if !strings.HasPrefix(eta, "~") {
		t.Errorf("ETA() = %q, want prefix ~", eta)
	}
}

func TestCLITracker_Done(t *testing.T) {
	tracker := NewCLITracker(100)
	tracker.Update(50)

	tracker.Done()

	// Update after Done should be no-op
	err := tracker.Update(10)
	if err != nil {
		t.Errorf("Update() after Done error = %v", err)
	}

	// Current should not have changed
	if tracker.Current() != 50 {
		t.Errorf("Current() after Done and Update = %d, want 50", tracker.Current())
	}

	// Second Done should be no-op (no panic)
	tracker.Done()
}

func TestCLITracker_String(t *testing.T) {
	tracker := NewCLITracker(100)
	tracker.Update(30)

	s := tracker.String()
	if !strings.Contains(s, "30/100") {
		t.Errorf("String() = %q, want contain '30/100'", s)
	}
	if !strings.Contains(s, "30.0%") {
		t.Errorf("String() = %q, want contain '30.0%%'", s)
	}
	// Should contain progress bar (█ or ░ chars)
	if !strings.ContainsAny(s, "█░") {
		t.Errorf("String() = %q, want contain progress bar chars", s)
	}
}

func TestCLITracker_WithPrefix(t *testing.T) {
	tracker := NewCLITrackerWithConfig(CLITrackerConfig{
		Total:  100,
		Prefix: "Downloading:",
	})

	tracker.Update(25)
	s := tracker.String()

	if !strings.HasPrefix(s, "Downloading: [") {
		t.Errorf("String() = %q, want prefix 'Downloading: ['", s)
	}
}

func TestCLITracker_ConcurrentUpdates(t *testing.T) {
	tracker := NewCLITracker(1000)
	var wg sync.WaitGroup

	// Launch 10 goroutines, each updating 100 times
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.Increment()
			}
		}()
	}

	wg.Wait()

	if tracker.Current() != 1000 {
		t.Errorf("Current() = %d, want 1000 after concurrent updates", tracker.Current())
	}
}

func TestCLITracker_ZeroTotal(t *testing.T) {
	tracker := NewCLITracker(0)

	if tracker.ProgressPercent() != 0 {
		t.Errorf("ProgressPercent() = %f, want 0 for zero total", tracker.ProgressPercent())
	}

	// Should not panic
	tracker.Update(10)
	tracker.Done()
}

func TestSilentTracker_Basic(t *testing.T) {
	tracker := NewSilentTracker(100)

	if tracker.Total() != 100 {
		t.Errorf("Total() = %d, want 100", tracker.Total())
	}

	tracker.Update(25)

	if tracker.Current() != 25 {
		t.Errorf("Current() = %d, want 25", tracker.Current())
	}

	// Silent tracker returns empty string
	if tracker.String() != "" {
		t.Errorf("String() = %q, want empty string", tracker.String())
	}
}

func TestSilentTracker_Done(t *testing.T) {
	tracker := NewSilentTracker(100)
	tracker.Update(50)
	tracker.Done()

	// Should not panic or print anything
	tracker.Update(10)
	if tracker.Current() != 60 {
		t.Errorf("Current() = %d, want 60", tracker.Current())
	}
}

func TestSilentTracker_ETA(t *testing.T) {
	tracker := NewSilentTracker(100)

	eta := tracker.ETA()
	if eta != "" {
		t.Errorf("ETA() = %q, want empty string when no progress", eta)
	}

	tracker.Update(50)
	time.Sleep(10 * time.Millisecond)

	eta = tracker.ETA()
	if eta == "" {
		t.Error("ETA() = empty string, want estimate after progress")
	}
}

func TestMultiTracker_Basic(t *testing.T) {
	multi := NewMultiTracker()

	t1 := NewSilentTracker(100)
	t2 := NewSilentTracker(50)

	multi.Register("task1", t1)
	multi.Register("task2", t2)

	if multi.Total() != 150 {
		t.Errorf("Total() = %d, want 150", multi.Total())
	}

	if multi.Current() != 0 {
		t.Errorf("Current() = %d, want 0", multi.Current())
	}

	t1.Update(25)
	t2.Update(10)

	if multi.Current() != 35 {
		t.Errorf("Current() = %d, want 35", multi.Current())
	}
}

func TestMultiTracker_UpdateByName(t *testing.T) {
	multi := NewMultiTracker()

	t1 := NewSilentTracker(100)
	multi.Register("task1", t1)

	err := multi.Update("task1", 25)
	if err != nil {
		t.Fatalf("Update('task1', 25) error = %v", err)
	}

	if t1.Current() != 25 {
		t.Errorf("task1.Current() = %d, want 25", t1.Current())
	}

	// Update non-existent tracker
	err = multi.Update("nonexistent", 10)
	if err == nil {
		t.Error("Update('nonexistent') expected error, got nil")
	}
}

func TestMultiTracker_Done(t *testing.T) {
	multi := NewMultiTracker()

	t1 := NewSilentTracker(100)
	t2 := NewSilentTracker(50)

	multi.Register("task1", t1)
	multi.Register("task2", t2)

	multi.Done()

	// Should mark all as done
	t1.Update(10)
	t2.Update(5)

	// Current still updates (Done is just for cleanup)
	if multi.Current() != 15 {
		t.Errorf("Current() = %d, want 15", multi.Current())
	}
}

func TestMultiTracker_String(t *testing.T) {
	multi := NewMultiTracker()

	t1 := NewSilentTracker(100)
	t2 := NewSilentTracker(50)

	multi.Register("task1", t1)
	multi.Register("task2", t2)

	t1.Update(25)
	t2.Update(10)

	s := multi.String()

	if !strings.Contains(s, "task1: 25/100") {
		t.Errorf("String() = %q, want contain 'task1: 25/100'", s)
	}
	if !strings.Contains(s, "task2: 10/50") {
		t.Errorf("String() = %q, want contain 'task2: 10/50'", s)
	}
}

func TestCLITrackerConfig_CustomWidth(t *testing.T) {
	tracker := NewCLITrackerWithConfig(CLITrackerConfig{
		Total: 100,
		Width: 50,
	})

	tracker.Update(50)
	s := tracker.String()

	// Should contain progress bar with 50 chars
	// Count the bracket-enclosed portion
	start := strings.Index(s, "[")
	end := strings.Index(s, "]")
	if start == -1 || end == -1 {
		t.Fatalf("String() = %q, want contain [progress bar]", s)
	}

	barContent := s[start+1 : end]
	barLen := utf8.RuneCountInString(barContent)
	if barLen != 50 {
		t.Errorf("Progress bar length = %d, want 50", barLen)
	}
}

func TestCLITrackerConfig_FastPrintInterval(t *testing.T) {
	tracker := NewCLITrackerWithConfig(CLITrackerConfig{
		Total:         100,
		PrintInterval: 10 * time.Millisecond, // Very fast for testing
	})

	// Update multiple times quickly
	for i := 0; i < 10; i++ {
		tracker.Update(10)
	}

	// Should not panic, should handle fast updates gracefully
	if tracker.Current() != 100 {
		t.Errorf("Current() = %d, want 100", tracker.Current())
	}
}

// Example demonstrating typical usage
func ExampleCLITracker() {
	tracker := NewCLITrackerWithConfig(CLITrackerConfig{
		Total:  1000,
		Prefix: "Downloading files:",
	})
	defer tracker.Done()

	for i := 0; i < 1000; i++ {
		// Process item...
		tracker.Increment()
	}
	fmt.Println("\nDone!")
}

// Example demonstrating silent tracker for testing
func ExampleSilentTracker() {
	tracker := NewSilentTracker(100)
	defer tracker.Done()

	for i := 0; i < 100; i++ {
		// Process item...
		tracker.Increment()
	}

	// Use tracker stats for assertions
	if tracker.Current() != 100 {
		panic("expected 100 items")
	}
}

// Example demonstrating multi-tracker for concurrent tasks
func ExampleMultiTracker() {
	multi := NewMultiTracker()

	t1 := NewSilentTracker(100)
	t2 := NewSilentTracker(50)

	multi.Register("downloads", t1)
	multi.Register("uploads", t2)

	// Update specific trackers
	multi.Update("downloads", 25)
	multi.Update("uploads", 10)

	fmt.Println(multi.String())
	// Output: downloads: 25/100, uploads: 10/50
}
