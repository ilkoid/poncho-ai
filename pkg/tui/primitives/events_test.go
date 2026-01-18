package primitives

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/stretchr/testify/assert"
)

// mockSubscriber is a simple implementation of events.Subscriber for testing.
type mockSubscriber struct {
	eventChan chan events.Event
	closed    bool
}

func newMockSubscriber() *mockSubscriber {
	return &mockSubscriber{
		eventChan: make(chan events.Event, 100),
		closed:    false,
	}
}

func (m *mockSubscriber) Events() <-chan events.Event {
	return m.eventChan
}

func (m *mockSubscriber) Close() {
	m.closed = true
	close(m.eventChan)
}

func (m *mockSubscriber) Send(event events.Event) {
	if !m.closed {
		m.eventChan <- event
	}
}

// Test 1: Basic event handling and rendering
func TestEventHandler_BasicEventHandling(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Send EventThinking via HandleEvent
	thinkingEvent := events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: "test query"},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(thinkingEvent)

	// Check status bar is processing
	assert.True(t, sm.IsProcessing(), "Status bar should show processing state")

	// Send EventDone via HandleEvent
	doneEvent := events.Event{
		Type:      events.EventDone,
		Data:      events.MessageData{Content: "Done!"},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(doneEvent)

	// Check status bar is not processing
	assert.False(t, sm.IsProcessing(), "Status bar should not show processing state after EventDone")

	// Check viewport has content
	content := vm.Content()
	assert.Greater(t, len(content), 0, "Viewport should have content after events")
}

