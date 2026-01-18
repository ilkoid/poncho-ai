# 08. Option B Implementation Plan

**Goal**: Restore Port & Adapter pattern compliance while preserving all existing functionality including interruptions.

**User Choices**:
- Approach: Option B (Port & Adapter) - Moderate refactoring effort
- Priority: Viewport & Resize, Event Handling, Status Bar & Spinner, Interruptions, Debug (JSON logs, screen save, Ctrl+G/Ctrl+S/Ctrl+L)
- Interruption: Extract to InterruptionManager
- Backward Compatibility: No - can break existing API

---



---
**Overall Timeline**: 2 weeks (10 working days)

### 📊 Phase Summary

| Phase | Duration | Focus | Deliverables | Risk |
|-------|----------|-------|--------------|------|
| **Phase 1A** | 1.5 days | Viewport + StatusBar primitives | ViewportManager, StatusBarManager | Medium (bug fixes) |
| **Phase 1B** | 1.5 days | Events + Interruption primitives | EventHandler, InterruptionManager | Low |
| **Phase 1C** | 1 day | Debug primitive | DebugManager | Low |
| **Phase 2** | 1.5 days | Base model | BaseModel | Medium (dependencies) |
| **Phase 3A** | 1 day | Model refactoring | Refactored Model | Low |
| **Phase 3B** | 1 day | InterruptionModel refactoring | Refactored InterruptionModel | Medium (interruptions) |
| **Phase 4** | 1 day | Entry points | Updated cmd/*/main.go | Low |
| **Phase 5** | 1.5 days | Testing | Unit tests, integration tests, manual | Medium |

**Total**: 10 days

---

## Phase 1A: Viewport & StatusBar Primitives (Days 1-3)

**🎯 Goal**: Create foundational UI primitives with bug fixes

### Stage 1.1.1: ViewportManager Implementation
**Time**: 1 day

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

---

### Stage 1.1.2: StatusBarManager Implementation
**Time**: 0.5 day

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

---

## Phase 1B: Events & Interruption Primitives (Days 4-5)

**🎯 Goal**: Create event handling and interruption primitives

### Stage 1.3.1: EventHandler Implementation
**Time**: 0.5 day

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

---

### Stage 1.4.1: InterruptionManager Implementation
**Time**: 1 day

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

---

## Phase 1C: Debug Primitive (Day 6)

**🎯 Goal**: Create debug functionality primitive

### Stage 1.5.1: DebugManager Implementation
**Time**: 1 day

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

---

## Phase 2: BaseModel Creation (Days 7-8)

**🎯 Goal**: Create Port & Adapter compliant base model

### Stage 2.1: BaseModel Structure
**Time**: 0.5 day

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

---

## Phase 3A: Model Refactoring (Day 9)

**🎯 Goal**: Refactor existing Model to use BaseModel

### Stage 3.1.1: Model Refactoring
**Time**: 0.5 day

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

---

### Stage 3.2.1: SimpleTui Refactoring
**Time**: 0.5 day

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

**🎯 Goal**: Refactor InterruptionModel to remove agent dependency

### Stage 3.3.1: InterruptionModel Refactoring
**Time**: 1 day

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

---

## Phase 4: Entry Points Update (Day 10 Afternoon)

**🎯 Goal**: Update all cmd/*/main.go to use new API

### Stage 4.1: Critical Entry Points
**Time**: 0.5 day

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

**🎯 Goal**: Comprehensive testing of all functionality

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


## Target Architecture

```
pkg/tui/
├── primitives/           # NEW: Reusable low-level components
│   ├── viewport.go      # ViewportManager (resize, smart scroll)
│   ├── status.go        # StatusBarManager (spinner, status line)
│   ├── events.go        # EventHandler (event → UI updates)
│   └── interruption.go  # InterruptionManager (user input handling)
│   ├── debug.go          # DebugManager (JSON logs, screen save, debug mode)
├── base.go              # NEW: BaseModel (no agent dependency)
├── model.go             # REFACTOR: Use BaseModel + primitives
├── interruption.go      # REFACTOR: InterruptionModel using InterruptionManager
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
// pkg/tui/model.go
import "github.com/ilkoid/poncho-ai/pkg/agent"  // ← VIOLATION

type Model struct {
    agent agent.Agent  // ← TIGHT COUPLING
}
```

**After (COMPLIANT)**:
```go
// pkg/tui/base.go
import (
    "github.com/ilkoid/poncho-ai/pkg/events"  // ← ONLY Port interface
    // NO import of pkg/agent
)

type BaseModel struct {
    eventSub events.Subscriber  // ← Port interface only
    // NO direct agent dependency
}
```

---

## Phase 1: Create Primitives (Week 1)

### 1.1 ViewportManager

**Location**: `pkg/tui/primitives/viewport.go`

**Purpose**: Unified viewport resize handling with smart scroll preservation.

**⚠️ CRITICAL BUGS FOUND IN CURRENT IMPLEMENTATION**

After thorough analysis of `pkg/tui/model.go:396-471`, **4 critical bugs** were identified that affect scroll behavior:

