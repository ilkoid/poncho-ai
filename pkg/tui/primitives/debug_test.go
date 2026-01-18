package primitives

import (
	"os"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/stretchr/testify/assert"
)

// Test 1: Basic ToggleDebug functionality
func TestDebugManager_ToggleDebug(t *testing.T) {
	cfg := DebugConfig{
		LogsDir:  "./debug_logs",
		SaveLogs: true,
	}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Initial state
	assert.False(t, dm.IsEnabled(), "Debug mode should be disabled initially")
	assert.False(t, sm.IsDebugMode(), "Status bar should not show debug mode initially")

	// Toggle ON
	msg := dm.ToggleDebug()
	assert.Contains(t, msg, "ON", "Message should contain 'ON'")
	assert.True(t, dm.IsEnabled(), "Debug mode should be enabled after toggle")
	assert.True(t, sm.IsDebugMode(), "Status bar should show debug mode after toggle")

	// Toggle OFF
	msg = dm.ToggleDebug()
	assert.Contains(t, msg, "OFF", "Message should contain 'OFF'")
	assert.False(t, dm.IsEnabled(), "Debug mode should be disabled after second toggle")
	assert.False(t, sm.IsDebugMode(), "Status bar should not show debug mode after second toggle")
}

// Test 2: ShouldLogEvent filters events correctly
func TestDebugManager_ShouldLogEvent(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Debug mode OFF - should not log any events
	dm.ToggleDebug() // Turn ON
	dm.ToggleDebug() // Turn OFF

	testEvents := []events.Event{
		{Type: events.EventThinking},
		{Type: events.EventToolCall},
		{Type: events.EventToolResult},
		{Type: events.EventUserInterruption},
		{Type: events.EventMessage},
		{Type: events.EventError},
		{Type: events.EventDone},
	}

	for _, event := range testEvents {
		assert.False(t, dm.ShouldLogEvent(event), "Should not log when debug mode is OFF")
	}

	// Debug mode ON - should log specific events
	dm.ToggleDebug() // Turn ON

	loggableEvents := []events.EventType{
		events.EventThinking,
		events.EventToolCall,
		events.EventToolResult,
		events.EventUserInterruption,
	}

	for _, eventType := range loggableEvents {
		assert.True(t, dm.ShouldLogEvent(events.Event{Type: eventType}),
			"Should log %s when debug mode is ON", eventType)
	}

	nonLoggableEvents := []events.EventType{
		events.EventMessage,
		events.EventError,
		events.EventDone,
		events.EventThinkingChunk,
	}

	for _, eventType := range nonLoggableEvents {
		assert.False(t, dm.ShouldLogEvent(events.Event{Type: eventType}),
			"Should not log %s even in debug mode", eventType)
	}
}

