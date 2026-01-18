# Primitives Quick Reference

**Generated:** 2026-01-18
**Purpose:** Quick reference for Phase 1 primitives

---

## 🎯 5 Core Primitives

### 1. ViewportManager (`viewport.go`)

```go
vm := primitives.NewViewportManager(primitives.ViewportConfig{
    MinWidth:  20,
    MinHeight: 1,
})

// Core operations
vm.Append(content, true)              // Smart scroll
vm.HandleResize(msg, 0, 1)            // Resize handler
content := vm.Content()                // Get all lines
vm.GotoBottom()                        // Scroll operations
```

**Key Features:**
- ✅ Smart scroll (preserves position)
- ✅ Word-wrap reflow
- ✅ Thread-safe

---

### 2. StatusBarManager (`status.go`)

```go
sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

// Core operations
rendered := sm.Render()                // Get status bar string
sm.SetProcessing(true)                 // Show spinner
sm.SetDebugMode(true)                  // Show DEBUG indicator
sm.SetCustomExtra(func() string {      // Custom info
    return "Todo: 3/12"
})
```

**Key Features:**
- ✅ Spinner with "✓ Ready" idle
- ✅ DEBUG indicator (red, bold)
- ✅ Custom extra callback

---

### 3. EventHandler (`events.go`)

```go
sub := events.NewChanEmitter(100).Subscribe()
eh := primitives.NewEventHandler(sub, vm, sm)

// Core operations (Phase 2 API - no tea.Cmd return)
eh.HandleEvent(event)                 // Process event (updates viewport/status)
eh.InitRenderers()                    // Initialize renderers (no-op, renderers already set)

// Custom renderer (optional)
eh.RegisterRenderer(events.EventMessage, func(event events.Event) (string, lipgloss.Style) {
    return "Custom: " + event.Data.(events.MessageData).Content, style
})
```

**⚠️ API Change (Phase 2):**
- `HandleEvent()` no longer returns `tea.Cmd` - caller (BaseModel) handles WaitForEvent
- `InitEventCmd()` removed - use `tui.ReceiveEventCmd()` directly in BaseModel
- This resolved import cycle between `pkg/tui` and `pkg/tui/primitives`

**Default Renderers:**
- EventThinking → "🤔 Thinking: query" (with spinner)
- EventToolCall → "🔧 Calling: tool_name(args)" (truncated to 50 chars)
- EventToolResult → "✓ Result: tool_name (Xms)" (with duration)
- EventUserInterruption → "⏸️ Interruption (iter N): message" (truncated to 60 chars)
- EventMessage → content (white)
- EventError → "❌ Error: message" (red)
- EventDone → content (white)

---

### 4. InterruptionManager (`interruption.go`)

```go
im := primitives.NewInterruptionManager(10)

// MANDATORY: Set callback (Rule 6 compliance)
im.SetOnInput(func(query string) tea.Cmd {
    return startAgentCmd(query)
})

// Core operations
cmd, shouldSend, err := im.HandleInput(query, isProcessing)
if shouldSend {
    im.GetChannel() <- query          // Send to agent
}

// Event handling
shouldDisplay, text := im.HandleEvent(event)
if shouldDisplay {
    vm.Append(systemStyle(text), true)
}
```

**Dual-Mode Logic:**
- `isProcessing=false` → Returns cmd to start agent
- `isProcessing=true` → Sets `shouldSend=true`, sends to channel

---

### 5. DebugManager (`debug.go`)

```go
dm := primitives.NewDebugManager(primitives.DebugConfig{
    LogsDir:  "./debug_logs",
    SaveLogs: true,
}, vm, sm)

// Core operations
msg := dm.ToggleDebug()                // Ctrl+G
filename, err := dm.SaveScreen()       // Ctrl+S
dm.SetLastLogPath(path)                // Track JSON logs
path := dm.GetLastLogPath()            // Ctrl+L

// Event logging
if dm.ShouldLogEvent(event) {
    text := dm.FormatEvent(event)      // "[DEBUG] Event: thinking"
    vm.Append(debugStyle(text), true)
}
```

**DEBUG Events Logged:**
- EventThinking → `[DEBUG] Event: thinking`
- EventToolCall → `[DEBUG] Tool call: tool_name`
- EventToolResult → `[DEBUG] Tool result: tool_name (Xms)`
- EventUserInterruption → `[DEBUG] Interruption (iter N): message`

---

