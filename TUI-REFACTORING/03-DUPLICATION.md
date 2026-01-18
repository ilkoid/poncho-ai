# 03. Code Duplication Analysis

## Overview

This document catalogs code duplication across TUI implementations in Poncho AI.

## Duplication Matrix

| Functionality | pkg/tui/model.go | pkg/tui/simple.go | internal/ui/model.go | Total Lines |
|---------------|------------------|-------------------|----------------------|-------------|
| **Viewport resize** | 75 (396-471) | 28 (303-331) | ~50 | ~150 |
| **Event handling** | 74 (319-393) | 52 (249-301) | ~60 | ~180 |
| **Key bindings** | 35 (97-132) | ~20 | ~30 | ~85 |
| **Status bar** | 60 (639-699) | 3 (376-379) | ~40 | ~100 |
| **Message styling** | 52 (782-834) | ~30 | ~40 | ~120 |
| **Init pattern** | ~40 | ~30 | ~40 | ~110 |
| **Update pattern** | ~100 | ~60 | ~100 | ~260 |
| **View pattern** | ~80 | ~40 | ~80 | ~200 |

**Estimated total duplication**: ~1200 lines of similar/identical code

## Duplication #1: Viewport Resize Logic

### Location A: pkg/tui/model.go:396-471

```go
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
    headerHeight := 1
    helpHeight := 0
    if m.showHelp {
        helpHeight = 3
    }
    footerHeight := m.textarea.Height() + 2 + 1 + 1
    vpHeight := msg.Height - headerHeight - helpHeight - footerHeight

    if !m.ready {
        m.viewport.Height = vpHeight
        m.viewport.Width = msg.Width
        m.ready = true
        return m, nil
    }

    if vpHeight > 0 && m.viewport.Height != vpHeight {
        m.viewport.Height = vpHeight
    }
    if msg.Width > 0 && m.viewport.Width != msg.Width {
        m.viewport.Width = msg.Width
    }

    return m, nil
}
```

### Location B: pkg/tui/simple.go:303-331

```go
func (t *SimpleTui) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
    headerHeight := t.config.StatusHeight
    footerHeight := t.textarea.Height() + 1
    vpHeight := msg.Height - headerHeight - footerHeight

    if !t.ready {
        t.viewport.Height = vpHeight
        t.viewport.Width = msg.Width
        t.ready = true
        return t, nil
    }

    if vpHeight > 0 && t.viewport.Height != vpHeight {
        t.viewport.Height = vpHeight
    }
    if msg.Width > 0 && t.viewport.Width != msg.Width {
        t.viewport.Width = msg.Width
    }

    return t, nil
}
```

### Similarity: ~85%

**Differences**:
- `Model` has help panel logic
- `SimpleTui` uses config-based heights
- Otherwise identical structure

### Location C: internal/ui/model.go

Similar logic with additional todo panel considerations.

---

## Duplication #2: Event Handling

### Location A: pkg/tui/model.go:319-393

```go
func (m *Model) handleAgentEvent(event events.Event) tea.Cmd {
    switch event.Type {
    case events.EventThinking:
        m.spinnerTick = time.Now()
        m.status = "🤔 Thinking..."
        m.isProcessing = true

    case events.EventThinkingChunk:
        data := event.Data.(events.ThinkingChunkData)
        m.thinkingContent = data.Accumulated

    case events.EventToolCall:
        data := event.Data.(events.ToolCallData)
        m.appendLog(systemStyle(fmt.Sprintf("[🔧 Tool: %s]", data.ToolName)))

    case events.EventToolResult:
        data := event.Data.(events.ToolResultData)
        m.appendLog(systemStyle(fmt.Sprintf("[✅ Tool result: %s] (%.2fs)",
            data.ToolName, data.Duration.Seconds())))

    case events.EventUserInterruption:
        data := event.Data.(events.UserInterruptionData)
        m.appendLog(systemStyle(fmt.Sprintf("[🔔 Interruption at iter %d]: %s",
            data.Iteration, data.Message)))

    case events.EventMessage:
        data := event.Data.(events.MessageData)
        m.appendLog(AIMessageStyle(data.Content))

    case events.EventError:
        err := event.Data.(events.ErrorData).Err
        m.appendLog(errorMessageStyle(fmt.Sprintf("Error: %v", err)))

    case events.EventDone:
        data := event.Data.(events.MessageData)
        m.appendLog(AIMessageStyle(data.Content))
        m.status = "✅ Done"
        m.isProcessing = false
    }
    return nil
}
```

