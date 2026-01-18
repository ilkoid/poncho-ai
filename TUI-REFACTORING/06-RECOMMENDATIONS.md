# 06. Refactoring Recommendations

## Overview

This document provides recommendations for future refactoring of the TUI architecture. These are NOT immediate action items, but rather options to consider after reviewing the current state analysis.

## Guiding Principles

Based on your choices:
- **Document only** — No immediate refactoring
- **Port & Adapter compliance** — `pkg/tui` should NOT depend on `pkg/agent`
- **Preserve features** — Keep interruptions, viewport logic, event handling, key bindings

## Recommendation Options

### Option A: Incremental Primitives Extraction (Conservative)

**Approach**: Create reusable primitives without breaking existing code

```
pkg/tui/
├── primitives/          # NEW: Reusable low-level components
│   ├── viewport.go     # ViewportManager
│   ├── input.go        # InputManager
│   ├── status.go       # StatusBarManager
│   ├── events.go       # EventHandler
│   └── styles.go       # StyleConfig
├── model.go            # EXISTING: Model, InterruptionModel (unchanged)
├── simple.go           # EXISTING: SimpleTui (unchanged)
└── adapter.go          # EXISTING: EventMsg, etc.
```

**Pros**:
- No breaking changes
- Can adopt primitives incrementally
- Existing code continues to work
- Low risk

**Cons**:
- Doesn't fix Port & Adapter violations
- Adds more code (duplication during transition)
- Longer migration path

**Effort**: ~3-5 days

---

### Option B: Restore Port & Adapter Compliance (Moderate)

**Approach**: Refactor `pkg/tui` to only depend on `events.Subscriber`

```
pkg/tui/
├── base.go             # NEW: Base model (no agent dependency)
├── agent_aware.go      # NEW: Optional agent integration layer
├── simple.go           # REFACTORED: Use base model
└── adapter.go          # EXISTING: EventMsg, etc.

cmd/*/main.go
├── interruption-test/  # Use agent_aware layer
├── poncho/             # Use agent_aware layer
└── simple-agent/       # Use base model only
```

**Key Changes**:

```go
// pkg/tui/base.go — NO agent dependency
package tui

import "github.com/ilkoid/poncho-ai/pkg/events"

type BaseModel struct {
    eventSub  events.Subscriber  // Only Port interface
    // NO agent.Agent field
}

func NewBaseModel(sub events.Subscriber) *BaseModel {
    return &BaseModel{eventSub: sub}
}
```

```go
// pkg/tui/agent_aware.go — Optional agent integration
package tui

import "github.com/ilkoid/poncho-ai/pkg/agent"

// AgentAwareModel wraps BaseModel with agent-specific features
type AgentAwareModel struct {
    base   *BaseModel
    client *agent.Client  // Agent dependency here
}

func NewAgentAwareModel(client *agent.Client) *AgentAwareModel {
    return &AgentAwareModel{
        base:   NewBaseModel(client.Subscribe()),
        client: client,
    }
}
```

**Pros**:
- Fixes Port & Adapter violation
- Maintains feature parity
- Clear separation of concerns
- Can be done incrementally

**Cons**:
- Moderate refactoring effort
- Some breaking changes to API
- Need to update all `cmd/*/main.go`

**Effort**: ~1-2 weeks

---

### Option C: Full Redesign (Radical)

**Approach**: Complete restructure of `pkg/tui/` with new architecture

```
pkg/tui/
├── primitives/          # Low-level reusable components
│   ├── viewport/       # ViewportManager, smart scroll
│   ├── input/          # InputManager, validation
│   ├── status/         # StatusBarManager, spinner
│   ├── events/         # EventHandler (subscriber only)
│   └── styles/         # StyleConfig, themes
├── components/         # High-level UI components
│   ├── chat.go         # Chat interface (messages, input)
│   ├── panel.go        # Panel layout (todo, debug)
│   └── help.go         # Help system
├── layouts/            # Predefined layouts
│   ├── simple.go       # Single panel layout
│   ├── split.go        # Split panel layout
│   └── full.go         # Full layout with all panels
└── builder/            # Builder pattern for composition
    └── tui_builder.go  # Fluent API for building TUI
```

**Usage**:

```go
// cmd/interruption-test/main.go
builder := tui.NewBuilder()
builder.
    WithLayout(tui.FullLayout).
    WithEventSubscriber(client.Subscribe()).
    WithInterruptionSupport().
    WithDebugPanel().
    WithKeyboardShortcuts(tui.DefaultKeyMap())

model := builder.Build()
```

**Pros**:
- Clean architecture from ground up
- Maximum flexibility and reusability
- Easy to test
- No code duplication

**Cons**:
- Major effort
- High risk
- Breaking all existing code
- Long development time

**Effort**: ~3-4 weeks

---

## Specific Recommendations by Component

### 1. Viewport Management

**Current**: Duplicated in 3 places

**Recommendation**: Extract to `pkg/tui/primitives/viewport.go`

```go
type ViewportManager struct {
    viewport viewport.Model
    config   ViewportConfig
    mu       sync.RWMutex
}

func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg)
func (vm *ViewportManager) Append(content string, style lipgloss.Style)
func (vm *ViewportManager) PreserveScrollOnResize()
```

