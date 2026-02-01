# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct pattern.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings.

**Architecture**:
- `pkg/state/CoreState` - Framework core (reusable, e-commerce helpers)
- `pkg/tui/` - TUI components with primitives layer (BaseModel, InterruptionModel)
- `pkg/chain/ReActCycle` - Chain + Agent interfaces
- `pkg/app/components.go` - Context propagation (Rule 11)
- `pkg/app/presets.go` - Preset system for quick launch
- `pkg/agent/Client` - Simple 2-line agent API (Facade)
- `pkg/events/` - Port & Adapter for UI decoupling
- `pkg/chain/bundle_resolver.go` - Token optimization (98% savings)
- Rule 6 Compliant: `pkg/` has NO imports from `internal/`

---

## Architecture Overview

### High-Level Structure

```
poncho-ai/
├── cmd/                    # Entry points (autonomous utilities)
├── examples/              # Usage examples (not utilities)
├── internal/              # App-specific logic (ui/)
├── pkg/                   # Reusable library packages
├── prompts/              # Prompt templates
└── config.yaml           # Main config
```

---

## The 13 Immutable Rules

| Rule | Description |
|------|-------------|
| **0: Code Reuse** | Use existing solutions first |
| **1: Tool Interface** | NEVER change - `Definition() ToolDefinition`, `Execute(ctx, argsJSON string) (string, error)` |
| **2: Configuration** | YAML with ENV support only |
| **3: Registry** | All tools via `Registry.Register()` |
| **4: LLM Abstraction** | Work through `Provider` interface only |
| **5: State** | Layered, thread-safe, no globals |
| **6: Package Structure** ⭐ | `pkg/` = reusable, `internal/` = app-specific, `cmd/` = test utilities |
| **7: Error Handling** | No `panic()` in business logic |
| **8: Extensibility** | Add via tools, LLM adapters, config |
| **9: Testing** | Use CLI utilities in `/examples` |
| **10: Documentation** | Godoc on public APIs |
| **11: Context Propagation** | All long-running ops accept `context.Context` |
| **12: Security & Secrets** | Never hardcode secrets, use ENV, HTTPS only |
| **13: Resource Localization** | Autonomous `/cmd` and `/examples` apps |

### Rule 6: Package Structure (Port & Adapter) ⭐

```
pkg/       - Library code, ready for reuse
internal/  - Application-specific logic
cmd/       - Entry points, test utilities only
```

**Port & Adapter:**
- Library (`pkg/`) defines Port interface (`events.Emitter`, `events.Subscriber`)
- Adapter (`pkg/tui`) implements Port (Rule 6 compliant: no imports from `pkg/agent`, `pkg/chain`)
- Business logic via **callback pattern** from `cmd/`

**Rule 11 Status**: ✅ FULLY COMPLIANT
- `pkg/app/components.go`: `Initialize(parentCtx, ...)` and `Execute(parentCtx, ...)`
- Context in Model struct for Bubble Tea
- All entry points pass context

---

## Architectural Patterns

### Port & Adapter
```
Library (pkg/agent) → Port (events.Emitter) ← Adapter (pkg/tui)
```
- `pkg/events` - Port (Emitter, Subscriber interfaces)
- `pkg/tui` - Adapter (implements Subscriber)
- `pkg/agent` - Library (uses Emitter)

### Primitives-Based TUI
UI built from 5 primitives in `pkg/tui/primitives/`:

| Primitive | Purpose | Pattern |
|-----------|---------|---------|
| **ViewportManager** | Smart scroll, resize | Repository |
| **StatusBarManager** | Spinner, status bar | State |
| **EventHandler** | Pluggable event renderers | Strategy |
| **InterruptionManager** | User input, channel | Callback |
| **DebugManager** | Screen save, JSON logs | Facade |

### Event System Flow
Six-phase flow: Emission → Transport (channel) → Subscription → Conversion → Processing → Rendering

**Event Types**:
- `EventThinking` - starts thinking
- `EventThinkingChunk` - streaming reasoning content
- `EventToolCall` - tool execution started
- `EventToolResult` - tool execution completed
- `EventUserInterruption` - user interrupted
- `EventMessage` - agent message
- `EventError` - error occurred
- `EventDone` - finished

### Interruption Mechanism
User can interrupt execution in real-time with message.

**Flow**:
```
User → TUI → inputChan (size=10) →
ReActExecutor (between iterations) →
loadInterruptionPrompt() (YAML or fallback) →
Emit EventUserInterruption → TUI
```