| Bug | Severity | Symptom | Impact |
|-----|----------|---------|--------|
| **#1: Timing Attack** | 🔴 High | Scroll position lost during resize | User gets "jumped" to wrong position |
| **#2: Height = 0** | 🔴 High | Scroll breaks in small windows | `ScrollUp/Down` stop working completely |
| **#3: Off-by-One** | 🟡 Medium | Incorrect clamp in edge cases | YOffset can be invalid in rare cases |
| **#4: Race Condition** | 🟡 Medium | Data loss during concurrent access | appendLog can lose data |

---

**BUG #1: Timing Attack - Inconsistent State** (`pkg/tui/model.go:420-451`)

**Problem**: Height is updated BEFORE computing `wasAtBottom`, but `wasAtBottom` uses the NEW Height with OLD content.

```go
// ❌ CURRENT CODE (BUGGY):
m.viewport.Height = vpHeight  // Line 421 - Height changed FIRST!
m.viewport.Width = vpWidth     // Line 422

// ... later ...

// ⚠️ wasAtBottom computed with NEW Height but OLD content!
totalLinesBefore := m.viewport.TotalLineCount()  // Line 442
wasAtBottom := m.viewport.YOffset + m.viewport.Height >= totalLinesBefore  // Line 443

// ... then SetContent() changes TotalLineCount!
m.viewport.SetContent(fullContent)  // Line 451
```

**Mental Experiment**:
```
Initial state:
  YOffset = 50, Height = 20, TotalLineCount = 100
  wasAtBottom = 50 + 20 >= 100 = false ✅

After resize (window expanded):
  YOffset = 50, Height = 30 (NEW!), TotalLineCount = 100 (OLD)
  wasAtBottom = 50 + 30 >= 100 = false ✅

After SetContent() (reflow with narrower width):
  YOffset = 50, Height = 30, TotalLineCount = 150 (NEW!)
  Actual: 50 + 30 >= 150 = false ❌

RESULT: User WAS at bottom with Height=20, but NOT at bottom with Height=30!
Scroll position is lost due to inconsistent state!
```

**FIX**: Compute `wasAtBottom` BEFORE changing Height:
```go
// ✅ FIXED CODE:
// 1. Compute wasAtBottom with OLD state
totalLinesBefore := m.viewport.TotalLineCount()
wasAtBottom := m.viewport.YOffset + m.viewport.Height >= totalLinesBefore

// 2. Update Height AFTER wasAtBottom
m.viewport.Height = vpHeight
m.viewport.Width = vpWidth

// 3. Reflow content
m.viewport.SetContent(fullContent)

// 4. Restore position
if wasAtBottom {
    m.viewport.GotoBottom()
}
```

---

**BUG #2: Height = 0 - Death of Scroll** (`pkg/tui/model.go:406-409`)

**Problem**: If computed `vpHeight < 0`, it's set to `0`, but viewport with `Height = 0` cannot scroll.

```go
// ❌ CURRENT CODE (BUGGY):
vpHeight := msg.Height - headerHeight - helpHeight - footerHeight
if vpHeight < 0 {
    vpHeight = 0  // ⚠️ Height=0 means "no viewport"!
}
```

**Mental Experiment**:
```
Scenario: Very small terminal window
  msg.Height = 10
  headerHeight = 1
  helpHeight = 3 (showHelp = true)
  footerHeight = 3 + 4 = 7

  vpHeight = 10 - 1 - 3 - 7 = -1
  if vpHeight < 0 { vpHeight = 0 }  // ❌ Set to 0!

Result:
  viewport.Height = 0

  shouldGotoBottom():
    return YOffset + 0 >= TotalLineCount()
    return YOffset >= TotalLineCount()

  If YOffset = 50, TotalLineCount = 100:
    return 50 >= 100 = false ❌

PROBLEM: viewport with Height=0 CANNOT scroll!
GotoBottom() doesn't work! ScrollUp/Down don't work!
```

**FIX**: Use minimum height of 1:
```go
// ✅ FIXED CODE:
vpHeight := msg.Height - headerHeight - helpHeight - footerHeight
if vpHeight < 1 {  // ✅ Minimum 1 line for viewport
    vpHeight = 1
}
```

---

**BUG #3: Off-by-One in Clamp Logic** (`pkg/tui/model.go:462-464`)

**Problem**: Clamp condition uses `>` instead of proper `maxOffset` calculation.

```go
// ❌ CURRENT CODE (BUGGY):
if m.viewport.YOffset > newTotalLines-m.viewport.Height {
    m.viewport.YOffset = newTotalLines - m.viewport.Height
}
```

**Mental Experiment**:
```
Scenario: User scrolled near bottom
  newTotalLines = 100
  viewport.Height = 20
  max YOffset = 100 - 20 = 80

User at position: YOffset = 80 (at very bottom)

Check:
  if 80 > 100 - 20 = 80 > 80 = false ❌

Result: Condition not met, YOffset stays 80 ✅

BUT! If YOffset = 81 (invalid state):
  if 81 > 80 = true ✅
  YOffset = 80  // Clamp works

PROBLEM: Condition `>` leaves valid values, but can miss invalid ones
in edge cases where YOffset > max (theoretically possible after reflow).
```

