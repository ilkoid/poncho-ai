# TUI Architecture Refactoring Analysis

> **Status**: 📋 Documentation Phase
> **Created**: 2026-01-18
> **Goal**: Analyze current TUI architecture and plan refactoring

## Overview

This directory contains comprehensive analysis of the Poncho AI TUI architecture, focusing on:

1. **Port & Adapter Pattern Compliance** — Is `pkg/tui` properly decoupled from `pkg/agent`?
2. **Code Duplication** — Where are we duplicating viewport, event handling, and styling logic?
3. **Agent-TUI Integration** — How do agent events flow to the UI?
4. **Interruption Mechanism** — How does user interruption work across layers?

## Current Decision

**User Choice**: Document only — no immediate refactoring

- ✅ Document current architecture
- ✅ Identify violations and duplication
- ✅ Provide recommendations for future
- ❌ No code changes without approval

## Architecture Principles

### Port & Adapter Pattern

```
Library (pkg/agent) ──depends on──▶ Port (events.Emitter interface)
                                     ▲
                                     │
UI (pkg/tui, internal/ui) ──implements──▶ Adapter (events.Subscriber)
```

**Key Rule**: `pkg/tui` should NOT depend on `pkg/agent`. Only on `events.Subscriber` interface.

### Current Violation

```go
// pkg/tui/model.go — VIOLATES Port & Adapter
import "github.com/ilkoid/poncho-ai/pkg/agent"

type Model struct {
    agent     agent.Agent  // ← Tight coupling!
    eventSub  events.Subscriber  // ← Correct: Port interface
}
```

### Should Be

```go
// pkg/tui/model.go — CORRECT Port & Adapter
import "github.com/ilkoid/poncho-ai/pkg/events"

type Model struct {
    eventSub  events.Subscriber  // ← Only Port interface
}
```

## Document Structure

```
TUI-REFACTORING/
├── README.md              ← This file (overview)
├── 01-CURRENT-STATE.md    ← Current architecture analysis
├── 02-VIOLATIONS.md       ← Port & Adapter violations
├── 03-DUPLICATION.md      ← Code duplication matrix
├── 04-INTERRUPTIONS.md    ← Interruption mechanism analysis
├── 05-EVENT-FLOW.md       ← Event flow diagrams
├── 06-RECOMMENDATIONS.md  ← Future refactoring recommendations
└── 07-PLAN.md            ← Original plan from Claude
```

## Key Files Referenced

| File | Purpose | Lines | Priority |
|------|---------|-------|----------|
| [pkg/tui/model.go](../pkg/tui/model.go) | Base TUI + InterruptionModel | ~1300 | 🔴 High |
| [pkg/tui/simple.go](../pkg/tui/simple.go) | Minimalist TUI | ~400 | 🟡 Medium |
| [pkg/events/events.go](../pkg/events/events.go) | Event types (Port) | ~190 | 🔴 High |
| [pkg/events/emitter.go](../pkg/events/emitter.go) | Emitter/Subscriber | ~100 | 🔴 High |
| [pkg/chain/executor.go](../pkg/chain/executor.go) | ReAct execution | ~400 | 🔴 High |
| [pkg/chain/interruption.go](../pkg/chain/interruption.go) | Interruption logic | ~100 | 🔴 High |
| [internal/ui/model.go](../internal/ui/model.go) | App-specific TUI | ~1000 | 🟡 Medium |
| [cmd/interruption-test/main.go](../cmd/interruption-test/main.go) | Example usage | ~170 | 🔴 High |

## Next Steps

1. Review all documents in this directory
2. Discuss trade-offs of different approaches
3. Decide on refactoring strategy (if any)
4. Create implementation plan based on decisions

---

**Last Updated**: 2026-01-18
