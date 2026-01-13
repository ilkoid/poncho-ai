# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct pattern.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings.

**Architecture**:
- `pkg/state/CoreState` - Framework core (reusable, includes e-commerce helpers)
- `internal/ui/` - TUI-specific (stores UI state separately from CoreState)
- `pkg/chain/ReActCycle` - Implements both Chain and Agent interfaces
- `pkg/app/components.go` - Component initialization with context propagation (Rule 11 compliant)
- `pkg/agent/Client` - Simple 2-line agent API (Facade pattern)
- `pkg/events/` - Port & Adapter pattern for UI decoupling
- Rule 6 Compliant: `pkg/` has NO imports from `internal/`

---

## Architecture Overview

### High-Level Structure

```
poncho-ai/
├── cmd/                    # Application entry points (autonomous utilities)
│   ├── poncho/            # Main TUI application (primary interface)
│   ├── maxiponcho/        # Fashion PLM analyzer (TUI)
│   ├── vision-cli/        # CLI utility for vision analysis
│   ├── chain-cli/         # CLI utility for testing Chain Pattern
│   ├── debug-test/        # CLI utility for testing debug logs
│   ├── tools-test/        # CLI utility for testing all enabled tools
│   ├── todo-agent/        # Standalone TUI for task management
│   ├── wb-ping-util-v2/   # Example of new 2-line agent API
│   ├── simple-agent/      # Minimal agent implementation example
│   ├── streaming-test/    # NEW: Streaming functionality test
│   └── wb-tools-test/     # CLI utility for testing WB tools
├── internal/              # Application-specific logic
│   └── ui/               # Bubble Tea TUI (app-specific implementation)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent Client facade (2-line agent API)
│   ├── app/              # Component initialization (shared across entry points)
│   ├── chain/            # Chain Pattern + ReAct implementation (modular agent execution)
│   ├── classifier/       # File classification engine
│   ├── config/           # YAML configuration with ENV support
│   ├── debug/            # JSON debug logging system
│   ├── events/           # Port & Adapter: Event interfaces (Emitter, Subscriber)
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer + options + streaming support
│   ├── models/           # Model Registry (centralized LLM provider management)
│   ├── prompt/           # Prompt loading and rendering + post-prompts
│   ├── s3storage/        # S3-compatible storage client
│   ├── state/            # Framework core state (CoreState)
│   ├── tui/              # Reusable TUI helpers (adapter for Bubble Tea)
│   ├── todo/             # Thread-safe task manager
│   ├── tools/            # Tool system (registry + std tools)
│   ├── utils/            # JSON sanitization utilities
│   └── wb/               # Wildberries API client
└── config.yaml           # Main configuration file
```

---

## The 13 Immutable Rules

| Rule | Description |
|------|-------------|
| **0: Code Reuse** | Use existing solutions first. Deep refactoring doesn't need backward compatibility unless explicitly stated |
| **1: Tool Interface** | NEVER change - `Definition() ToolDefinition`, `Execute(ctx, argsJSON string) (string, error)` |
| **2: Configuration** | YAML with ENV support only |
| **3: Registry** | All tools via `Registry.Register()` |
| **4: LLM Abstraction** | Work through `Provider` interface only |
| **5: State** | Layered, thread-safe, no globals |
| **6: Package Structure** | `pkg/` = reusable, `internal/` = app-specific, `cmd/` = test utilities only |
| **7: Error Handling** | No `panic()` in business logic |
| **8: Extensibility** | Add via tools, LLM adapters, config |
| **9: Testing** | Use CLI utilities in `/cmd` (test purpose only) |
| **10: Documentation** | Godoc on public APIs |
| **11: Context Propagation** | All long-running operations must accept and respect `context.Context` through all layers |
| **12: Security & Secrets** | Never hardcode secrets. Use ENV vars `${VAR}`, validate inputs, redact sensitive data in logs, HTTPS only |
| **13: Resource Localization** | Autonomous `/cmd` apps with local config/prompts |

