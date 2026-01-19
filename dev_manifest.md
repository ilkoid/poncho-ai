# Immutable Development Rules

## Core Rules

### 0. Rule of Code Reuse
Any development in the codebase should first use existing solutions. If existing code blocks development, it can be replaced (refactoring).

### 1. Rule of Tool Interface
```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```
**NEVER change this interface.** All tools must implement only this contract.
"Raw In, String Out" - this principle remains immutable.

### 2. Rule of Configuration
All settings must be in a single YAML with ENV variable support. No hardcoding in code.
AppConfig structure can extend, but existing fields don't change.
YAML config lies next to the executable of each utility.

### 3. Rule of Tool Registry
All tools register via `Registry.Register()`. No direct tool calls bypassing the registry.

### 4. Rule of LLM Abstraction
Work with AI models only through the `Provider` interface. No direct API calls to specific providers in business logic.

### 5. Rule of State Management
Global state only through `GlobalState` with thread-safe access. No global variables.

### 6. Rule of Package Structure ⭐ (Port & Adapter Pattern)
```
pkg/       - Library code, ready for reuse
internal/  - Application-specific logic
cmd/       - Entry points, only initialization and orchestration
```

**Port & Adapter Compliance:**
- ✅ Library (`pkg/`) defines Port interface (`events.Emitter`, `events.Subscriber`)
- ✅ Adapter (`pkg/tui`, `internal/ui`) implements Port
- ❌ Library (`pkg/tui`) must NOT import business logic (`pkg/agent`, `pkg/chain`)
- ✅ Business logic injection via **callback pattern** from `cmd/` layer

**Example - Correct (Rule 6 compliant):**
```go
// pkg/tui/simple.go - Library code
import "github.com/ilkoid/poncho-ai/pkg/events"  // ✅ Only Port interface

type SimpleTui struct {
    subscriber events.Subscriber  // ✅ Port interface only
    onInput    func(input string) // Callback for business logic
}
```

**Example - Incorrect (Rule 6 violation):**
```go
// pkg/tui/model.go - Library code
import "github.com/ilkoid/poncho-ai/pkg/agent"  // ❌ Violates Rule 6

type Model struct {
    agent agent.Agent  // ❌ Tight coupling to business logic
}
```

### 7. Rule of Error Handling
All errors must return up the call stack. No `panic()` in business logic.
Framework must ensure resilience against LLM hallucinations.

### 8. Rule of Extensibility
New features added only through:
- New tools in `pkg/tools/std/` or custom packages
- New LLM adapters in `pkg/llm/`
- Config extensions (breaking changes allowed with user notification)

### 9. Rule of Testing
Each tool must have mockable dependencies for unit tests. No direct HTTP calls without abstraction.
**Instead of tests initially:** Prepare utility in `/examples` for verification.
Config, prompts, logs - all should lie next to the utility. Runs from its own folder.

### 10. Rule of Documentation
All public APIs must have godoc comments. Interface changes must update usage examples.

### 11. Rule of Context Propagation
All long-running operations must accept and respect `context.Context` through all layers.

**Requirements:**
- All `Tool.Execute()` methods must respect cancellation
- LLM calls pass context through all layers
- HTTP clients use context for requests
- Background goroutines inherit parent context
- Use `select` for context checks in loops

### 12. Rule of Security & Secrets
Never hardcode secrets. Use ENV variables `${VAR}` or secret management.
Validate all inputs, redact sensitive data in logs, use HTTPS only.

### 13. Rule of Resource Localization
Any app in `/cmd` or in `/example` must be autonomous and store resources nearby:
- **Prompts**: `{app_dir}/prompts/` (flat structure, no nested folders)
- **Config**: `{app_dir}/config.yaml` (next to executable)
- **Logs**: `{app_dir}/logs/` or stdout for CLI utilities

Each `/cmd` app implements `ConfigPathFinder`, searching `config.yaml` only in its directory.

