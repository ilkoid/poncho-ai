# Phase 3 Implementation Checklist

**Generated:** 2026-01-18
**Plan:** Option B (Primitives-Based Approach)
**Status:** Phase 2 Complete, Phase 3 Ready to Start
**Prerequisites:** Phases 1A, 1B, 1C, 2 Complete (70 tests passing)

---

## 📋 Phase 3 Overview

**Goal:** Refactor existing TUI models (`pkg/tui/model.go`, `pkg/tui/simple.go`) to use BaseModel and primitives while preserving all functionality.

**Duration:** 1 day (2 stages × 0.5 day)

**Approach:** Composition over embedding - models will embed BaseModel and add app-specific features.

**Rule 6 Compliance:** ✅ Must maintain - no business logic in pkg/tui
**Rule 11 Compliance:** ✅ Must maintain - context propagation through all layers

---

## 🎯 Stage 3A: Model Refactoring (0.5 day)

**Location:** `pkg/tui/model.go`
**Current State:** ~1300 lines, contains both Model and InterruptionModel
**Target State:** Model embeds BaseModel, Interrupt embeds Model with InterruptionManager

---

### ✅ Pre-Implementation Checklist

Before starting Stage 3A:

- [ ] **Verify Phase 2 tests pass:** `go test ./pkg/tui/... -v` (70 tests)
- [ ] **Verify no import cycles:** `go list -f '{{.ImportPath}} {{.Imports}}' ./pkg/tui/...`
- [ ] **Read current model.go:** Understand existing functionality
- [ ] **Read PRIMITIVES-CHEATSHEET.md:** Understand Phase 2 API
- [ ] **Backup current state:** Create branch `phase-3-model-refactor`

---

### 📝 Step-by-Step Implementation

#### Step 3A.1: Analyze Current Model Structure

**Current Analysis (from model.go:1-100):**

```go
// Current state (lines 1-100)
type Model struct {
    // Agent dependencies (VIOLATES Rule 6)
    agent     agent.Agent
    coreState *state.CoreState

    // UI components
    viewport   viewport.Model
    textarea   textarea.Model
    spinner    spinner.Model
    keys       KeyMap

    // State
    mu                  sync.RWMutex
    isProcessing        bool
    debugMode           bool
    ready               bool
    showHelp            bool
    lastDebugPath       string

    // Events
    eventSub events.Subscriber

    // Configuration
    chainCfg chain.ChainConfig
    ctx      context.Context
}
```

**Action Items:**
- [ ] Identify all fields that map to primitives:
  - [ ] `viewport` → `viewportMgr`
  - [ ] `spinner/isProcessing` → `statusMgr`
  - [ ] `debugMode/lastDebugPath` → `debugMgr`
  - [ ] Event handling → `eventHdlr`
- [ ] Identify unique fields to preserve:
  - [ ] `agent` - Keep for backward compatibility (deprecated)
  - [ ] `coreState` - Keep for todo operations
  - [ ] `chainCfg` - Keep for interruption support
- [ ] Identify methods to delegate to BaseModel:
  - [ ] `handleWindowSize()` → BaseModel.viewportMgr.HandleResize()
  - [ ] `handleAgentEvent()` → BaseModel.eventHdlr.HandleEvent()
  - [ ] `renderStatusLine()` → BaseModel.statusMgr.Render()

---

#### Step 3A.2: Create New Model Structure

**Target Structure:**

```go
// pkg/tui/model.go - REFACTORED

package tui

import (
    "context"
    "fmt"
    "sync"

    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui/primitives"
    // Rule 6: NO imports from pkg/agent, pkg/chain (except for types in deprecated fields)
)

// Model represents the main TUI model with app-specific features.
// Embeds BaseModel for all common functionality.
//
// Rule 6 Compliance: Only reusable code, no business logic.
// Business logic injected via callbacks from cmd/ layer.
type Model struct {
    // Embed BaseModel (composition over inheritance)
    *BaseModel

    // Additional fields for backward compatibility (deprecated)
    // ⚠️ DEPRECATED: Use callbacks instead of direct agent access
    agent     interface{} // agent.Agent - use interface{} to avoid import
    coreState interface{} // *state.CoreState - use interface{} to avoid import

    // App-specific configuration
    chainCfg interface{} // chain.ChainConfig - use interface{} to avoid import

    // Additional UI components (if needed beyond BaseModel)
    mu sync.RWMutex
}
```

