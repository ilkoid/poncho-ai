package primitives

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// Test 1: Basic functionality - initial content and append
func TestViewportManager_BasicFunctionality(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})

	// Test SetInitialContent
	vm.SetInitialContent("Line 1\nLine 2")
	content := vm.Content()
	assert.Equal(t, []string{"Line 1\nLine 2"}, content)

	// Test Append
	vm.Append("Line 3", true)
	content = vm.Content()
	assert.Equal(t, []string{"Line 1\nLine 2", "Line 3"}, content)

	// Verify viewport has content
	vp := vm.GetViewport()
	assert.Greater(t, vp.TotalLineCount(), 0)
}

// Test 2: Bug #2 - Minimum height enforcement (Height = 0 bug)
func TestViewportManager_MinHeightEnforcement(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})
	vm.SetInitialContent("Test content")

	// Simulate very small window (header + footer > total height)
	msg := tea.WindowSizeMsg{
		Width:  80,
		Height: 5, // Very small
	}

	vm.HandleResize(msg, 3, 3) // header=3, footer=3, so vpHeight would be -1

	// Verify minimum height is enforced
	width, height := vm.GetDimensions()
	assert.Equal(t, 80, width)
	assert.Equal(t, 1, height, "Height should be minimum 1, not 0 or negative")
}

// Test 3: Bug #1 - Scroll position preservation during resize (Timing Attack)
func TestViewportManager_ScrollPositionPreservation(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})
	vm.SetInitialContent("Line 1\nLine 2\nLine 3\nLine 4\nLine 5")

	// Initial resize
	msg1 := tea.WindowSizeMsg{Width: 80, Height: 20}
	vm.HandleResize(msg1, 3, 2) // vpHeight = 15

	// Scroll to bottom
	vm.GotoBottom()
	vp := vm.GetViewport()
	initialYOffset := vp.YOffset

	// Resize to larger window
	msg2 := tea.WindowSizeMsg{Width: 80, Height: 30}
	vm.HandleResize(msg2, 3, 2) // vpHeight = 25

	// Should still be at bottom after resize
	vp = vm.GetViewport()
	assert.True(t, vp.YOffset >= initialYOffset,
		"Scroll position should be preserved or improved after resize")

	// Verify we're at or near bottom
	totalLines := vp.TotalLineCount()
	assert.True(t, vp.YOffset+vp.Height >= totalLines-1,
		"Should be at bottom after resize when wasAtBottom=true")
}

// Test 4: Bug #3 - Proper clamp logic (Off-by-One)
func TestViewportManager_ProperClampLogic(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})

	// Add limited content
	vm.SetInitialContent("Line 1\nLine 2\nLine 3")

	// Set dimensions
	msg := tea.WindowSizeMsg{Width: 80, Height: 20}
	vm.HandleResize(msg, 3, 2) // vpHeight = 15

	// Manually scroll to a position
	vm.ScrollUp(10)

	// Resize to smaller window
	msg2 := tea.WindowSizeMsg{Width: 80, Height: 10}
	vm.HandleResize(msg2, 3, 2) // vpHeight = 5

	// Verify YOffset is properly clamped
	vp := vm.GetViewport()
	totalLines := vp.TotalLineCount()
	maxOffset := totalLines - vp.Height
	if maxOffset < 0 {
		maxOffset = 0
	}

	assert.True(t, vp.YOffset <= maxOffset,
		"YOffset should be clamped to maxOffset=%d, got %d", maxOffset, vp.YOffset)
	assert.True(t, vp.YOffset >= 0,
		"YOffset should never be negative, got %d", vp.YOffset)
}

// Test 5: Bug #4 - Thread safety (Race condition)
func TestViewportManager_ThreadSafety(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})
	vm.SetInitialContent("Initial content")

	done := make(chan bool)

	// Goroutine 1: Concurrent resizes
	go func() {
		for i := 0; i < 100; i++ {
			msg := tea.WindowSizeMsg{Width: 80, Height: 20 + i%10}
			vm.HandleResize(msg, 3, 2)
		}
		done <- true
	}()

	// Goroutine 2: Concurrent appends
	go func() {
		for i := 0; i < 100; i++ {
			vm.Append("Append line", true)
		}
		done <- true
	}()

	// Goroutine 3: Concurrent reads
	go func() {
		for i := 0; i < 100; i++ {
			_ = vm.GetViewport()
			_ = vm.Content()
			_, _ = vm.GetDimensions()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify final state is consistent
	content := vm.Content()
	vp := vm.GetViewport()

	// Should have initial + 100 appends
	assert.Equal(t, 101, len(content))
	assert.Greater(t, vp.TotalLineCount(), 0)
}

// Test 6: Smart scroll preservation
func TestViewportManager_SmartScrollPreservation(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})

	// Initial setup
	vm.SetInitialContent("Line 1\nLine 2\nLine 3")
	msg := tea.WindowSizeMsg{Width: 80, Height: 10}
	vm.HandleResize(msg, 3, 2)
	vm.GotoBottom()

	// Append with preservePosition=true
	vm.Append("New line at bottom", true)

	// Should still be at bottom
	vp := vm.GetViewport()
	totalLines := vp.TotalLineCount()
	assert.True(t, vp.YOffset+vp.Height >= totalLines-1,
		"Should auto-scroll to bottom when preservePosition=true")
}

// Test 7: Minimum width enforcement
func TestViewportManager_MinWidthEnforcement(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})
	vm.SetInitialContent("Test content")

	// Simulate very narrow window
	msg := tea.WindowSizeMsg{
		Width:  10, // Very narrow
		Height: 20,
	}

	vm.HandleResize(msg, 3, 2)

	// Verify minimum width is enforced
	width, _ := vm.GetDimensions()
	assert.Equal(t, 20, width, "Width should be minimum 20")
}

// Test 8: Scroll operations
func TestViewportManager_ScrollOperations(t *testing.T) {
	vm := NewViewportManager(ViewportConfig{})

	// Add enough content to scroll - each append creates a new line
	for i := 1; i <= 30; i++ {
		if i == 1 {
			vm.SetInitialContent("Line 1")
		} else {
			vm.Append("Line "+string(rune('0'+i)), true)
		}
	}

	msg := tea.WindowSizeMsg{Width: 80, Height: 10}
	vm.HandleResize(msg, 3, 2) // vpHeight = 5

	// Go to bottom first
	vm.GotoBottom()
	vp := vm.GetViewport()
	initialOffset := vp.YOffset

	// Test scroll up
	vm.ScrollUp(5)
	vp = vm.GetViewport()
	assert.Greater(t, vp.YOffset, 0, "ScrollUp should increase YOffset")
	assert.Less(t, vp.YOffset, initialOffset, "ScrollUp should decrease YOffset from bottom")

	// Test scroll down
	vm.ScrollDown(3)
	vp = vm.GetViewport()
	assert.GreaterOrEqual(t, vp.YOffset, 0, "ScrollDown should keep YOffset non-negative")

	// Test GotoTop
	vm.GotoTop()
	vp = vm.GetViewport()
	assert.Equal(t, 0, vp.YOffset, "GotoTop should set YOffset to 0")

	// Test GotoBottom
	vm.GotoBottom()
	vp = vm.GetViewport()
	totalLines := vp.TotalLineCount()
	assert.True(t, vp.YOffset+vp.Height >= totalLines-1,
		"GotoBottom should scroll to bottom")
}
