// Package primitives предоставляет reusable low-level UI компоненты.
//
// Это foundational primitives для построения TUI приложений:
// - ViewportManager: управление viewport с smart scroll
// - StatusBarManager: статус-бар с spinner и индикаторами
// - EventHandler: обработка событий с pluggable renderers
// - InterruptionManager: обработка прерываний с callback паттерном
package primitives

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"sync"
)

// EventHandler manages event handling with pluggable renderers.
//
// Uses Strategy pattern for event rendering - each event type can have
// a custom renderer that converts the event to styled content.
//
// Thread-safe: uses sync.RWMutex for concurrent access.
type EventHandler struct {
	subscriber  events.Subscriber
	viewportMgr *ViewportManager
	statusMgr   *StatusBarManager
	renderers   map[events.EventType]EventRenderer
	mu          sync.RWMutex
}

// EventRenderer is a function that renders an event to styled content.
//
// Strategy pattern: allows custom rendering for each event type.
// Returns (content string, style lipgloss.Style) for flexible styling.
type EventRenderer func(event events.Event) (content string, style lipgloss.Style)

// NewEventHandler creates a new EventHandler with default renderers.
//
// Parameters:
//   - sub: events.Subscriber for receiving agent events
//   - vm: ViewportManager for appending rendered content
//   - sm: StatusBarManager for status updates (spinner on/off)
//
// Automatically registers default renderers for all event types.
func NewEventHandler(sub events.Subscriber, vm *ViewportManager, sm *StatusBarManager) *EventHandler {
	eh := &EventHandler{
		subscriber:  sub,
		viewportMgr: vm,
		statusMgr:   sm,
		renderers:   make(map[events.EventType]EventRenderer),
		mu:          sync.RWMutex{},
	}

	// Register default renderers
	eh.registerDefaultRenderers()

	return eh
}

// registerDefaultRenderers registers pre-configured renderers for all event types.
//
// Default renderers:
//   - EventThinking: "🤔 Thinking..."
//   - EventThinkingChunk: (no output, used for streaming)
//   - EventToolCall: "🔧 Calling: tool_name(args)"
//   - EventToolResult: "✓ Result: tool_name (Xms)"
//   - EventUserInterruption: "⏸️ Interruption (iteration N): message"
//   - EventMessage: AI response (full content)
//   - EventError: "❌ Error: message"
//   - EventDone: Final answer (full content)
func (eh *EventHandler) registerDefaultRenderers() {
	// EventThinking: shows spinner is already handled in HandleEvent
	eh.RegisterRenderer(events.EventThinking, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.ThinkingData); ok {
			return "🤔 Thinking: " + data.Query, lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
		}
		return "🤔 Thinking...", lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	})

	// EventThinkingChunk: streaming reasoning content (typically not displayed)
	eh.RegisterRenderer(events.EventThinkingChunk, func(event events.Event) (string, lipgloss.Style) {
		return "", lipgloss.Style{}
	})

	// EventToolCall: shows tool invocation
	eh.RegisterRenderer(events.EventToolCall, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.ToolCallData); ok {
			args := data.Args
			if len(args) > 50 {
				args = args[:47] + "..."
			}
			return "🔧 Calling: " + data.ToolName + "(" + args + ")",
				lipgloss.NewStyle().Foreground(lipgloss.Color("228")) // Yellow
		}
		return "🔧 Calling...", lipgloss.NewStyle().Foreground(lipgloss.Color("228"))
	})

	// EventToolResult: shows tool execution result
	eh.RegisterRenderer(events.EventToolResult, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.ToolResultData); ok {
			return "✓ Result: " + data.ToolName + " (" + data.Duration.String() + ")",
				lipgloss.NewStyle().Foreground(lipgloss.Color("86")) // Cyan
		}
		return "✓ Result", lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	})

	// EventUserInterruption: shows user interruption
	eh.RegisterRenderer(events.EventUserInterruption, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			msg := data.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			return "⏸️ Interruption (iteration " + string(rune('0'+data.Iteration)) + "): " + msg,
				lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // Orange
		}
		return "⏸️ Interruption", lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	})

	// EventMessage: shows AI response
	eh.RegisterRenderer(events.EventMessage, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.MessageData); ok {
			return data.Content, lipgloss.NewStyle().Foreground(lipgloss.Color("15")) // White
		}
		return "", lipgloss.Style{}
	})

	// EventError: shows error message
	eh.RegisterRenderer(events.EventError, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.ErrorData); ok {
			return "❌ Error: " + data.Err.Error(),
				lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Red
		}
		return "❌ Error", lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	})

	// EventDone: shows final answer
	eh.RegisterRenderer(events.EventDone, func(event events.Event) (string, lipgloss.Style) {
		if data, ok := event.Data.(events.MessageData); ok {
			return data.Content, lipgloss.NewStyle().Foreground(lipgloss.Color("15")) // White
		}
		return "", lipgloss.Style{}
	})
}

// RegisterRenderer registers a custom renderer for a specific event type.
//
// Thread-safe: uses mutex lock.
//
// Allows overriding default renderers for custom behavior.
func (eh *EventHandler) RegisterRenderer(eventType events.EventType, renderer EventRenderer) {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	eh.renderers[eventType] = renderer
}

// HandleEvent processes an event and updates viewport/status bar.
//
// Actions:
//   1. Updates status bar (spinner on/off based on event type)
//   2. Renders event using registered renderer
//   3. Appends styled content to viewport
//
// Thread-safe: uses mutex read lock for renderer access.
//
// Note: Does NOT return tea.Cmd to avoid import cycle.
// The caller (BaseModel) is responsible for returning WaitForEvent Cmd.
func (eh *EventHandler) HandleEvent(event events.Event) {
	// Update status bar based on event type
	switch event.Type {
	case events.EventThinking:
		eh.statusMgr.SetProcessing(true)
	case events.EventDone, events.EventError:
		eh.statusMgr.SetProcessing(false)
	}

	// Render event if renderer is registered
	eh.mu.RLock()
	renderer, ok := eh.renderers[event.Type]
	eh.mu.RUnlock()

	if ok {
		content, style := renderer(event)
		if content != "" {
			styledContent := style.Render(content)
			eh.viewportMgr.Append(styledContent, true)
		}
	}
}

// InitRenderers initializes all default renderers.
//
// This is called separately from NewEventHandler to allow
// for custom renderer registration before initialization.
// This method exists for API compatibility and can be called in tea.Batch().
func (eh *EventHandler) InitRenderers() {
	// Renderers are already registered in NewEventHandler
	// This method exists for API compatibility
}

// GetRenderer returns the registered renderer for an event type.
//
// Thread-safe: uses mutex read lock.
//
// Returns (renderer, ok) where ok is false if no renderer is registered.
func (eh *EventHandler) GetRenderer(eventType events.EventType) (EventRenderer, bool) {
	eh.mu.RLock()
	defer eh.mu.RUnlock()
	renderer, ok := eh.renderers[eventType]
	return renderer, ok
}

// UnregisterRenderer removes a renderer for an event type.
//
// Thread-safe: uses mutex lock.
func (eh *EventHandler) UnregisterRenderer(eventType events.EventType) {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	delete(eh.renderers, eventType)
}