**Action Items:**
- [ ] Create new Model struct with BaseModel embedding
- [ ] Use `interface{}` for deprecated fields to avoid imports
- [ ] Add deprecation comments for agent/coreState fields
- [ ] Preserve all public methods for backward compatibility

---

#### Step 3A.3: Refactor NewModel Constructor

**Before:**
```go
// OLD (lines 200-300) - has agent dependency
func NewModel(
    ctx context.Context,
    agent agent.Agent,  // ❌ VIOLATES Rule 6
    coreState *state.CoreState,
    eventSub events.Subscriber,
    chainCfg chain.ChainConfig,
) *Model {
    // 100+ lines of initialization...
}
```

**After:**
```go
// NEW - Rule 6 compliant
func NewModel(
    ctx context.Context,
    eventSub events.Subscriber,
) *Model {
    // Create BaseModel
    base := NewBaseModel(ctx, eventSub)

    return &Model{
        BaseModel: base,
        agent:     nil,  // Deprecated - use callbacks
        coreState: nil,  // Deprecated - use callbacks
        chainCfg:  nil,  // Deprecated - use callbacks
    }
}

// Deprecated constructor for backward compatibility
// ⚠️ DEPRECATED: Use NewModel() + SetCallbacks() instead
func NewModelWithAgent(
    ctx context.Context,
    agent interface{},  // agent.Agent
    coreState interface{},  // *state.CoreState
    eventSub events.Subscriber,
    chainCfg interface{},  // chain.ChainConfig
) *Model {
    model := NewModel(ctx, eventSub)
    model.agent = agent
    model.coreState = coreState
    model.chainCfg = chainCfg
    return model
}
```

**Action Items:**
- [ ] Implement `NewModel(ctx, eventSub)` - Rule 6 compliant
- [ ] Implement `NewModelWithAgent()` - deprecated, for backward compatibility
- [ ] Add deprecation warnings to old constructor
- [ ] Update godoc comments

---

#### Step 3A.4: Update Methods to Delegate to BaseModel

**Pattern: Delegate to embedded primitives**

```go
// Example 1: Window resize handling
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
    // Delegate to BaseModel's viewport manager
    m.GetViewportMgr().HandleResize(msg, 1, 3)  // header + footer
    m.ready = true
    return m, nil
}

// Example 2: Event handling
func (m *Model) handleAgentEvent(event events.Event) tea.Cmd {
    // Delegate to BaseModel's event handler
    m.GetEventHandler().HandleEvent(event)

    // Model-specific handling (if any)
    switch event.Type {
    case events.EventUserInterruption:
        // Model-specific interruption handling
    }

    // Continue waiting for events
    return WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
        return EventMsg(e)
    })
}

// Example 3: Status rendering
func (m *Model) renderStatusLine() string {
    // Delegate to BaseModel's status manager
    return m.GetStatusBarMgr().Render()
}
```

**Action Items:**
- [ ] Refactor `handleWindowSize()` to use `GetViewportMgr().HandleResize()`
- [ ] Refactor `handleAgentEvent()` to use `GetEventHandler().HandleEvent()`
- [ ] Refactor `renderStatusLine()` to use `GetStatusBarMgr().Render()`
- [ ] Refactor `ToggleDebug()` to use `GetDebugManager().ToggleDebug()`
- [ ] Refactor `SaveScreen()` to use `GetDebugManager().SaveScreen()`
- [ ] Remove duplicated code (~150 lines expected reduction)

---

#### Step 3A.5: Preserve App-Specific Features

**Features to preserve:**

1. **Todo Panel** (if present):
   ```go
   func (m *Model) renderTodoPanel() string {
       // Todo-specific rendering
       // Uses m.coreState (deprecated) or callback
   }
   ```

2. **Custom Key Bindings** (if any):
   ```go
   func (m *Model) handleCustomKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
       // App-specific key handling beyond BaseModel
   }
   ```

3. **Custom Event Renderers** (if any):
   ```go
   func (m *Model) registerCustomRenderers() {
       eh := m.GetEventHandler()
       eh.RegisterRenderer(events.EventCustom, m.renderCustomEvent)
   }
   ```

**Action Items:**
- [ ] Identify all app-specific features in current Model
- [ ] Preserve app-specific rendering methods
- [ ] Preserve app-specific key bindings
- [ ] Preserve app-specific event handlers
- [ ] Ensure no business logic in Model (only UI logic)

---

#### Step 3A.6: Update Tests

**Test Migration:**