**Key Features**:
- Buffered channel (size=10) for inter-goroutine comms
- Non-blocking checks via `select` with `default`
- YAML config: `chains.default.interruption_prompt`
- Event emission via `EventUserInterruption`

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

**Categories**: WB API, S3 (basic/batch/download), Vision, Planner.

**WB Tools Registration** (SRP Refactored):
The `setupWBTools()` function (lines 564-596) has been refactored from 166 lines to 20 lines by extracting category-specific functions:

| Function | Tools Registered |
|----------|------------------|
| `setupWBContentTools()` | search_wb_products, get_wb_parent_categories, get_wb_subjects, ping_wb_api |
| `setupWBFeedbacksTools()` | get_wb_feedbacks, get_wb_questions, get_wb_new_feedbacks_questions, etc. |
| `setupWBCharacteristicsTools()` | get_wb_subjects_by_name, get_wb_characteristics, get_wb_tnved, get_wb_brands |
| `setupWBServiceTools()` | reload_wb_dictionaries |
| `setupWBAnalyticsTools()` | get_wb_product_funnel, get_wb_search_positions, get_wb_campaign_stats, etc. |

### Model Registry (`pkg/models/`)
Centralized LLM provider management with dynamic switching.

**Features**:
- Models from `config.yaml` registered at startup
- Thread-safe via `sync.RWMutex`
- Fallback: `GetWithFallback(requested, default)`
- Runtime switching via post-prompts

### LLM Abstraction (`pkg/llm/`)
**Options Pattern**:
```go
llm.Generate(ctx, messages, llm.WithModel("glm-4.6"), llm.WithTemperature(0.5))
```

**StreamingProvider Interface**:
```go
type StreamingProvider interface {
    Provider  // Extends base Provider
    GenerateStream(ctx, messages, callback func(StreamChunk), opts) (Message, error)
}
```

**StreamChunk Types**:
- `ChunkThinking` - reasoning_content from thinking mode
- `ChunkContent` - regular response content
- `ChunkError` - streaming error
- `ChunkDone` - streaming complete

**Features**:
- Opt-out design (enabled by default)
- Thinking mode support (Zai GLM)
- Thread-safe callback
- Event-based UI updates via EventThinkingChunk

### Bundle System (`pkg/chain/bundle_resolver.go`)
**Purpose**: Token optimization through dynamic tool expansion.

**Token Savings**:
- Without bundles: 100 tools = ~15,000 tokens
- With bundles: 10 bundles = ~300 tokens (98% savings)

**Flow**:
1. LLM sees bundle definitions (~300 tokens)
2. LLM calls bundle name (e.g., "wb_content_tools")
3. BundleResolver.expandBundle() detects bundle call
4. Expands to real tool definitions
5. Injects as system message
6. Re-runs LLM with expanded context

**Configuration**:
```yaml
tool_resolution_mode: "bundle-first"  # or "flat"
enable_bundles: ["wb-tools", "vision-tools"]
tool_bundles:
  wb_content_tools:
    description: "Wildberries Content API..."
    tools: ["search_wb_products", "get_wb_parent_categories", ...]
```

### Preset System (`pkg/app/presets.go`)
**Purpose**: Quick app launch with predefined configurations.

**Built-in Presets**:
- `simple-cli` - Minimal CLI interface
- `interactive-tui` - Full TUI with streaming
- `full-featured` - All features enabled

**Usage**:
```go
// Load config and apply preset overlay
client, _ := agent.NewFromPreset(ctx, "interactive-tui", "config.yaml")

// Or manually
preset, _ := app.GetPreset("interactive-tui")
cfg, _ := app.LoadConfigWithPreset("config.yaml", preset)
```

**Custom Presets**:
```go
app.RegisterPreset("my-ecommerce", &app.PresetConfig{
    Type: app.AppTypeTUI,
    EnableBundles: []string{"wb-tools", "vision-tools"},
    Models: app.ModelSelection{Reasoning: "glm-4.6"},
    UI: app.TUIConfig{Title: "My E-commerce AI"},
})
```

### ReActCycle (`pkg/chain/`)
**PHASE 1-5 COMPLETE**: Template-Execution separation with Observer pattern.

**Architecture** (SRP Refactored):
```
ReActCycle (Immutable Template) → Execute() →
ReActExecution (Runtime State) → Execute() →
ReActExecutor (Orchestrator) → Observer Notifications
```