**Rule 11 Compliance Status**: ✅ **FULLY COMPLIANT** (2026-01-12)
- `pkg/app/components.go`: `Initialize(parentCtx, ...)` and `Execute(parentCtx, ...)`
- `pkg/tui/`: Context stored in Model struct for Bubble Tea integration
- All entry points pass `context.Background()` or parent context

---

## Core Components

### Tool System (`pkg/tools/`)

**Interface**:
```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

**Categories**: WB Content/Feedbacks API, WB Dictionaries, S3 Basic/Batch, Planner.

**Key**: Registry Pattern, thread-safe, YAML-driven.

### Model Registry (`pkg/models/`)

Centralized LLM provider management with dynamic model switching.

**Features**:
- All models from `config.yaml` registered at startup
- Thread-safe operations via `sync.RWMutex`
- Fallback mechanism: `GetWithFallback(requested, default)`
- Runtime model switching via post-prompts

**Usage**:
```go
// Register models from config
registry, err := models.NewRegistryFromConfig(cfg)

// Get provider for specific model
provider, modelDef, err := registry.Get("glm-4.6")

// Get with fallback
provider, modelDef, actualModel, err := registry.GetWithFallback("custom-model", "glm-4.6")
```

### LLM Abstraction (`pkg/llm/`)

**Options Pattern**:
```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}

// Usage
llm.Generate(ctx, messages, llm.WithModel("glm-4.6"), llm.WithTemperature(0.5))
```

**Streaming Support** (NEW):

```go
type StreamingProvider interface {
    Provider  // Embedded for backward compatibility

    GenerateStream(
        ctx context.Context,
        messages []Message,
        callback func(StreamChunk),
        opts ...any,
    ) (Message, error)
}

// StreamChunk represents a portion of streaming response
type StreamChunk struct {
    Type             ChunkType  // ChunkThinking, ChunkContent, ChunkError, ChunkDone
    Content          string     // Accumulated content
    ReasoningContent string     // Accumulated reasoning_content (thinking mode)
    Delta            string     // Incremental changes (for real-time UI)
    Done             bool
    Error            error
}
```

**Streaming Configuration** (`config.yaml`):
```yaml
app:
  streaming:
    enabled: true        # Opt-out design (default: true)
    thinking_only: true  # Only send reasoning_content events
```

**Streaming Usage**:
```go
if streamingProvider, ok := provider.(llm.StreamingProvider); ok {
    response, err := streamingProvider.GenerateStream(
        ctx,
        messages,
        func(chunk llm.StreamChunk) {
            switch chunk.Type {
            case llm.ChunkThinking:
                // Handle reasoning_content
                fmt.Print(chunk.Delta)
            case llm.ChunkContent:
                // Handle regular content
                fmt.Print(chunk.Delta)
            }
        },
        llm.WithStream(true),
        llm.WithThinkingOnly(true),
    )
}
```

### ReActCycle (`pkg/chain/`)

**PHASE 1-5 REFACTOR COMPLETE**: Template-Execution separation with Observer pattern.

Implements both **Chain** and **Agent** interfaces with streaming and event support:

```go
// Chain - full control
output, err := reactCycle.Execute(ctx, chain.ChainInput{
    UserQuery: "What categories exist?",
    State:     coreState,
    Registry:  toolsRegistry,
})

// Agent - simple
result, err := reactCycle.Run(ctx, query)
history := reactCycle.GetHistory()
```

**Architecture**:
- **ReActCycle**: Immutable template (thread-safe for concurrent Execute())
- **ReActExecution**: Runtime state (created per execution, never shared)
- **ReActExecutor**: Orchestrates iteration loop with observer notifications
- **Observers**: Handle cross-cutting concerns (debug, events)

**Key Features**:
- ✅ **Concurrent Execution**: Multiple Execute() calls can run simultaneously
- ✅ **Type-Safe Signals**: ExecutionSignal enum (SignalFinalAnswer, SignalNeedUserInput, etc.)
- ✅ **Observer Pattern**: ChainDebugRecorder, EmitterObserver, EmitterIterationObserver
- ✅ **Event Emitter**: Integration with pkg/events for UI decoupling
- ✅ **Streaming Support**: StreamingProvider for real-time responses
- ✅ **Context Propagation**: All methods accept `context.Context` (Rule 11 compliant)

**Thread Safety**:
- Multiple goroutines can call Execute() concurrently
- No global mutex held during LLM calls or tool execution
- Each execution gets isolated ReActExecution instance

**Note**: `internal/agent/orchestrator.go` was DELETED. Use ReActCycle instead.
**See Also**: `pkg/chain/` section below for detailed architecture

### Simple Agent API (`pkg/agent/`)

**Facade Pattern**: Ultra-simple API for creating AI agents in **2 lines**.

```go
// Before (50+ lines of boilerplate):
cfg, _ := config.Load(path)
comps, _ := app.Initialize(cfg, 10, "")
cycleConfig := chain.ReActCycleConfig{...}
reactCycle := chain.NewReActCycle(cycleConfig)
reactCycle.SetModelRegistry(...)
reactCycle.SetRegistry(...)
reactCycle.SetState(...)
input := chain.ChainInput{...}
output, _ := reactCycle.Execute(ctx, input)