```go
// OLD test
func TestModel_NewModel(t *testing.T) {
    agent := &mockAgent{}
    state := state.NewCoreState()
    eventSub := events.NewChanEmitter(100).Subscribe()

    model := NewModel(ctx, agent, state, eventSub, chainCfg)

    assert.NotNil(t, model)
    assert.Equal(t, agent, model.agent)
}

// NEW test
func TestModel_NewModel(t *testing.T) {
    eventSub := events.NewChanEmitter(100).Subscribe()

    model := NewModel(ctx, eventSub)

    assert.NotNil(t, model)
    assert.NotNil(t, model.BaseModel)
    assert.NotNil(t, model.GetViewportMgr())
    assert.NotNil(t, model.GetStatusBarMgr())
}

func TestModel_NewModelDeprecated(t *testing.T) {
    // Test backward compatibility
    agent := &mockAgent{}
    state := state.NewCoreState()
    eventSub := events.NewChanEmitter(100).Subscribe()

    model := NewModelWithAgent(ctx, agent, state, eventSub, chainCfg)

    assert.NotNil(t, model)
    assert.Equal(t, agent, model.agent)
}
```

**Action Items:**
- [ ] Update `model_test.go` with new tests
- [ ] Add deprecation tests for old constructor
- [ ] Add backward compatibility tests
- [ ] Ensure all tests pass

---

#### Step 3A.7: Verification Checklist

**After completing Step 3A, verify:**

- [ ] **No import cycles:** `go list -f '{{.ImportPath}} {{.Imports}}' ./pkg/tui`
- [ ] **Rule 6 compliance:** `grep -E "pkg/agent|pkg/chain|internal/" pkg/tui/model.go` (should only find comments)
- [ ] **Tests pass:** `go test ./pkg/tui/... -run TestModel -v`
- [ ] **Backward compatibility:** Old code using `NewModelWithAgent()` still works
- [ ] **Code reduction:** ~150 lines removed (duplicated with BaseModel)
- [ ] **Manual test:** `cd cmd/todo-agent && go run main.go` - all features work

**Completion Criteria for Stage 3A:**
- [ ] Model refactored to embed BaseModel
- [ ] All duplicated code removed
- [ ] All existing functionality preserved
- [ ] Tests updated and passing
- [ ] Rule 6 compliance verified

---

## 🎯 Stage 3B: SimpleTui Refactoring (0.5 day)

**Location:** `pkg/tui/simple.go`
**Current State:** ~400 lines, SimpleTui with own implementation
**Target State:** SimpleTui uses BaseModel primitives

---

### ✅ Pre-Implementation Checklist

Before starting Stage 3B:

- [ ] **Verify Stage 3A complete:** Model refactoring done
- [ ] **Verify all tests pass:** `go test ./pkg/tui/... -v`
- [ ] **Read current simple.go:** Understand existing functionality
- [ ] **Identify differences from Model:** SimpleTui is minimalist

---

### 📝 Step-by-Step Implementation

#### Step 3B.1: Analyze Current SimpleTui Structure

**Current Analysis (from simple.go:1-100):**

```go
// Current state
type SimpleTui struct {
    // Dependencies
    subscriber events.Subscriber
    onInput    func(string)  // Callback (Rule 6 compliant ✅)

    // UI components
    viewport   viewport.Model
    textarea   textarea.Model
    status     StatusBar  // Custom struct

    // Configuration
    config     SimpleUIConfig
    keys       key.Binding

    // State
    mu         sync.RWMutex
    ready      bool
}
```

**Key Differences from Model:**
- [ ] Simpler structure (no todo panel, no debug)
- [ ] Uses callback pattern (Rule 6 compliant ✅)
- [ ] Has custom StatusBar struct (not from primitives)
- [ ] Has custom config system

**Action Items:**
- [ ] Identify fields that map to primitives
- [ ] Identify unique features to preserve
- [ ] Plan migration strategy

---

#### Step 3B.2: Create New SimpleTui Structure

**Target Structure:**

```go
// pkg/tui/simple.go - REFACTORED

package tui

import (
    "context"
    "fmt"
    "sync"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/bubbles/textarea"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui/primitives"
)

// SimpleTui represents a minimalist TUI component.
//
// Rule 6 Compliance: Only reusable code, business logic via callback.
type SimpleTui struct {
    // Embed BaseModel for common functionality
    *BaseModel

    // Additional configuration
    config SimpleUIConfig

    // Callback for input handling (MANDATORY - Rule 6 compliant)
    mu      sync.RWMutex
    onInput func(string) tea.Cmd
}

// SimpleUIConfig defines configuration for SimpleTui
type SimpleUIConfig struct {
    Title   string
    Prompt  string
    Colors  ColorScheme
}
```

