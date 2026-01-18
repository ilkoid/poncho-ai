package primitives

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
)

// DebugManager manages debug functionality with JSON-logging, screen saving, and debug mode toggle.
//
// Features:
//   - Toggle debug mode (Ctrl+G)
//   - Save screen to markdown (Ctrl+S)
//   - Track last JSON debug log path (Ctrl+L)
//   - Format events for DEBUG display
//
// Thread-safe: uses sync.RWMutex for concurrent access.
type DebugManager struct {
	debugMode   bool
	lastLogPath string
	mu          sync.RWMutex

	// Configuration
	logsDir  string
	saveLogs bool

	// Dependencies
	viewportMgr *ViewportManager
	statusMgr   *StatusBarManager
}

// DebugConfig holds configuration for DebugManager.
type DebugConfig struct {
	LogsDir   string // "./debug_logs"
	SaveLogs  bool   // true = save JSON logs
}

// NewDebugManager creates a new DebugManager.
//
// Parameters:
//   - cfg: Configuration for debug logging
//   - vm: ViewportManager for getting content during screen save
//   - sm: StatusBarManager for updating DEBUG indicator
//
// Returns:
//   - A new DebugManager with debug mode initially disabled
func NewDebugManager(cfg DebugConfig, vm *ViewportManager, sm *StatusBarManager) *DebugManager {
	return &DebugManager{
		debugMode:   false,
		lastLogPath: "",
		mu:          sync.RWMutex{},
		logsDir:     cfg.LogsDir,
		saveLogs:    cfg.SaveLogs,
		viewportMgr: vm,
		statusMgr:   sm,
	}
}

// ToggleDebug switches debug mode on/off.
//
// Actions:
//   1. Toggles debugMode flag
//   2. Updates status bar DEBUG indicator
//   3. Returns message for display in viewport
//
// Usage in Bubble Tea Update():
//
//	case key.Matches(msg, keys.DebugToggle):
//	    msg := debugMgr.ToggleDebug()
//	    m.viewportMgr.Append(systemStyle(msg), true)
//	    return m, nil
//
// Returns formatted message: "Debug mode: ON" or "Debug mode: OFF"
func (dm *DebugManager) ToggleDebug() string {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	dm.debugMode = !dm.debugMode

	// Update status bar DEBUG indicator
	dm.statusMgr.SetDebugMode(dm.debugMode)

	status := "OFF"
	if dm.debugMode {
		status = "ON"
	}
	return fmt.Sprintf("Debug mode: %s", status)
}

// IsEnabled returns current debug mode state.
//
// Thread-safe: uses mutex read lock.
//
// Returns true if debug mode is enabled.
func (dm *DebugManager) IsEnabled() bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.debugMode
}

// ShouldLogEvent returns true if event should be logged in DEBUG mode.
//
// Returns false if debug mode is disabled.
//
// Logged events:
//   - EventThinking
//   - EventToolCall
//   - EventToolResult
//   - EventUserInterruption
//
// Thread-safe: uses mutex read lock via IsEnabled().
func (dm *DebugManager) ShouldLogEvent(event events.Event) bool {
	if !dm.IsEnabled() {
		return false
	}

	// Log these events in DEBUG mode
	switch event.Type {
	case events.EventThinking,
		events.EventToolCall,
		events.EventToolResult,
		events.EventUserInterruption:
		return true
	}
	return false
}