// After (2 lines):
client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
result, _ := client.Run(ctx, query)
```

**Basic Usage**:
```go
import "github.com/ilkoid/poncho-ai/pkg/agent"

func main() {
    client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
    result, _ := client.Run(context.Background(), "Find products under 1000₽")
    fmt.Println(result)
}
```

**With Custom Tool**:
```go
client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
client.RegisterTool(&MyPriceChecker{})
result, _ := client.Run(ctx, "Check price of SKU123")
```

**Advanced Access** (when needed):
```go
registry := client.GetModelRegistry()  // Direct model access
tools := client.GetToolsRegistry()     // Direct tool access
state := client.GetState()             // Direct CoreState access
cfg := client.GetConfig()              // Direct config access
```

**Features**:
- ✅ Auto-loads config.yaml
- ✅ Auto-registers tools (only `enabled: true`)
- ✅ Creates ModelRegistry, ToolsRegistry, CoreState automatically
- ✅ Thread-safe
- ✅ Compatible with both TUI and CLI
- ✅ No circular imports (Agent interface in `pkg/chain`)
- ✅ Streaming support via event system

**Events Support** (Port & Adapter):
```go
client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})

// Set emitter for UI integration
emitter := events.NewChanEmitter(100)
client.SetEmitter(emitter)

// Subscribe to agent events
sub := client.Subscribe()
for event := range sub.Events() {
    switch event.Type {
    case events.EventThinking:
        ui.showSpinner()
    case events.EventThinkingChunk:
        // Handle streaming reasoning content
        ui.updateThinking(event.Data.(events.ThinkingChunkData).Chunk)
    case events.EventMessage:
        ui.showMessage(event.Data.(string))
    case events.EventDone:
        ui.showResult(event.Data.(string))
    }
}
```

### Event System (`pkg/events/`)

**Port & Adapter Pattern**: Decouple agent logic from UI implementation through event interfaces.

**Interfaces**:
```go
// Emitter - Port for sending events (used by pkg/agent, pkg/chain)
type Emitter interface {
    Emit(ctx context.Context, event Event)
}

// Subscriber - Port for receiving events (used by UI)
type Subscriber interface {
    Events() <-chan Event
    Close()
}
```

**Event Types**:
- `EventThinking` - Agent starts thinking
- `EventThinkingChunk` - **NEW**: Streaming reasoning content (for thinking mode)
- `EventToolCall` - Tool execution started
- `EventToolResult` - Tool execution completed
- `EventMessage` - Agent generated message
- `EventError` - Error occurred
- `EventDone` - Agent finished

**Event Data Structures**:
```go
// ThinkingChunkData - streaming reasoning content
type ThinkingChunkData struct {
    Chunk       string // Delta (new content)
    Accumulated string // Full accumulated content
}

// ToolCallData - tool invocation info
type ToolCallData struct {
    ToolName string
    Args     string
}