**Action Items:**
- [ ] Create new SimpleTui struct with BaseModel embedding
- [ ] Preserve SimpleUIConfig for backward compatibility
- [ ] Add deprecation comments if needed
- [ ] Update godoc comments

---

#### Step 3B.3: Refactor NewSimpleTui Constructor

**Before:**
```go
// OLD
func NewSimpleTui(sub events.Subscriber, cfg SimpleUIConfig) *SimpleTui {
    return &SimpleTui{
        subscriber: sub,
        config:     cfg,
        viewport:   viewport.New(0, 0),
        textarea:   textarea.New(),
        status:     StatusBar{...},
        onInput:    nil,  // Must be set via OnInput()
    }
}
```

**After:**
```go
// NEW - uses BaseModel
func NewSimpleTui(ctx context.Context, sub events.Subscriber, cfg SimpleUIConfig) *SimpleTui {
    // Create BaseModel
    base := NewBaseModel(ctx, sub)

    // Customize BaseModel
    base.SetTitle(cfg.Title)

    return &SimpleTui{
        BaseModel: base,
        config:    cfg,
        onInput:   nil,  // Must be set via OnInput()
    }
}
```

**Action Items:**
- [ ] Implement `NewSimpleTui(ctx, sub, cfg)`
- [ ] Add ctx parameter (Rule 11 compliance)
- [ ] Preserve SimpleUIConfig
- [ ] Update godoc comments

---

#### Step 3B.4: Update Methods to Delegate to BaseModel

**Pattern: Delegate to embedded BaseModel**

```go
// Example 1: Input handling
func (t *SimpleTui) handleInput(input string) tea.Cmd {
    t.mu.RLock()
    handler := t.onInput
    t.mu.RUnlock()

    if handler == nil {
        t.Append("Error: onInput callback not set", true)
        return nil
    }

    return handler(input)
}

// Example 2: Event handling
func (t *SimpleTui) handleEvent(event events.Event) tea.Cmd {
    // Delegate to BaseModel's event handler
    t.GetEventHandler().HandleEvent(event)

    return WaitForEvent(t.GetSubscriber(), func(e events.Event) tea.Msg {
        return EventMsg(e)
    })
}
```

**Action Items:**
- [ ] Refactor `handleInput()` to use callback
- [ ] Refactor `handleEvent()` to use `GetEventHandler().HandleEvent()`
- [ ] Refactor `renderStatus()` to use `GetStatusBarMgr().Render()`
- [ ] Remove duplicated code (~100 lines expected reduction)

---

#### Step 3B.5: Preserve Callback Pattern

**SimpleTui's key feature: Callback pattern for Rule 6 compliance**

```go
// OnInput sets the callback for handling user input.
// This is the MAIN injection point for business logic (Rule 6 compliant).
//
// Business logic lives in cmd/ layer, not in pkg/tui.
func (t *SimpleTui) OnInput(handler func(string) tea.Cmd) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.onInput = handler
}

// HandleKeyPress processes key presses
func (t *SimpleTui) HandleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch {
    case key.Matches(msg, t.keys.Enter):
        input := t.textarea.Value()
        if input == "" {
            return t, nil
        }
        t.textarea.Reset()
        return t, t.handleInput(input)

    case key.Matches(msg, t.keys.Quit):
        return t, tea.Quit

    default:
        var cmd tea.Cmd
        t.textarea, cmd = t.textarea.Update(msg)
        return t, cmd
    }
}
```

**Action Items:**
- [ ] Preserve `OnInput()` callback method
- [ ] Preserve callback pattern in all input handling
- [ ] Ensure no business logic in SimpleTui
- [ ] Update godoc comments with Rule 6 explanation

---

#### Step 3B.6: Update Tests

**Test Migration:**

```go
// OLD test
func TestSimpleTui_NewSimpleTui(t *testing.T) {
    sub := events.NewChanEmitter(100).Subscribe()
    cfg := SimpleUIConfig{Title: "Test"}

    tui := NewSimpleTui(sub, cfg)

    assert.NotNil(t, tui)
    assert.Equal(t, cfg.Title, tui.config.Title)
}

// NEW test
func TestSimpleTui_NewSimpleTui(t *testing.T) {
    ctx := context.Background()
    sub := events.NewChanEmitter(100).Subscribe()
    cfg := SimpleUIConfig{Title: "Test"}

    tui := NewSimpleTui(ctx, sub, cfg)

    assert.NotNil(t, tui)
    assert.NotNil(t, tui.BaseModel)
    assert.Equal(t, "Test", tui.GetTitle())
}
```