**FIX**: Use explicit `maxOffset` variable:
```go
// ✅ FIXED CODE:
newTotalLines := m.viewport.TotalLineCount()
maxOffset := newTotalLines - m.viewport.Height
if maxOffset < 0 {
    maxOffset = 0
}
if m.viewport.YOffset > maxOffset {
    m.viewport.YOffset = maxOffset
}
```

---

**BUG #4: Race Condition in wasAtBottom** (`pkg/tui/model.go:442-443`)

**Problem**: `handleWindowSize()` doesn't use mutex, concurrent access to viewport causes data loss.

```go
// ❌ CURRENT CODE (NO LOCKING):
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
    // ❌ NO m.mu.Lock()!

    totalLinesBefore := m.viewport.TotalLineCount()
    wasAtBottom := m.viewport.YOffset + m.viewport.Height >= totalLinesBefore

    // ... reflow ...

    m.viewport.SetContent(fullContent)  // ⚠️ Can conflict with appendLog!

    return m, nil
}
```

**Mental Experiment**:
```
Scenario: Concurrent access to viewport

Thread 1 (handleWindowSize):
  totalLinesBefore = m.viewport.TotalLineCount()  // = 100
  // ← CONTEXT SWITCH!

Thread 2 (appendLog):
  m.viewport.SetContent(newContent)  // TotalLineCount = 110

Thread 1 (continues):
  wasAtBottom = YOffset + Height >= totalLinesBefore
            = 80 + 20 >= 100 = true ✅

  // But TotalLineCount is ALREADY 110!
  // wasAtBottom should be false!

  m.viewport.SetContent(fullContent)  // Overwrites Thread 2!

RESULT: Data loss from Thread 2! User loses messages!
```

**FIX**: Use mutex lock in `handleWindowSize()`:
```go
// ✅ FIXED CODE:
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
    m.mu.Lock()  // ✅ Thread-safety
    defer m.mu.Unlock()

    // ... all viewport operations ...

    return m, nil
}
```

---

**Current Implementation** (`pkg/tui/model.go:396-471`):
- **handleWindowSize()**: ~75 lines handling resize with reflow
- **Double storage**: `logLines` (original) + `viewport` (wrapped)
- **Smart scroll**: `shouldGotoBottom()` checks position before content change
- **Position preservation**: `wasAtBottom` computed before reflow, `GotoBottom()` after
- **Thread-safety**: `mu sync.RWMutex` exists but NOT used in `handleWindowSize()` ❌

**Current Flow**:
```
┌─────────────────────────────────────────────────────────────────────────────┐
│              RESIZE ALGORITHM (current - HAS BUGS!)                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. COMPUTE DIMENSIONS                                                       │
│     vpHeight = msg.Height - headerHeight - helpHeight - footerHeight        │
│     if vpHeight < 0 { vpHeight = 0 }  // ❌ BUG #2: Should be < 1          │
│     vpWidth = msg.Width (min 20)                                            │
│                                                                              │
│  2. UPDATE HEIGHT (❌ TOO EARLY!)                                            │
│     m.viewport.Height = vpHeight  // ❌ BUG #1: Changed before wasAtBottom │
│     m.viewport.Width = vpWidth                                              │
│                                                                              │
│  3. COMPUTE wasAtBottom (❌ WITH NEW HEIGHT!)                               │
│     totalLinesBefore = m.viewport.TotalLineCount()                          │
│     wasAtBottom = m.viewport.YOffset + m.viewport.Height >= totalLinesBefore│
│                                                                              │
│  4. REFLOW CONTENT                                                           │
│     for each line in logLines:                                              │
│         wrapped = wrap.String(line, vpWidth)                                │
│     m.viewport.SetContent(fullContent)  // Changes TotalLineCount!         │
│                                                                              │
│  5. RESTORE POSITION                                                         │
│     if wasAtBottom:                                                          │
│         m.viewport.GotoBottom()                                             │
│     else:                                                                   │
│         if m.viewport.YOffset > newTotalLines-m.viewport.Height:  // ❌ BUG #3│
│             m.viewport.YOffset = newTotalLines - m.viewport.Height          │
│                                                                              │
│  ❌ NO MUTEX: Thread 4 (appendLog) can conflict!  // ❌ BUG #4              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Preserve-by-Design Strategy**:
```go
// ViewportManager: Fixed version with ALL bugs resolved
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
    vm.mu.Lock()  // ✅ FIX #4: Thread-safety
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
    // ⭐ KEY: Use original lines from logLines!
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

// SetInitialContent sets initial content (for first run)
func (vm *ViewportManager) SetInitialContent(content string) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    vm.logLines = []string{content}
    vm.viewport.SetContent(content)
    vm.viewport.YOffset = 0
}
```

**Key Methods**:
```go
// HandleResize() - FIXED version with all 4 bugs resolved
func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg, headerHeight, footerHeight int)

// Append() - Thread-safe append with smart scroll
func (vm *ViewportManager) Append(content string, preservePosition bool)

// GetViewport() - Thread-safe access to underlying viewport
func (vm *ViewportManager) GetViewport() viewport.Model

// SetInitialContent() - Sets content on first run
func (vm *ViewportManager) SetInitialContent(content string)
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

**What Changes vs What Stays The Same**:

| Aspect | Current (Buggy) | After Refactoring (Fixed) | Changes? |
|--------|----------------|---------------------------|----------|
| **Double storage** | logLines (original) + viewport (wrapped) | logLines (original) + viewport (wrapped) | ❌ No |
| **Reflow algorithm** | wrap.String() for each line | wrap.String() for each line | ❌ No |
| **Smart scroll** | wasAtBottom + GotoBottom() | wasAtBottom + GotoBottom() | ❌ No |
| **Timing order** | Height changed BEFORE wasAtBottom ❌ | wasAtBottom BEFORE Height change ✅ | ✅ Yes (FIXED) |
| **Min height** | vpHeight < 0 → vpHeight = 0 ❌ | vpHeight < 1 → vpHeight = 1 ✅ | ✅ Yes (FIXED) |
| **Clamp logic** | Direct comparison ❌ | Explicit maxOffset variable ✅ | ✅ Yes (FIXED) |
| **Thread-safety** | Mutex exists but NOT used ❌ | Mutex actually used ✅ | ✅ Yes (FIXED) |
| **Min width** | 20 characters | 20 characters | ❌ No |

**Benefits**:
- ✅ **Bug fixes**: All 4 critical bugs resolved
- ✅ **Reusability**: ViewportManager can be used in any TUI
- ✅ **Testability**: Can test ViewportManager in isolation (~150 lines vs ~600 lines)
- ✅ **~150 lines saved**: Eliminates duplication across 3 TUI implementations
- ✅ **Better encapsulation**: logLines hidden inside ViewportManager

### 1.2 StatusBarManager

**Location**: `pkg/tui/primitives/status.go`

**Purpose**: Unified status bar rendering with spinner and dynamic indicators.

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

**Current Implementation** (`pkg/tui/model.go:639-699`):
- **Spinner part**: Dynamic spinner (processing) or "✓ Ready" (idle)
  - Background: `235` (dark gray)
  - Foreground: `86` (cyan) when processing, `242` (gray) when idle
  - Padding: `0, 1` (horizontal)
- **DEBUG indicator**: Red background with white text, bold
  - Background: `196` (red)
  - Foreground: `15` (white)
  - Bold: `true`
- **Extra part**: Custom text via callback (e.g., "Todo: 3/12")
  - Background: `235` (dark gray)
  - Foreground: `252` (gray)
- **Thread-safe**: Uses `sync.RWMutex` for concurrent access

**Preserve-by-Design Strategy**:
```go
// StatusBarManager: Exact copy of current logic, extracted to primitive
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
```

**Key Methods**:
```go
// Render() - EXACT copy of renderStatusLine() logic (lines 639-699)
func (sm *StatusBarManager) Render() string

// SetProcessing(processing bool) - Controls spinner state
func (sm *StatusBarManager) SetProcessing(processing bool)

// SetDebugMode(enabled bool) - Toggles DEBUG indicator
func (sm *StatusBarManager) SetDebugMode(enabled bool)

// SetCustomExtra(fn func() string) - Extension point for custom text
func (sm *StatusBarManager) SetCustomExtra(fn func() string)
```

**Integration Examples**:

1. **InterruptionModel** (with todo stats):
```go
base.SetCustomStatusExtra(func() string {
    pending, done, _ := base.coreState.GetTodoStats()
    return fmt.Sprintf("Todo: %d/%d", done, pending+done)
})
```

2. **SimpleTui** (no extra):
```go
// No custom extra → status bar shows only spinner + DEBUG
```

**Preservation Guarantees**:
- ✅ **Visual appearance 1:1**: Same colors, padding, layout
- ✅ **Behavior 1:1**: Thread-safe, dynamic updates via callback
- ✅ **Extension point 1:1**: `customStatusExtra` callback preserved
- ✅ **DEBUG indicator 1:1**: Red background, white text, bold
- ✅ **Spinner animation 1:1**: Same spinner.Dot, same colors

**Eliminates**: ~100 lines of duplication
**Benefits**: Configurable colors, unit-testable, reusable across all TUIs

### 1.3 EventHandler

**Location**: `pkg/tui/primitives/events.go`

**Purpose**: Unified event handling with pluggable renderers.

**Key Methods**:
```go
type EventHandler struct {
    subscriber   events.Subscriber
    viewportMgr  *ViewportManager
    statusMgr    *StatusBarManager
    renderers    map[events.EventType]EventRenderer
    mu           sync.RWMutex
}

type EventRenderer func(event events.Event) (content string, style lipgloss.Style)

func (eh *EventHandler) RegisterRenderer(eventType events.EventType, renderer EventRenderer)
func (eh *EventHandler) HandleEvent(event events.Event) tea.Cmd
```

**Eliminates**: ~180 lines of duplication

### 1.4 InterruptionManager

**Location**: `pkg/tui/primitives/interruption.go`

**Purpose**: Unified interruption handling with user input capture.

**Current Implementation** (`pkg/tui/model.go:851-1210`):
- **InterruptionModel**: ~300 lines handling interruptions
- **handleKeyPressWithInterruption()**: Enter key handling (~80 lines)
- **handleAgentEventWithInterruption()**: EventUserInterruption processing (~90 lines)
- **Callback pattern**: `SetOnInput()` for business logic injection (Rule 6 compliant)

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