// ToolResultData - tool execution result
type ToolResultData struct {
    ToolName string
    Result   string
    Duration time.Duration
}
```

**ChanEmitter** - Standard implementation:
```go
emitter := events.NewChanEmitter(100) // buffered
sub := emitter.Subscribe()
emitter.Emit(ctx, events.Event{Type: events.EventThinking, Data: "query"})
```

**Thread-safe**, respects `context.Context` (Rule 11).

### TUI Package (`pkg/tui/`)

**Purpose**: Adapter layer between `pkg/events` and Bubble Tea framework.

**Components**:
- `adapter.go` - EventMsg type, ReceiveEventCmd, WaitForEvent
- `model.go` - Base TUI Model with agent integration and context storage
- `run.go` - Ready-to-use TUI runner

**Basic Usage**:
```go
import "github.com/ilkoid/poncho-ai/pkg/tui"

client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})

// 1. Simple: use pre-built TUI
if err := tui.Run(context.Background(), client); err != nil {
    log.Fatal(err)
}

// 2. Advanced: customize
err := tui.RunWithOpts(context.Background(), client,
    tui.WithTitle("My AI App"),
    tui.WithPrompt("> "),
)
```

**Context Handling** (Rule 11):
- Model stores `ctx context.Context` field
- `Run()` accepts context as first parameter
- Context propagated through all agent operations

**Architecture**:
- `pkg/events.*` - Port (interfaces)
- `pkg/tui.*` - Adapter helpers (reusable utilities)
- `internal/ui.*` - Concrete TUI implementation (app-specific)

**Rule 6 Compliant**: Only reusable code in `pkg/tui`, no app-specific logic.

### App Initialization (`pkg/app/`)

**Rule 11 Compliant**: Context propagation through all layers.

```go
// Initialize creates all components with context propagation
func Initialize(
    parentCtx context.Context,  // NEW: Rule 11 compliance
    cfg *config.AppConfig,
    maxIters int,
    systemPrompt string,
) (*Components, error)

// Execute runs agent with context propagation
func Execute(
    parentCtx context.Context,  // NEW: Rule 11 compliance
    c *Components,
    query string,
    timeout time.Duration,
) (*ExecutionResult, error)
```

**Usage**:
```go
// CLI entry point
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

components, err := app.Initialize(ctx, cfg, 10, "")
result, err := app.Execute(ctx, components, query, timeout)
```

**Components Structure**:
```go
type Components struct {
    Config         *config.AppConfig
    State          *state.CoreState
    ModelRegistry  *models.Registry
    ToolsRegistry  *tools.Registry
    ReActCycle     *chain.ReActCycle
}
```

### Tool Post-Prompts

Special prompts that activate after tool execution to guide LLM response AND override model parameters.

**Config** (`config.yaml`):
```yaml
tools:
  get_wb_parent_categories:
    post_prompt: "wb/parent_categories_analysis.yaml"
```

**Prompt** (`prompts/wb/parent_categories_analysis.yaml`):
```yaml
config:
  model: "glm-4.6"
  temperature: 0.7
messages:
  - role: system
    content: "Format as table..."
```

### State Management

**Repository Pattern** with type-safe operations:

```go
// Typed keys
type Key string
const (KeyHistory, KeyFiles, KeyTodo, KeyDictionaries, KeyStorage, KeyToolsRegistry Key)

// Generic helpers
GetType[T any](s *CoreState, key Key) (T, bool)
SetType[T any](s *CoreState, key Key, value T) error
UpdateType[T any](s *CoreState, key Key, fn func(T) T) error
```

**CoreState** (`pkg/state/core.go`):
- Dependencies: Config, S3, Dictionaries, Todo, ToolsRegistry
- Thread-safe storage: `mu sync.RWMutex`, `store map[string]any`
- Implements: MessageRepository, FileRepository, TodoRepository, DictionaryRepository, StorageRepository
- E-commerce helpers: `SetCurrentArticle()`, `GetCurrentArticleID()`, `GetCurrentArticle()`

### Chain Pattern (`pkg/chain/`)

**PHASE 1-5 REFACTOR COMPLETE**: Template-Execution separation with Observer pattern.

#### Architecture Overview

The chain package implements the ReAct (Reasoning + Acting) pattern with a clean separation
between immutable template (ReActCycle) and runtime state (ReActExecution).

```
ReActCycle (Immutable Template)
    ↓ Execute()
ReActExecution (Runtime State)
    ↓ Execute()