**Action Items:**
- [ ] Update `simple_test.go` with new tests
- [ ] Add ctx parameter to all tests
- [ ] Add backward compatibility tests
- [ ] Ensure all tests pass

---

#### Step 3B.7: Verification Checklist

**After completing Step 3B, verify:**

- [ ] **No import cycles:** `go list -f '{{.ImportPath}} {{.Imports}}' ./pkg/tui`
- [ ] **Rule 6 compliance:** `grep -E "pkg/agent|pkg/chain|internal/" pkg/tui/simple.go` (should only find comments)
- [ ] **Tests pass:** `go test ./pkg/tui/... -run TestSimpleTui -v`
- [ ] **Backward compatibility:** Old code using SimpleTui still works
- [ ] **Code reduction:** ~100 lines removed (duplicated with BaseModel)
- [ ] **Manual test:** `cd cmd/simple-agent && go run main.go "test"` - works

**Completion Criteria for Stage 3B:**
- [ ] SimpleTui refactored to use BaseModel
- [ ] All duplicated code removed
- [ ] Callback pattern preserved
- [ ] Tests updated and passing
- [ ] Rule 6 compliance verified

---

## 🎯 Stage 3C: InterruptionModel Refactoring (0.5 day)

**Location:** `pkg/tui/model.go` (InterruptionModel section)
**Current State:** InterruptionModel embedded in model.go, ~300 lines
**Target State:** InterruptionModel uses BaseModel + InterruptionManager primitive

---

### ✅ Pre-Implementation Checklist

Before starting Stage 3C:

- [ ] **Verify Stages 3A and 3B complete:** Model and SimpleTui refactored
- [ ] **Verify InterruptionManager primitive exists:** `pkg/tui/primitives/interruption.go`
- [ ] **Read current InterruptionModel:** Understand interruption flow
- [ ] **Read 04-INTERRUPTIONS.md:** Understand architecture requirements

---

### 📝 Step-by-Step Implementation

#### Step 3C.1: Analyze Current InterruptionModel Structure

**Current Analysis (from model.go:850-1210):**

```go
// Current state (lines 850-1210)
type InterruptionModel struct {
    // Base model fields
    viewport   viewport.Model
    textarea   textarea.Model
    spinner    spinner.Model
    // ... (duplicated with Model)

    // Interruption-specific
    agent      agent.Agent  // ❌ VIOLATES Rule 6
    coreState  *state.CoreState
    inputChan  chan string
    onInput    func(query string) tea.Cmd  // ✅ Callback (Rule 6 compliant)
    chainCfg   chain.ChainConfig
}
```

**Key Features:**
- [ ] Interruption flow: User → inputChan → ReActExecutor → Event → UI
- [ ] Callback pattern for agent startup (Rule 6 compliant ✅)
- [ ] EventUserInterruption handling
- [ ] Channel-based inter-goroutine communication

**Action Items:**
- [ ] Identify all interruption-specific logic
- [ ] Identify code that maps to InterruptionManager
- [ ] Plan migration to primitive

---

#### Step 3C.2: Create New InterruptionModel Structure

**Target Structure:**

```go
// pkg/tui/model.go - InterruptionModel section

// InterruptionModel represents a TUI model with interruption support.
//
// Rule 6 Compliance: Only reusable code, business logic via callback.
// Interruption logic extracted to InterruptionManager primitive.
type InterruptionModel struct {
    // Embed BaseModel for common functionality
    *BaseModel

    // Interruption manager primitive
    interruptMgr *primitives.InterruptionManager

    // App-specific state (deprecated - use callbacks)
    coreState interface{}  // *state.CoreState - for todo operations
    chainCfg  interface{}  // chain.ChainConfig - for interruption prompt

    mu sync.RWMutex
}
```

**Action Items:**
- [ ] Create new InterruptionModel struct
- [ ] Embed BaseModel for common functionality
- [ ] Add InterruptionManager for interruption logic
- [ ] Use `interface{}` for deprecated fields

---

#### Step 3C.3: Refactor NewInterruptionModel Constructor