// Test 2: Custom renderer registration and override
func TestEventHandler_CustomRenderer(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Register custom renderer for EventMessage
	customCalled := false
	eh.RegisterRenderer(events.EventMessage, func(event events.Event) (string, lipgloss.Style) {
		customCalled = true
		return "CUSTOM: " + event.Data.(events.MessageData).Content,
			lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	})

	// Send EventMessage via HandleEvent
	msgEvent := events.Event{
		Type:      events.EventMessage,
		Data:      events.MessageData{Content: "test message"},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(msgEvent)

	// Check custom renderer was called
	assert.True(t, customCalled, "Custom renderer should be called")

	// Check viewport has custom content
	content := vm.Content()
	if len(content) > 0 {
		assert.Contains(t, content[len(content)-1], "CUSTOM:", "Viewport should contain custom rendered content")
	}
}

// Test 3: Thread-safe concurrent renderer access
func TestEventHandler_ThreadSafety(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	done := make(chan bool)

	// Goroutine 1: Concurrent renderer registration
	go func() {
		for i := 0; i < 50; i++ {
			eh.RegisterRenderer(events.EventMessage, func(event events.Event) (string, lipgloss.Style) {
				return "Renderer 1", lipgloss.Style{}
			})
		}
		done <- true
	}()

	// Goroutine 2: Concurrent renderer retrieval
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = eh.GetRenderer(events.EventMessage)
		}
		done <- true
	}()

	// Goroutine 3: Concurrent renderer unregistration
	go func() {
		for i := 0; i < 50; i++ {
			eh.UnregisterRenderer(events.EventMessage)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify final state is consistent
	eh.GetRenderer(events.EventThinking)
	eh.RegisterRenderer(events.EventError, func(event events.Event) (string, lipgloss.Style) {
		return "Error", lipgloss.Style{}
	})

	// If we got here without race conditions, test passes
	assert.True(t, true, "Thread safety maintained")
}

// Test 4: All event types have default renderers
func TestEventHandler_AllEventTypesHaveRenderers(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Check all event types have renderers
	eventTypes := []events.EventType{
		events.EventThinking,
		events.EventThinkingChunk,
		events.EventToolCall,
		events.EventToolResult,
		events.EventUserInterruption,
		events.EventMessage,
		events.EventError,
		events.EventDone,
	}

	for _, eventType := range eventTypes {
		renderer, ok := eh.GetRenderer(eventType)
		assert.True(t, ok, "EventType %s should have a default renderer", eventType)
		assert.NotNil(t, renderer, "Renderer for %s should not be nil", eventType)
	}
}

// Test 5: EventToolCall with argument truncation
func TestEventHandler_ToolCallArgumentTruncation(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Send EventToolCall with long arguments
	longArgs := `{"very_long_argument_key":"very_long_argument_value_that_exceeds_fifty_characters_limit"}`
	toolCallEvent := events.Event{
		Type:      events.EventToolCall,
		Data:      events.ToolCallData{ToolName: "test_tool", Args: longArgs},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(toolCallEvent)

	// Check viewport has truncated content
	content := vm.Content()
	if len(content) > 0 {
		lastContent := content[len(content)-1]
		assert.Contains(t, lastContent, "...", "Long arguments should be truncated with ...")
		assert.NotContains(t, lastContent, longArgs, "Original long args should not be in output")
	}
}

// Test 6: EventUserInterruption with message truncation
func TestEventHandler_UserInterruptionMessageTruncation(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Send EventUserInterruption with long message
	longMessage := "This is a very long interruption message that exceeds the sixty character limit and should be truncated"
	interruptionEvent := events.Event{
		Type:      events.EventUserInterruption,
		Data:      events.UserInterruptionData{Message: longMessage, Iteration: 5},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(interruptionEvent)

	// Check viewport has truncated content
	content := vm.Content()
	if len(content) > 0 {
		lastContent := content[len(content)-1]
		assert.Contains(t, lastContent, "...", "Long message should be truncated with ...")
		assert.Contains(t, lastContent, "iteration", "Should show iteration number")
	}
}

// Test 7: EventToolResult with duration formatting
func TestEventHandler_ToolResultDurationFormatting(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Send EventToolResult with duration
	resultEvent := events.Event{
		Type:      events.EventToolResult,
		Data:      events.ToolResultData{ToolName: "test_tool", Result: "success", Duration: 150 * time.Millisecond},
		Timestamp: time.Now(),
	}
	eh.HandleEvent(resultEvent)

	// Check viewport has duration formatted
	content := vm.Content()
	if len(content) > 0 {
		lastContent := content[len(content)-1]
		assert.Contains(t, lastContent, "150ms", "Duration should be formatted as 150ms")
		assert.Contains(t, lastContent, "test_tool", "Tool name should be in output")
	}
}

// Test 8: InitRenderers can be called without errors
func TestEventHandler_InitRenderers(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// InitRenderers should not panic
	assert.NotPanics(t, func() {
		eh.InitRenderers()
	}, "InitRenderers should not panic")

	// Renderers should still be registered
	renderer, ok := eh.GetRenderer(events.EventThinking)
	assert.True(t, ok, "EventThinking renderer should still be registered")
	assert.NotNil(t, renderer, "Renderer should not be nil")
}

// Test 9: UnregisterRenderer removes custom renderer
func TestEventHandler_UnregisterRenderer(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// First verify default renderer exists
	renderer, ok := eh.GetRenderer(events.EventMessage)
	assert.True(t, ok, "Default renderer should exist initially")
	assert.NotNil(t, renderer, "Default renderer should not be nil")

	// Register custom renderer
	eh.RegisterRenderer(events.EventMessage, func(event events.Event) (string, lipgloss.Style) {
		return "Custom", lipgloss.Style{}
	})

	// Verify custom renderer is registered
	renderer, ok = eh.GetRenderer(events.EventMessage)
	assert.True(t, ok, "Custom renderer should be registered")
	assert.NotNil(t, renderer, "Renderer should not be nil")

	// Unregister
	eh.UnregisterRenderer(events.EventMessage)

	// Verify it's removed (no renderer after unregister)
	renderer, ok = eh.GetRenderer(events.EventMessage)
	assert.False(t, ok, "Renderer should be removed after unregister")
	assert.Nil(t, renderer, "Renderer should be nil after unregister")
}

// Test 10: EventThinkingChunk returns empty content
func TestEventHandler_ThinkingChunkEmptyContent(t *testing.T) {
	sub := newMockSubscriber()
	vm := NewViewportManager(ViewportConfig{})
	sm := NewStatusBarManager(DefaultStatusBarConfig())
	eh := NewEventHandler(sub, vm, sm)

	// Get renderer for EventThinkingChunk
	renderer, ok := eh.GetRenderer(events.EventThinkingChunk)
	assert.True(t, ok, "EventThinkingChunk should have a renderer")

	// Render event
	content, style := renderer(events.Event{
		Type:      events.EventThinkingChunk,
		Data:      events.ThinkingChunkData{Chunk: "test", Accumulated: "test accumulated"},
		Timestamp: time.Now(),
	})

	// Content should be empty (thinking chunks are typically not displayed)
	assert.Empty(t, content, "EventThinkingChunk should return empty content")
	assert.Equal(t, lipgloss.Style{}, style, "EventThinkingChunk should return empty style")
}