ReActExecutor (Orchestrator)
    ↓ Observer Notifications
Observers (Debug, Events)
```

#### Core Components

**ReActCycle** (Immutable Template):
- Created once, shared across all Execute() calls
- Thread-safe for concurrent execution
- Holds: registries, config, step templates, runtime defaults
- No global mutex - uses RWMutex only for runtime defaults

**ReActExecution** (Runtime State):
- Created per Execute() invocation
- Never shared between goroutines
- Pure data container with no execution logic
- Holds: chain context, step instances, emitter, debug recorder

**ReActExecutor** (Orchestrator):
- Implements StepExecutor interface
- Executes iteration loop with observer notifications
- Coordinates LLM and Tool steps

**Observers** (Cross-Cutting Concerns):
- ChainDebugRecorder: Records debug logs (implements ExecutionObserver)
- EmitterObserver: Sends final events (EventDone, EventError)
- EmitterIterationObserver: Sends iteration events (EventThinking, EventToolCall, etc.)

#### Execution Signals (Type-Safe)

```go
type ExecutionSignal int

const (
    SignalNone ExecutionSignal = iota  // Continue to next step
    SignalFinalAnswer                    // Execution complete
    SignalNeedUserInput                  // Waiting for user input
    SignalError                          // Execution failed
)

type StepResult struct {
    Action NextAction
    Signal ExecutionSignal
    Error  error
}
```

#### Step Interface

```go
type Step interface {
    Name() string
    Execute(ctx context.Context, chainCtx *ChainContext) StepResult
}
```

#### StepExecutor Interface

```go
type StepExecutor interface {
    Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error)
}
```

#### ExecutionObserver Interface

```go
type ExecutionObserver interface {
    OnStart(ctx context.Context, exec *ReActExecution)
    OnIterationStart(iteration int)
    OnIterationEnd(iteration int)
    OnFinish(result ChainOutput, err error)
}
```

#### Thread Safety

- **ReActCycle**: Thread-safe for concurrent Execute() calls
  - Immutable fields: No synchronization needed
  - Runtime defaults: Protected by sync.RWMutex
  - Multiple goroutines can call Execute() simultaneously

- **ReActExecution**: Not thread-safe (never shared)
  - Created per execution, used by only one goroutine
  - No synchronization needed

- **ReActExecutor**: Thread-safe with isolated executions
  - Observers list set before execution
  - Each execution uses isolated ReActExecution

#### Usage Examples

**Chain Interface** (full control):
```go
output, err := reactCycle.Execute(ctx, chain.ChainInput{
    UserQuery: "What categories exist?",
    State:     coreState,
    Registry:  toolsRegistry,
})
```

**Agent Interface** (simple):
```go
result, err := reactCycle.Run(ctx, query)
history := reactCycle.GetHistory()
```

**Concurrent Execution**:
```go
// Multiple concurrent Execute() calls are safe
var wg sync.WaitGroup
for i := 0; i < 5; i++ {
    wg.Add(1)
    go func(query string) {
        defer wg.Done()
        output, _ := reactCycle.Execute(ctx, chain.ChainInput{
            UserQuery: query,
        })
    }(queries[i])
}
wg.Wait()
```

**Key Features**:
- ✅ Template-Execution separation (Phase 1)
- ✅ Type-safe execution signals (Phase 2)
- ✅ Real step pipeline with StepExecutor (Phase 3)
- ✅ Observer pattern for cross-cutting concerns (Phase 4)
- ✅ Comprehensive documentation and architecture contracts (Phase 5)
- ✅ Debug support via ChainDebugRecorder (ExecutionObserver)
- ✅ Event emission via EmitterObserver and EmitterIterationObserver
- ✅ Streaming support via StreamingProvider
- ✅ Context propagation (Rule 11)

**Files**:
- `react.go` - ReActCycle template
- `execution.go` - ReActExecution runtime state
- `executor.go` - ReActExecutor and ExecutionObserver interface
- `observers.go` - EmitterObserver, EmitterIterationObserver
- `step.go` - Step interface, ExecutionSignal, StepResult
- `llm_step.go` - LLMInvocationStep implementation
- `tool_step.go` - ToolExecutionStep implementation
- `debug.go` - ChainDebugRecorder (ExecutionObserver implementation)

**See Also**: ADR-007.md for complete refactoring documentation

### Debug System (`pkg/debug/`)

JSON trace recording. Configure in `config.yaml`:
```yaml
app:
  debug_logs:
    enabled: true
    logs_dir: "./debug_logs"
    include_tool_args: true