**Preserve-by-Design Strategy**:
```go
// InterruptionManager: Exact extraction of interruption logic to reusable primitive
type InterruptionManager struct {
    inputChan  chan string
    onInput    func(query string) tea.Cmd  // MANDATORY: Business logic injection
    mu         sync.RWMutex

    // Configuration
    bufferSize int
}

func NewInterruptionManager(bufferSize int) *InterruptionManager

// SetOnInput устанавливает callback для обработки пользовательского ввода (MANDATORY)
// Это business logic injection point - UI вызывает, cmd/ реализует
func (im *InterruptionManager) SetOnInput(handler func(query string) tea.Cmd)

// HandleInput обрабатывает пользовательский ввод из textarea
// Возвращает:
//   - cmd: Bubble Tea команда для выполнения
//   - shouldSendToChannel: true если нужно отправить в inputChan (прерывание)
//   - err: ошибка если callback не установлен
func (im *InterruptionManager) HandleInput(input string, isProcessing bool) (cmd tea.Cmd, shouldSendToChannel bool, err error)

// GetChannel возвращает inputChan для передачи в agent.Execute()
// Это канал для межгорутинной коммуникации между UI и ReAct executor
func (im *InterruptionManager) GetChannel() chan string

// HandleEvent обрабатывает EventUserInterruption и возвращает текст для UI
// Extracted from handleAgentEventWithInterruption (line 1056-1064)
func (im *InterruptionManager) HandleEvent(event events.Event) (shouldDisplay bool, displayText string)
```

**Integration: InterruptionModel after refactoring**:
```go
// pkg/tui/interruption.go (REFACTORING)

type InterruptionModel struct {
    base         *BaseModel
    interruptMgr *primitives.InterruptionManager  // ✅ Extracted to primitive
    chainCfg     chain.ChainConfig
}

// NewInterruptionModel - NO AGENT DEPENDENCY
func NewInterruptionModel(
    ctx context.Context,
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

**Usage Comparison**:

**Before refactoring** (`cmd/interruption-test/main.go`):
```go
inputChan := make(chan string, 10)
chainCfg := tui.DefaultChainConfig()

baseModel := tui.NewInterruptionModel(
    ctx,
    client,          // ❌ Direct dependency
    coreState,       // ❌ Direct dependency
    sub,
    inputChan,       // ❌ Passing channel
    chainCfg,
)
baseModel.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))
```

**After refactoring** (`cmd/interruption-test/main.go`):
```go
inputChan := make(chan string, 10)
chainCfg := tui.DefaultChainConfig()

// ✅ NO AGENT DEPENDENCY - only Subscriber
baseModel := tui.NewInterruptionModel(
    ctx,
    sub,       // ✅ Only Port interface
    chainCfg,
)

// Set interruption channel
baseModel.SetInterruptionChannel(inputChan)

// Set callback (MANDATORY)
baseModel.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))
```

**Preservation Guarantees**:
- ✅ **Flow 1:1**: User → Enter → inputChan → ReActExecutor → EventUserInterruption → UI
- ✅ **Callback pattern 1:1**: `SetOnInput(createAgentLauncher(...))`
- ✅ **Channel 1:1**: `inputChan chan string` for inter-goroutine communication
- ✅ **Event handling 1:1**: "⏸️ Interruption (iteration N): message"
- ✅ **ReAct executor 1:1**: Checks inputChan between iterations
- ✅ **UI text 1:1**: `> User input` display

**Reusability Examples**:

1. **SimpleTui with interruptions**:
```go
model := tui.NewInterruptionModel(ctx, sub, chainCfg)
inputChan := make(chan string, 10)
model.SetInterruptionChannel(inputChan)
model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, false))
```

2. **Custom TUI with InterruptionManager**:
```go
type CustomModel struct {
    base         *tui.BaseModel
    interruptMgr *primitives.InterruptionManager  // ✅ Reusable!
}

func NewCustomModel(ctx context.Context, eventSub events.Subscriber) *CustomModel {
    base := tui.NewBaseModel(ctx, eventSub)
    interruptMgr := primitives.NewInterruptionManager(10)

    return &CustomModel{
        base:         base,
        interruptMgr: interruptMgr,
    }
}