### Location B: pkg/tui/simple.go:249-301

```go
func (t *SimpleTui) handleAgentEvent(event events.Event) tea.Cmd {
    switch event.Type {
    case events.EventThinking:
        t.mu.Lock()
        t.status = "🤔 Thinking..."
        t.isProcessing = true
        t.mu.Unlock()

    case events.EventThinkingChunk:
        data := event.Data.(events.ThinkingChunkData)
        // Similar logic

    case events.EventToolCall:
        data := event.Data.(events.ToolCallData)
        t.appendMessage(fmt.Sprintf("[🔧 Tool: %s]", data.ToolName), "system")

    // ... similar cases for all event types
    }
    return nil
}
```

### Similarity: ~75%

**Differences**:
- `SimpleTui` uses mutex for thread safety
- `Model` has spinner tracking
- Otherwise identical event types and structure

---

## Duplication #3: Key Bindings

### Location A: pkg/tui/model.go:97-132

```go
func DefaultKeyMap() KeyMap {
    return KeyMap{
        Quit: key.NewBinding(
            key.WithKeys("ctrl+c", "esc"),
            key.WithHelp("Ctrl+C", "quit"),
        ),
        ScrollUp: key.NewBinding(
            key.WithKeys("ctrl+u", "pgup"),
            key.WithHelp("Ctrl+U", "scroll up"),
        ),
        ScrollDown: key.NewBinding(
            key.WithKeys("ctrl+d", "pgdown"),
            key.WithHelp("Ctrl+D", "scroll down"),
        ),
        ToggleHelp: key.NewBinding(
            key.WithKeys("ctrl+h"),
            key.WithHelp("Ctrl+H", "toggle help"),
        ),
        ConfirmInput: key.NewBinding(
            key.WithKeys("enter"),
            key.WithHelp("Enter", "send query"),
        ),
        SaveToFile: key.NewBinding(
            key.WithKeys("ctrl+s"),
            key.WithHelp("Ctrl+S", "save to file"),
        ),
        ToggleDebug: key.NewBinding(
            key.WithKeys("ctrl+g"),
            key.WithHelp("Ctrl+G", "toggle debug"),
        ),
        ShowDebugPath: key.NewBinding(
            key.WithKeys("ctrl+l"),
            key.WithHelp("Ctrl+L", "show debug log path"),
        ),
    }
}
```

### Location B: pkg/tui/simple.go

Similar but simplified keymap structure.

### Location C: internal/ui/model.go

Extended keymap with additional app-specific bindings.

### Similarity: ~60%

---

## Duplication #4: Status Bar Rendering

### Location A: pkg/tui/model.go:639-699

```go
func (m *Model) renderStatusLine() string {
    // Spinner
    spinnerStr := ""
    if m.isProcessing {
        spinnerStr = m.spinner.View()
    }

    // Status
    statusStr := m.status
    if statusStr == "" {
        statusStr = "Ready"
    }

    // Custom extra info
    extraStr := ""
    if m.customStatusExtra != nil {
        extraStr = m.customStatusExtra()
    }

    // Debug indicator
    debugStr := ""
    if m.debugMode {
        debugStr = "[DEBUG] "
    }

    // Todo stats
    todoStr := ""
    if len(m.todos) > 0 {
        pending := 0
        done := 0
        for _, t := range m.todos {
            if t.Status == todo.StatusPending {
                pending++
            } else if t.Status == todo.StatusDone {
                done++
            }
        }
        todoStr = fmt.Sprintf("📋 %d/%d ", done, pending+done)
    }

    // Compose status line
    // ... (styling logic)
}
```

