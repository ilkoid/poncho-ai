package primitives

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
)

// InterruptionManager manages user interruption handling with callback pattern.
//
// This implements Rule 6 compliance: business logic is injected via SetOnInput()
// callback from cmd/ layer, keeping pkg/tui reusable.
//
// Interruption Flow:
//   User → Enter → HandleInput() → inputChan → ReActExecutor → EventUserInterruption → UI
//
// Thread-safe: uses sync.RWMutex for concurrent access.
type InterruptionManager struct {
	inputChan chan string
	onInput   func(query string) tea.Cmd // MANDATORY: Business logic injection
	mu        sync.RWMutex

	// Configuration
	bufferSize int
}

// NewInterruptionManager creates a new InterruptionManager.
//
// Parameters:
//   - bufferSize: channel buffer size for interruptions (typically 10)
//
// Returns:
//   - A new InterruptionManager with initialized channel
//
// Note: SetOnInput() MUST be called before using HandleInput()
func NewInterruptionManager(bufferSize int) *InterruptionManager {
	return &InterruptionManager{
		inputChan: make(chan string, bufferSize),
		bufferSize: bufferSize,
		mu:        sync.RWMutex{},
	}
}

// SetOnInput sets the callback for processing user input (MANDATORY).
//
// This is the business logic injection point for Rule 6 compliance:
// - UI (pkg/tui) calls this callback
// - cmd/ layer implements the callback (createAgentLauncher)
// - No business logic embedded in pkg/tui
//
// The callback receives the user query and returns a Bubble Tea Cmd.
// Example:
//
//	model.SetInterruptionCallback(func(query string) tea.Cmd {
//	    return tea.Sequentially(
//	        startAgentCmd(query),
//	        waitForEventCmd,
//	    )
//	})
func (im *InterruptionManager) SetOnInput(handler func(query string) tea.Cmd) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.onInput = handler
}

// HandleInput processes user input from textarea.
//
// Dual-mode logic:
//   - If agent NOT executing: Start agent execution
//   - If agent IS executing: Send interruption to channel
//
// Parameters:
//   - input: User input string
//   - isProcessing: True if agent is currently executing
//
// Returns:
//   - cmd: Bubble Tea command to execute
//   - shouldSendToChannel: True if input should be sent to inputChan (interruption)
//   - err: Error if callback not set
//
// Usage in Bubble Tea Update():
//
//	case key.Matches(msg, keys.Enter):
//	    input := m.textarea.Value()
//	    cmd, shouldSend, err := interruptMgr.HandleInput(input, m.isProcessing)
//	    if err != nil {
//	        return m, showError(err)
//	    }
//	    if shouldSend {
//	        interruptMgr.GetChannel() <- input
//	    }
//	    return m, cmd
func (im *InterruptionManager) HandleInput(input string, isProcessing bool) (cmd tea.Cmd, shouldSendToChannel bool, err error) {
	im.mu.RLock()
	handler := im.onInput
	im.mu.RUnlock()

	if handler == nil {
		return nil, false, fmt.Errorf("no input handler set. Call SetOnInput() first")
	}

	// Get command from handler
	cmd = handler(input)

	// Determine if we should send to channel (interruption mode)
	if isProcessing {
		shouldSendToChannel = true
	}

	return cmd, shouldSendToChannel, nil
}

// GetChannel returns the inputChan for passing to agent.Execute().
//
// This channel is used for inter-goroutine communication between UI and ReAct executor.
// The ReAct executor checks this channel between iterations for user interruptions.
//
// Usage:
//
//	chainInput := chain.ChainInput{
//	    UserQuery:    query,
//	    State:        state,
//	    Registry:     registry,
//	    UserInputChan: interruptMgr.GetChannel(), // ← Pass to agent
//	}
//	output, _ := client.Execute(ctx, chainInput)
func (im *InterruptionManager) GetChannel() chan string {
	return im.inputChan
}

// HandleEvent processes EventUserInterruption and returns text for UI display.
//
// Extracted from handleAgentEventWithInterruption in the original implementation.
//
// Parameters:
//   - event: Event from agent (should be EventUserInterruption)
//
// Returns:
//   - shouldDisplay: True if event should be displayed in viewport
//   - displayText: Formatted text to display
//
// Usage in Bubble Tea Update():
//
//	case EventMsg(event):
//	    shouldDisplay, text := interruptMgr.HandleEvent(events.Event(event))
//	    if shouldDisplay {
//	        m.viewportMgr.Append(systemStyle(text), true)
//	    }
func (im *InterruptionManager) HandleEvent(event events.Event) (shouldDisplay bool, displayText string) {
	if event.Type == events.EventUserInterruption {
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			return true, fmt.Sprintf("⏸️ Interruption (iteration %d): %s",
				data.Iteration, truncate(data.Message, 60))
		}
	}
	return false, ""
}

// truncate truncates a string to max length, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// IsCallbackSet returns true if the onInput callback has been set.
//
// Thread-safe: uses mutex read lock.
func (im *InterruptionManager) IsCallbackSet() bool {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.onInput != nil
}

// Close closes the input channel.
//
// Should be called when the TUI is shutting down.
// Thread-safe: uses mutex lock.
func (im *InterruptionManager) Close() {
	im.mu.Lock()
	defer im.mu.Unlock()

	if im.inputChan != nil {
		close(im.inputChan)
		im.inputChan = nil
	}
}

// GetBufferSize returns the configured buffer size.
func (im *InterruptionManager) GetBufferSize() int {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.bufferSize
}