**Executor Methods** (Extracted from 268-line `Execute()`):
| Method | Purpose |
|--------|---------|
| `initializeExecution()` | Initialize execution, notify observers |
| `executeLLMStep()` | Execute LLM invocation, emit events |
| `handleToolExecution()` | Execute tools, emit results |
| `handleToolInterruption()` | Process user interruption |
| `checkUserInterruption()` | Non-blocking interruption check |
| `finalizeExecution()` | Build output, notify observers |
| `notifyIterationStart()` | Observer notification helper |
| `notifyIterationEnd()` | Observer notification helper |
| `notifyFinishWithError()` | Error handling helper |

**Key**:
- Template immutable (thread-safe for concurrent Execute())
- Execution per call (never shared)
- Observer pattern for cross-cutting concerns
- Type-safe signals (SignalFinalAnswer, etc.)
- Streaming support via `StreamingProvider`
- Rule 11: Context propagated

### Simple Agent API (`pkg/agent/`)
**Facade Pattern**: Ultra-simple 2-line API.

```go
client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
result, _ := client.Run(ctx, query)
```

**Features**:
- Auto-loads config.yaml
- Auto-registers tools (only `enabled: true`)
- Creates ModelRegistry, ToolsRegistry, CoreState
- Thread-safe
- Supports streaming and interruptions

**With Interruptions**:
```go
inputChan := make(chan string, 10)
output, _ := client.Execute(ctx, chain.ChainInput{
    UserQuery:    "Analyze",
    State:        client.GetState(),
    Registry:     client.GetToolsRegistry(),
    UserInputChan: inputChan,
})
```

### Event System (`pkg/events/`)
**Port & Adapter**: Decouple agent from UI via event interfaces.

**Interfaces**:
```go
type Emitter interface {
    Emit(ctx context.Context, event Event)
}

type Subscriber interface {
    Events() <-chan Event
    Close()
}
```

**Event Data**:
- `ThinkingChunkData` - streaming reasoning
- `ToolCallData` - tool invocation
- `ToolResultData` - execution result
- `UserInterruptionData` - interruption with message

### TUI Package (`pkg/tui/`)
Adapter between `pkg/events` and Bubble Tea.

**Primitives** (`pkg/tui/primitives/`):
- ViewportManager - smart scroll, resize
- StatusBarManager - spinner, status bar
- EventHandler - pluggable event renderers
- InterruptionManager - user input, channel
- DebugManager - screen save, JSON logs

**Models**:
1. **BaseModel** - foundation (embeds all 5 primitives)
2. **InterruptionModel** - interruption support (requires `SetOnInput()` callback)

**Rule 6**: Only reusable code in `pkg/tui`, no app-specific logic.

### App Initialization (`pkg/app/`)
**Rule 11**: Context propagation through all layers.

**Architecture** (SRP Refactored):
The `Initialize()` function (lines 390-442) has been refactored from 211 lines to 35 lines by extracting focused helper functions:

| Helper Function | Purpose |
|-----------------|---------|
| `createS3Client()` | Creates optional S3 client |
| `createWBClient()` | Creates WB API client with ping check |
| `loadWBDictionaries()` | Loads e-commerce dictionaries |
| `createModelRegistry()` | Creates LLM model registry |
| `createCoreState()` | Creates CoreState with TodoManager |
| `getVisionLLM()` | Retrieves vision model from registry |
| `loadAgentPrompts()` | Loads system and tool post-prompts |
| `createReActCycle()` | Creates ReActCycle instance |
| `setupReActCycleDependencies()` | Sets registry, state, bundle resolver |
| `configureReActCycle()` | Full ReActCycle configuration |
| `attachDebugRecorder()` | Attaches debug recorder |

**Usage**:
```go
components, err := app.Initialize(ctx, cfg, 10, "")
result, err := app.Execute(ctx, components, query, timeout)
```

**Components**:
```go
type Components struct {
    Config        *config.AppConfig
    State         *state.CoreState
    ModelRegistry *models.Registry
    VisionLLM     llm.Provider
    WBClient      *wb.Client
    Orchestrator  *chain.ReActCycle
}
```

### State Management (`pkg/state/`)
**Repository Pattern** with type-safe operations.

**Typed Keys** (pkg/state/keys.go):
- `KeyHistory`, `KeyFiles`, `KeyCurrentArticle`, `KeyTodo`
- `KeyDictionaries`, `KeyStorage`, `KeyToolsRegistry`

**Generic Helpers** (pkg/state/generic.go):
- `GetType[T](s, key)`, `SetType[T](s, key, value)`, `UpdateType[T](s, key, fn)`

