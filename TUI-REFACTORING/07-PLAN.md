# 07. Original Plan from Claude

This is the original plan created during the analysis session.

---

# TUI Architecture Analysis & Documentation Plan

## Overview

Analysis of the Poncho AI TUI architecture focusing on:
- Port & Adapter pattern compliance
- Code duplication across TUI implementations
- Agent-TUI interaction patterns
- Interruption mechanism architecture

## Context

The user has chosen to:
1. **Document only** — no immediate refactoring
2. **Follow Port & Adapter** — `pkg/tui` should NOT depend on `pkg/agent`
3. **Preserve key features** — Interruptions, Viewport logic, Event handling, Key bindings

## Key Findings

### 1. Current Architecture State

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CURRENT TUI ARCHITECTURE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  pkg/events/ (PORT - interfaces)                                            │
│  ├─ Emitter interface    — Library depends on this                          │
│  ├─ Subscriber interface  — UI depends on this                              │
│  └─ Event types + EventData                                                │
│                                                                             │
│  pkg/tui/ (ADAPTER - reusable UI components)                                │
│  ├─ model.go         → Model, InterruptionModel                             │
│  ├─ simple.go        → SimpleTui                                            │
│  ├─ adapter.go       → EventMsg, ReceiveEventCmd                            │
│  └─ viewport_helpers.go → Smart scroll helpers                              │
│                                                                             │
│  internal/ui/ (APP-SPECIFIC TUI)                                            │
│  └─ model.go         → MainModel (todo-panel, custom features)             │
│                                                                             │
│  cmd/*/main.go (ENTRY POINTS)                                               │
│  ├─ interruption-test/ → InterruptionModel example                         │
│  ├─ poncho/           → MainModel usage                                     │
│  └─ todo-agent/       → Custom TUI                                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2. Port & Adapter Pattern: VIOLATIONS DETECTED

**Current state** (`pkg/tui/model.go`):

```go
// Model struct - VIOLATES Port & Adapter
type Model struct {
    agent     agent.Agent        // ← DEPENDS ON pkg/agent
    coreState *state.CoreState   // ← DEPENDS ON pkg/state
    eventSub  events.Subscriber  // ← CORRECT: Port interface
}
```

**Problem**: `pkg/tui` directly imports and depends on `pkg/agent`, creating tight coupling.

**Correct Port & Adapter**:
```go
// Model struct - SHOULD BE
type Model struct {
    eventSub  events.Subscriber  // ← ONLY Port interface
    // No direct dependency on agent.Agent
}
```

### 3. Code Duplication Matrix

| Component | pkg/tui/model.go | pkg/tui/simple.go | internal/ui/model.go | Priority |
|-----------|------------------|-------------------|----------------------|----------|
| Viewport resize | ✓ (lines 396-471) | ✓ (lines 303-331) | ✓ | High |
| Event handling | ✓ (lines 319-393) | ✓ (lines 249-301) | ✓ | High |
| Key bindings | ✓ (lines 97-132) | ✓ | ✓ | Medium |
| Status bar | ✓ (lines 639-699) | ✓ (lines 376-379) | ✓ | Medium |
| Message styling | ✓ (lines 782-834) | ✓ | ✓ | Low |

### 4. Interruption Mechanism Analysis

**Flow**:
```
User Input → inputChan → ReActExecutor (between iterations)
    → loadInterruptionPrompt() → EventUserInterruption
    → InterruptionModel.handleAgentEventWithInterruption()
```

**Files**:
- `pkg/chain/interruption.go` — `loadInterruptionPrompt()`
- `pkg/chain/executor.go:262-313` — Interruption check
- `pkg/tui/model.go:1044-1131` — Event handling
- `cmd/interruption-test/main.go:126-166` — `createAgentLauncher()`

**Architecture Issue**: Interruption logic is split across `pkg/chain`, `pkg/tui`, and `cmd/`.

### 5. Event Flow (Current State)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          EVENT FLOW DIAGRAM                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. Agent emits event:                                                       │
│     pkg/chain/executor.go → emitter.Emit(ctx, Event{...})                   │
│                                                                             │
│  2. Emitter forwards to channel:                                            │
│     pkg/events/emitter.go → ChanEmitter.events <- event                     │
│                                                                             │
│  3. Subscriber receives event:                                              │
│     events.Subscriber.Events() <-chan Event                                 │
│                                                                             │
│  4. TUI converts to Bubble Tea message:                                     │
│     pkg/tui/adapter.go → EventMsg(events.Event)                             │
│                                                                             │
│  5. TUI Model processes:                                                    │
│     InterruptionModel.Update(EventMsg) → handleAgentEventWithInterruption() │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Recommendations (for Future Refactoring)

### 1. Restore Port & Adapter Compliance

**Current violation**:
```go
// pkg/tui/model.go
import "github.com/ilkoid/poncho-ai/pkg/agent"  // ← VIOLATION

type Model struct {
    agent agent.Agent  // ← TIGHT COUPLING
}
```

**Recommended fix**:
```go
// pkg/tui/model.go
import (
    "github.com/ilkoid/poncho-ai/pkg/events"  // ← ONLY Port
    // NO import of pkg/agent
)

type Model struct {
    eventSub events.Subscriber  // ← Port interface only
}
```

**Business logic stays in cmd/**:
```go
// cmd/interruption-test/main.go
import (
    "github.com/ilkoid/poncho-ai/pkg/agent"  // ← OK here
    "github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
    client, _ := agent.New(...)  // ← Agent creation in cmd/

    // Pass only Subscriber to TUI
    sub := client.Subscribe()
    model := tui.NewModel(sub)

    // Business logic callback
    model.SetOnInput(func(query string) tea.Cmd {
        return client.Run(ctx, query)  // ← Agent call in cmd/
    })
}
```

### 2. Extract Common Primitives

Create `pkg/tui/primitives/` with reusable components:

```
pkg/tui/primitives/
├── viewport.go    → ViewportManager (resize, scroll, styling)
├── input.go       → InputManager (textarea, validation)
├── status.go      → StatusBarManager (spinner, status line)
├── events.go      → EventHandler (event → UI updates)
└── keys.go        → KeyBindings (configured keymaps)
```

### 3. Interruption Mechanism Consolidation

Current split:
- `pkg/chain/interruption.go` — Prompt loading
- `pkg/chain/executor.go` — Interruption check
- `pkg/tui/model.go` — Event handling
- `cmd/*/main.go` — Channel creation

Proposed consolidation:
- Keep interruption logic in `pkg/chain/`
- TUI only receives `EventUserInterruption` via Subscriber
- No special handling in TUI beyond standard event processing

## Critical Files

| File | Purpose | Lines | Priority |
|------|---------|-------|----------|
| `pkg/tui/model.go` | Base TUI model + InterruptionModel | ~1300 | High |
| `pkg/tui/simple.go` | Minimalist TUI component | ~400 | Medium |
| `pkg/events/events.go` | Event types (Port) | ~190 | High |
| `pkg/events/emitter.go` | Emitter/Subscriber implementations | ~100 | High |
| `pkg/chain/executor.go` | ReAct execution + interruption check | ~400 | High |
| `pkg/chain/interruption.go` | Interruption prompt loading | ~100 | High |
| `internal/ui/model.go` | App-specific TUI | ~1000 | Medium |
| `cmd/interruption-test/main.go` | Interruption example | ~170 | High |

## Next Steps

Since user chose "Document only":

1. Create comprehensive ADR (Architecture Decision Record)
2. Document current state with diagrams
3. Identify violations of Port & Adapter pattern
4. Catalog code duplication with line numbers
5. Provide recommendations for future refactoring

No code changes will be made without user approval.
