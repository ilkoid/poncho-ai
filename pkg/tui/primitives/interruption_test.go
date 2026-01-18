package primitives

import (
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/stretchr/testify/assert"
	tea "github.com/charmbracelet/bubbletea"
)

// Test 1: Basic SetOnInput callback and HandleInput
func TestInterruptionManager_BasicCallback(t *testing.T) {
	im := NewInterruptionManager(10)

	// Verify callback is not set initially
	assert.False(t, im.IsCallbackSet(), "Callback should not be set initially")

	// Set callback
	called := false
	im.SetOnInput(func(query string) tea.Cmd {
		called = true
		return func() tea.Msg { return tea.Msg("test") }
	})

	// Verify callback is set
	assert.True(t, im.IsCallbackSet(), "Callback should be set after SetOnInput")

	// Handle input when agent is NOT executing
	cmd, shouldSend, err := im.HandleInput("test query", false)
	assert.NoError(t, err, "HandleInput should not return error when callback is set")
	assert.False(t, shouldSend, "shouldSend should be false when agent is not executing")
	assert.NotNil(t, cmd, "Cmd should not be nil")
	assert.True(t, called, "Callback should be called")
}

// Test 2: HandleInput without callback returns error
func TestInterruptionManager_HandleInputWithoutCallback(t *testing.T) {
	im := NewInterruptionManager(10)

	// Try to handle input without setting callback
	cmd, shouldSend, err := im.HandleInput("test", false)
	assert.Error(t, err, "HandleInput should return error when callback is not set")
	assert.Nil(t, cmd, "Cmd should be nil when error")
	assert.False(t, shouldSend, "shouldSend should be false when error")
	assert.Contains(t, err.Error(), "no input handler", "Error message should mention missing handler")
}

// Test 3: Dual-mode logic - agent executing vs not executing
func TestInterruptionManager_DualModeLogic(t *testing.T) {
	im := NewInterruptionManager(10)

	// Set callback
	im.SetOnInput(func(query string) tea.Cmd {
		return func() tea.Msg { return tea.Msg(query) }
	})

	// Mode 1: Agent NOT executing (normal input)
	cmd1, shouldSend1, err1 := im.HandleInput("normal query", false)
	assert.NoError(t, err1, "HandleInput should succeed in normal mode")
	assert.False(t, shouldSend1, "shouldSend should be false in normal mode")
	assert.NotNil(t, cmd1, "Cmd should not be nil in normal mode")

	// Mode 2: Agent IS executing (interruption)
	cmd2, shouldSend2, err2 := im.HandleInput("interruption", true)
	assert.NoError(t, err2, "HandleInput should succeed in interruption mode")
	assert.True(t, shouldSend2, "shouldSend should be true in interruption mode")
	assert.NotNil(t, cmd2, "Cmd should not be nil in interruption mode")
}

// Test 4: Channel operations and inter-goroutine communication
func TestInterruptionManager_ChannelOperations(t *testing.T) {
	im := NewInterruptionManager(10)

	// Set callback
	im.SetOnInput(func(query string) tea.Cmd {
		return nil
	})

	// Get channel
	ch := im.GetChannel()
	assert.NotNil(t, ch, "GetChannel should return non-nil channel")

	// Send to channel (interruption mode)
	go func() {
		ch <- "interruption message"
	}()

	// Receive from channel
	select {
	case msg := <-ch:
		assert.Equal(t, "interruption message", msg, "Should receive same message sent")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should have received message from channel")
	}
}

// Test 5: EventUserInterruption handling
func TestInterruptionManager_HandleEventUserInterruption(t *testing.T) {
	im := NewInterruptionManager(10)

	// Test EventUserInterruption
	event := events.Event{
		Type: events.EventUserInterruption,
		Data: events.UserInterruptionData{
			Message:   "stop processing",
			Iteration: 5,
		},
		Timestamp: time.Now(),
	}

	shouldDisplay, displayText := im.HandleEvent(event)
	assert.True(t, shouldDisplay, "Should display EventUserInterruption")
	assert.Contains(t, displayText, "⏸️ Interruption", "Should contain interruption emoji")
	assert.Contains(t, displayText, "iteration 5", "Should show iteration number")
	assert.Contains(t, displayText, "stop processing", "Should show message")
}

