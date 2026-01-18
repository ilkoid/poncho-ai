# TUI Refactoring: Option B - Detailed Implementation Plan

**Goal**: Restore Port & Adapter pattern compliance while preserving all existing functionality including interruptions.

**User Choices**:
- Approach: **Option B (Port & Adapter)** - Moderate refactoring effort
- Priority: Viewport & Resize, Event Handling, Status Bar & Spinner, Interruptions, Debug (JSON logs, screen save, Ctrl+G/Ctrl+S/Ctrl+L)
- Interruption: Extract to InterruptionManager
- Backward Compatibility: **No** - can break existing API

---

## 📊 Execution Roadmap

**Overall Timeline**: 2 weeks (10 working days)

### Phase Summary

| Phase | Duration | Focus | Deliverables | Risk |
|-------|----------|-------|--------------|------|
| **Phase 1A** | 1.5 days | Viewport + StatusBar primitives | ViewportManager, StatusBarManager | Medium (bug fixes) |
| **Phase 1B** | 1.5 days | Events + Interruption primitives | EventHandler, InterruptionManager | Low |
| **Phase 1C** | 1 day | Debug primitive | DebugManager | Low |
| **Phase 2** | 1.5 days | Base model | BaseModel | Medium (dependencies) |
| **Phase 3A** | 1 day | Model refactoring | Refactored Model, SimpleTui | Low |
| **Phase 3B** | 1 day | InterruptionModel refactoring | Refactored InterruptionModel | Medium (interruptions) |
| **Phase 4** | 1 day | Entry points | Updated cmd/*/main.go | Low |
| **Phase 5** | 1.5 days | Testing | Unit tests, integration tests, manual | Medium |

**Total**: 10 days

---

## 🏗️ Target Architecture

```
pkg/tui/
├── primitives/           # NEW: Reusable low-level components
│   ├── viewport.go      # ViewportManager (resize, smart scroll, 4 bug fixes!)
│   ├── status.go        # StatusBarManager (spinner, status bar, DEBUG indicator)
│   ├── events.go        # EventHandler (pluggable event renderers)
│   ├── interruption.go  # InterruptionManager (user input, channel, callback)
│   └── debug.go          # DebugManager (JSON logs, screen save, debug mode)
├── base.go              # NEW: BaseModel (no agent dependency, Port & Adapter compliant)
├── model.go             # REFACTOR: Use BaseModel + primitives
├── interruption.go      # REFACTOR: Use BaseModel + InterruptionManager
├── simple.go            # REFACTOR: Use BaseModel + primitives
├── adapter.go           # EXISTING: EventMsg, ReceiveEventCmd
├── viewport_helpers.go  # EXISTING: Smart scroll helpers
├── components.go        # EXISTING: Shared styling functions
├── colors.go            # EXISTING: Color schemes
└── run.go               # EXISTING: Ready-to-use TUI runners
```

### Port & Adapter Compliance

**Before (VIOLATION)**:
```go
// pkg/tui/model.go - CURRENT
import "github.com/ilkoid/poncho-ai/pkg/agent"  // ← VIOLATION

type Model struct {
    agent agent.Agent  // ← TIGHT COUPLING
    coreState *state.CoreState
    eventSub  events.Subscriber
}
```

**After (COMPLIANT)**:
```go
// pkg/tui/base.go - NEW
import (
    "github.com/ilkoid/poncho-ai/pkg/events"  // ← ONLY Port interface
    // NO import of pkg/agent
)

type BaseModel struct {
    viewportMgr  *primitives.ViewportManager
    statusMgr    *primitives.StatusBarManager
    eventHdlr    *primitives.EventHandler
    debugMgr     *primitives.DebugManager
    eventSub     events.Subscriber  // ← Port interface only
    ctx          context.Context
    // NO direct agent dependency
}
```

---

## Phase 1A: Viewport & StatusBar Primitives (Days 1-3)

**🎯 Phase Goal**: Create foundational UI primitives with bug fixes

### Stage 1.1.1: ViewportManager Implementation

**Time**: 1 day

**Location**: `pkg/tui/primitives/viewport.go`

**⚠️ CRITICAL BUGS FOUND**

After thorough analysis of `pkg/tui/model.go:396-471`, **4 critical bugs** were identified:

| Bug | Severity | Symptom | Impact |
|-----|----------|---------|--------|
| **#1: Timing Attack** | 🔴 High | Scroll position lost during resize | User gets "jumped" to wrong position |
| **#2: Height = 0** | 🔴 High | Scroll breaks in small windows | `ScrollUp/Down` stop working completely |
| **#3: Off-by-One** | 🟡 Medium | Incorrect clamp in edge cases | YOffset can be invalid in rare cases |
| **#4: Race Condition** | 🟡 Medium | Data loss during concurrent access | appendLog can lose data |

**✅ Expected Outcomes**:
- [ ] `pkg/tui/primitives/viewport.go` created
- [ ] `ViewportManager` struct with thread-safe operations
- [ ] `HandleResize()` with all 4 bugs fixed
- [ ] `Append()` with smart scroll preservation
- [ ] `GetViewport()` for thread-safe access
- [ ] Unit tests for resize scenarios (5 test cases)

**🔍 Completion Criteria**:
```bash
# All tests pass
go test ./pkg/tui/primitives/ -run TestViewportManager -v

# Manual verification
cd cmd/interruption-test && go run main.go
# Resize window multiple times → scroll position preserved
```

**⚠️ Risks**:
- Bug #4 (race condition) may require additional mutex testing
- Min height = 1 may cause layout issues in very small windows

**📦 Dependencies**:
- None (foundational primitive)

**🐛 Bug #1: Timing Attack - Inconsistent State**

**Problem**: Height is updated BEFORE computing `wasAtBottom`, but `wasAtBottom` uses the NEW Height with OLD content.

**Fix**: Compute `wasAtBottom` BEFORE changing Height:
```go
// ✅ FIXED CODE:
// 1. Compute wasAtBottom with OLD state
totalLinesBefore := vm.viewport.TotalLineCount()
wasAtBottom := vm.viewport.YOffset + vm.viewport.Height >= totalLinesBefore

// 2. Update Height AFTER wasAtBottom
vm.viewport.Height = vpHeight
vm.viewport.Width = vpWidth

// 3. Reflow content
vm.viewport.SetContent(fullContent)

// 4. Restore position
if wasAtBottom {
    vm.viewport.GotoBottom()
}
```

**🐛 Bug #2: Height = 0 - Death of Scroll**

**Problem**: If computed `vpHeight < 0`, it's set to `0`, but viewport with `Height = 0` cannot scroll.

**Fix**: Use minimum height of 1:
```go
// ✅ FIXED CODE:
vpHeight := msg.Height - headerHeight - footerHeight
if vpHeight < 1 {  // ✅ Minimum 1 line for viewport
    vpHeight = 1
}
```

**🐛 Bug #3: Off-by-One in Clamp Logic**

**Problem**: Clamp condition uses `>` instead of proper `maxOffset` calculation.

**Fix**: Use explicit `maxOffset` variable:
```go
// ✅ FIXED CODE:
newTotalLines := vm.viewport.TotalLineCount()
maxOffset := newTotalLines - vm.viewport.Height
if maxOffset < 0 {
    maxOffset = 0
}
if vm.viewport.YOffset > maxOffset {
    vm.viewport.YOffset = maxOffset
}
```

**🐛 Bug #4: Race Condition**

**Problem**: `handleWindowSize()` doesn't use mutex, concurrent access to viewport causes data loss.

**Fix**: Use mutex lock in `HandleResize()`:
```go
// ✅ FIXED CODE:
func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg, headerHeight, footerHeight int) {
    vm.mu.Lock()  // ✅ FIX #4: Thread-safety
    defer vm.mu.Unlock()
    // ... all viewport operations ...
}
```

**Implementation**:
```go
package primitives

import (
    "github.com/charmbracelet/bubbles/viewport"
    "github.com/muesli/reflow/wrap"
    "strings"
    "sync"
)

type ViewportManager struct {
    viewport viewport.Model
    logLines []string  // ⭐ Original lines without word-wrap (KEY to reflow!)
    mu       sync.RWMutex  // ✅ Actually USED for thread-safety
}

type ViewportConfig struct {
    MinWidth  int
    MinHeight int
}

func NewViewportManager(cfg ViewportConfig) *ViewportManager {
    return &ViewportManager{
        viewport: viewport.New(0, 0),
        logLines: []string{},
        mu:       sync.RWMutex{},
    }
}

// HandleResize processes window resize events
// ✅ FIXED: All 4 bugs resolved
func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg, headerHeight, footerHeight int) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // 1. Compute dimensions
    vpHeight := msg.Height - headerHeight - footerHeight
    if vpHeight < 1 {  // ✅ FIX #2: Minimum 1, not 0
        vpHeight = 1
    }
    vpWidth := msg.Width
    if vpWidth < 20 {
        vpWidth = 20
    }

    // ✅ FIX #1: Compute wasAtBottom BEFORE changing Height
    totalLinesBefore := vm.viewport.TotalLineCount()
    wasAtBottom := vm.viewport.YOffset + vm.viewport.Height >= totalLinesBefore

    // Update dimensions AFTER wasAtBottom
    vm.viewport.Height = vpHeight
    vm.viewport.Width = vpWidth

    // Reflow content with new word-wrap
    var wrappedLines []string
    for _, line := range vm.logLines {
        wrapped := wrap.String(line, vpWidth)
        wrappedLines = append(wrappedLines, wrapped)
    }
    fullContent := strings.Join(wrappedLines, "\n")
    vm.viewport.SetContent(fullContent)

    // Restore scroll position
    if wasAtBottom {
        vm.viewport.GotoBottom()
    } else {
        // ✅ FIX #3: Proper clamp logic
        newTotalLines := vm.viewport.TotalLineCount()
        maxOffset := newTotalLines - vm.viewport.Height
        if maxOffset < 0 {
            maxOffset = 0
        }
        if vm.viewport.YOffset > maxOffset {
            vm.viewport.YOffset = maxOffset
        }
    }
}

// Append adds content with smart scroll
// ✅ FIXED: Thread-safe, preserves position
func (vm *ViewportManager) Append(content string, preservePosition bool) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Store original line
    vm.logLines = append(vm.logLines, content)

    // Reflow all content
    var wrappedLines []string
    for _, line := range vm.logLines {
        wrapped := wrap.String(line, vm.viewport.Width)
        wrappedLines = append(wrappedLines, wrapped)
    }
    fullContent := strings.Join(wrappedLines, "\n")

    // Smart scroll
    if preservePosition {
        wasAtBottom := vm.viewport.YOffset + vm.viewport.Height >= vm.viewport.TotalLineCount()
        vm.viewport.SetContent(fullContent)
        if wasAtBottom {
            vm.viewport.GotoBottom()
        }
    } else {
        vm.viewport.SetContent(fullContent)
    }
}

// GetViewport returns the underlying viewport.Model
func (vm *ViewportManager) GetViewport() viewport.Model {
    vm.mu.RLock()
    defer vm.mu.RUnlock()
    return vm.viewport
}

// SetInitialContent sets content on first run
func (vm *ViewportManager) SetInitialContent(content string) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    vm.logLines = []string{content}
    vm.viewport.SetContent(content)
    vm.viewport.YOffset = 0
}
```

**Preservation Guarantees**:
- ✅ **Double storage 1:1**: logLines (original) + viewport (wrapped)
- ✅ **Reflow algorithm 1:1**: wrap.String() for each line on resize
- ✅ **Smart scroll 1:1**: wasAtBottom computed BEFORE content change
- ✅ **Position preservation 1:1**: GotoBottom() if wasAtBottom, else clamp
- ✅ **Min constraints**: Width min 20, Height min 1 (FIXED from 0)
- ✅ **Thread-safety**: Mutex actually used (FIXED)
- ✅ **Timing order**: wasAtBottom BEFORE Height change (FIXED)
- ✅ **Clamp logic**: Explicit maxOffset variable (FIXED)

**Benefits**:
- ✅ **Bug fixes**: All 4 critical bugs resolved
- ✅ **Reusability**: ViewportManager can be used in any TUI
- ✅ **Testability**: Can test ViewportManager in isolation (~200 lines vs ~600 lines)
- ✅ **~150 lines saved**: Eliminates duplication across 3 TUI implementations
- ✅ **Better encapsulation**: logLines hidden inside ViewportManager

---

### Stage 1.1.2: StatusBarManager Implementation

**Time**: 0.5 day

**Location**: `pkg/tui/primitives/status.go`

**Purpose**: Unified status bar rendering with spinner and dynamic indicators.

**✅ Expected Outcomes**:
- [ ] `pkg/tui/primitives/status.go` created
- [ ] `StatusBarManager` struct with thread-safe operations
- [ ] `Render()` method with exact color preservation
- [ ] `SetProcessing()` for spinner state
- [ ] `SetDebugMode()` for DEBUG indicator
- [ ] `SetCustomExtra()` for extension point
- [ ] Unit tests for render scenarios (3 test cases)

**🔍 Completion Criteria**:
```bash
# Visual verification
go test ./pkg/tui/primitives/ -run TestStatusBarManager -v

# Colors match exactly:
# Spinner: 86 (cyan) when processing, 242 (gray) when idle
# Background: 235 (dark gray)
# DEBUG: 196 (red) background, 15 (white) text, bold
```

**⚠️ Risks**:
- None low (straightforward extraction)

**📦 Dependencies**:
- None (can be developed in parallel with ViewportManager)

**Visual Layout**:
```
┌─────────────────────────────────────────────────────────────────────┐
│  [ Spinner Part ]           [ Extra Part ]                          │
│  ┌──────────────────┐        ┌──────────────────────────────────┐  │
│  │ ⟋ Processing     │   OR   │ ✓ Ready                           │  │
│  │ Фон: 235 (серый) │        │ Фон: 235 (серый)                  │  │
│  │ Цвет: 86 (cyan)  │        │ Цвет: 242 (серый)                 │  │
│  └──────────────────┘        └──────────────────────────────────┘  │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────┐     │
│  │  DEBUG  │  │ Todo: 3/12                                    │     │
│  │  Фон: 196 (красный) │ Фон: 235 (серый)                     │     │
│  │  Цвет: 15 (белый)  │ Цвет: 252 (серый)                     │     │
│  │  Bold: true        │                                         │     │
│  └────────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
```

**Implementation**:
```go
package primitives

import (
    "github.com/charmbracelet/bubbles/spinner"
    "github.com/charmbracelet/lipgloss"
    "sync"
)

type StatusBarManager struct {
    spinner      spinner.Model
    isProcessing bool
    debugMode    bool
    mu           sync.RWMutex

    // Configuration (exact copy of current colors)
    cfg StatusBarConfig

    // Extension point (exact copy of customStatusExtra)
    customExtra func() string
}

type StatusBarConfig struct {
    // Spinner colors
    SpinnerColor    lipgloss.Color // 86 (cyan) when processing
    IdleColor       lipgloss.Color // 242 (gray) when ready

    // Background colors
    BackgroundColor lipgloss.Color // 235 (dark gray)
    DebugColor      lipgloss.Color // 196 (red)

    // Text colors
    DebugText       lipgloss.Color // 15 (white)
    ExtraText       lipgloss.Color // 252 (gray)
}

func DefaultStatusBarConfig() StatusBarConfig {
    return StatusBarConfig{
        SpinnerColor:    lipgloss.Color("86"),
        IdleColor:       lipgloss.Color("242"),
        BackgroundColor: lipgloss.Color("235"),
        DebugColor:      lipgloss.Color("196"),
        DebugText:       lipgloss.Color("15"),
        ExtraText:       lipgloss.Color("252"),
    }
}

func NewStatusBarManager(cfg StatusBarConfig) *StatusBarManager {
    s := spinner.New()
    s.Spinner = spinner.Dot
    s.Style = lipgloss.NewStyle().Foreground(cfg.SpinnerColor)

    return &StatusBarManager{
        spinner:      s,
        isProcessing: false,
        mu:           sync.RWMutex{},
        cfg:          cfg,
    }
}

// Render returns the status bar as a styled string
func (sm *StatusBarManager) Render() string {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    // Spinner part
    var spinnerText string
    if sm.isProcessing {
        spinnerText = sm.spinner.View()
    } else {
        spinnerText = "✓ Ready"
    }

    spinnerPart := lipgloss.NewStyle().
        Background(sm.cfg.BackgroundColor).
        Padding(0, 1).
        Foreground(func() lipgloss.Color {
            if sm.isProcessing {
                return sm.cfg.SpinnerColor
            }
            return sm.cfg.IdleColor
        }()).
        Render(spinnerText)

    // DEBUG indicator (red background)
    var extraPart string
    if sm.debugMode {
        extraPart = lipgloss.NewStyle().
            Background(sm.cfg.DebugColor).
            Foreground(sm.cfg.DebugText).
            Bold(true).
            Padding(0, 1).
            Render(" DEBUG")
    }

    // Custom extra info (e.g., "Todo: 3/12")
    if sm.customExtra != nil {
        extraInfo := sm.customExtra()
        if extraInfo != "" {
            extraPart += lipgloss.NewStyle().
                Background(sm.cfg.BackgroundColor).
                Padding(0, 1).
                Foreground(sm.cfg.ExtraText).
                Render(extraInfo)
        }
    }

    return spinnerPart + extraPart
}

// SetProcessing sets the processing state
func (sm *StatusBarManager) SetProcessing(processing bool) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.isProcessing = processing
}

// SetDebugMode toggles DEBUG indicator
func (sm *StatusBarManager) SetDebugMode(enabled bool) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.debugMode = enabled
}

// SetCustomExtra sets the callback for custom status extra info
func (sm *StatusBarManager) SetCustomExtra(fn func() string) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.customExtra = fn
}
```

**Preservation Guarantees**:
- ✅ **Visual appearance 1:1**: Same colors, padding, layout
- ✅ **Behavior 1:1**: Thread-safe, dynamic updates via callback
- ✅ **Extension point 1:1**: `customStatusExtra` callback preserved
- ✅ **DEBUG indicator 1:1**: Red background (196), white text (15), bold

**Eliminates**: ~100 lines of duplication
**Benefits**: Configurable colors, unit-testable, reusable across all TUIs

---

## Phase 1B: Events & Interruption Primitives (Days 4-5)

**🎯 Phase Goal**: Create event handling and interruption primitives

### Stage 1.3.1: EventHandler Implementation

**Time**: 0.5 day

**Location**: `pkg/tui/primitives/events.go`

**Purpose**: Unified event handling with pluggable renderers.

**✅ Expected Outcomes**:
- [ ] `pkg/tui/primitives/events.go` created
- [ ] `EventHandler` struct with pluggable renderers
- [ ] `RegisterRenderer()` for custom event rendering
- [ ] `HandleEvent()` with status bar integration
- [ ] Pre-configured renderers for all event types
- [ ] Unit tests for event flow (4 test cases)

**🔍 Completion Criteria**:
```bash
go test ./pkg/tui/primitives/ -run TestEventHandler -v

# Event flow verified:
# Event → EventHandler → Renderer → ViewportManager.Append()
```

**⚠️ Risks**:
- None low (well-defined pattern)

**📦 Dependencies**:
- ViewportManager (for appending content)
- StatusBarManager (for status updates)

**Implementation**:
```go
package primitives

import (
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

type EventHandler struct {
    subscriber   events.Subscriber
    viewportMgr  *ViewportManager
    statusMgr    *StatusBarManager
    renderers    map[events.EventType]EventRenderer
    mu           sync.RWMutex
}

// EventRenderer is a function that renders an event to a string with style
type EventRenderer func(event events.Event) (content string, style lipgloss.Style)

func NewEventHandler(sub events.Subscriber, vm *ViewportManager, sm *StatusBarManager) *EventHandler {
    return &EventHandler{
        subscriber:  sub,
        viewportMgr: vm,
        statusMgr:   sm,
        renderers:   make(map[events.EventType]EventRenderer),
        mu:          sync.RWMutex{},
    }
}

// RegisterRenderer registers a renderer for a specific event type
func (eh *EventHandler) RegisterRenderer(eventType events.EventType, renderer EventRenderer) {
    eh.mu.Lock()
    defer eh.mu.Unlock()
    eh.renderers[eventType] = renderer
}

// HandleEvent processes an event and returns a Bubble Tea Cmd
func (eh *EventHandler) HandleEvent(event events.Event) tea.Cmd {
    // Update status bar based on event type
    switch event.Type {
    case events.EventThinking:
        eh.statusMgr.SetProcessing(true)
    case events.EventDone, events.EventError:
        eh.statusMgr.SetProcessing(false)
    }

    // Render event if renderer is registered
    if renderer, ok := eh.renderers[event.Type]; ok {
        content, style := renderer(event)
        styledContent := style.Render(content)
        eh.viewportMgr.Append(styledContent, true)
    }

    // Continue waiting for events
    return tui.WaitForEvent(eh.subscriber, func(e events.Event) tea.Msg {
        return tui.EventMsg(e)
    })
}
```

**Eliminates**: ~180 lines of duplication
**Benefits**: Pluggable renderers, consistent event handling across TUIs

---

### Stage 1.4.1: InterruptionManager Implementation

**Time**: 1 day

**Location**: `pkg/tui/primitives/interruption.go`

**Purpose**: Unified interruption handling with user input capture.

**Current Implementation** (`pkg/tui/model.go:851-1210`):
- **InterruptionModel**: ~300 lines handling interruptions
- **handleKeyPressWithInterruption()**: Enter key handling (~80 lines)
- **handleAgentEventWithInterruption()**: EventUserInterruption processing (~90 lines)
- **Callback pattern**: `SetOnInput()` for business logic injection (Rule 6 compliant)

**✅ Expected Outcomes**:
- [ ] `pkg/tui/primitives/interruption.go` created
- [ ] `InterruptionManager` struct with callback pattern
- [ ] `SetOnInput()` for business logic injection
- [ ] `HandleInput()` with dual-mode logic
- [ ] `GetChannel()` for inter-goroutine communication
- [ ] `HandleEvent()` for EventUserInterruption
- [ ] Unit tests for interruption flow (5 test cases)

**🔍 Completion Criteria**:
```bash
go test ./pkg/tui/primitives/ -run TestInterruptionManager -v

# Interruption flow verified:
# User → Enter → HandleInput() → inputChan → ReActExecutor → Event → UI
```

**⚠️ Risks**:
- Medium: Interruption mechanism is complex
- Mitigation: Reuse current `cmd/interruption-test` as reference

**📦 Dependencies**:
- None (independent primitive)

**Current Flow**:
```
┌─────────────────────────────────────────────────────────────────────────────┐
│           INTERRUPTION FLOW (current - works perfectly!)                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. User enters text and presses Enter                                      │
│     └─► InterruptionModel.Update() catches tea.KeyMsg                       │
│         └─► handleKeyPressWithInterruption()                                │
│             ├─► If agent NOT executing:                                    │
│             │   └─► onInput callback (createAgentLauncher)                 │
│             │       └─► client.Execute(ctx, ChainInput{UserInputChan})     │
│             │           └─► Agent starts                                   │
│             │                                                               │
│             └─► If agent IS executing:                                     │
│                 └─► inputChan <- userInput                                 │
│                     └─► ReActExecutor checks channel between iterations     │
│                         └─► INTERRUPTION HANDLING:                          │
│                             ├─► Loads interruption_handler.yaml             │
│                             ├─► Appends message to history                  │
│                             ├─► Sends EventUserInterruption                 │
│                             └─► Continues loop with new prompt             │
│                                                                              │
│  2. UI receives event                                                        │
│     └─► InterruptionModel.handleAgentEventWithInterruption()               │
│         └─► EventUserInterruption                                          │
│             └─► "⏸️ Interruption (iteration N): message"                  │
│                                                                              │
│  3. User sees result in viewport                                            │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Implementation**:
```go
package primitives

import (
    "github.com/ilkoid/poncho-ai/pkg/events"
    "fmt"
    tea "github.com/charmbracelet/bubbletea"
    "sync"
)

type InterruptionManager struct {
    inputChan  chan string
    onInput    func(query string) tea.Cmd  // MANDATORY: Business logic injection
    mu         sync.RWMutex

    // Configuration
    bufferSize int
}

func NewInterruptionManager(bufferSize int) *InterruptionManager {
    return &InterruptionManager{
        inputChan: make(chan string, bufferSize),
        bufferSize: bufferSize,
        mu:         sync.RWMutex{},
    }
}

// SetOnInput устанавливает callback для обработки пользовательского ввода (MANDATORY)
// Это business logic injection point - UI вызывает, cmd/ реализует
func (im *InterruptionManager) SetOnInput(handler func(query string) tea.Cmd) {
    im.mu.Lock()
    defer im.mu.Unlock()
    im.onInput = handler
}

// HandleInput обрабатывает пользовательский ввод из textarea
// Возвращает:
//   - cmd: Bubble Tea команда для выполнения
//   - shouldSendToChannel: true если нужно отправить в inputChan (прерывание)
//   - err: ошибка если callback не установлен
func (im *InterruptionManager) HandleInput(input string, isProcessing bool) (cmd tea.Cmd, shouldSendToChannel bool, err error) {
    im.mu.RLock()
    handler := im.onInput
    im.mu.RUnlock()

    if handler == nil {
        return nil, false, fmt.Errorf("no input handler set. Call SetOnInput() first")
    }

    if isProcessing {
        // Send interruption to channel
        return handler(input), true, nil
    }

    return handler(input), false, nil
}

// GetChannel возвращает inputChan для передачи в agent.Execute()
// Это канал для межгорутинной коммуникации между UI и ReAct executor
func (im *InterruptionManager) GetChannel() chan string {
    return im.inputChan
}

// HandleEvent обрабатывает EventUserInterruption и возвращает текст для UI
// Extracted from handleAgentEventWithInterruption (line 1056-1064)
func (im *InterruptionManager) HandleEvent(event events.Event) (shouldDisplay bool, displayText string) {
    if event.Type == events.EventUserInterruption {
        if data, ok := event.Data.(events.UserInterruptionData); ok {
            return true, fmt.Sprintf("⏸️ Interruption (iteration %d): %s",
                data.Iteration, truncate(data.Message, 60))
        }
    }
    return false, ""
}
```

**Preservation Guarantees**:
- ✅ **Flow 1:1**: User → Enter → inputChan → ReActExecutor → EventUserInterruption → UI
- ✅ **Callback pattern 1:1**: `SetOnInput(createAgentLauncher(...))`
- ✅ **Channel 1:1**: `inputChan chan string` for inter-goroutine communication
- ✅ **Event handling 1:1**: "⏸️ Interruption (iteration N): message"
- ✅ **ReAct executor 1:1**: Checks inputChan between iterations
- ✅ **UI text 1:1**: `> User input` display

**Eliminates**: ~170 lines of duplication
**Benefits**: Reusable interruption handling, unit-testable, clean architecture, no agent dependency in pkg/tui

---

## Phase 1C: Debug Primitive (Day 6)

**🎯 Phase Goal**: Create debug functionality primitive

### Stage 1.5.1: DebugManager Implementation

**Time**: 1 day

**Location**: `pkg/tui/primitives/debug.go`

**Purpose**: Unified debug functionality with JSON-logging, screen saving, and debug mode toggle.

**✅ Expected Outcomes**:
- [ ] `pkg/tui/primitives/debug.go` created
- [ ] `DebugManager` struct with thread-safe operations
- [ ] `ToggleDebug()` with status bar integration
- [ ] `SaveScreen()` with ANSI stripping
- [ ] `SetLastLogPath()` / `GetLastLogPath()`
- [ ] `ShouldLogEvent()` / `FormatEvent()` for DEBUG messages
- [ ] Unit tests for debug flow (4 test cases)

**🔍 Completion Criteria**:
```bash
go test ./pkg/tui/primitives/ -run TestDebugManager -v

# Key bindings verified:
# Ctrl+S → Save to poncho_log_*.md
# Ctrl+G → Toggle DEBUG mode
# Ctrl+L → Show debug log path
```

**⚠️ Risks**:
- None low (straightforward extraction)

**📦 Dependencies**:
- ViewportManager (for getting content)
- StatusBarManager (for DEBUG indicator)

**Key Bindings**:
- **Ctrl+S** (`SaveToFile`): Save screen content to markdown file
- **Ctrl+G** (`ToggleDebug`): Toggle debug mode (show DEBUG indicator in status bar)
- **Ctrl+L** (`ShowDebugPath`): Show path to last JSON debug log file

**Features**:
1. **Screen Save (Ctrl+S)**:
   - Saves current viewport content to `poncho_log_YYYYMMDD_HHMMSS.md`
   - Strips ANSI color codes for clean markdown
   - Shows success/error message in viewport

2. **Debug Mode Toggle (Ctrl+G)**:
   - Toggles `debugMode` flag
   - Shows "Debug mode: ON/OFF" message
   - Adds red "DEBUG" indicator in status bar
   - When enabled, shows DEBUG events in viewport:
     - `[DEBUG] Event: EventThinking`
     - `[DEBUG] Event: EventToolCall`
     - `[DEBUG] Tool call: get_wb_parent_categories`

3. **Debug Log Path (Ctrl+L)**:
   - Shows path to last JSON debug log: `📁 Debug log: ./debug_logs/debug_20260118_123456.json`
   - Or: `📁 No debug log available yet`
   - JSON logs contain full execution trace (LLM requests, tool calls, results)

**JSON Debug Logging System** (`pkg/debug/recorder.go`, `pkg/chain/debug.go`):

**Architecture**:
```
ChainDebugRecorder (pkg/chain/debug.go)
    ↓ wraps
debug.Recorder (pkg/debug/recorder.go)
    ↓ records to
JSON files in ./debug_logs/
```

**Features**:
- **Base64 Truncation**: Automatically truncates base64 encoded images (>100 chars) to prevent log bloat
- **Configurable**: `IncludeToolArgs`, `IncludeToolResults`, `MaxResultSize`
- **Thread-safe**: `sync.Mutex` protects all operations
- **Auto-created**: `debug_logs/` directory created if missing
- **Observer Pattern**: ChainDebugRecorder implements `ExecutionObserver` interface

**Implementation**:
```go
package primitives

import (
    "fmt"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/ilkoid/poncho-ai/pkg/events"
)

type DebugManager struct {
    debugMode    bool
    lastLogPath  string
    mu           sync.RWMutex

    // Configuration
    logsDir      string
    saveLogs     bool

    // Dependencies
    viewportMgr  *ViewportManager
    statusMgr    *StatusBarManager

    // Callbacks (injected by cmd/)
    onInput func(query string) (cmd tea.Cmd, shouldSendToChannel bool, err error)
}

type DebugConfig struct {
    LogsDir         string  // "./debug_logs"
    SaveLogs        bool    // true = save JSON logs
    IncludeToolArgs bool
    IncludeToolResults bool
    MaxResultSize   int
}

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

// ToggleDebug switches debug mode on/off
// Returns message to display in viewport
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

// IsEnabled returns current debug mode state
func (dm *DebugManager) IsEnabled() bool {
    dm.mu.RLock()
    defer dm.mu.RUnlock()
    return dm.debugMode
}

// ShouldLogEvent returns true if event should be logged in DEBUG mode
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

// FormatEvent formats event for DEBUG display
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

// SetLastLogPath stores the path to the last JSON debug log
func (dm *DebugManager) SetLastLogPath(path string) {
    dm.mu.Lock()
    defer dm.mu.Unlock()
    dm.lastLogPath = path

    // Notify status manager to update
    dm.statusMgr.SetLastLogPath(path)
}

// GetLastLogPath returns the path to the last JSON debug log
func (dm *DebugManager) GetLastLogPath() string {
    dm.mu.RLock()
    defer dm.mu.RUnlock()
    return dm.lastLogPath
}

// SaveScreen saves current viewport content to markdown file
// Returns filename or error
func (dm *DebugManager) SaveScreen() (string, error) {
    // Get content from viewport manager
    content := dm.viewportMgr.GetContent()

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

// stripANSICodes removes ANSI escape sequences from string
func stripANSICodes(s string) string {
    result := strings.Builder{}
    i := 0
    for i < len(s) {
        if s[i] == 0x1B { // ESC character
            i++ // Skip ESC
            for i < len(s) && (s[i] < '@' || s[i] > '~') {
                i++
            }
            if i < len(s) {
                i++ // Skip final char
            }
        } else {
            result.WriteByte(s[i])
            i++
        }
    }
    return result.String()
}

// SetOnInput sets callback for processing user input (MANDATORY)
// This is the main integration point for interruptions
func (dm *DebugManager) SetOnInput(handler func(query string) (tea.Cmd, bool, error)) {
    dm.mu.Lock()
    defer dm.mu.Unlock()
    dm.onInput = handler
}
```

**Preservation Guarantees**:
- ✅ **Ctrl+S behavior 1:1**: Save to `poncho_log_YYYYMMDD_HHMMSS.md` with ANSI stripping
- ✅ **Ctrl+G behavior 1:1**: Toggle debug mode, show "Debug mode: ON/OFF"
- ✅ **Ctrl+L behavior 1:1**: Show `📁 Debug log: ./debug_logs/debug_*.json`
- ✅ **DEBUG indicator 1:1**: Red background (196), white text (15), bold
- ✅ **DEBUG messages 1:1**: `[DEBUG] Event: EventThinking`, `[DEBUG] Tool call: ...`
- ✅ **JSON logging 1:1**: ChainDebugRecorder records to `./debug_logs/debug_*.json`
- ✅ **Base64 truncation 1:1**: Automatic in debug.Recorder (100 char limit)
- ✅ **Thread-safety 1:1**: Mutex protects all operations

**Eliminates**: ~100 lines of duplication (after accounting for shared code)
**Benefits**: Unified debug functionality, reusable across all TUIs, cleaner separation of concerns, easier to test

---

## Phase 2: BaseModel Creation (Days 7-8)

**🎯 Phase Goal**: Create Port & Adapter compliant base model

### Stage 2.1: BaseModel Structure

**Time**: 0.5 day

**Location**: `pkg/tui/base.go`

**✅ Expected Outcomes**:
- [ ] `pkg/tui/base.go` created
- [ ] `BaseModel` struct with all primitives
- [ ] `NewBaseModel()` factory function
- [ ] `Init()` method with Bubble Tea commands
- [ ] No `pkg/agent` imports (Port & Adapter compliant)

**🔍 Completion Criteria**:
```bash
# Verify no agent imports
grep -r "pkg/agent" pkg/tui/base.go
# Should return nothing

# Basic functionality
cd cmd/simple-agent && go run main.go "test"
# TUI starts without errors
```

**⚠️ Risks**:
- Medium: Integrating all primitives
- Mitigation: Use dependency injection pattern

**📦 Dependencies**:
- All Phase 1 primitives (ViewportManager, StatusBarManager, EventHandler, InterruptionManager, DebugManager)

**Implementation**:
```go
package tui

import (
    "context"
    "github.com/charmbracelet/bubbles/textarea"
    tea "github.com/charmbracelet/bubbletea"

    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui/primitives"
)

type BaseModel struct {
    // Primitives
    viewportMgr *primitives.ViewportManager
    statusMgr   *primitives.StatusBarManager
    eventHdlr   *primitives.EventHandler
    debugMgr     *primitives.DebugManager

    // Dependencies (Port interface only)
    eventSub events.Subscriber

    // Context (Rule 11)
    ctx context.Context

    // Configuration
    title   string
    ready   bool
    showHelp bool

    // UI Components
    textarea textarea.Model

    // Key bindings
    keys KeyMap
}

func NewBaseModel(ctx context.Context, eventSub events.Subscriber) *BaseModel {
    // Create primitives
    vm := primitives.NewViewportManager(primitives.ViewportConfig{
        MinWidth:  20,
        MinHeight: 1,
    })

    sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

    eh := primitives.NewEventHandler(eventSub, vm, sm)

    dm := primitives.NewDebugManager(primitives.DebugConfig{
        LogsDir:         "./debug_logs",
        SaveLogs:        true,
        IncludeToolArgs: true,
        IncludeToolResults: true,
        MaxResultSize:   10000,
    }, vm, sm)

    // Create textarea
    ta := textarea.New()
    ta.FocusMode = textarea.Focused
    ta.Placeholder = "Enter your query..."
    ta.Cursor.LineStyle = "┈"
    ta.Cursor.Mode = textarea.CursorSend
    ta.CharLimit = 0 // No limit

    // Create help model
    h := help.New()
    h.ShowAll = false

    // Create keymap
    keys := DefaultKeyMap()

    return &BaseModel{
        viewportMgr: vm,
        statusMgr:   sm,
        eventHdlr:   eh,
        debugMgr:     dm,
        eventSub:     eventSub,
        ctx:          ctx,
        title:        "AI Agent",
        ready:        false,
        showHelp:      false,
        textarea:      ta,
        keys:         keys,
    }
}

func (m *BaseModel) Init() tea.Cmd {
    return tea.Batch(
        m.textarea.Focus(),
        m.eventHdlr.InitRenderers(),
        tui.ReceiveEventCmd(m.eventSub, func(e events.Event) tea.Msg {
            return tui.EventMsg(e)
        }),
    )
}

func (m *BaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        return m.handleWindowSize(msg)

    case tea.KeyMsg:
        return m.handleKeyPress(msg)

    case tui.EventMsg:
        event := events.Event(msg)
        return m, m.eventHdlr.HandleEvent(event)

    default:
        var cmd tea.Cmd
        m.textarea, cmd = m.textarea.Update(msg)
        return m, cmd
    }
}

func (m *BaseModel) View() string {
    if !m.ready {
        return "Initializing..."
    }

    // Title
    title := m.title
    if m.showHelp {
        title = fmt.Sprintf("%s %s", title, m.keys.Help.String())
    }

    // Viewport
    viewport := m.viewportMgr.GetViewport()

    // Divider
    divider := "│\n"

    // Help
    var help string
    if m.showHelp {
        help = m.keys.Help(m.keys)
    }

    // Status bar
    status := m.statusMgr.Render()

    // Textarea
    input := m.textarea.View()

    return fmt.Sprintf("%s\n%s\n%s\n%s", title, viewport, divider, help, status, input)
}
```

---

### Stage 2.2: BaseModel Update & View Methods

**Time**: 1 day

**✅ Expected Outcomes**:
- [ ] `Update()` method with all key bindings
- [ ] `View()` method with proper layout
- [ ] Event handling through EventHandler
- [ ] Debug mode integration
- [ ] Help system integration
- [ ] Manual testing of all interactions

**🔍 Completion Criteria**:
```bash
# All key bindings work:
# Ctrl+C → Quit
# Ctrl+H → Toggle help
# Ctrl+↑/↓ → Scroll
# Ctrl+S → Save screen
# Ctrl+G → Toggle debug
# Ctrl+L → Show debug path
```

**⚠️ Risks**:
- Medium: Complex event flow
- Mitigation: Test each binding independently

**📦 Dependencies**:
- Stage 2.1 (BaseModel structure)

**Key Bindings Implementation**:
```go
func (m *BaseModel) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch {
    case key.Matches(msg, m.keys.Quit):
        return m, tea.Quit

    case key.Matches(msg, m.keys.ToggleHelp):
        m.showHelp = !m.showHelp
        m.keys.Help.ShowAll = m.showHelp
        return m, nil

    case key.Matches(msg, m.keys.ScrollUp):
        m.viewportMgr.GetViewport().ScrollUp(1)
        return m, nil

    case key.Matches(msg, m.keys.ScrollDown):
        m.viewportMgr.GetViewport().ScrollDown(1)
        return m, nil

    case key.Matches(msg, m.keys.SaveToFile):
        filename, err := m.debugMgr.SaveScreen()
        if err != nil {
            m.viewportMgr.Append(errorStyle(fmt.Sprintf("❌ Save failed: %v", err)), true)
        } else {
            m.viewportMgr.Append(systemStyle(fmt.Sprintf("✅ Saved: %s", filename)), true)
        }
        return m, nil

    case key.Matches(msg, m.keys.ToggleDebug):
        msg := m.debugMgr.ToggleDebug()
        m.viewportMgr.Append(systemStyle(msg), true)
        return m, nil

    case key.Matches(msg, keys.ShowDebugPath):
        path := m.debugMgr.GetLastLogPath()
        if path != "" {
            m.viewportMgr.Append(systemStyle(fmt.Sprintf("📁 Debug log: %s", path)), true)
        } else {
            m.viewportMgr.Append(systemStyle("📁 No debug log available yet"), true)
        }
        return m, nil

    default:
        var cmd tea.Cmd
        m.textarea, cmd = m.textarea.Update(msg)
        return m, cmd
    }
}
```

---

## Phase 3A: Model Refactoring (Day 9)

**🎯 Phase Goal**: Refactor existing Model to use BaseModel

### Stage 3.1.1: Model Refactoring

**Time**: 0.5 day

**Location**: `pkg/tui/model.go`

**✅ Expected Outcomes**:
- [ ] `pkg/tui/model.go` refactored to embed BaseModel
- [ ] Remove duplicated code (~150 lines)
- [ ] Preserve all existing functionality
- [ ] Update tests to use new structure

**🔍 Completion Criteria**:
```bash
# Verify functionality preserved
cd cmd/todo-agent && go run main.go
# All features work: todo operations, debug, etc.
```

**⚠️ Risks**:
- Low: Straightforward extraction

**📦 Dependencies**:
- Phase 2 (BaseModel)

**Implementation**:
```go
// pkg/tui/model.go - REFACTORED

type Model struct {
    base *BaseModel  // Embed BaseModel

    // Additional fields for backward compatibility (deprecated)
    agent     agent.Agent
    coreState *state.CoreState
}

func NewModel(ctx context.Context, agent agent.Agent, coreState *state.CoreState, eventSub events.Subscriber) *Model {
    base := NewBaseModel(ctx, eventSub)

    // Configure event handlers with legacy renderers
    base.eventHdlr.RegisterRenderer(events.EventThinking, m.renderThinking)
    base.eventHdlr.RegisterRenderer(events.EventMessage, m.renderMessage)
    // ... other renderers ...

    return &Model{
        base:       base,
        agent:      agent,
        coreState:  coreState,
    }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // Delegate to base model first
    newBase, baseCmd := m.base.Update(msg)
    m.base = newBase.(*BaseModel)

    // Then handle Model-specific logic
    switch msg.(type) {
    case tea.KeyMsg:
        // Model-specific key handling
        return m, m.handleKeyPress(msg)

    case tui.EventMsg:
        // Model-specific event handling
        return m, m.handleAgentEvent(events.Event(msg))
    }

    // Apply base model command
    return m, baseCmd
}
```

---

### Stage 3.2.1: SimpleTui Refactoring

**Time**: 0.5 day

**Location**: `pkg/tui/simple.go`

**✅ Expected Outcomes**:
- [ ] `pkg/tui/simple.go` refactored to use BaseModel
- [ ] Remove duplicated code (~100 lines)
- [ ] Preserve all existing functionality

**🔍 Completion Criteria**:
```bash
# Test SimpleTui still works
cd cmd/simple-agent && go run main.go "show categories"
```

**⚠️ Risks**:
- None low

**📦 Dependencies**:
- Phase 2 (BaseModel)

---

## Phase 3B: InterruptionModel Refactoring (Day 10 Morning)

**🎯 Phase Goal**: Refactor InterruptionModel to remove agent dependency

### Stage 3.3.1: InterruptionModel Refactoring

**Time**: 1 day

**Location**: `pkg/tui/interruption.go`

**✅ Expected Outcomes**:
- [ ] `pkg/tui/interruption.go` refactored
- [ ] Remove `*agent.Client` dependency
- [ ] Use InterruptionManager primitive
- [ ] Callback pattern via `SetOnInput()`
- [ ] Interruption flow preserved 1:1

**🔍 Completion Criteria**:
```bash
# Verify interruption flow
cd cmd/interruption-test && go run main.go
# 1. Enter query → agent starts
# 2. Enter interruption → agent receives it
# 3. UI shows "⏸️ Interruption (iteration N): ..."
# 4. Agent continues with new context
```

**⚠️ Risks**:
- **HIGH**: Interruption mechanism is critical
- Mitigation: Comprehensive testing, preserve current flow exactly

**📦 Dependencies**:
- Phase 1B (InterruptionManager)
- Phase 2 (BaseModel)

**Implementation**:
```go
// pkg/tui/interruption.go - REFACTORED

type InterruptionModel struct {
    base         *BaseModel
    interruptMgr *primitives.InterruptionManager
    chainCfg     chain.ChainConfig
}

// NewInterruptionModel - NO AGENT DEPENDENCY
func NewInterruptionModel(
    ctx context.Context,
    coreState *state.CoreState,  // ← Still needed for todo operations
    eventSub events.Subscriber,  // ✅ Only Port interface
    chainCfg chain.ChainConfig,
) *InterruptionModel {
    base := NewBaseModel(ctx, eventSub)
    interruptMgr := primitives.NewInterruptionManager(10)

    // Configure interruption event renderer
    base.eventHdlr.RegisterRenderer(events.EventUserInterruption, func(event events.Event) (string, lipgloss.Style) {
        shouldDisplay, displayText := interruptMgr.HandleEvent(event)
        if shouldDisplay {
            return displayText, systemStyle()
        }
        return "", lipgloss.Style{}
    })

    return &InterruptionModel{
        base:         base,
        interruptMgr: interruptMgr,
        chainCfg:     chainCfg,
    }
}

// SetOnInput устанавливает callback для запуска агента (MANDATORY)
// Delegates to InterruptionManager
func (m *InterruptionModel) SetOnInput(handler func(query string) tea.Cmd) {
    m.interruptMgr.SetOnInput(handler)
}

// handleKeyPressWithInterruption - simplified version
func (m *InterruptionModel) handleKeyPressWithInterruption(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch {
    case key.Matches(msg, m.base.keys.ConfirmInput):
        input := m.base.textarea.Value()
        if input == "" {
            return m, nil
        }

        m.base.textarea.Reset()
        m.base.appendLog(userStyle(fmt.Sprintf("> %s", input)))

        // Delegate to InterruptionManager
        isProcessing := m.base.IsProcessing()
        cmd, shouldSendToChannel, err := m.interruptMgr.HandleInput(input, isProcessing)

        if err != nil {
            m.base.appendLog(errorStyle(fmt.Sprintf("❌ Error: %v", err)))
            return m, nil
        }

        if shouldSendToChannel {
            // Send interruption to channel
            m.interruptMgr.GetChannel() <- input
        }

        return m, cmd
    }
    return m, nil
}
```

**Preservation Guarantees**:
- ✅ **Flow 1:1**: User → Enter → inputChan → ReActExecutor → EventUserInterruption → UI
- ✅ **Callback pattern 1:1**: `SetOnInput(createAgentLauncher(...))`
- ✅ **Channel 1:1**: `inputChan chan string` for inter-goroutine communication
- ✅ **Event handling 1:1**: "⏸️ Interruption (iteration N): message"

---

## Phase 4: Entry Points Update (Day 10 Afternoon)

**🎯 Phase Goal**: Update all cmd/*/main.go to use new API