## 🔄 Complete Initialization Pattern (Phase 2: Using BaseModel)

### Option A: Direct BaseModel Usage (Recommended)

```go
package main

import (
    "context"
    "github.com/charmbracelet/bubbletea"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
    ctx := context.Background()
    emitter := events.NewChanEmitter(100)
    sub := emitter.Subscribe()

    // Create BaseModel (all primitives included)
    model := tui.NewBaseModel(ctx, sub)

    // Customize
    model.SetTitle("My AI App")
    model.SetCustomStatus(func() string {
        return "Queries: 42"
    })

    // Run Bubble Tea program
    p := tea.NewProgram(model, tea.WithAltScreen())
    p.Run()
}
```

### Option B: Custom Model with Primitives (Advanced)

```go
package main

import (
    "context"
    "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/bubbles/textarea"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui/primitives"
    tea "github.com/charmbracelet/bubbletea"
)

type MyModel struct {
    viewportMgr  *primitives.ViewportManager
    statusMgr    *primitives.StatusBarManager
    eventHandler *primitives.EventHandler
    interruptMgr *primitives.InterruptionManager
    debugMgr     *primitives.DebugManager

    // Additional fields
    textarea     textarea.Model
    keys         KeyMap
}

func NewMyModel(ctx context.Context, sub events.Subscriber) *MyModel {
    // Create primitives
    vm := primitives.NewViewportManager(primitives.ViewportConfig{})
    sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())
    eh := primitives.NewEventHandler(sub, vm, sm)
    im := primitives.NewInterruptionManager(10)
    dm := primitives.NewDebugManager(primitives.DebugConfig{
        LogsDir:  "./debug_logs",
        SaveLogs: true,
    }, vm, sm)

    // Create textarea
    ta := textarea.New()
    ta.Placeholder = "Enter your query..."

    // MANDATORY: Set interruption callback
    im.SetOnInput(func(query string) tea.Cmd {
        return func() tea.Msg {
            return StartAgentMsg{Query: query}
        }
    })

    return &MyModel{
        viewportMgr:  vm,
        statusMgr:    sm,
        eventHandler: eh,
        interruptMgr: im,
        debugMgr:     dm,
        textarea:     ta,
    }
}

func (m *MyModel) Init() tea.Cmd {
    // Phase 2 API: No InitEventCmd - use tui.ReceiveEventCmd directly
    return tea.Batch(
        m.textarea.Focus(),
        tui.ReceiveEventCmd(eventSub, func(e events.Event) tea.Msg {
            return tui.EventMsg(e)
        }),
    )
}

func (m *MyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tui.EventMsg:
        event := events.Event(msg)

        // Phase 2 API: HandleEvent doesn't return tea.Cmd
        m.eventHandler.HandleEvent(event)

        // Caller handles WaitForEvent
        return m, tui.WaitForEvent(eventSub, func(e events.Event) tea.Msg {
            return tui.EventMsg(e)
        })

    case tea.KeyMsg:
        // Key handling...
    }
    return m, nil
}

func (m *MyModel) View() string {
    // Render using primitives
    viewport := m.viewportMgr.GetViewport().View()
    status := m.statusMgr.Render()
    textarea := m.textarea.View()

    return fmt.Sprintf("%s\n%s\n%s", viewport, textarea, status)
}
```

**⚠️ Phase 2 API Changes:**
- `eh.HandleEvent()` now returns nothing (not `tea.Cmd`)
- `eh.InitEventCmd()` removed - use `tui.ReceiveEventCmd()` directly
- This resolved import cycle - see IMPLEMENTATION-REPORT.md for details

---

## 🔄 Legacy Initialization Pattern (Phase 1)

**Deprecated:** Use BaseModel from Phase 2 instead.