```

### Configuration (`config.yaml`)

```yaml
app:
  streaming:
    enabled: true        # Opt-out design (default: true)
    thinking_only: true  # Only send reasoning_content events
  debug_logs:
    enabled: false
    logs_dir: "./debug_logs"

models:
  default_reasoning: "glm-4.6"
  default_chat: "glm-4.6"
  default_vision: "glm-4.6v-flash"
  definitions:
    glm-4.6:
      provider: "zai"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5
      thinking: "enabled"  # Zai GLM deep reasoning mode

tools:
  get_wb_parent_categories:
    enabled: true
    endpoint: "https://content-api.wildberries.ru"

s3:
  endpoint: "storage.yandexcloud.net"
  bucket: "plm-ai"
```

---

## Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Facade** | `pkg/agent/Client` | Simple 2-line API over ReActCycle |
| **Port & Adapter** | `pkg/events/`, `pkg/tui/` | Decouple agent from UI implementation |
| **Repository** | `pkg/state/` | Unified storage with domain interfaces |
| **Registry** | `pkg/tools/`, `pkg/models/` | Tool and Model registration/discovery |
| **Factory** | `pkg/factory/` | LLM provider creation |
| **Options** | `pkg/llm/`, `pkg/llm/streaming_options.go` | Runtime parameter overrides |
| **Command** | `internal/ui/` | TUI command handling (local, not in pkg/) |
| **ReAct** | `pkg/chain/` | Agent reasoning loop |
| **Chain of Responsibility** | `pkg/chain/` | Modular step-based execution |
| **Template-Execution** | `pkg/chain/` | Immutable template + runtime state (Phase 1) |
| **Observer** | `pkg/chain/` | Cross-cutting concerns (debug, events) (Phase 4) |
| **Recorder** | `pkg/debug/` | JSON trace recording |
| **Streaming** | `pkg/llm/StreamingProvider` | Real-time response streaming |

---

## Building and Running

```bash
# Main TUI
go run cmd/poncho/main.go

# Chain CLI
go run cmd/chain-cli/main.go "show categories"

# Debug test
go run cmd/debug-test/main.go

# Tools test (all enabled tools)
cd cmd/tools-test && go run main.go

# Todo agent (standalone TUI)
cd cmd/todo-agent && go run main.go

# Simple agent (2-line API demo)
go run cmd/simple-agent/main.go "show categories"

# wb-ping-util-v2 (demonstrates 2-line API)
go run cmd/wb-ping-util-v2/main.go

# Streaming test (real-time events)
go run cmd/streaming-test/main.go "Explain quantum computing"
```

---

## Creating a New Tool

1. Create file in `pkg/tools/std/`
2. Implement `Tool` interface
3. Add config to `config.yaml`
4. Add to `getAllKnownToolNames()` in `pkg/app/components.go`

```go
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct { Param1 string `json:"param1"` }
    json.Unmarshal([]byte(argsJSON), &args)
    return `{"result": "success"}`, nil
}
```

---

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | LLM provider |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | Storage |
| `WB_API_KEY` | Wildberries API |

---

## Key Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/minio/minio-go/v7` - S3 client
- `github.com/sashabaranov/go-openai` - OpenAI API
- `golang.org/x/time/rate` - Rate limiting

---

## Thread-Safe Components

CoreState, ToolsRegistry, ModelRegistry, TodoManager, wb.Client.limiters, TUI MainModel (sync.RWMutex).

---

## Per-Tool Rate Limiting

Each WB tool gets its own rate limiter instance (e.g., `get_wb_feedbacks`: 60/min, `get_wb_parent_categories`: 100/min).

---

**Last Updated**: 2026-01-13
**Version**: 5.0 (ReActCycle refactoring complete - Template-Execution separation, Observer pattern, Type-safe signals)