### Stage 4.1: Critical Entry Points

**Time**: 0.5 day

**Files to Update**:
1. **cmd/interruption-test/main.go** - Updated as shown below
2. **cmd/poncho/main.go** - Use InterruptionModel with new API
3. **cmd/todo-agent/main.go** - Use SimpleTui with BaseModel
4. **cmd/simple-agent/main.go** - Use SimpleTui directly

**✅ Expected Outcomes**:
- [ ] `cmd/interruption-test/main.go` updated
- [ ] `cmd/poncho/main.go` updated
- [ ] `cmd/todo-agent/main.go` updated
- [ ] New API usage documented

**🔍 Completion Criteria**:
```bash
# All entry points work
for cmd in interruption-test poncho todo-agent; do
  cd cmd/$cmd && go run main.go
done
```

**⚠️ Risks**:
- Medium: API changes
- Mitigation: Update documentation, provide migration examples

**📦 Dependencies**:
- Phase 3 (all refactored models)

**New API Usage** (`cmd/interruption-test/main.go`):
```go
// OLD API
model := tui.NewInterruptionModel(
    ctx, client, coreState, sub, inputChan, chainCfg
)

// NEW API
model := tui.NewInterruptionModel(ctx, coreState, sub, chainCfg)
model.SetInterruptionChannel(inputChan)
model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))
```