func (m *CustomModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if key.Matches(msg, m.base.keys.ConfirmInput) {
            input := m.base.textarea.Value()
            cmd, shouldSend, _ := m.interruptMgr.HandleInput(input, m.base.IsProcessing())

            if shouldSend {
                m.interruptMgr.GetChannel() <- input
            }

            return m, cmd
        }
    }
    return m, nil
}
```

**What Changes vs What Stays The Same**:

| Aspect | Current | After Refactoring | Changes? |
|--------|---------|-------------------|----------|
| **Message flow** | User → Enter → inputChan → ReActExecutor → Event → UI | User → Enter → inputChan → ReActExecutor → Event → UI | ❌ No |
| **Callback pattern** | `SetOnInput(createAgentLauncher(...))` | `SetOnInput(createAgentLauncher(...))` | ❌ No |
| **Channel** | `inputChan chan string` | `inputChan chan string` | ❌ No |
| **EventUserInterruption handling** | Display "⏸️ Interruption..." | Display "⏸️ Interruption..." | ❌ No |
| **ReAct executor** | Checks inputChan between iterations | Checks inputChan between iterations | ❌ No |
| **UI text** | `> User input` | `> User input` | ❌ No |
| **Reusability** | Only InterruptionModel | InterruptionManager reusable in any TUI | ✅ Yes |
| **Testability** | Test InterruptionModel (~300 lines) | Test InterruptionManager (~80 lines) | ✅ Yes |
| **Separation of concerns** | InterruptionModel knows about inputChan, chainCfg | InterruptionManager manages channel, InterruptionModel only UI | ✅ Yes |
| **Port & Adapter compliance** | pkg/tui depends on pkg/agent | pkg/tui/primitives depends only on pkg/events | ✅ Yes |
| **Code size** | ~300 lines in InterruptionModel | ~80 lines in InterruptionManager + ~50 in InterruptionModel | ✅ ~170 lines saved |

**Eliminates**: ~170 lines of duplication
**Benefits**: Reusable interruption handling, unit-testable, clean architecture, no agent dependency in pkg/tui

---


### 1.5 DebugManager


**Location**: `pkg/tui/primitives/debug.go`

**Purpose**: Unified debug functionality with JSON-logging, screen saving, and debug mode toggle.

**Current Implementation** (`pkg/tui/model.go:119-130`, `pkg/tui/model.go:493-504`, `pkg/tui/model.go:713-768`):

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

**JSON Log Structure**:
```json
{
  "run_id": "debug_20260118_123456",
  "timestamp": "2026-01-18T12:34:56Z",
  "user_query": "Show categories",
  "iterations": [
    {
      "number": 1,
      "duration_ms": 1234,
      "llm_request": {
        "model": "glm-4.6",
        "temperature": 0.5,
        "max_tokens": 2000,
        "messages_count": 5
      },
      "llm_response": {
        "content": "...",
        "tool_calls": [
          {"name": "get_wb_parent_categories", "arguments": "{...}"}
        ]
      },
      "tool_executions": [
        {
          "name": "get_wb_parent_categories",
          "args": "{...}",
          "result": "{...}",
          "duration_ms": 234,
          "success": true
        }
      ]
    }
  ],
  "visited_tools": ["get_wb_parent_categories", "get_wb_content"],
  "errors": [],
  "total_duration_ms": 5678
}
```

**Features**:
- **Base64 Truncation**: Automatically truncates base64 encoded images (>100 chars) to prevent log bloat
- **Configurable**: `IncludeToolArgs`, `IncludeToolResults`, `MaxResultSize`
- **Thread-safe**: `sync.Mutex` protects all operations
- **Auto-created**: `debug_logs/` directory created if missing
- **Observer Pattern**: ChainDebugRecorder implements `ExecutionObserver` interface

**Preserve-by-Design Strategy**:
```go
// DebugManager: Unified debug functionality for all TUIs
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
    onSaveScreen func() (string, error)
    onGetLogPath func() string
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
    
    // Notify status manager to update (optional: could show "LOG" indicator)
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

// SetOnSaveScreen sets callback for custom screen saving logic
// Allows cmd/ layer to inject business logic (e.g., custom filename)
func (dm *DebugManager) SetOnSaveScreen(fn func() (string, error)) {
    dm.mu.Lock()
    defer dm.mu.Unlock()
    dm.onSaveScreen = fn
}

// SetOnGetLogPath sets callback for getting debug log path
// Allows cmd/ layer to inject business logic (e.g., from ChainOutput)
func (dm *DebugManager) SetOnGetLogPath(fn func() string) {
    dm.mu.Lock()
    defer dm.mu.Unlock()
    dm.onGetLogPath = fn
}
```

**Integration: BaseModel with DebugManager**:
```go
// pkg/tui/base.go (UPDATED)

type BaseModel struct {
    // ... existing fields ...
    
    debugMgr *primitives.DebugManager
}

func NewBaseModel(ctx context.Context, eventSub events.Subscriber) *BaseModel {
    // ... existing initialization ...
    
    // Create DebugManager
    debugCfg := primitives.DebugConfig{
        LogsDir: "./debug_logs",
        SaveLogs: true,
    }
    debugMgr := primitives.NewDebugManager(debugCfg, vm, sm)
    
    return &BaseModel{
        // ... existing fields ...
        debugMgr: debugMgr,
    }
}

func (m *BaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch {
        case key.Matches(msg, m.keys.SaveToFile):
            // Save screen to markdown
            filename, err := m.debugMgr.SaveScreen()
            if err != nil {
                m.viewportMgr.Append(errorStyle(fmt.Sprintf("❌ Save failed: %v", err)), true)
            } else {
                m.viewportMgr.Append(systemStyle(fmt.Sprintf("✅ Saved: %s", filename)), true)
            }
            return m, nil
            
        case key.Matches(msg, m.keys.ToggleDebug):
            // Toggle debug mode
            msg := m.debugMgr.ToggleDebug()
            m.viewportMgr.Append(systemStyle(msg), true)
            return m, nil
            
        case key.Matches(msg, m.keys.ShowDebugPath):
            // Show debug log path
            path := m.debugMgr.GetLastLogPath()
            if path != "" {
                m.viewportMgr.Append(systemStyle(fmt.Sprintf("📁 Debug log: %s", path)), true)
            } else {
                m.viewportMgr.Append(systemStyle("📁 No debug log available yet"), true)
            }
            return m, nil
        }
    case tui.EventMsg:
        event := events.Event(msg)
        
        // DEBUG mode event logging
        if m.debugMgr.ShouldLogEvent(event) {
            debugMsg := m.debugMgr.FormatEvent(event)
            m.viewportMgr.Append(systemStyle(debugMsg), true)
        }
        
        // Regular event handling...
        return m, m.eventHdlr.HandleEvent(event)
    }
    // ...
}
```

**StatusBarManager Integration**:
```go
// pkg/tui/primitives/status.go (UPDATED)