```go
// This is the OLD pattern before Phase 2
package main

import (
    "github.com/charmbracelet/bubbletea"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui/primitives"
)

type MyModel struct {
    viewportMgr  *primitives.ViewportManager
    statusMgr    *primitives.StatusBarManager
    eventHandler *primitives.EventHandler
    interruptMgr *primitives.InterruptionManager
    debugMgr     *primitives.DebugManager
}

func NewMyModel() *MyModel {
    // Create primitives
    vm := primitives.NewViewportManager(primitives.ViewportConfig{})
    sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

    emitter := events.NewChanEmitter(100)
    sub := emitter.Subscribe()

    eh := primitives.NewEventHandler(sub, vm, sm)
    im := primitives.NewInterruptionManager(10)
    dm := primitives.NewDebugManager(primitives.DebugConfig{
        LogsDir:  "./debug_logs",
        SaveLogs: true,
    }, vm, sm)

    // MANDATORY: Set interruption callback
    im.SetOnInput(func(query string) tea.Cmd {
        return func() tea.Msg {
            return StartAgentMsg{Query: query}
        }
    })

    return &MyModel{
        viewportMgr:  vm,
        statusMgr:    sm,
        eventHandler: eh,
        interruptMgr: im,
        debugMgr:     dm,
    }
}

func (m *MyModel) Init() tea.Cmd {
    // OLD API: InitEventCmd (removed in Phase 2)
    return m.eventHandler.InitEventCmd()
}

func (m *MyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tui.EventMsg:
        event := events.Event(msg)
        return m, m.eventHandler.HandleEvent(event)  // OLD API
    }
    return m, nil
}
```

---

        case key.Matches(msg, keys.DebugToggle):
            text := m.debugMgr.ToggleDebug()
            m.viewportMgr.Append(systemStyle(text), true)
            return m, nil

        case key.Matches(msg, keys.SaveScreen):
            filename, err := m.debugMgr.SaveScreen()
            if err != nil {
                m.viewportMgr.Append(errorStyle(err.Error()), true)
            } else {
                m.viewportMgr.Append(systemStyle("Saved: "+filename), true)
            }
            return m, nil
        }

    case tui.EventMsg:
        event := events.Event(msg)
        _ = m.eventHandler.HandleEvent(event)

        if m.debugMgr.ShouldLogEvent(event) {
            text := m.debugMgr.FormatEvent(event)
            m.viewportMgr.Append(debugStyle(text), true)
        }

        // Check for interruption event
        if shouldDisplay, text := m.interruptMgr.HandleEvent(event); shouldDisplay {
            m.viewportMgr.Append(systemStyle(text), true)
        }

        return m, nil
    }

    return m, nil
}

func (m *MyModel) View() string {
    return fmt.Sprintf(
        "%s\n%s\n%s",
        m.viewportMgr.GetViewport().View(),
        m.textarea.View(),
        m.statusMgr.Render(),
    )
}
```

---

## 🔑 Key Bindings Reference

| Key | Action | Primitive | Method |
|-----|--------|-----------|--------|
| **Enter** | Submit input | InterruptionManager | `HandleInput()` |
| **Ctrl+G** | Toggle debug | DebugManager | `ToggleDebug()` |
| **Ctrl+S** | Save screen | DebugManager | `SaveScreen()` |
| **Ctrl+L** | Show log path | DebugManager | `GetLastLogPath()` |
| **Ctrl+C** | Quit | Bubble Tea | Built-in |

---

## ⚙️ Configuration Options

### ViewportManager

```go
type ViewportConfig struct {
    MinWidth  int  // Minimum viewport width (default: 20)
    MinHeight int  // Minimum viewport height (default: 1)
}
```

### StatusBarManager

```go
type StatusBarConfig struct {
    SpinnerColor    lipgloss.Color  // Spinner color (default: "86" cyan)
    IdleColor       lipgloss.Color  // Ready state color (default: "242" gray)
    BackgroundColor lipgloss.Color  // Status bar bg (default: "235" dark gray)
    DebugColor      lipgloss.Color  // DEBUG indicator bg (default: "196" red)
    DebugText       lipgloss.Color  // DEBUG indicator text (default: "15" white)
    ExtraText       lipgloss.Color  // Custom info color (default: "252" gray)
}

// Use default:
sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

// Or custom:
sm := primitives.NewStatusBarManager(primitives.StatusBarConfig{
    SpinnerColor: lipgloss.Color("228"), // Yellow
    // ... other fields
})
```

### DebugManager

```go
type DebugConfig struct {
    LogsDir  string  // Directory for JSON logs (default: "./debug_logs")
    SaveLogs bool    // Enable JSON logging (default: true)
}