---

### Stage 4.2: Remaining Entry Points

**Time**: 0.5 day

**✅ Expected Outcomes**:
- [ ] `cmd/simple-agent/main.go` updated
- [ ] `cmd/streaming-test/main.go` updated
- [ ] Any other custom cmd/ updated

**🔍 Completion Criteria**:
```bash
# All cmd/ build without errors
go build ./cmd/...
```

**⚠️ Risks**:
- None low

**📦 Dependencies**:
- Stage 4.1 (critical entry points)

---

## Phase 5: Testing & Validation (Day 11-12)

**🎯 Phase Goal**: Comprehensive testing of all functionality

### Stage 5.1: Unit Testing

**Time**: 0.5 day

**✅ Expected Outcomes**:
- [ ] ViewportManager tests (resize, scroll, thread-safety)
- [ ] StatusBarManager tests (render, colors, state)
- [ ] EventHandler tests (event flow, renderers)
- [ ] InterruptionManager tests (callback, channel, events)
- [ ] DebugManager tests (toggle, save, log path)
- [ ] Test coverage > 80%

**🔍 Completion Criteria**:
```bash
go test ./pkg/tui/primitives/... -cover
# Coverage > 80% for all primitives
```

**⚠️ Risks**:
- None low

**📦 Dependencies**:
- All primitives implemented

