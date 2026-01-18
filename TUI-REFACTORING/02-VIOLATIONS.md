# 02. Port & Adapter Violations

## Overview

This document identifies violations of the Port & Adapter pattern in the current TUI architecture.

## Port & Adapter Pattern

### Definition

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PORT & ADAPTER PATTERN                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Library (pkg/agent)                                                         │
│      │                                                                      │
│      │ depends on                                                           │
│      ▼                                                                      │
│  ┌─────────────────┐                                                        │
│  │   Port (Iface)  │ ← Abstract interface                                  │
│  │  Emitter        │   defined in library                                  │
│  └─────────────────┘                                                        │
│      ▲                                                                      │
│      │ implements                                                           │
│      │                                                                      │
│  ┌─────────────────┐                                                        │
│  │   Adapter       │ ← Concrete implementation                             │
│  │  ChanEmitter    │   provided by infrastructure/UI                       │
│  └─────────────────┘                                                        │
│                                                                             │
│  UI (pkg/tui, internal/ui/)                                                 │
│      │                                                                      │
│      │ depends on                                                           │
│      ▼                                                                      │
│  ┌─────────────────┐                                                        │
│  │   Port (Iface)  │ ← Abstract interface                                  │
│  │  Subscriber     │   defined in library                                  │
│  └─────────────────┘                                                        │
│      ▲                                                                      │
│      │ implements                                                           │
│      │                                                                      │
│  ┌─────────────────┐                                                        │
│  │   Adapter       │ ← Concrete implementation                             │
│  │  ChanSubscriber │   provided by infrastructure                          │
│  └─────────────────┘                                                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Key Principle

**Library code (`pkg/`) should NOT depend on concrete implementations.**

- ✅ Library defines Port interface (`events.Emitter`, `events.Subscriber`)
- ✅ UI implements Adapter (`ChanEmitter`, `ChanSubscriber`)
- ❌ Library (`pkg/tui`) should NOT import business logic (`pkg/agent`)

## Violation #1: pkg/tui imports pkg/agent

### Current Code