type StatusBarManager struct {
    // ... existing fields ...
    
    debugMode   bool
    lastLogPath string
}

func (sm *StatusBarManager) SetDebugMode(enabled bool) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.debugMode = enabled
}

func (sm *StatusBarManager) SetLastLogPath(path string) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.lastLogPath = path
}

func (sm *StatusBarManager) Render() string {
    sm.mu.RLock()
    defer sm.mu.RUnlock()
    
    // ... existing spinner part ...
    
    // DEBUG indicator (red background)
    var extraPart string
    if sm.debugMode {
        extraPart = lipgloss.NewStyle().
            Background(lipgloss.Color("196")).
            Foreground(lipgloss.Color("15")).
            Bold(true).
            Padding(0, 1).
            Render(" DEBUG")
    }
    
    // Optional: Log path indicator
    if sm.lastLogPath != "" && sm.debugMode {
        extraPart += lipgloss.NewStyle().
            Background(lipgloss.Color("235")).
            Foreground(lipgloss.Color("242")).
            Padding(0, 1).
            Render(" 📁 LOG")
    }
    
    return spinnerPart + extraPart
}
```

**Usage Example** (`cmd/interruption-test/main.go`):
```go
// Create BaseModel with DebugManager
model := tui.NewBaseModel(ctx, sub)

// Configure callbacks for debug log path
model.debugMgr.SetOnGetLogPath(func() string {
    // This will be called from EventDone handler
    // to extract debug path from ChainOutput
    return lastDebugPath  // Stored in cmd/ layer
})

// In event handler:
case events.EventDone:
    if data, ok := event.Data.(events.DoneData); ok {
        if data.DebugPath != "" {
            model.debugMgr.SetLastLogPath(data.DebugPath)
        }
    }
```

**What Changes vs What Stays The Same**:

| Aspect | Current | After Refactoring | Changes? |
|--------|---------|-------------------|----------|
| **Ctrl+S binding** | Saves to `poncho_log_*.md` | Saves to `poncho_log_*.md` | ❌ No |
| **Ctrl+G binding** | Toggles debug mode | Toggles debug mode | ❌ No |
| **Ctrl+L binding** | Shows debug log path | Shows debug log path | ❌ No |
| **DEBUG indicator** | Red "DEBUG" in status bar | Red "DEBUG" in status bar | ❌ No |
| **DEBUG messages** | `[DEBUG] Event: ...` in viewport | `[DEBUG] Event: ...` in viewport | ❌ No |
| **JSON logging** | ChainDebugRecorder saves JSON | ChainDebugRecorder saves JSON | ❌ No |
| **Base64 truncation** | Automatic in debug.Recorder | Automatic in debug.Recorder | ❌ No |
| **ANSI stripping** | stripANSICodes() | stripANSICodes() | ❌ No |
| **Reusability** | Only in Model/InterruptionModel | DebugManager reusable in any TUI | ✅ Yes |
| **Testability** | Test Model (~1200 lines) | Test DebugManager (~200 lines) | ✅ Yes |
| **Separation of concerns** | Debug logic mixed with Model | DebugManager manages all debug logic | ✅ Yes |
| **Code size** | ~60 lines in Model + ~40 in InterruptionModel | ~200 lines in DebugManager (reusable) | ✅ ~100 lines saved (after accounting for duplication)** |

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

**Key Files**:
- [pkg/tui/model.go:119-130](pkg/tui/model.go#L119-L130) - Key bindings (SaveToFile, ToggleDebug, ShowDebugPath)
- [pkg/tui/model.go:493-504](pkg/tui/model.go#L493-L504) - Ctrl+S and Ctrl+G handlers
- [pkg/tui/model.go:713-768](pkg/tui/model.go#L713-L768) - saveToMarkdown() and stripANSICodes()
- [pkg/tui/model.go:1050-1071](pkg/tui/model.go#L1050-L1071) - DEBUG mode event logging
- [pkg/debug/recorder.go](pkg/debug/recorder.go) - JSON debug logging with base64 truncation
- [pkg/chain/debug.go](pkg/chain/debug.go) - ChainDebugRecorder wrapper (ExecutionObserver)
- [pkg/app/components.go:331-343](pkg/app/components.go#L331-L343) - ChainDebugRecorder initialization


---

## Phase 2: Create BaseModel (Week 1)

**Location**: `pkg/tui/base.go`

**Purpose**: Base TUI model without agent dependency (Port & Adapter compliant).

```go
type BaseModel struct {
    // Primitives
    viewportMgr *primitives.ViewportManager
    statusMgr   *primitives.StatusBarManager
    eventHdlr   *primitives.EventHandler

    // Dependencies (Port interface only)
    eventSub events.Subscriber

    // Context (Rule 11)
    ctx context.Context

    // Configuration
    title   string
    ready   bool
    showHelp bool

    // Key bindings
    keys KeyMap
}