---

## Architectural Patterns

### Port & Adapter Pattern
Library depends on Port interface, Adapter implements Port:
- `pkg/events` - Port (interfaces: `Emitter`, `Subscriber`)
- `pkg/tui` - Adapter (implements `Subscriber`)
- `pkg/agent` - Library (uses `Emitter` interface)

**Dependency Direction:**
```
Library (pkg/agent) → Port (events.Emitter) ← Adapter (pkg/tui)
```

### Primitives-Based Architecture (TUI)
UI components built from reusable primitives in `pkg/tui/primitives/`:

| Primitive | Purpose | Pattern |
|-----------|---------|---------|
| **ViewportManager** | Smart scroll, resize handling | Repository |
| **StatusBarManager** | Spinner, status bar, DEBUG indicator | State |
| **EventHandler** | Pluggable event renderers | Strategy |
| **InterruptionManager** | User input, channel, **MANDATORY callback** | Callback |
| **DebugManager** | Screen save, debug mode, JSON logs | Facade |

**Key Principles:**
- Composition over inheritance
- Each primitive has Single Responsibility
- Thread-safe via `sync.RWMutex`
- Callback pattern for business logic injection (Rule 6 compliant)

### Event System Flow
Six-phase flow from agent to UI:

1. **Emission** - Agent emits events via `Emitter.Emit(ctx, Event)`
2. **Transport** - `ChanEmitter` sends to buffered channel (size=100)
3. **Subscription** - `Subscriber.Events()` returns read-only channel
4. **Conversion** - `EventMsg` wraps `events.Event` as Bubble Tea message
5. **Processing** - TUI `Update()` handles `EventMsg`, updates state
6. **Rendering** - Bubble Tea renders updated `View()`

**Event Types:**
- `EventThinking` - Agent starts thinking
- `EventThinkingChunk` - Streaming reasoning content
- `EventToolCall` - Tool execution started
- `EventToolResult` - Tool execution completed
- `EventUserInterruption` - User interrupted execution
- `EventMessage` - Agent generated message
- `EventError` - Error occurred
- `EventDone` - Agent finished

### Interruption Mechanism
User can interrupt agent execution in real-time:

**Flow:**
```
User (types "todo: add test") → TUI → inputChan (size=10) →
ReActExecutor (checks between iterations) →
loadInterruptionPrompt() (YAML or fallback) →
Emit EventUserInterruption → TUI displays interruption
```

**Key Features:**
- Buffered channel (size=10) for inter-goroutine communication
- Non-blocking checks via `select` with `default` case
- YAML configuration: `chains.default.interruption_prompt`
- Fallback to default prompt if YAML missing
- Event emission via `EventUserInterruption`

**Configuration (`config.yaml`):**
```yaml
chains:
  default:
    interruption_prompt: "prompts/interruption_handler.yaml"
```

---

## Special Emphases

**"Raw In, String Out" (Rule 1):** Best solution for LLM tools. Typed arguments at interface level would create hell with `interface{}` and reflection. Each tool knows how to parse its JSON - infinite flexibility.

**Registry (Rule 3):** Transforms Poncho from script to modular system. Can build binary with one tool set for admins, another for users - just change `main.go`, no core changes.

**LLM Abstraction (Rule 4):** Critical. Today OpenAI is trendy, tomorrow DeepSeek, day after local Llama. `Provider` interface guarantees framework survives hype changes.

**Error Handling & Resilience (Rule 7):** More important for AI agents than web. Model will err, output broken JSON. No panic + proper error return = only way to build stable robot.

**Port & Adapter (Rule 6):** **THE MOST CRITICAL RULE.** Eliminates circular dependencies, enables testing, makes `pkg/` truly reusable. TUI refactoring eliminated `pkg/tui` → `pkg/agent` dependency via callback pattern.

---

**Last Updated:** 2026-01-19
**Version:** 7.0 (English, TUI-REFACTORING integration)