// FormatEvent formats event for DEBUG display.
//
// Returns formatted string for viewport display.
// Format depends on event type:
//   - EventThinking: "[DEBUG] Event: EventThinking"
//   - EventToolCall: "[DEBUG] Tool call: tool_name"
//   - EventToolResult: "[DEBUG] Tool result: tool_name (Xms)"
//   - EventUserInterruption: "[DEBUG] Interruption (iter N): message"
//
// Usage in Bubble Tea Update():
//
//	if debugMgr.ShouldLogEvent(event) {
//	    msg := debugMgr.FormatEvent(events.Event(event))
//	    m.viewportMgr.Append(debugStyle(msg), true)
//	}
func (dm *DebugManager) FormatEvent(event events.Event) string {
	switch event.Type {
	case events.EventThinking:
		return fmt.Sprintf("[DEBUG] Event: %s", event.Type)
	case events.EventToolCall:
		if data, ok := event.Data.(events.ToolCallData); ok {
			return fmt.Sprintf("[DEBUG] Tool call: %s", data.ToolName)
		}
	case events.EventToolResult:
		if data, ok := event.Data.(events.ToolResultData); ok {
			return fmt.Sprintf("[DEBUG] Tool result: %s (%dms)",
				data.ToolName, data.Duration.Milliseconds())
		}
	case events.EventUserInterruption:
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			return fmt.Sprintf("[DEBUG] Interruption (iter %d): %s",
				data.Iteration, truncate(data.Message, 60))
		}
	}
	return fmt.Sprintf("[DEBUG] Event: %s", event.Type)
}

// SetLastLogPath stores the path to the last JSON debug log.
//
// Thread-safe: uses mutex lock.
func (dm *DebugManager) SetLastLogPath(path string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.lastLogPath = path
}

// GetLastLogPath returns the path to the last JSON debug log.
//
// Thread-safe: uses mutex read lock.
//
// Returns empty string if no log has been created yet.
func (dm *DebugManager) GetLastLogPath() string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.lastLogPath
}

// SaveScreen saves current viewport content to markdown file.
//
// Actions:
//   1. Gets content from ViewportManager
//   2. Generates filename: poncho_log_YYYYMMDD_HHMMSS.md
//   3. Strips ANSI codes from content
//   4. Writes to markdown file
//
// Returns filename or error.
//
// Usage in Bubble Tea Update():
//
//	case key.Matches(msg, keys.SaveScreen):
//	    filename, err := debugMgr.SaveScreen()
//	    if err != nil {
//	        m.viewportMgr.Append(errorStyle(err.Error()), true)
//	    } else {
//	        m.viewportMgr.Append(systemStyle("Screen saved to: "+filename), true)
//	    }
//	    return m, nil
func (dm *DebugManager) SaveScreen() (string, error) {
	// Get content from viewport manager
	content := dm.viewportMgr.Content()

	// Generate filename
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("poncho_log_%s.md", timestamp)

	// Build markdown content
	var md strings.Builder
	md.WriteString("# Poncho AI Session Log\n\n")
	md.WriteString(fmt.Sprintf("**Generated:** %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	md.WriteString("---\n\n")

	// Strip ANSI codes and write content
	for _, line := range content {
		cleanLine := stripANSICodes(line)
		md.WriteString(cleanLine)
		md.WriteString("\n")
	}

	// Write to file
	err := os.WriteFile(filename, []byte(md.String()), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to save screen: %w", err)
	}

	return filename, nil
}

// stripANSICodes removes ANSI escape sequences from string.
//
// ANSI escape sequences start with ESC (0x1B) followed by
// '[' and parameter characters, ending with a letter (a-z, A-Z).
//
// Example:
//   Input:  "\x1b[31mError\x1b[0m"
//   Output: "Error"
func stripANSICodes(s string) string {
	var result strings.Builder
	inEscape := false

	for i := 0; i < len(s); i++ {
		if s[i] == 0x1B { // ESC character
			inEscape = true
			// Skip the ESC and look for '['
			if i+1 < len(s) && s[i+1] == '[' {
				i++ // Skip '[' as well
			}
			continue
		}

		// If we're in an escape sequence, skip until we hit a letter (a-z, A-Z)
		if inEscape {
			// Check if current byte is the terminator (a-z, A-Z)
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}

		// Regular character, add to result
		result.WriteByte(s[i])
	}

	return result.String()
}
