# 05. Event Flow Analysis

## Overview

This document traces the complete flow of events from the agent to the TUI using the Port & Adapter pattern.

## Event Type Definitions

**File**: [pkg/events/events.go](../pkg/events/events.go)

```go
const (
    // Lifecycle events
    EventThinking      EventType = "thinking"       // Agent starts thinking
    EventMessage       EventType = "message"        // Agent generates message
    EventDone          EventType = "done"           // Agent finished
    EventError         EventType = "error"          // Error occurred

    // Tool events
    EventToolCall      EventType = "tool_call"      // Tool execution started
    EventToolResult    EventType = "tool_result"    // Tool execution completed

    // Interruption events
    EventUserInterruption EventType = "user_interruption"  // User interrupted

    // Streaming events
    EventThinkingChunk EventType = "thinking_chunk" // Reasoning content delta
)
```

## Complete Event Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          COMPLETE EVENT FLOW                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  PHASE 1: EVENT EMISSION (Agent Side)                                       │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  pkg/chain/executor.go                                              │   │
│  │      │                                                               │   │
│  │      └─▶ ReActExecutor.Execute(ctx, *ReActExecution)                │   │
│  │                                                                       │   │
│  │      For each iteration:                                             │   │
│  │          1. OnIterationStart(observers)                              │   │
│  │                                                                       │   │
│  │          2. Execute LLMInvocationStep                                │   │
│  │              │                                                       │   │
│  │              └─▶ Emit EventThinking                                 │   │
│  │                                                                       │   │
│  │          3. Execute ToolExecutionStep (if needed)                    │   │
│  │              │                                                       │   │
│  │              ├─▶ Emit EventToolCall                                 │   │
│  │              └─▶ Emit EventToolResult                               │   │
│  │                                                                       │   │
│  │          4. Check interruption (between iterations)                  │   │
│  │              │                                                       │   │
│  │              └─▶ Emit EventUserInterruption (if input)               │   │
│  │                                                                       │   │
│  │          5. OnIterationEnd(observers)                                │   │
│  │                                                                       │   │
│  │      6. OnFinish(observers)                                          │   │
│  │          │                                                           │   │
│  │          └─▶ Emit EventDone or EventError                           │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    │ Emit(ctx, Event)                      │
│                                    ▼                                        │
│  PHASE 2: EVENT TRANSPORT (Port Implementation)                            │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  pkg/events/emitter.go                                               │   │
│  │                                                                       │   │
│  │  type ChanEmitter struct {                                           │   │
│  │      events  chan Event                                              │   │
│  │      mu      sync.RWMutex                                            │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  │  func (ce *ChanEmitter) Emit(ctx context.Context, event Event) {     │   │
│  │      select {                                                         │   │
│  │      case ce.events <- event:  // Send to channel                    │   │
│  │      case <-ctx.Done():       // Respect cancellation                │   │
│  │          return                                                        │   │
│  │      }                                                                 │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    │ Buffered channel (size=100)           │
│                                    ▼                                        │
│  PHASE 3: EVENT SUBSCRIPTION (UI Side)                                    │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  cmd/*/main.go                                                       │   │
│  │                                                                       │   │
│  │  emitter := events.NewChanEmitter(100)                               │   │
│  │  client.SetEmitter(emitter)                                          │   │
│  │  sub := emitter.Subscribe()  // Create subscriber                    │   │
│  │                                                                       │   │
│  │  // Pass subscriber to TUI (Port interface only!)                    │   │
│  │  model := tui.NewInterruptionModel(ctx, client, sub, ...)            │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    │ Sub.Events() <-chan Event             │
│                                    ▼                                        │
│  PHASE 4: EVENT CONVERSION (Adapter Layer)                                │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  pkg/tui/adapter.go                                                  │   │
│  │                                                                       │   │
│  │  // EventMsg wraps events.Event as Bubble Tea message                │   │
│  │  type EventMsg events.Event                                          │   │
│  │                                                                       │   │
│  │  // ReceiveEventCmd creates command to read from event channel       │   │
│  │  func ReceiveEventCmd(sub events.Subscriber) tea.Cmd {               │   │
│  │      return func() tea.Msg {                                         │   │
│  │          select {                                                     │   │
│  │          case event := <-sub.Events():                               │   │
│  │              return EventMsg(event)  // Convert to Bubble Tea        │   │
│  │          case <-time.After(100 * time.Millisecond):                 │   │
│  │              return nil  // No event yet                             │   │
│  │          }                                                           │   │
│  │      }                                                               │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    │ tea.Cmd                                │
│                                    ▼                                        │
│  PHASE 5: EVENT PROCESSING (TUI Update)                                    │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  pkg/tui/model.go                                                    │   │
│  │                                                                       │   │
│  │  func (m *InterruptionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {│
│  │      switch msg := msg.(type) {                                       │   │
│  │      case EventMsg:  // Converted event                              │   │
│  │          return m.handleAgentEventWithInterruption(events.Event(msg)) │
│  │      // ... other message types                                      │   │
│  │      }                                                                 │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  │  func (m *InterruptionModel) handleAgentEventWithInterruption(       │   │
│  │      event events.Event) tea.Cmd {                                   │   │
│  │      switch event.Type {                                             │   │
│  │      case events.EventThinking:                                      │   │
│  │          m.status = "🤔 Thinking..."                                 │   │
│  │          m.isProcessing = true                                       │   │
│  │                                                                       │   │
│  │      case events.EventToolCall:                                     │   │
│  │          data := event.Data.(events.ToolCallData)                    │   │
│  │          m.appendLog("[🔧 Tool: %s]", data.ToolName)                │   │
│  │                                                                       │   │
│  │      case events.EventUserInterruption:                              │   │
│  │          data := event.Data.(events.UserInterruptionData)            │   │
│  │          m.appendLog("[🔔 INTERRUPTION at iter %d]", data.Iteration) │   │
│  │                                                                       │   │
│  │      case events.EventDone:                                         │   │
│  │          data := event.Data.(events.MessageData)                     │   │
│  │          m.appendLog(data.Content)                                   │   │
│  │          m.status = "✅ Done"                                        │   │
│  │                                                                       │   │
│  │      case events.EventError:                                        │   │
│  │          err := event.Data.(events.ErrorData).Err                    │   │
│  │          m.appendLog("❌ Error: %v", err)                            │   │
│  │      }                                                                 │   │
│  │      return WaitForEvent(sub)  // Continue listening                 │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    │ Bubble Tea rendering                   │
│                                    ▼                                        │
│  PHASE 6: UI UPDATE (View)                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                                                                       │   │
│  │  func (m *InterruptionModel) View() string {                         │   │
│  │      return fmt.Sprintf(                                             │   │
│  │          "%s\n%s\n%s",                                               │   │
│  │          renderHeader(m.status, m.spinner),                          │   │
│  │          m.viewport.View(),  // Event log                            │   │
│  │          m.textarea.View(),  // Input field                          │   │
│  │      )                                                                │   │
│  │  }                                                                   │   │
│  │                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Key Event Flow Examples

### Example 1: Tool Call Event Flow

```
1. Agent decides to call tool
   └─▶ pkg/chain/executor.go:ToolExecutionStep.Execute()

2. Emit EventToolCall
   └─▶ exec.emitter.Emit(ctx, Event{
           Type: EventToolCall,
           Data: ToolCallData{ToolName: "get_wb_categories", Args: "{...}"}
       })

3. ChanEmitter sends to channel
   └─▶ ce.events <- event

4. Subscriber receives from channel
   └─▶ event := <-sub.Events()

5. Convert to Bubble Tea message
   └─▶ EventMsg(event)

6. TUI Update() handles EventMsg
   └─▶ handleAgentEventWithInterruption()

7. Update viewport with tool call info
   └─▶ m.appendLog("[🔧 Tool: get_wb_categories]")

8. Bubble Tea re-renders View()
   └─▶ User sees tool call in UI
```

### Example 2: Interruption Event Flow

```
1. User types "todo: add test task" and presses Enter
   └─▶ InterruptionModel.handleKeyPressWithInterruption()

2. Send to input channel
   └─▶ inputChan <- "todo: add test task"

3. ReActExecutor checks channel between iterations
   └─▶ select {
           case msg := <-exec.UserInputChan:
               // Process interruption
       }

4. Load interruption prompt
   └─▶ loadInterruptionPrompt(exec.Config.InterruptionPrompt)

5. Emit EventUserInterruption
   └─▶ exec.emitter.Emit(ctx, Event{
           Type: EventUserInterruption,
           Data: UserInterruptionData{
               Message: "todo: add test task",
               Iteration: 3,
               PromptSource: "yaml:prompts/interruption_handler.yaml"
           }
       })

6. TUI receives event via Subscriber
   └─▶ handleAgentEventWithInterruption()

7. Display interruption in viewport
   └─▶ m.appendLog("[🔔 INTERRUPTION at iteration 3]\ntodo: add test task")

8. User sees interruption in UI
```

## Thread Safety

### ChanEmitter (Thread-safe)

```go
type ChanEmitter struct {
    events chan Event
    mu     sync.RWMutex
}

func (ce *ChanEmitter) Emit(ctx context.Context, event Event) {
    select {
    case ce.events <- event:  // Channel send is thread-safe
    case <-ctx.Done():
        return
    }
}
```

### Subscriber (Thread-safe)

```go
type ChanSubscriber struct {
    events chan Event
}

func (s *ChanSubscriber) Events() <-chan Event {
    return s.events  // Read-only channel, thread-safe for multiple receivers
}
```

### Model Update (Single-threaded)

```go
// Bubble Tea guarantees Update() is called sequentially
// No mutex needed within Update() itself
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // Safe to modify m without mutex here
}
```

---

## Summary

| Phase | Component | Responsibility |
|-------|-----------|----------------|
| 1. Emission | `pkg/chain/executor.go` | Emit events during execution |
| 2. Transport | `pkg/events/emitter.go` | Thread-safe channel delivery |
| 3. Subscription | `cmd/*/main.go` | Create subscriber for TUI |
| 4. Conversion | `pkg/tui/adapter.go` | Event → Bubble Tea message |
| 5. Processing | `pkg/tui/model.go` | Handle events, update UI state |
| 6. Rendering | Bubble Tea | Display UI to user |

**Key insight**: The event system is well-designed and follows Port & Adapter pattern correctly. The issue is not with events, but with `pkg/tui` directly depending on `pkg/agent`.

---

**Next**: [06-RECOMMENDATIONS.md](./06-RECOMMENDATIONS.md) — Future refactoring recommendations