func NewBaseModel(ctx context.Context, eventSub events.Subscriber) *BaseModel
func (m *BaseModel) Init() tea.Cmd
func (m *BaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (m *BaseModel) View() string
```

**Benefits**:
- No dependency on pkg/agent
- Pure UI component (Rule 6 compliant)
- Reusable across all applications

---

## Phase 3: Refactor Existing Models (Week 2)

### 3.1 Model Refactoring

**Location**: `pkg/tui/model.go`

```go
type Model struct {
    base *BaseModel  // Embed BaseModel

    // Additional fields for backward compatibility
    // TODO: Deprecate these fields
}

func NewModel(ctx context.Context, agent agent.Agent, coreState *state.CoreState, eventSub events.Subscriber) *Model {
    base := NewBaseModel(ctx, eventSub)

    // Configure event handlers with legacy renderers
    base.eventHdlr.RegisterRenderer(events.EventThinking, m.renderThinking)
    base.eventHdlr.RegisterRenderer(events.EventMessage, m.renderMessage)
    // ...

    return &Model{base: base}
}
```

### 3.2 InterruptionModel Refactoring

**Location**: `pkg/tui/interruption.go`

```go
type InterruptionModel struct {
    base         *BaseModel
    interruptMgr *primitives.InterruptionManager
    chainCfg     chain.ChainConfig
}

func NewInterruptionModel(
    ctx context.Context,
    coreState *state.CoreState,
    eventSub events.Subscriber,
    chainCfg chain.ChainConfig,
) *InterruptionModel {
    base := NewBaseModel(ctx, eventSub)
    interruptMgr := primitives.NewInterruptionManager(10)

    return &InterruptionModel{
        base:         base,
        interruptMgr: interruptMgr,
        chainCfg:     chainCfg,
    }
}

func (m *InterruptionModel) SetOnInput(handler func(query string) tea.Cmd) {
    m.interruptMgr.SetOnInput(handler)
}
```

### 3.3 New API Usage

**Location**: `cmd/interruption-test/main.go`

```go
// OLD API
model := tui.NewInterruptionModel(ctx, client, coreState, sub, inputChan, chainCfg)

// NEW API
model := tui.NewInterruptionModel(ctx, coreState, sub, chainCfg)
model.SetInterruptionChannel(inputChan)
model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))
```

---

## Phase 4: Update All Entry Points (Week 2)

### Files to Update

1. **cmd/interruption-test/main.go** - Updated as shown above
2. **cmd/poncho/main.go** - Use InterruptionModel with new API
3. **cmd/todo-agent/main.go** - Use SimpleTui with BaseModel
4. **cmd/simple-agent/main.go** - Use SimpleTui directly

---

## Phase 5: Testing & Validation (Week 2)

### Verification Checklist

- [ ] Port & Adapter compliance: `pkg/tui` does NOT import `pkg/agent`
- [ ] Viewport resize preserves scroll position
- [ ] Status bar shows spinner correctly
- [ ] Ctrl+G toggles debug mode
- [ ] Ctrl+S saves to markdown file
- [ ] Ctrl+L shows debug log path
- [ ] Interruptions work: user input → inputChan → agent → EventUserInterruption → UI
- [ ] Event stream flows correctly: agent → emitter → subscriber → TUI
- [ ] No regression in existing functionality

---

## Critical Files to Modify

| File | Changes | Priority |
|------|---------|----------|
| `pkg/tui/primitives/viewport.go` | NEW: ViewportManager | High |
| `pkg/tui/primitives/status.go` | NEW: StatusBarManager | High |
| `pkg/tui/primitives/events.go` | NEW: EventHandler | High |
| `pkg/tui/primitives/interruption.go` | NEW: InterruptionManager | High |
| `pkg/tui/base.go` | NEW: BaseModel (no agent dependency) | High |
| `pkg/tui/model.go` | REFACTOR: Use BaseModel + primitives | High |
| `pkg/tui/interruption.go` | REFACTOR: Use InterruptionManager | High |
| `cmd/interruption-test/main.go` | UPDATE: New API usage | High |

---

## Timeline Summary

| Week | Tasks |
|------|-------|
| 1 | Create primitives (ViewportManager, StatusBarManager, EventHandler, InterruptionManager), Create BaseModel |
| 2 | Refactor Model and InterruptionModel, Update all cmd/*/main.go, Testing & validation |

---

**Related Documents**:
- [01-CURRENT-STATE.md](./01-CURRENT-STATE.md) - Current architecture analysis
- [02-VIOLATIONS.md](./02-VIOLATIONS.md) - Port & Adapter violations
- [06-RECOMMENDATIONS.md](./06-RECOMMENDATIONS.md) - Detailed recommendations
- [07-PLAN.md](./07-PLAN.md) - Original Claude plan