**Benefit**: Eliminate ~150 lines of duplication

---

### 2. Event Handling

**Current**: Duplicated event switching logic

**Recommendation**: Create configurable event handler

```go
type EventHandler struct {
    subscriber   events.Subscriber
    viewportMgr  *ViewportManager
    statusMgr    *StatusBarManager
    renderers    map[events.EventType]EventRenderer
}

type EventRenderer func(event events.Event) (string, lipgloss.Style)

func (eh *EventHandler) HandleEvent(event events.Event) tea.Cmd {
    if renderer, ok := eh.renderers[event.Type]; ok {
        content, style := renderer(event)
        eh.viewportMgr.Append(content, style)
    }
    return WaitForEvent(eh.subscriber)
}
```

**Benefit**: Eliminate ~180 lines of duplication, pluggable renderers

---

### 3. Interruption Mechanism

**Current**: Logic split across `pkg/chain`, `pkg/tui`, `cmd/`

**Recommendation**: Consolidate in `pkg/chain/`, keep TUI as pure event consumer

```
pkg/chain/
├── interruption.go      # Existing: loadInterruptionPrompt()
├── executor.go          # Existing: interruption check
└── interruption_handler.go  # NEW: Consolidated logic

pkg/tui/
└── model.go             # Remove interruption-specific code
                          # Only handle EventUserInterruption like any other event
```

**Benefit**: Cleaner separation, easier to test

---

### 4. Status Bar

**Current**: 3 different implementations

**Recommendation**: Extract to `pkg/tui/primitives/status.go`

```go
type StatusBarManager struct {
    spinner      spinner.Model
    status       string
    extraInfo    func() string
    config       StatusBarConfig
}

type StatusBarConfig struct {
    ShowSpinner    bool
    ShowTimestamp  bool
    ShowModelInfo  bool
    CustomStyler   func(status string) string
}
```

**Benefit**: Eliminate ~100 lines of duplication

---

## Migration Path (If Refactoring)

### Phase 1: Primitives (1 week)

1. Create `pkg/tui/primitives/` package
2. Extract `ViewportManager`
3. Extract `StatusBarManager`
4. Write tests for primitives

### Phase 2: Event Handler (1 week)

1. Create `EventHandler` in primitives
2. Add configurable renderers
3. Migrate `Model` to use `EventHandler`
4. Migrate `SimpleTui` to use `EventHandler`

### Phase 3: Port & Adapter (1 week)

1. Create `BaseModel` without agent dependency
2. Create `AgentAwareModel` as wrapper
3. Update `cmd/*/main.go` to use appropriate model
4. Deprecate old `Model` constructor

### Phase 4: Cleanup (3-5 days)

1. Remove deprecated code
2. Update documentation
3. Add examples
4. Performance testing

---

## Risk Assessment

| Option | Risk Level | Breaking Changes | Time to Complete |
|--------|------------|------------------|------------------|
| A: Primitives only | 🟢 Low | None | 3-5 days |
| B: Port & Adapter | 🟡 Medium | Some | 1-2 weeks |
| C: Full redesign | 🔴 High | Major | 3-4 weeks |

---

## Decision Framework

Choose **Option A** if:
- You want minimal risk
- You're okay with temporary duplication
- You prefer gradual evolution

Choose **Option B** if:
- You want to fix Port & Adapter violations
- You prefer moderate, controlled changes
- You can afford 1-2 weeks

Choose **Option C** if:
- You're starting a new major version
- You want maximum architectural purity
- You have 3-4 weeks and tolerance for risk

Choose **Do Nothing** if:
- Current code works fine
- You have higher priorities
- You want to gather more requirements first

---

## Immediate Actions (Regardless of Option)

1. ✅ Document current architecture (DONE — this directory)
2. ⏳ Discuss with team/stakeholders
3. ⏳ Prioritize features vs technical debt
4. ⏳ Decide on refactoring approach
5. ⏳ Create detailed implementation plan (if proceeding)

---

## Questions to Consider

Before refactoring, answer:

1. **Priority**: Is fixing TUI architecture higher than new features?
2. **Resources**: Who will do the refactoring? What's their availability?
3. **Testing**: Do we have tests to ensure nothing breaks?
4. **Migration**: Can we afford to break existing `cmd/*/main.go` files?
5. **Users**: Will anyone be affected by API changes?

---

## Summary

| Aspect | Current State | Recommended Future |
|--------|---------------|-------------------|
| Agent coupling | `pkg/tui` imports `pkg/agent` | `pkg/tui` only uses `events.Subscriber` |
| Duplication | ~635 lines duplicated | Extract to primitives (~320 saved) |
| Interruptions | Logic split across layers | Consolidate in `pkg/chain/` |
| Architecture | Mixed concerns | Clear Port & Adapter separation |

**My recommendation**: Start with **Option A** (primitives extraction) to reduce duplication with minimal risk, then consider **Option B** (Port & Adapter compliance) if time permits.

---

**Next**: [07-PLAN.md](./07-PLAN.md) — Original plan from Claude