// Test 3: FormatEvent formats events correctly
func TestDebugManager_FormatEvent(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Test thinking
	thinkingEvent := events.Event{
		Type: events.EventThinking,
		Data: events.ThinkingData{Query: "test query"},
		Timestamp: time.Now(),
	}
	formatted := dm.FormatEvent(thinkingEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "thinking", "Should contain event type")

	// Test EventToolCall
	toolCallEvent := events.Event{
		Type: events.EventToolCall,
		Data: events.ToolCallData{ToolName: "test_tool", Args: "{}"},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(toolCallEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "test_tool", "Should contain tool name")

	// Test EventToolResult
	toolResultEvent := events.Event{
		Type: events.EventToolResult,
		Data: events.ToolResultData{ToolName: "test_tool", Result: "success", Duration: 150 * time.Millisecond},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(toolResultEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "test_tool", "Should contain tool name")
	assert.Contains(t, formatted, "150ms", "Should contain duration")

	// Test EventUserInterruption
	interruptionEvent := events.Event{
		Type: events.EventUserInterruption,
		Data: events.UserInterruptionData{Message: "stop processing", Iteration: 5},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(interruptionEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "iter 5", "Should contain iteration number")
	assert.Contains(t, formatted, "stop processing", "Should contain message")

	// Test unknown event type
	unknownEvent := events.Event{
		Type: events.EventMessage,
		Data: events.MessageData{Content: "test"},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(unknownEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "message", "Should contain event type for unknown events")
}

// Test 4: SetLastLogPath / GetLastLogPath
func TestDebugManager_LastLogPath(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Initial state
	path := dm.GetLastLogPath()
	assert.Empty(t, path, "Last log path should be empty initially")

	// Set path
	testPath := "./debug_logs/debug_20260118_123456.json"
	dm.SetLastLogPath(testPath)

	// Get path
	path = dm.GetLastLogPath()
	assert.Equal(t, testPath, path, "Should return the same path that was set")

	// Update path
	newPath := "./debug_logs/debug_20260118_234567.json"
	dm.SetLastLogPath(newPath)
	path = dm.GetLastLogPath()
	assert.Equal(t, newPath, path, "Should return the updated path")
}

// Test 5: SaveScreen with ANSI stripping
func TestDebugManager_SaveScreen(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Add some content with ANSI codes
	vm.Append("\x1b[31mError message\x1b[0m", false)
	vm.Append("\x1b[32mSuccess message\x1b[0m", false)
	vm.Append("Plain text without colors", false)

	// Save screen
	filename, err := dm.SaveScreen()
	assert.NoError(t, err, "SaveScreen should not return error")
	assert.NotEmpty(t, filename, "Filename should not be empty")
	assert.Contains(t, filename, "poncho_log_", "Filename should contain prefix")
	assert.Contains(t, filename, ".md", "Filename should have .md extension")

	// Read file and verify content
	content, err := os.ReadFile(filename)
	assert.NoError(t, err, "Should be able to read saved file")
	contentStr := string(content)

	// Check that ANSI codes were stripped
	assert.NotContains(t, contentStr, "\x1b[", "Content should not contain ANSI escape codes")
	assert.Contains(t, contentStr, "Error message", "Should contain error message without ANSI")
	assert.Contains(t, contentStr, "Success message", "Should contain success message without ANSI")
	assert.Contains(t, contentStr, "Plain text without colors", "Should contain plain text")
	assert.Contains(t, contentStr, "# Poncho AI Session Log", "Should contain markdown header")

	// Clean up
	os.Remove(filename)
}

// Test 6: stripANSICodes helper function
func TestDebugManager_stripANSICodes(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Red color",
			input:    "\x1b[31mError\x1b[0m",
			expected: "Error",
		},
		{
			name:     "Bold text",
			input:    "\x1b[1mBold\x1b[0m",
			expected: "Bold",
		},
		{
			name:     "Multiple styles",
			input:    "\x1b[31;1mRed and Bold\x1b[0m",
			expected: "Red and Bold",
		},
		{
			name:     "Mixed ANSI and plain",
			input:    "Plain \x1b[31mRed\x1b[0m Plain",
			expected: "Plain Red Plain",
		},
		{
			name:     "No ANSI codes",
			input:    "Plain text",
			expected: "Plain text",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Only ANSI codes",
			input:    "\x1b[31m\x1b[0m",
			expected: "",
		},
		{
			name:     "Complex escape sequence",
			input:    "\x1b[38;5;196mRGB color\x1b[0m",
			expected: "RGB color",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := stripANSICodes(tc.input)
			assert.Equal(t, tc.expected, result, "stripANSICodes should produce expected output")
		})
	}
}

// Test 7: Thread-safe concurrent access
func TestDebugManager_ThreadSafety(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	done := make(chan bool)

	// Goroutine 1: Concurrent ToggleDebug calls
	go func() {
		for i := 0; i < 50; i++ {
			dm.ToggleDebug()
		}
		done <- true
	}()

	// Goroutine 2: Concurrent IsEnabled calls
	go func() {
		for i := 0; i < 50; i++ {
			_ = dm.IsEnabled()
		}
		done <- true
	}()

	// Goroutine 3: Concurrent SetLastLogPath calls
	go func() {
		for i := 0; i < 50; i++ {
			dm.SetLastLogPath("./debug_logs/test.json")
		}
		done <- true
	}()

	// Goroutine 4: Concurrent GetLastLogPath calls
	go func() {
		for i := 0; i < 50; i++ {
			_ = dm.GetLastLogPath()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done
	<-done

	// Verify final state is consistent
	_ = dm.IsEnabled()
	_ = dm.GetLastLogPath()

	// If we got here without race conditions, test passes
	assert.True(t, true, "Thread safety maintained")
}

// Test 8: DebugManager preserves configuration
func TestDebugManager_PreservesConfig(t *testing.T) {
	cfg := DebugConfig{
		LogsDir:  "./custom_debug_logs",
		SaveLogs: false,
	}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Configuration should be preserved
	assert.Equal(t, "./custom_debug_logs", dm.logsDir, "Should preserve logsDir")
	assert.False(t, dm.saveLogs, "Should preserve saveLogs")

	// Dependencies should be set
	assert.Same(t, vm, dm.viewportMgr, "Should preserve viewport manager")
	assert.Same(t, sm, dm.statusMgr, "Should preserve status manager")
}

// Test 9: EventUserInterruption message truncation in FormatEvent
func TestDebugManager_InterruptionMessageTruncation(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Test with long message (exceeds 60 chars)
	longMessage := "This is a very long interruption message that exceeds the sixty character limit and should be truncated"
	interruptionEvent := events.Event{
		Type: events.EventUserInterruption,
		Data: events.UserInterruptionData{
			Message:   longMessage,
			Iteration: 3,
		},
		Timestamp: time.Now(),
	}

	formatted := dm.FormatEvent(interruptionEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "iter 3", "Should show iteration number")
	assert.Contains(t, formatted, "...", "Long message should be truncated with ...")
	assert.Less(t, len(formatted), len(longMessage)+30, "Truncated message should be shorter than original")
}

// Test 10: FormatEvent handles events with minimal data
func TestDebugManager_FormatEvent_MinimalData(t *testing.T) {
	cfg := DebugConfig{LogsDir: "./debug_logs", SaveLogs: true}
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	dm := NewDebugManager(cfg, vm, sm)

	// Test EventToolCall with minimal data
	toolCallEvent := events.Event{
		Type:      events.EventToolCall,
		Data:      events.ToolCallData{ToolName: "minimal_tool", Args: "{}"},
		Timestamp: time.Now(),
	}
	formatted := dm.FormatEvent(toolCallEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "minimal_tool", "Should show tool name")

	// Test EventToolResult with minimal data
	toolResultEvent := events.Event{
		Type:      events.EventToolResult,
		Data:      events.ToolResultData{ToolName: "minimal_tool", Result: "ok", Duration: 1},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(toolResultEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "minimal_tool", "Should show tool name")

	// Test EventUserInterruption with minimal data
	interruptionEvent := events.Event{
		Type:      events.EventUserInterruption,
		Data:      events.UserInterruptionData{Message: "", Iteration: 0},
		Timestamp: time.Now(),
	}
	formatted = dm.FormatEvent(interruptionEvent)
	assert.Contains(t, formatted, "[DEBUG]", "Should contain [DEBUG] prefix")
	assert.Contains(t, formatted, "iter 0", "Should show iteration number")
}