dm := primitives.NewDebugManager(primitives.DebugConfig{
    LogsDir:  "./custom_logs",
    SaveLogs: true,
}, vm, sm)
```

### InterruptionManager

```go
// Buffer size controls channel capacity
im := primitives.NewInterruptionManager(10)  // 10 message buffer
```

---

## 🧪 Testing Your Model

```go
func TestMyModel_BasicFunctionality(t *testing.T) {
    vm := primitives.NewViewportManager(primitives.ViewportConfig{})
    sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

    // Test initialization
    assert.NotNil(t, vm)
    assert.NotNil(t, sm)

    // Test viewport
    vm.Append("Test line", false)
    content := vm.Content()
    assert.Len(t, content, 1)

    // Test status bar
    sm.SetProcessing(true)
    assert.True(t, sm.IsProcessing())

    // Test debug toggle
    dm := primitives.NewDebugManager(primitives.DebugConfig{}, vm, sm)
    msg := dm.ToggleDebug()
    assert.Contains(t, msg, "ON")
}
```

---

## 🚨 Common Pitfalls

### ❌ WRONG: Forgetting SetOnInput

```go
im := primitives.NewInterruptionManager(10)
// ❌ Missing: im.SetOnInput(...)
cmd, _, err := im.HandleInput(query, false)
// Error: "no input handler set"
```

### ✅ CORRECT: Always set callback

```go
im := primitives.NewInterruptionManager(10)
im.SetOnInput(func(query string) tea.Cmd {
    return startAgentCmd(query)
})
cmd, _, err := im.HandleInput(query, false)
// ✅ Works!
```

### ❌ WRONG: Not using channel return value

```go
cmd, shouldSend, _ := im.HandleInput(query, true)
// ❌ Missing: check shouldSend
```

### ✅ CORRECT: Check dual-mode result

```go
cmd, shouldSend, _ := im.HandleInput(query, isProcessing)
if shouldSend {
    im.GetChannel() <- query
}
return m, cmd
```

---

## 📊 Thread Safety Notes

All primitives are thread-safe using `sync.RWMutex`:

- ✅ Safe to read from multiple goroutines
- ✅ Safe to write from multiple goroutines
- ✅ No race conditions detected

**However:**
- ⚠️ Do NOT pass primitive pointers to goroutines without synchronization
- ⚠️ Always use primitive methods, never access internal fields directly

---

## 🎨 Styling Tips

```go
import "github.com/charmbracelet/lipgloss"

// Define styles once
var (
    systemStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("242"))

    debugStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("228"))  // Yellow

    errorStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("196")).  // Red
        Bold(true)
)

// Use with primitives
vm.Append(systemStyle("System message"), true)
vm.Append(debugStyle("[DEBUG] Debug info"), true)
vm.Append(errorStyle("Error occurred"), true)
```

---

## 📚 Quick API Reference

### ViewportManager

| Method | Purpose |
|--------|---------|
| `Append(content, preservePosition)` | Add content with smart scroll |
| `HandleResize(msg, header, footer)` | Process resize events |
| `Content()` | Get all lines |
| `GetViewport()` | Get underlying viewport.Model |
| `ScrollUp(n)`, `ScrollDown(n)` | Scroll operations |
| `GotoTop()`, `GotoBottom()` | Jump to top/bottom |

### StatusBarManager

| Method | Purpose |
|--------|---------|
| `Render()` | Get rendered status bar string |
| `SetProcessing(bool)` | Show/hide spinner |
| `SetDebugMode(bool)` | Show/hide DEBUG indicator |
| `SetCustomExtra(fn)` | Set custom info callback |

### EventHandler

| Method | Purpose |
|--------|---------|
| `HandleEvent(event)` | Process event, update viewport/status |
| `InitEventCmd()` | Start listening for events |
| `RegisterRenderer(type, fn)` | Add custom renderer |
| `UnregisterRenderer(type)` | Remove renderer |

### InterruptionManager

| Method | Purpose |
|--------|---------|
| `SetOnInput(fn)` | **MANDATORY** callback |
| `HandleInput(input, isProcessing)` | Process user input |
| `GetChannel()` | Get interruption channel |
| `HandleEvent(event)` | Format interruption for display |
| `IsCallbackSet()` | Check if callback set |
| `Close()` | Close channel |

### DebugManager

| Method | Purpose |
|--------|---------|
| `ToggleDebug()` | Switch debug mode, return message |
| `IsEnabled()` | Check if debug mode on |
| `ShouldLogEvent(event)` | Check if event should be logged |
| `FormatEvent(event)` | Format event for DEBUG display |
| `SetLastLogPath(path)` | Store JSON log path |
| `GetLastLogPath()` | Get stored path |
| `SaveScreen()` | Save viewport to markdown |

---

**Last Updated:** 2026-01-18
**Phase:** 1 Complete
**Next:** Phase 2 - BaseModel Creation