**Repository Interfaces**:
- `MessageRepository` - chat history
- `FileRepository` - file management with vision
- `TodoRepository` - task management
- `DictionaryRepository` - e-commerce dictionaries
- `StorageRepository` - S3 client
- `ToolsRepository` - tool registry

### Client Storage Architecture

| Client | Stored In | Pattern | Access |
|--------|-----------|---------|--------|
| **S3 Client** | `CoreState.store` | Repository | `state.GetStorage()` |
| **LLM Providers** | `ModelRegistry` | Registry | `modelRegistry.Get()` |
| **WB Client** | ❌ NOT in State | DI | Passed to tools |

**Thread Safety**:
- CoreState: `sync.RWMutex`
- ModelRegistry: `sync.RWMutex`
- WB Client: `sync.RWMutex`

### S3 Batch Tools (`pkg/tools/std/s3_batch.go`)
**Purpose**: Batch operations with classification and vision analysis.

**Context Overflow Problem** (SOLVED):
- Parallel image calls → context overflow (~550KB)
- Solution: `analyze_article_images_batch` - sequential with aggregation

**Tools**:
- `classify_and_download_s3_files` - classify by tags (sketch, plm_data, marketing)
- `analyze_article_images_batch` - sequential vision analysis (max_images limit)

### S3 Download Tool (`pkg/tools/std/s3_download.go`)
**Purpose**: Download files/folders from S3.

**Safety**:
- No bucket download (key cannot be "/")
- Path traversal detection
- Max depth: 1 folder

### Debug System (`pkg/debug/`)
JSON trace recording with base64 truncation.

**Features**:
- Detects and truncates base64 images (>100 chars)
- Configurable `max_result_size`
- Includes tool args/results in logs

---

## Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Facade** | `pkg/agent/Client` | Simple 2-line API |
| **Port & Adapter** | `pkg/events/`, `pkg/tui/` | UI decoupling |
| **Callback** | `pkg/tui/` | Business logic injection (Rule 6) |
| **Repository** | `pkg/state/` | Unified storage |
| **Registry** | `pkg/tools/`, `pkg/models/` | Registration/discovery |
| **Factory** | `pkg/models/` | LLM provider creation |
| **Options** | `pkg/llm/` | Runtime parameter overrides |
| **Dependency Injection** | `pkg/app/`, `pkg/tools/std/` | DI for WB client |
| **ReAct** | `pkg/chain/` | Agent reasoning |
| **Chain of Responsibility** | `pkg/chain/` | Modular execution |
| **Template-Execution** | `pkg/chain/` | Immutable + runtime state |
| **Observer** | `pkg/chain/` | Cross-cutting concerns |
| **Streaming** | `pkg/llm/StreamingProvider` | Real-time responses |
| **Fallback** | `pkg/chain/interruption.go` | Default prompt |

---

## Building and Running

```bash
# Main TUI
go run cmd/poncho/main.go

# Simple agent
go run cmd/simple-agent/main.go "show categories"

# wb-ping-util-v2 (2-line API demo)
go run cmd/wb-ping-util-v2/main.go

# Streaming test
go run cmd/streaming-test/main.go "Explain quantum computing"

# Interruptible agent
cd examples/interruptible-agent && go run main.go "Show parent categories"
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

| Component | Mutex | Purpose |
|-----------|-------|---------|
| **CoreState** | `sync.RWMutex` | Store map (read/write) |
| **ModelRegistry** | `sync.RWMutex` | Models map |
| **ToolsRegistry** | `sync.RWMutex` | Tools map |
| **WB Client** | `sync.RWMutex` | Rate limiters map |
| **TodoManager** | `sync.RWMutex` | Task list |
| **TUI MainModel** | `sync.RWMutex` | UI state |

**Concurrent Execution**:
- **ReActCycle**: Multiple `Execute()` calls safe
- **ReActExecution**: Per execution (never shared)
- **No Global Mutex**: No blocking during LLM or tool calls

---

## Code Quality Notes

**SRP Refactoring Completed** (2026-02-01):
- `Initialize()`: 211 → 35 lines (83% reduction)
- `Execute()`: 268 → 57 lines (79% reduction)
- `setupWBTools()`: 166 → 20 lines (88% reduction)
- Total: 645 → 112 lines (83% reduction)
- All refactoring focused on extracting focused, single-responsibility functions
- Maintained compilation and functionality throughout

**Design Philosophy**:
- SOLID principles as best practices, not dogmatic rules
- Reasonable balance between clean code and practicality
- Functions should have clear, single purposes without excessive complexity

---

**Last Updated**: 2026-02-01
**Version**: 7.2 (SRP refactoring complete, improved code organization)