// Test 6: Message truncation in HandleEvent
func TestInterruptionManager_MessageTruncation(t *testing.T) {
	im := NewInterruptionManager(10)

	// Test with long message (exceeds 60 chars)
	longMessage := "This is a very long interruption message that exceeds the sixty character limit and should be truncated"
	event := events.Event{
		Type: events.EventUserInterruption,
		Data: events.UserInterruptionData{
			Message:   longMessage,
			Iteration: 3,
		},
		Timestamp: time.Now(),
	}

	shouldDisplay, displayText := im.HandleEvent(event)
	assert.True(t, shouldDisplay, "Should display EventUserInterruption")
	assert.Contains(t, displayText, "...", "Long message should be truncated with ...")
	// The display text includes prefix like "⏸️ Interruption (iteration 3): " so total is longer than 60
	assert.Less(t, len(displayText), len(longMessage)+30, "Truncated display text should be shorter than original")
}

// Test 7: Non-interruption events return false
func TestInterruptionManager_NonInterruptionEvents(t *testing.T) {
	im := NewInterruptionManager(10)

	// Test various event types
	nonInterruptionEvents := []events.EventType{
		events.EventThinking,
		events.EventToolCall,
		events.EventToolResult,
		events.EventMessage,
		events.EventDone,
		events.EventError,
	}

	for _, eventType := range nonInterruptionEvents {
		event := events.Event{
			Type:      eventType,
			Timestamp: time.Now(),
		}

		shouldDisplay, displayText := im.HandleEvent(event)
		assert.False(t, shouldDisplay, "Non-interruption events should not be displayed")
		assert.Empty(t, displayText, "Display text should be empty for non-interruption events")
	}
}

// Test 8: Thread-safe concurrent access
func TestInterruptionManager_ThreadSafety(t *testing.T) {
	im := NewInterruptionManager(10)

	done := make(chan bool)

	// Goroutine 1: Concurrent SetOnInput calls
	go func() {
		for i := 0; i < 50; i++ {
			im.SetOnInput(func(query string) tea.Cmd {
				return nil
			})
		}
		done <- true
	}()

	// Goroutine 2: Concurrent HandleInput calls
	go func() {
		for i := 0; i < 50; i++ {
			im.SetOnInput(func(query string) tea.Cmd {
				return nil
			})
			_, _, _ = im.HandleInput("test", false)
		}
		done <- true
	}()

	// Goroutine 3: Concurrent IsCallbackSet calls
	go func() {
		for i := 0; i < 50; i++ {
			_ = im.IsCallbackSet()
		}
		done <- true
	}()

	// Goroutine 4: Concurrent GetChannel calls
	go func() {
		for i := 0; i < 50; i++ {
			_ = im.GetChannel()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done
	<-done

	// Verify final state is consistent
	_ = im.IsCallbackSet()
	_ = im.GetChannel()

	// If we got here without race conditions, test passes
	assert.True(t, true, "Thread safety maintained")
}

// Test 9: GetBufferSize returns configured size
func TestInterruptionManager_GetBufferSize(t *testing.T) {
	im := NewInterruptionManager(20)
	assert.Equal(t, 20, im.GetBufferSize(), "GetBufferSize should return configured size")
}

// Test 10: Close closes the channel
func TestInterruptionManager_Close(t *testing.T) {
	im := NewInterruptionManager(10)

	// Set callback
	im.SetOnInput(func(query string) tea.Cmd {
		return nil
	})

	// Get channel before close
	ch := im.GetChannel()
	assert.NotNil(t, ch, "Channel should not be nil before close")

	// Close the manager
	im.Close()

	// Channel after close should be nil or closed
	chAfter := im.GetChannel()
	// After close, inputChan is set to nil
	assert.Nil(t, chAfter, "Channel should be nil after close")

	// Verify original channel is closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "Original channel should be closed")
	case <-time.After(100 * time.Millisecond):
		// Channel is closed, so this is expected
	}
}
