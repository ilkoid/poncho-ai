# 01. Current State Analysis

## Overview

This document describes the current architecture of TUI components in Poncho AI.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CURRENT TUI ARCHITECTURE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  pkg/events/ (PORT - interfaces)                                    │   │
│  │  ├─ Emitter interface      → Library depends on this                │   │
│  │  ├─ Subscriber interface  → UI depends on this                      │   │
│  │  └─ Event types + EventData                                        │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▲                                        │
│                                    │ implements                             │
│                                    │                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  pkg/tui/ (ADAPTER - reusable UI components)                        │   │
│  │  ├─ model.go                                                        │   │
│  │  │  ├─ Model (base, ~600 lines)                                    │   │
│  │  │  └─ InterruptionModel (~300 lines)                              │   │
│  │  ├─ simple.go                                                       │   │
│  │  │  └─ SimpleTui (~400 lines)                                      │   │
│  │  ├─ adapter.go                                                      │   │
│  │  │  └─ EventMsg, ReceiveEventCmd                                   │   │
│  │  └─ viewport_helpers.go                                            │   │
│  │      └─ Smart scroll helpers                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▲                                        │
│                                    │ extends/composes                       │
│                                    │                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  internal/ui/ (APP-SPECIFIC TUI)                                    │   │
│  │  └─ model.go                                                        │   │
│  │      └─ MainModel (~1000 lines, with todo-panel)                   │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▲                                        │
│                                    │ uses                                   │
│                                    │                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  cmd/*/main.go (ENTRY POINTS)                                       │   │
│  │  ├─ interruption-test/main.go  → InterruptionModel example         │   │
│  │  ├─ poncho/main.go                → MainModel usage                 │   │
│  │  └─ todo-agent/main.go            → Custom TUI                     │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. Model ([pkg/tui/model.go](../pkg/tui/model.go))

**Purpose**: Base TUI model for AI agents

**Key Features**:
- Bubble Tea components (viewport, textarea, spinner, help)
- Agent event handling via `events.Subscriber`
- Todo list display
- Debug mode toggle (Ctrl+G)
- Help panel (Ctrl+H)

**Structure**:
```go
type Model struct {
    // Bubble Tea components
    viewport viewport.Model
    textarea textarea.Model
    spinner  spinner.Model
    help     help.Model

    // Dependencies
    agent     agent.Agent        // ⚠️ VIOLATES Port & Adapter
    coreState *state.CoreState   // ⚠️ STATE dependency
    eventSub  events.Subscriber  // ✅ Correct: Port interface

    // State
    isProcessing bool
    todos        []todo.Task
    ready        bool
    showHelp     bool
    debugMode    bool

    // Options
    title   string
    prompt  string
    keys    KeyMap
    ctx     context.Context
}
```

**Lines of Code**: ~600 (base) + ~300 (InterruptionModel)

**Key Methods**:
- `NewModel()` — Creates base model with agent dependency
- `Init()` — Bubble Tea initialization
- `Update()` — Message handling
- `View()` — Rendering
- `handleAgentEvent()` — Event processing (lines 319-393)
- `handleWindowSize()` — Viewport resize (lines 396-471)
- `renderStatusLine()` — Status bar rendering (lines 639-699)

### 2. InterruptionModel ([pkg/tui/model.go:851+](../pkg/tui/model.go#L851))

**Purpose**: Extended model with interruption support

**Key Features**:
- Composes base `Model` via pointer
- Interruption channel (`inputChan chan string`)
- Callback-based business logic (`SetOnInput()`)
- Interrupts agent execution between iterations

**Structure**:
```go
type InterruptionModel struct {
    base       *Model              // Composition over inheritance
    inputChan  chan string         // Interruption channel
    chainCfg   chain.ChainConfig   // ReAct configuration
    mu         sync.RWMutex
    fullLLMLogging bool
    lastDebugPath string
    onInput    func(query string) tea.Cmd  // MANDATORY callback
}
```

**Key Methods**:
- `NewInterruptionModel()` — Creates model with agent dependency
- `Update()` — Intercepts EventMsg before base model
- `handleAgentEventWithInterruption()` — Event processing with interruptions
- `handleKeyPressWithInterruption()` — Enter key handling
- `SetOnInput()` — Sets business logic callback

**Lines of Code**: ~300

### 3. SimpleTui ([pkg/tui/simple.go](../pkg/tui/simple.go))

**Purpose**: Minimalist "lego brick" TUI component

**Key Features**:
- Pure UI, no business logic
- Callback pattern via `OnInput()`
- Smart viewport scroll
- Configurable color schemes

**Structure**:
```go
type SimpleTui struct {
    config       SimpleUIConfig
    subscriber   events.Subscriber  // ✅ Correct: Port interface only
    onInput      func(input string) // Callback for business logic
    quitChan     chan struct{}

    // Bubble Tea components
    viewport     viewport.Model
    textarea     textarea.Model

    // State
    mu           sync.RWMutex
    messages     []string
    ready        bool
    isProcessing bool
}
```

**Lines of Code**: ~400

**Key Methods**:
- `NewSimpleTui()` — Creates model with subscriber only
- `OnInput()` — Sets input callback
- `Run()` — Starts Bubble Tea program
- `handleAgentEvent()` — Event processing (lines 249-301)
- `handleWindowSize()` — Viewport resize (lines 303-331)

### 4. MainModel ([internal/ui/model.go](../internal/ui/model.go))

**Purpose**: App-specific TUI with todo panel

**Key Features**:
- Extends base TUI functionality
- Todo panel integration
- Custom commands

**Structure**:
```go
type MainModel struct {
    // Bubble Tea components
    viewport viewport.Model
    textarea textarea.Model
    todoViewport viewport.Model  // Additional panel

    // Dependencies
    agent       agent.Agent      // ⚠️ VIOLATES Port & Adapter
    coreState   *state.CoreState
    eventSub    events.Subscriber
    agentState  *agentState       // Custom state management

    // ... additional fields
}
```

**Lines of Code**: ~1000

## Key Observations

### ✅ Strengths

1. **Port & Adapter in events/** — Clean interface separation
2. **Callback pattern** — Business logic in `cmd/` via `SetOnInput()`
3. **Smart scroll** — Viewport helpers preserve scroll position
4. **Event system** — Clean event flow from agent to UI

### ⚠️ Issues

1. **Agent coupling** — `pkg/tui` depends on `pkg/agent`
2. **Code duplication** — 3 implementations of same logic
3. **Mixed concerns** — UI + business logic in same files
4. **Interruption split** — Logic scattered across `pkg/chain`, `pkg/tui`, `cmd/`

## Dependency Graph

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          DEPENDENCY GRAPH                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  pkg/agent/                                                                 │
│      │                                                                      │
│      ├── implements ──▶ agent.Agent interface                               │
│      │                                                                      │
│      └── depends on ──▶ pkg/events/Emitter interface (Port)                │
│                                                                             │
│  pkg/tui/model.go                                                           │
│      │                                                                      │
│      ├── imports ──▶ pkg/agent ⚠️ (VIOLATION)                              │
│      ├── imports ──▶ pkg/events ✅ (Port)                                   │
│      ├── imports ──▶ pkg/state ✅ (CoreState)                               │
│      └── imports ──▶ pkg/chain ✅ (ChainConfig)                             │
│                                                                             │
│  pkg/tui/simple.go                                                          │
│      │                                                                      │
│      ├── imports ──▶ pkg/events ✅ (Port only)                              │
│      └── NO import of pkg/agent ✅ (CORRECT)                                │
│                                                                             │
│  internal/ui/model.go                                                       │
│      │                                                                      │
│      ├── imports ──▶ pkg/agent ⚠️ (OK in internal/)                         │
│      ├── imports ──▶ pkg/events ✅                                          │
│      └── imports ──▶ pkg/tui ✅ (reuses base components)                    │
│                                                                             │
│  cmd/*/main.go                                                              │
│      │                                                                      │
│      ├── imports ──▶ pkg/agent ✅ (Business logic layer)                    │
│      ├── imports ──▶ pkg/tui ✅                                            │
│      └── imports ──▶ pkg/events ✅                                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

**Next**: [02-VIOLATIONS.md](./02-VIOLATIONS.md) — Port & Adapter violations