---

### Stage 5.2: Integration Testing

**Time**: 0.5 day

**✅ Expected Outcomes**:
- [ ] Interruption flow end-to-end test
- [ ] Event stream from agent to TUI
- [ ] Debug log creation and path retrieval
- [ ] Screen save functionality
- [ ] All key bindings

**🔍 Completion Criteria**:
```bash
# Run integration tests
go test ./pkg/tui/... -tags=integration

# Manual verification checklist completed
```

**⚠️ Risks**:
- Medium: Complex interactions
- Mitigation: Test each feature independently

**📦 Dependencies**:
- All phases completed

---

### Stage 5.3: Manual Testing & Verification

**Time**: 0.5 day

**✅ Expected Outcomes**:
- [ ] `cmd/interruption-test` full scenario
- [ ] `cmd/poncho` full functionality
- [ ] `cmd/todo-agent` todo operations
- [ ] Resize behavior in all TUIs
- [ ] Debug mode in all TUIs
- [ ] Screen save in all TUIs

**🔍 Completion Criteria**:
```markdown
## Verification Checklist

### Port & Adapter Compliance
- [ ] pkg/tui does NOT import pkg/agent
- [ ] Only imports pkg/events (Port interface)

### Functionality Preservation
- [ ] Viewport resize preserves scroll position
- [ ] Status bar shows spinner correctly
- [ ] Ctrl+G toggles debug mode
- [ ] Ctrl+S saves to markdown file
- [ ] Ctrl+L shows debug log path
- [ ] Interruptions work: user → inputChan → agent → Event → UI
- [ ] Event stream flows: agent → emitter → subscriber → TUI
- [ ] No regression in existing functionality
```