### Location B: pkg/tui/simple.go:376-379

```go
func (t *SimpleTui) renderStatusBar() string {
    return t.config.Colors.Status.Render(
        fmt.Sprintf("%s%s %s",
            t.statusSymbol,
            t.config.Title,
            t.statusText,
        ),
    )
}
```

### Similarity: ~40%

`SimpleTui` has much simpler status bar without todos, debug, spinner.

---

## Duplication #5: Message Styling

### Location A: pkg/tui/model.go:782-834

```go
func SystemMessageStyle(text string) string {
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("242")).   // Grey
        Render(text)
}

func AIMessageStyle(text string) string {
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("86")).    // Cyan
        Render(text)
}

func errorMessageStyle(text string) string {
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("196")).   // Red
        Bold(true).
        Render(text)
}

func userMessageStyle(text string) string {
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("226")).   // Yellow
        Bold(true).
        Render(text)
}

func thinkingStyle(text string) string {
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("99")).    // Purple
        Bold(true).
        Render(text)
}
```

### Location B: pkg/tui/simple.go

Similar styling functions with different color schemes.

### Location C: internal/ui/model.go

Extended styling with additional message types.

### Similarity: ~70%

---

## Impact Analysis

### Maintenance Cost

| Scenario | Impact |
|----------|--------|
| **Bug fix in viewport resize** | Must fix in 3 places |
| **Add new event type** | Must update 3 handlers |
| **Change key binding** | Must update 3 keymaps |
| **Modify status bar** | Must update 3 renderers |

### Code Quality Issues

1. **Inconsistency**: Slight variations between implementations
2. **Drift**: Fixes applied to one location, forgotten in others
3. **Testing**: Similar tests duplicated across components
4. **Documentation**: Same logic documented multiple times

---

## Solution: Extract Common Primitives

### Proposed Structure

```
pkg/tui/primitives/
├── viewport.go    → ViewportManager (unified resize logic)
├── events.go      → EventHandler (unified event handling)
├── status.go      → StatusBarManager (unified status rendering)
├── keys.go        → KeyBindings (configurable keymaps)
└── styles.go      → StyleConfig (unified styling)
```

### Example: ViewportManager

```go
// pkg/tui/primitives/viewport.go
type ViewportManager struct {
    viewport viewport.Model
    config   ViewportConfig
    mu       sync.RWMutex
}

type ViewportConfig struct {
    HeaderHeight  int
    FooterHeight  int
    HelpHeight    int
    SmartScroll   bool
    WrapText      bool
}

func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg) tea.Cmd {
    // Single implementation used by all TUIs
}

func (vm *ViewportManager) Append(content string, style lipgloss.Style) {
    // Smart scroll with position preservation
}
```

### Benefits

| Before | After |
|--------|-------|
| 3 viewport implementations | 1 ViewportManager |
| ~150 lines duplicated | ~80 lines shared |
| Bug fixes in 3 places | Bug fix in 1 place |
| Inconsistent behavior | Consistent behavior |

---

## Summary

| Category | Lines Duplicated | Potential Savings |
|----------|------------------|-------------------|
| Viewport resize | ~150 | ~70 (1 impl) |
| Event handling | ~180 | ~100 (1 impl) |
| Key bindings | ~85 | ~40 (1 impl) |
| Status bar | ~100 | ~50 (1 impl) |
| Message styling | ~120 | ~60 (1 impl) |
| **Total** | **~635** | **~320** |

**Potential code reduction**: ~315 lines (50% savings)

---

**Next**: [04-INTERRUPTIONS.md](./04-INTERRUPTIONS.md) — Interruption mechanism analysis