**File**: [pkg/tui/model.go:38](../pkg/tui/model.go#L38)

```go
import (
    // ...
    "github.com/ilkoid/poncho-ai/pkg/agent"  // ⚠️ VIOLATION
    "github.com/ilkoid/poncho-ai/pkg/chain"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/state"
    // ...
)
```

### Problem

```go
// pkg/tui/model.go:157
type Model struct {
    // Dependencies
    agent     agent.Agent        // ⚠️ DIRECT DEPENDENCY ON AGENT
    coreState *state.CoreState   // ⚠️ STATE DEPENDENCY
    eventSub  events.Subscriber  // ✅ CORRECT: Port interface
    // ...
}
```

### Why This Is Wrong

1. **Circular dependency risk**: If `pkg/agent` imports `pkg/tui`, we have a cycle
2. **Tight coupling**: TUI cannot be used without `agent` package
3. **Violates Rule 6**: `pkg/` should be reusable, not tied to specific business logic
4. **Makes testing harder**: Cannot test TUI without mocking entire agent

### Correct Approach

```go
// pkg/tui/model.go — SHOULD BE
import (
    "github.com/ilkoid/poncho-ai/pkg/events"  // ✅ Only Port interface
    // NO import of pkg/agent
)

type Model struct {
    eventSub events.Subscriber  // ✅ Port interface only
    // NO agent.Agent field
}
```

### Business Logic in cmd/

```go
// cmd/interruption-test/main.go — CORRECT
import (
    "github.com/ilkoid/poncho-ai/pkg/agent"  // ✅ OK in cmd/
    "github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
    client, _ := agent.New(...)  // Agent creation in cmd/

    // Pass only Subscriber to TUI
    sub := client.Subscribe()
    model := tui.NewModel(sub)

    // Business logic callback (cmd/ layer)
    model.SetOnInput(func(query string) tea.Cmd {
        result, _ := client.Run(ctx, query)
        return func() tea.Msg { return result }
    })
}
```

## Violation #2: InterruptionModel requires agent.Client

### Current Code

**File**: [pkg/tui/model.go:897-914](../pkg/tui/model.go#L897-L914)

```go
func NewInterruptionModel(
    ctx context.Context,
    client *agent.Client,  // ⚠️ REQUIRES CONCRETE TYPE
    coreState *state.CoreState,
    eventSub events.Subscriber,
    inputChan chan string,
    chainCfg chain.ChainConfig,
) *InterruptionModel {
    // Creates base Model with agent dependency
    base := NewModel(ctx, client, coreState, eventSub)  // ⚠️

    return &InterruptionModel{
        base:       base,
        inputChan:  inputChan,
        chainCfg:   chainCfg,
        mu:         sync.RWMutex{},
    }
}
```

### Problem

`InterruptionModel` constructor requires `*agent.Client`, which:
- Creates tight coupling to agent implementation
- Prevents using TUI with other agent types
- Violates dependency inversion principle

### Correct Approach

```go
// pkg/tui/model.go — SHOULD BE
func NewInterruptionModel(
    ctx context.Context,
    eventSub events.Subscriber,  // ✅ Only Port interface
    inputChan chan string,
    config InterruptionConfig,   // Configuration struct
) *InterruptionModel {
    // No agent.Client parameter
}
```

## Violation #3: Direct ChainConfig dependency

### Current Code

**File**: [pkg/tui/model.go:857](../pkg/tui/model.go#L857)

```go
type InterruptionModel struct {
    base       *Model
    inputChan  chan string
    chainCfg   chain.ChainConfig  // ⚠️ DEPENDS ON chain PACKAGE
    // ...
}
```

### Problem

`InterruptionModel` stores `chain.ChainConfig`, which:
- Ties TUI to specific chain implementation
- Makes TUI less reusable
- Mixes infrastructure concerns with UI

### Correct Approach

```go
// Configuration instead of direct dependency
type InterruptionConfig struct {
    MaxIterations  int
    Timeout        time.Duration
    DebugEnabled   bool
    // UI-related config, not chain internals
}

type InterruptionModel struct {
    config    InterruptionConfig  // ✅ Generic config
    // ...
}
```

## Violation Matrix

| Component | Violation | Severity | Impact |
|-----------|-----------|----------|--------|
| `pkg/tui/model.go:Model` | Imports `pkg/agent` | 🔴 High | Tight coupling, violates Rule 6 |
| `pkg/tui/model.go:InterruptionModel` | Requires `*agent.Client` | 🔴 High | Not reusable |
| `pkg/tui/model.go:InterruptionModel` | Stores `chain.ChainConfig` | 🟡 Medium | Tied to chain impl |
| `internal/ui/model.go` | Imports `pkg/agent` | ✅ OK | App-specific layer |

## Exception: internal/ui is allowed to depend on pkg/agent

**File**: [internal/ui/model.go:11](../internal/ui/model.go#L11)

```go
package ui

import (
    "github.com/ilkoid/poncho-ai/pkg/agent"  // ✅ OK in internal/
    // ...
)
```

**Why This Is Correct**:
- `internal/` is app-specific, not reusable
- Rule 6 allows `internal/` to have app-specific logic
- This is the Adapter implementation layer

## Correct Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CORRECT PORT & ADAPTER                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  pkg/events/ (Port)                                                         │
│  ├─ Emitter interface     ────────────▶ pkg/agent depends on this          │
│  └─ Subscriber interface  ────────────▶ pkg/tui depends on this            │
│                                                                             │
│  pkg/agent/ (Library)                                                        │
│  └─ Uses Emitter interface (doesn't know about UI)                          │
│                                                                             │
│  pkg/tui/ (Library - UI Framework)                                          │
│  ├─ Uses Subscriber interface (doesn't know about agent)                    │
│  └─ Provides reusable UI components                                        │
│                                                                             │
│  internal/ui/ (App-Specific Adapter)                                        │
│  ├─ Implements Subscriber (creates events from agent)                       │
│  ├─ May import pkg/agent (business logic)                                   │
│  └─ App-specific UI features                                               │
│                                                                             │
│  cmd/*/main.go (Application)                                                │
│  ├─ Creates agent.Client                                                   │
│  ├─ Creates UI (pkg/tui or internal/ui)                                    │
│  ├─ Connects them via events                                               │
│  └─ Business logic callbacks                                               │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## SimpleTui: Correct Example

**File**: [pkg/tui/simple.go:54](../pkg/tui/simple.go#L54)

```go
import (
    "github.com/ilkoid/poncho-ai/pkg/events"  // ✅ Only Port
    // NO import of pkg/agent
)

type SimpleTui struct {
    subscriber events.Subscriber  // ✅ Port interface only
    onInput    func(input string) // Callback pattern
    // ...
}
```

**This is the correct pattern!**

## Summary

| Component | Current State | Should Be |
|-----------|---------------|-----------|
| `pkg/tui/model.go` | ❌ Imports `pkg/agent` | ✅ Only `events.Subscriber` |
| `pkg/tui/simple.go` | ✅ Only `events` | ✅ Already correct |
| `internal/ui/model.go` | ✅ Imports `pkg/agent` | ✅ OK (app-specific) |

---

**Next**: [03-DUPLICATION.md](./03-DUPLICATION.md) — Code duplication analysis