**Before:**
```go
// OLD (lines 900-950) - has agent dependency
func NewInterruptionModel(
    ctx context.Context,
    agent agent.Agent,  // ❌ VIOLATES Rule 6
    coreState *state.CoreState,
    eventSub events.Subscriber,
    inputChan chan string,
    chainCfg chain.ChainConfig,
) *InterruptionModel {
    // 50+ lines of initialization...
}
```

**After:**
```go
// NEW - Rule 6 compliant
func NewInterruptionModel(
    ctx context.Context,
    eventSub events.Subscriber,
) *InterruptionModel {
    // Create BaseModel
    base := NewBaseModel(ctx, eventSub)

    // Create InterruptionManager
    interruptMgr := primitives.NewInterruptionManager(10)

    return &InterruptionModel{
        BaseModel:     base,
        interruptMgr:  interruptMgr,
        coreState:     nil,  // Deprecated - use callbacks
        chainCfg:      nil,  // Deprecated - use callbacks
    }
}

// Deprecated constructor for backward compatibility
// ⚠️ DEPRECATED: Use NewInterruptionModel() + SetOnInput() instead
func NewInterruptionModelWithAgent(
    ctx context.Context,
    agent interface{},  // agent.Agent
    coreState interface{},  // *state.CoreState
    eventSub events.Subscriber,
    inputChan chan string,
    chainCfg interface{},  // chain.ChainConfig
) *InterruptionModel {
    model := NewInterruptionModel(ctx, eventSub)
    model.coreState = coreState
    model.chainCfg = chainCfg
    return model
}
```

**Action Items:**
- [ ] Implement `NewInterruptionModel(ctx, eventSub)`
- [ ] Implement `NewInterruptionModelWithAgent()` - deprecated
- [ ] Remove agent parameter from main constructor
- [ ] Add deprecation warnings

---

#### Step 3C.4: Refactor Interruption Handling

**Pattern: Delegate to InterruptionManager**

```go
// SetOnInput устанавливает callback для запуска агента (MANDATORY)
// Это business logic injection point - UI вызывает, cmd/ реализует
//
// Rule 6 Compliance: Business logic lives in cmd/ layer.
func (m *InterruptionModel) SetOnInput(handler func(query string) tea.Cmd) {
    m.interruptMgr.SetOnInput(handler)
}

// handleKeyPressWithInterruption обрабатывает Enter с поддержкой прерываний
func (m *InterruptionModel) handleKeyPressWithInterruption(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch {
    case key.Matches(msg, m.base.keys.ConfirmInput):
        input := m.base.textarea.Value()
        if input == "" {
            return m, nil
        }

        m.base.textarea.Reset()
        m.base.Append(systemStyle(fmt.Sprintf("> %s", input)), true)

        // Delegate to InterruptionManager
        isProcessing := m.base.IsProcessing()
        cmd, shouldSendToChannel, err := m.interruptMgr.HandleInput(input, isProcessing)

        if err != nil {
            m.base.Append(errorStyle(fmt.Sprintf("❌ Error: %v", err)), true)
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

**Action Items:**
- [ ] Refactor `SetOnInput()` to delegate to InterruptionManager
- [ ] Refactor `handleKeyPressWithInterruption()` to use InterruptionManager
- [ ] Remove duplicated interruption logic (~100 lines)
- [ ] Preserve interruption flow 1:1

---

#### Step 3C.5: Refactor Event Handling

**Pattern: Handle EventUserInterruption via InterruptionManager**

```go
// handleAgentEventWithInterruption обрабатывает события от агента
func (m *InterruptionModel) handleAgentEventWithInterruption(event events.Event) tea.Cmd {
    // Handle interruption events
    if shouldDisplay, displayText := m.interruptMgr.HandleEvent(event); shouldDisplay {
        m.base.Append(systemStyle(displayText), true)
    }

    // Handle other events via BaseModel's EventHandler
    m.base.GetEventHandler().HandleEvent(event)

    // Continue waiting for events
    return WaitForEvent(m.base.GetSubscriber(), func(e events.Event) tea.Msg {
        return EventMsg(e)
    })
}
```

**Action Items:**
- [ ] Refactor event handling to use InterruptionManager
- [ ] Preserve EventUserInterruption display format
- [ ] Remove duplicated event logic (~50 lines)

---

#### Step 3C.6: Update Tests

**Test Migration:**

```go
// OLD test
func TestInterruptionModel_NewInterruptionModel(t *testing.T) {
    agent := &mockAgent{}
    state := state.NewCoreState()
    eventSub := events.NewChanEmitter(100).Subscribe()
    inputChan := make(chan string, 10)
    chainCfg := chain.DefaultChainConfig()

    model := NewInterruptionModel(ctx, agent, state, eventSub, inputChan, chainCfg)

    assert.NotNil(t, model)
    assert.NotNil(t, model.inputChan)
}