**⚠️ Risks**:
- **HIGH**: Feature regression
- Mitigation: Comprehensive checklist, side-by-side comparison

**📦 Dependencies**:
- All testing completed

---

## 📚 Related Documents

- [01-CURRENT-STATE.md](./01-CURRENT-STATE.md) - Current architecture analysis
- [02-VIOLATIONS.md](./02-VIOLATIONS.md) - Port & Adapter violations
- [03-RECOMMENDATIONS.md](./03-RECOMMENDATIONS.md) - Detailed recommendations
- [07-ORIGINAL-PLAN.md](./07-ORIGINAL-PLAN.md) - Original Claude plan

---

## 🎯 Success Criteria

1. **Port & Adapter Compliance**: `pkg/tui` only depends on `events.Subscriber` interface
2. **No Duplication**: Viewport, status, event handling, interruption code unified in primitives
3. **Feature Parity**: All existing functionality preserved
4. **Interruptions Work**: User input → agent → EventUserInterruption → UI flow remains intact
5. **Clean API**: Simple, reusable TUI components that are easy to test

---

## 🚀 Quick Start: Next Steps

1. ✅ Review this plan in detail
2. ✅ Approve plan (user will do this)
3. ⏳ Start with Phase 1A: ViewportManager + StatusBarManager
4. ⏳ Continue through all phases
5. ⏸ Verify against checklist after each phase

**Ready to implement?** Start with Phase 1A, Stage 1.1.1 (ViewportManager)!