// NEW test
func TestInterruptionModel_NewInterruptionModel(t *testing.T) {
    ctx := context.Background()
    eventSub := events.NewChanEmitter(100).Subscribe()

    model := NewInterruptionModel(ctx, eventSub)

    assert.NotNil(t, model)
    assert.NotNil(t, model.BaseModel)
    assert.NotNil(t, model.interruptMgr)
    assert.NotNil(t, model.interruptMgr.GetChannel())
}

func TestInterruptionModel_SetOnInput(t *testing.T) {
    // Test callback pattern
    model := NewInterruptionModel(ctx, eventSub)

    called := false
    model.SetOnInput(func(query string) tea.Cmd {
        called = true
        assert.Equal(t, "test query", query)
        return nil
    })

    input := "test query"
    isProcessing := false
    cmd, shouldSend, err := model.interruptMgr.HandleInput(input, isProcessing)

    assert.True(t, called)
    assert.Nil(t, err)
    assert.False(t, shouldSend)  // Agent not running
}
```

**Action Items:**
- [ ] Update InterruptionModel tests
- [ ] Add callback tests
- [ ] Add InterruptionManager integration tests
- [ ] Ensure all tests pass

---

#### Step 3C.7: Verification Checklist

**After completing Step 3C, verify:**

- [ ] **No import cycles:** `go list -f '{{.ImportPath}} {{.Imports}}' ./pkg/tui`
- [ ] **Rule 6 compliance:** `grep -E "pkg/agent|pkg/chain|internal/" pkg/tui/model.go` (should only find comments)
- [ ] **Tests pass:** `go test ./pkg/tui/... -run TestInterruptionModel -v`
- [ ] **Backward compatibility:** Old code still works
- [ ] **Interruption flow verified:**
  - [ ] User enters query → agent starts
  - [ ] User enters interruption → agent receives it
  - [ ] UI shows "⏸️ Interruption (iteration N): ..."
  - [ ] Agent continues with new context
- [ ] **Code reduction:** ~150 lines removed (duplicated with BaseModel + InterruptionManager)
- [ ] **Manual test:** `cd cmd/interruption-test && go run main.go` - interruption flow works

**Completion Criteria for Stage 3C:**
- [ ] InterruptionModel refactored to use BaseModel + InterruptionManager
- [ ] Agent dependency removed from constructor
- [ ] All interruption functionality preserved
- [ ] Tests updated and passing
- [ ] Rule 6 compliance verified

---

## 🎯 Phase 3 Final Verification

### Overall Completion Checklist

**After completing all stages (3A, 3B, 3C):**

- [ ] **All tests pass:**
  ```bash
  go test ./pkg/tui/... -v
  # Expected: 70+ tests passing (55 primitives + BaseModel + Model tests)
  ```

- [ ] **No import cycles:**
  ```bash
  go list -f '{{.ImportPath}} {{.Imports}}' ./pkg/tui/...
  # Expected: No circular dependencies
  ```

- [ ] **Rule 6 compliance:**
  ```bash
  grep -r "pkg/agent\|pkg/chain\|internal/" pkg/tui/*.go | grep -v "^Binary"
  # Expected: Only finds comments, no actual imports
  ```

- [ ] **Rule 11 compliance:**
  ```bash
  grep -r "context.Context" pkg/tui/*.go
  # Expected: All models store ctx and propagate it
  ```

- [ ] **Code reduction:**
  ```bash
  # Before Phase 3: ~3000 lines in pkg/tui/
  # After Phase 3: ~2600 lines in pkg/tui/ (expected ~400 line reduction)
  ```

- [ ] **Manual testing:**
  - [ ] `cd cmd/interruption-test && go run main.go` - interruption flow works
  - [ ] `cd cmd/todo-agent && go run main.go` - todo operations work
  - [ ] `cd cmd/simple-agent && go run main.go "test"` - simple TUI works
  - [ ] Resize behavior preserves scroll position
  - [ ] Debug mode toggle works (Ctrl+G)
  - [ ] Screen save works (Ctrl+S)
  - [ ] Debug log path works (Ctrl+L)

- [ ] **Backward compatibility:**
  - [ ] Old `cmd/` code still compiles without changes
  - [ ] Deprecated constructors work with warnings
  - [ ] No breaking changes to public API

---

## 🚨 Common Pitfalls to Avoid

### ❌ DO NOT:

1. **Add business logic to pkg/tui**
   - ❌ Wrong: `model.agent.Execute(ctx, query)` in pkg/tui
   - ✅ Right: `model.SetOnInput(func(query) tea.Cmd { return agentCmd })`

2. **Import pkg/agent or pkg/chain in pkg/tui**
   - ❌ Wrong: `import "github.com/ilkoid/poncho-ai/pkg/agent"`
   - ✅ Right: Use `interface{}` for deprecated fields
   - ✅ Right: Use callbacks from cmd/ layer

3. **Break existing functionality**
   - ❌ Wrong: Remove InterruptionModel without migration
   - ✅ Right: Deprecate old constructors, provide new ones

4. **Forget context propagation**
   - ❌ Wrong: `func NewModel(eventSub events.Subscriber) *Model`
   - ✅ Right: `func NewModel(ctx context.Context, eventSub events.Subscriber) *Model`

5. **Create tight coupling**
   - ❌ Wrong: Embed agent.Client directly in Model
   - ✅ Right: Use callback pattern for business logic injection

---

### ✅ DO:

1. **Use callback pattern for Rule 6 compliance**
   ```go
   model.SetOnInput(func(query string) tea.Cmd {
       return startAgentCmd(query)  // Business logic in cmd/
   })
   ```

2. **Delegate to primitives**
   ```go
   m.GetViewportMgr().HandleResize(msg, header, footer)
   m.GetStatusBarMgr().SetProcessing(true)
   m.GetEventHandler().HandleEvent(event)
   ```

3. **Preserve existing behavior 1:1**
   ```go
   // Same output, different implementation
   // Before: direct implementation
   // After: delegate to primitive
   ```

4. **Add deprecation warnings**
   ```go
   // ⚠️ DEPRECATED: Use NewModel() + SetCallbacks() instead
   func NewModelWithAgent(...) *Model
   ```

5. **Write tests for all changes**
   ```go
   func TestModel_NewModel(t *testing.T) { ... }
   func TestModel_NewModelDeprecated(t *testing.T) { ... }
   ```

---

## 📊 Phase 3 Success Metrics

| Metric | Before | After | Target |
|--------|--------|-------|--------|
| **Lines of code** | ~3000 | ~2600 | -400 lines |
| **Test count** | 70 | 85+ | +15 tests |
| **Rule 6 violations** | 3 | 0 | 100% compliant |
| **Import cycles** | 0 | 0 | No new cycles |
| **Backward compatibility** | 100% | 100% | No breaking changes |
| **Manual test coverage** | 80% | 100% | All features work |

---

## 🎯 Phase 3 Completion Criteria

Phase 3 is **COMPLETE** when:

- [ ] **All 3 stages complete:** 3A (Model), 3B (SimpleTui), 3C (InterruptionModel)
- [ ] **All tests passing:** 85+ tests (70 primitives + 15 refactored models)
- [ ] **Rule 6 compliance:** No business logic imports in pkg/tui
- [ ] **Rule 11 compliance:** Context propagated through all layers
- [ ] **Code reduction:** ~400 lines removed (duplicated with primitives)
- [ ] **Backward compatibility:** Old code still works
- [ ] **Manual testing:** All cmd/ entry points work correctly
- [ ] **Documentation:** PRIMITIVES-CHEATSHEET.md updated with Phase 3 patterns

**Estimated Time:** 1 day (2 stages × 0.5 day)
**Risk Level:** 🟡 Medium (breaking changes to internal API)
**Mitigation:** Comprehensive testing, backward compatibility preserved

---

## 📚 Related Documents

- [09-OPTION-B-DETAILED.md](./09-OPTION-B-DETAILED.md) - Original plan
- [IMPLEMENTATION-REPORT.md](./IMPLEMENTATION-REPORT.md) - Phases 1-2 completion
- [PRIMITIVES-CHEATSHEET.md](./PRIMITIVES-CHEATSHEET.md) - Phase 1-2 API reference
- [dev_manifest.md](../dev_manifest.md) - Architectural rules (especially Rule 6, Rule 11)

---

**Ready to start Phase 3?** Begin with Stage 3A (Model Refactoring)!

**Last Updated:** 2026-01-18
**Version:** 1.0
