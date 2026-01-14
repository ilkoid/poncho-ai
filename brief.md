# Poncho AI: Framework Architecture Documentation

## Executive Summary

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct (Reasoning + Acting) pattern. The framework provides a structured approach to agent development, handling routine tasks (prompt engineering, JSON validation, conversation history, task planning) while allowing developers to focus on business logic through isolated tools.

**Primary Use Case**: E-commerce automation for Wildberries marketplace with multimodal AI capabilities for processing fashion sketches and PLM (Product Lifecycle Management) data.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings, ensuring maximum flexibility and minimal dependencies.

**Core Strengths**:
- **Modular Architecture**: Clean separation between tools, orchestrator, state management, and UI
- **LLM-Agnostic**: Works with any OpenAI-compatible API through abstraction layer
- **Resilient Design**: No panics in business logic, graceful error handling
- **Extensible**: Easy to add new tools, commands, and LLM providers
- **Production-Ready**: Includes comprehensive debugging, logging, and testing utilities

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [CLI Tools](#2-cli-tools)
3. [Core Components](#3-core-components)
4. [Configuration System](#4-configuration-system)
5. [The 11 Immutable Rules](#5-the-11-immutable-rules)
6. [Dependencies and Technologies](#6-dependencies-and-technologies)
7. [Development Workflow](#7-development-workflow)
8. [Key Design Principles](#8-key-design-principles)

---

## 1. Architecture Overview

### 1.1 High-Level Structure

```
poncho-ai/
├── cmd/                    # Application entry points
│   ├── poncho/            # Main TUI application
│   ├── maxiponcho/        # Wildberries PLM analysis TUI
│   └── */                 # CLI utilities (chain-cli, model-registry-test, vision, debug)
├── internal/              # Application-specific logic
│   └── ui/               # Bubble Tea TUI (app-specific implementation)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent Client facade (2-line agent API)
│   ├── app/              # Component initialization
│   ├── chain/            # Chain Pattern + ReAct implementation (Template/Execution split)
│   ├── classifier/       # File classification engine
│   ├── config/           # YAML configuration
│   ├── debug/            # Debug logging
│   ├── events/           # Port & Adapter: Event interfaces (Emitter, Subscriber)
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer (Provider + StreamingProvider)
│   ├── models/           # Model Registry (centralized LLM providers)
│   ├── prompt/           # Prompt loading/rendering
│   ├── state/            # Framework core (CoreState)
│   ├── tui/              # Reusable TUI helpers (adapter for Bubble Tea)
│   ├── tools/            # Tool system (registry + std tools)
│   └── [other packages]  # S3, WB, utils, etc.
├── prompts/              # YAML prompt templates
└── config.yaml           # Main configuration
```

### 1.2 Component Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│                    LAYER ARCHITECTURE (Rule 6)                         │
├────────────────────────────────────────────────────────────────────────┤
│                                                                        │
│  ┌─────────────────┐    ┌──────────────────┐    ┌────────────┐      │
│  │   cmd/poncho    │───▶│   CoreState      │◀───│   Tools    │      │
│  │  (Entry Point)  │    │   (pkg/state)    │    │  Registry  │      │
│  └─────────────────┘    └──────────────────┘    └────────────┘      │
│           │                       │▲                                 │
│           │                       ││                                 │
│           ▼                       ││                                 │
│  ┌─────────────────┐    ┌─────────┴───────────┐    ┌─────────┐      │
│  │  agent.Client   │◀───│   CoreState        │    │   UI    │      │
│  │   (pkg/agent)   │    │   (pkg/state)      │    │(internal)│      │
│  │                 │    │                    │    │         │      │
│  │ Events:         │    │ E-commerce helpers │    │ Separate │      │
│  │ - SetEmitter()  │    │ - SetCurrentArticle│    │  fields: │      │
│  │ - Subscribe()   │    └────────────────────┘    │ orch,    │      │
│  └─────────────────┘                              │ modelID  │      │
│           │                                        └─────────┘      │
│           ▼                                                          │
│  ┌─────────────────┐                                                  │
│  │  ReActCycle     │                                                  │
│  │  (pkg/chain)    │                                                  │
│  └─────────────────┘                                                  │
│           │                                                          │
│           ▼                                                          │
│  ┌─────────────────────────────────────────────────┐                │
│  │        Port & Adapter (pkg/events, pkg/tui)     │                │
│  │  ┌──────────────┐         ┌──────────────┐     │                │
│  │  │   Emitter    │────────▶│  Subscriber   │     │                │
│  │  │   (Port)     │         │   (Port)      │     │                │
│  │  └──────────────┘         └──────────────┘     │                │
│  │         ▲                         ▲             │                │
│  │         │                         │             │                │
│  │  agent.Client                pkg/tui.*          │                │
│  │  (sends events)             (Adapter)           │                │
│  └─────────────────────────────────────────────────┘                │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘

Key Points:
  - pkg/state/CoreState: Framework core (includes e-commerce helpers)
  - pkg/agent/Client: Facade with 2-line API, event support, streaming
  - pkg/events.*: Port interfaces (Emitter, Subscriber)
  - pkg/tui.*: Adapter helpers for Bubble Tea
  - pkg/chain/ReActCycle: Immutable template for agent execution
  - internal/ui/: App-specific TUI (extends pkg/tui)
  - Rule 6 Compliance: pkg/ has ZERO imports from internal/
  - Port & Adapter: Decouples agent from UI implementation
```

### 1.3 Data Flow

```
User Input → Command Registry → Agent.Client → ReActCycle (Template) →
Create Execution → LLM Provider (Streaming/Sync) → Tool Execution → Update History

Event Flow (Port & Adapter):
  Agent.Client → Emitter.Emit(Event) → Channel → Subscriber.Events() →
  pkg/tui.ReceiveEventCmd() → Bubble Tea Update() → UI Refresh
```

---

## 2. CLI Tools

### 2.1 Overview

| Tool | Purpose | Interface | Key Features |
|------|---------|-----------|--------------|
| **poncho** | Main TUI application | Bubble Tea | Full agent interface, file management, vision |
| **maxiponcho** | WB PLM analysis TUI | Bubble Tea | S3 integration, WB API, product analysis |
| **chain-cli** | Chain Pattern testing | CLI | Debug logging, JSON output, model override |
| **model-registry-test** | Model Registry testing | CLI | List models, test retrieval, verify fallback |
| **vision-cli** | Vision analysis | CLI | Multimodal queries, image analysis |
| **debug-test** | Debug logs testing | CLI | Debug recorder simulation |
| **wb-tools-test** | S3 & WB tools tester | CLI | Sequential tool execution, JSON results |
| **todo-agent** | Todo management TUI | Bubble Tea | Task planning with AI, standalone |
| **wb-ping-util-v2** | WB API diagnostic | CLI | Example of new agent API (2-line) |
| **simple-agent** | Agent API example | CLI | Minimal agent implementation |

### 2.2 Main Applications

#### poncho - Main TUI
**Location**: `cmd/poncho/main.go`

**Usage**:
```bash
go run cmd/poncho/main.go
# or
go build -o poncho cmd/poncho/main.go && ./poncho
```

**Commands**: `todo`, `load <id>`, `render <file>`, `ask <query>`, or any natural language.

#### maxiponcho - WB PLM Analysis
**Location**: `cmd/maxiponcho/main.go`

**Usage**:
```bash
go run cmd/maxiponcho/main.go
```

Uses custom config in `cmd/maxiponcho/config.yaml`.

### 2.3 CLI Utilities

All utilities require `config.yaml` next to binary (Rule 11 compliance).

**chain-cli**:
```bash
./chain-cli "query"                           # Basic usage
./chain-cli -debug "query"                    # With debug
./chain-cli -model glm-4.6 "query"            # Specific model
./chain-cli -json "query" | jq .              # JSON output
```

**model-registry-test**:
```bash
./model-registry-test                         # List all registered models
./model-registry-test -test glm-4.6          # Test specific model
```

**vision-cli**:
```bash
./vision-cli                                  # Interactive
./vision-cli -query "describe image"          # Direct query
```

**tools-test** (comprehensive tool testing):
```bash
cd cmd/tools-test && go run main.go           # Tests all enabled tools
```

**todo-agent** (standalone TUI for todo management):
```bash
cd cmd/todo-agent && go run main.go           # Autonomous task planner
```

**wb-ping-util-v2** (demonstrates new 2-line API):
```bash
./wb-ping-util-v2                             # S3 + WB API diagnostics
```

---

## 3. Core Components

### 3.1 Tool System (`pkg/tools/`)

**Design Principle**: "Raw In, String Out"

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

**Key Features**:
- **Registry Pattern**: Dynamic tool registration via `Registry.Register()`
- **Thread-Safe**: Protected by `sync.RWMutex`
- **LLM-Agnostic**: Works with any LLM provider

**Standard Tools** (`pkg/tools/std/`):
- **Planner**: `plan_add_task`, `plan_mark_done`, `plan_mark_failed`, `plan_clear`
- **WB Catalog**: `get_wb_parent_categories`, `get_wb_subjects`, `ping_wb_api`
- **WB Products**: `search_wb_products`, `get_wb_characteristics`, `get_wb_tnved`
- **WB References**: `get_wb_brands`, `get_wb_colors`, `get_wb_countries`, etc.
- **WB Analytics**:
    - `get_wb_campaign_stats`: Campaign statistics (views, clicks, orders)
    - `get_wb_keyword_stats`: Keyword statistics
    - `get_wb_attribution_summary`: Organic vs Ad attribution
    - `get_wb_search_positions`: Product search positions
    - `get_wb_top_search_queries`: Top search queries for product
    - `get_wb_top_organic_positions`: Top 10 organic positions
- **WB Feedbacks (Stub)**: `get_wb_feedbacks`, `get_wb_questions`, etc.

**Selective Tool Registration** (Bit Flags):
```go
const (
    ToolWB      ToolSet = 1 << iota  // Wildberries tools
    ToolPlanner                       // Task planner
    ToolsAll                          // All tools
)

// Usage
components := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolWB|appcomponents.ToolPlanner)
```

### 3.2 Model Registry (`pkg/models/`)

**Purpose**: Centralized LLM provider management with dynamic model switching.

**Design**: Mirrors `tools.Registry` pattern for consistency.

```go
type Registry struct {
    mu     sync.RWMutex
    models map[string]ModelEntry
}

type ModelEntry struct {
    Provider llm.Provider
    Config   config.ModelDef
}
```

**Key Methods**:
- `Register(name, modelDef, provider)` - Register model
- `Get(name)` - Get provider by name
- `GetWithFallback(requested, default)` - Get with fallback
- `ListNames()` - List all registered models
- `NewRegistryFromConfig(cfg)` - Initialize from YAML

**Benefits**:
- All models from `config.yaml` registered at startup
- Post-prompts can switch models dynamically via `model` field
- Thread-safe operations
- Fallback mechanism for missing models

### 3.3 ReActCycle (`pkg/chain/`)

**Purpose**: Implements both **Chain** and **Agent** interfaces using a Template/Execution pattern.

**Architecture (Phase 1-4 Refactor)**:
- **ReActCycle (Template)**: Immutable, created once. Holds config, registries, step templates. Thread-safe.
- **ReActExecution (Runtime)**: Created per `Execute()` call. Holds runtime state (history, steps). Not shared.
- **Observer Pattern**: Handles cross-cutting concerns (debug logs, events).

```go
// Agent interface (simple)
result, err := reactChain.Run(ctx, query)
history := reactChain.GetHistory()

// Chain interface (full control)
output, err := reactChain.Execute(ctx, chain.ChainInput{
    UserQuery: query,
    State:     coreState,
    Registry:  registry,
})
```

**Components**:
- `Step` interface - Composable operations
- `LLMInvocationStep` - Call LLM with context
- `ToolExecutionStep` - Execute tool calls
- `ChainContext` - Thread-safe execution state
- `ChainDebugRecorder` - Debug integration via Observer
- `Signals` - Typed execution control (`SignalNone`, `SignalFinalAnswer`, `SignalNeedUserInput`, `SignalError`)

**Benefits**:
- Rule 6 compliant (no internal/ imports)
- Reusable across CLI, TUI, HTTP API
- Dual pattern support
- YAML-configurable
- Thread-safe concurrent execution

### 3.3.1 Simple Agent API (`pkg/agent/`)

**Facade**: Ultra-simple facade for creating AI agents in **2 lines**.

**Problem Solved**: Original API required 50+ lines of boilerplate code.

**Comparison**:

| Aspect | Old API | New API |
|--------|---------|---------|
| Lines of code | 50+ | 2 |
| Concepts | Config, Components, ReActCycle, ChainInput | Client |
| Imports | 8+ packages | 1 package |

**Usage Examples**:

**Basic (80% use cases)**:
```go
package main

import (
    "context"
    "fmt"
    "github.com/ilkoid/poncho-ai/pkg/agent"
)

func main() {
    client, _ := agent.New(context.Background(), agent.Config{ConfigPath: "config.yaml"})
    result, _ := client.Run(context.Background(), "Find products under 1000₽")
    fmt.Println(result)
}
```

**With Custom Tool**:
```go
client, _ := agent.New(ctx, agent.Config{ConfigPath: "config.yaml"})
client.RegisterTool(&MyPriceCheckerTool{})
result, _ := client.Run(ctx, "Check price of SKU123")
```

**Advanced Access** (when needed):
```go
registry := client.GetModelRegistry()  // Direct model access
tools := client.GetToolsRegistry()     // Direct tool access
state := client.GetState()             // Direct CoreState access
cfg := client.GetConfig()              // Direct config access
```

**What `agent.New()` does automatically**:
1. Loads `config.yaml`
2. Creates S3 client (optional)
3. Creates WB client
4. Loads WB dictionaries
5. Creates CoreState
6. Creates ModelRegistry
7. Creates ToolsRegistry
8. Registers tools (only `enabled: true`)
9. Loads system prompt and post-prompts
10. Creates ReActCycle with debug recorder

**Architecture**:
- Facade pattern over `ReActCycle`
- No circular imports (Agent interface in `pkg/chain/agent.go`)
- Thread-safe
- Compatible with both TUI and CLI

### 3.4 State Management (Repository Pattern)

**Architecture**: Layered repository with unified storage.

**Repository Interfaces** (`pkg/state/repository.go`):
```go
type UnifiedStore interface {
    Get(key string) (any, bool)
    Set(key string, value any) error
    Update(key string, fn func(any) any) error
    Delete(key string) error
    Exists(key string) bool
    List() []string
}

type MessageRepository interface {
    UnifiedStore
    Append(msg llm.Message) error
    GetHistory() []llm.Message
}

type FileRepository interface {
    UnifiedStore
    UpdateFileAnalysis(tag string, file *s3storage.FileMeta) error
    GetFiles() map[string][]*s3storage.FileMeta
}

type TodoRepository interface {
    UnifiedStore
    AddTask(description string) (int, error)
    CompleteTask(id int) error
    FailTask(id int, reason string) error
    GetTodoString() string
}
```

**CoreState** (`pkg/state/core.go`):
```go
type CoreState struct {
    // Dependencies (read-only)
    Config          *config.AppConfig
    S3              *s3storage.Client
    Dictionaries    *wb.Dictionaries
    Todo            *todo.Manager
    ToolsRegistry   *tools.Registry

    // Thread-safe storage
    mu    sync.RWMutex
    store map[string]any
}

// E-commerce helpers (moved from internal/app)
func (s *CoreState) SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) error
func (s *CoreState) GetCurrentArticleID() string
func (s *CoreState) GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta)
```

**UI State** (`internal/ui/model.go`):
```go
type MainModel struct {
    // Framework core (library code)
    coreState    *state.CoreState

    // UI-specific state (TUI only)
    orchestrator     agent.Agent
    currentArticleID string
    currentModel     string
    isProcessing     bool
    mu               sync.RWMutex
    // ...
}
```

**Benefits**:
- Rule 6 compliance (pkg/ has no internal/ imports)
- TUI apps explicitly pass CoreState + UI fields
- Single source of truth
- Thread-safe by design

### 3.5 Tool Post-Prompts

**Purpose**: Tool-specific system prompts that activate after tool execution.

**Configuration** (`prompts/tool_postprompts.yaml`):
```yaml
tools:
  get_wb_parent_categories:
    post_prompt: "wb/parent_categories_analysis.yaml"
    enabled: true
```

**Flow**: Tool executes → Post-prompt loaded → LLM formats response → Prompt reset.

**Benefits**:
- Tool-specific formatting
- Separation of concerns
- Easy customization via YAML

### 3.6 LLM Abstraction (`pkg/llm/`)

```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}

type StreamingProvider interface {
    Provider
    GenerateStream(ctx context.Context, messages []Message, callback func(StreamChunk), opts ...any) (Message, error)
}

// Options pattern
llm.Generate(ctx, messages, llm.WithModel("glm-4.6"), llm.WithTemperature(0.5))
```

**Implementation**: OpenAI-compatible adapter covers 99% of modern APIs. Supports streaming and "Thinking Mode" (Zai GLM).

**Factory**: `pkg/factory/llm_factory.go` creates providers from config.

### 3.7 Command Registry (`internal/app/commands.go`)

**Purpose**: Extensible TUI command system using Command Pattern.

**Built-in Commands**:
- `todo` / `t` - Todo management
- `load` - Load article from S3
- `render` - Render prompt template
- `ask` - Query AI agent
- (unknown) - Delegate to agent

### 3.8 Debug System (`pkg/debug/`)

**Purpose**: JSON trace recording for agent execution.

**Configuration**:
```yaml
app:
  debug_logs:
    enabled: true
    save_logs: true
    logs_dir: "./debug_logs"
    include_tool_args: true
    include_tool_results: true
    max_result_size: 5000
```

**Output**: JSON files with timestamps, LLM requests/responses, tool executions, duration.

### 3.9 Component Initialization (`pkg/app/`)

**Purpose**: Reusable initialization across entry points.

```go
type Components struct {
    Config        *config.AppConfig
    State         *state.CoreState  // Rule 6: no internal/ imports
    ModelRegistry *models.Registry
    LLM           llm.Provider      // DEPRECATED: Use ModelRegistry
    VisionLLM     llm.Provider
    WBClient      *wb.Client
    Orchestrator  agent.Agent
}

type ExecutionResult struct {
    Response   string
    TodoString string
    TodoStats  TodoStats
    History    []llm.Message
    Duration   time.Duration
}
```

**Key Functions**:
- `InitializeConfig(finder)` - Load configuration
- `Initialize(cfg, maxIters, systemPrompt, toolSet)` - Create components
- `Execute(components, query, timeout)` - Run agent query
- `SetupTools(state, wbClient, toolSet)` - Register tools

### 3.10 Port & Adapter Pattern (`pkg/events/`, `pkg/tui/`)

**Decouples agent logic from UI implementation.**

**Problem**: Without this pattern, agent code depends on specific UI framework (Bubble Tea), making:
- Library code (pkg/) dependent on app-specific code (internal/)
- Testing difficult (need to import UI framework)
- Reuse in other contexts impossible (Web API, CLI, etc.)

**Solution**: Port & Adapter pattern (Hexagonal Architecture).

**Port** (`pkg/events/`): Interfaces for event communication.

```go
// Emitter - Port for sending events (used by pkg/agent)
type Emitter interface {
    Emit(ctx context.Context, event Event)
}

// Subscriber - Port for receiving events (used by UI)
type Subscriber interface {
    Events() <-chan Event
    Close()
}

// Event types
const (
    EventThinking   EventType = "thinking"
    EventToolCall   EventType = "tool_call"
    EventToolResult EventType = "tool_result"
    EventMessage    EventType = "message"
    EventError      EventType = "error"
    EventDone       EventType = "done"
)
```

**Adapter** (`pkg/tui/`): Connects Port to Bubble Tea.

```go
// EventMsg converts events.Event to Bubble Tea message
type EventMsg events.Event

// ReceiveEventCmd returns Bubble Tea Cmd for reading events
func ReceiveEventCmd(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd

// WaitForEvent continues reading events in Update()
func WaitForEvent(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd

// Run - ready-to-use TUI
func Run(client *agent.Client) error

// RunWithOpts - customizable TUI
func RunWithOpts(client *agent.Client, opts ...Option) error
```

**Usage** (in TUI):

```go
// cmd/poncho/main.go
client, _ := agent.New(ctx, agent.Config{ConfigPath: "config.yaml"})

// Create emitter
emitter := events.NewChanEmitter(100)
client.SetEmitter(emitter)

// Subscribe to events
sub := client.Subscribe()

// Initialize TUI model with subscriber
tuiModel := ui.InitialModel(client.GetState(), client, cfg.Models.DefaultChat, sub)

// Bubble Tea automatically processes events via ReceiveEventCmd
p := tea.NewProgram(tuiModel)
p.Run()
```

**Usage** (in Bubble Tea Update):

```go
// internal/ui/update.go
func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tui.EventMsg:
        event := events.Event(msg)
        switch event.Type {
        case events.EventThinking:
            m.isProcessing = true
            return m, tui.WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
                return tui.EventMsg(e)
            })
        // ... handle other event types
        }
    }
    return m, nil
}
```

**Benefits**:
- Rule 6 compliant: `pkg/` has no imports from `internal/`
- Testable: Can mock Emitter/Subscriber for unit tests
- Reusable: Same agent works with TUI, Web, CLI
- Clean separation: Library code doesn't know about UI framework

---

## 4. Configuration System

### 4.1 YAML + ENV Variables

**Format**: `config.yaml` with `${VAR}` syntax.

**Structure**:
```yaml
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

s3:
  endpoint: "storage.yandexcloud.net"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"

wb:
  api_key: "${WB_API_KEY}"

app:
  prompts_dir: "./prompts"
  debug_logs:
    enabled: true
    logs_dir: "./debug_logs"
  streaming:
    enabled: true

# Chain Configuration (Rule 2: YAML-driven)
chains:
  default:
    type: "react"
    max_iterations: 10
    timeout: "5m"
    tool_post_prompts:
      get_wb_parent_categories: "wb/parent_categories_analysis.yaml"
```

### 4.2 Required Environment Variables

| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | AI provider |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | Storage |
| `WB_API_KEY` | Wildberries API |

### 4.3 Configuration Loading

**Auto-Discovery**: Uses `ConfigPathFinder` interface.

**Search Order**:
1. Next to binary (for standalone utilities)
2. Current directory: `./config.yaml`
3. Project root: `./config.yaml`

**Rule 11**: Standalone utilities must have config next to binary.

---

## 5. The 11 Immutable Rules

These rules are defined in [`dev_manifest.md`](dev_manifest.md).

### Rule 0: Code Reuse
Use existing solutions first. Refactor when necessary.

### Rule 1: Tool Interface Contract
Never change the [`Tool`](pkg/tools/registry.go) interface. "Raw In, String Out" is immutable.

**Why**: Best solution for LLM tools. Each tool knows how to parse its JSON.

### Rule 2: Configuration
All settings in YAML with ENV support. No hardcoded values.

### Rule 3: Registry Usage
All tools via `Registry.Register()`. All models via `models.Registry`. No direct calls bypassing registry.

### Rule 4: LLM Abstraction
Work with AI models **only** through [`Provider`](pkg/llm/llm.go) interface.

**Why**: Critical for surviving hype cycle. Today OpenAI, tomorrow something else.

### Rule 5: State Management
Layered architecture, thread-safe, no globals.

**Architecture**:
- `pkg/state/CoreState` - Framework core (reusable)
- `internal/app/AppState` - Application-specific (embeds CoreState)
- Repository Pattern - Domain-specific interfaces

### Rule 6: Package Structure
- `pkg/` - Library code (NO imports from `internal/`)
- `internal/` - Application-specific
- `cmd/` - Entry points only

**Status**: ✅ Full compliance achieved. `pkg/` has ZERO imports from `internal/`. `pkg/app/components` returns `*state.CoreState`. Port & Adapter pattern (`pkg/events`, `pkg/tui`) enables clean UI integration without breaking Rule 6.

### Rule 7: Error Handling
No `panic()` in business logic. All errors returned up stack.

**Why**: For AI agents, critical. LLM will make mistakes and output broken JSON.

### Rule 8: Extensibility
New features through tools, LLM adapters, or config extensions only.

### Rule 9: Testing
Use CLI utilities for verification instead of unit tests during development.

### Rule 10: Documentation
Godoc on all public APIs.

### Rule 11: Resource Localization
Applications in `/cmd` must be autonomous with resources nearby.

**Why**: Turns utilities into self-contained artifacts that work like Linux tools.

---

## 6. Dependencies and Technologies

### 6.1 Go Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/charmbracelet/bubbletea` | TUI framework (TEA pattern) |
| `github.com/minio/minio-go/v7` | S3-compatible storage |
| `gopkg.in/yaml.v3` | YAML configuration |
| `github.com/sashabaranov/go-openai` | OpenAI-compatible API |
| `golang.org/x/time/rate` | Rate limiting |

### 6.2 External Services

| Service | Purpose |
|---------|---------|
| **ZAI API** | LLM provider |
| **Yandex S3** | PLM data storage |
| **Wildberries API** | E-commerce data |

### 6.3 Technology Stack

**Language**: Go 1.21+

**Architecture**:
- Pattern: ReAct (Reasoning + Acting)
- UI: Bubble Tea (TEA pattern)
- Config: YAML with ENV expansion
- Storage: S3-compatible
- Logging: JSON debug logs

**Key Decisions**:
- "Raw In, String Out" - Maximum flexibility
- LLM-agnostic - Works with any OpenAI-compatible API
- Thread-safe - Mutex protection
- No panics - Resilient error handling
- Modular - Clear separation of concerns

---

## 7. Development Workflow

### 7.1 Building and Running

```bash
# Main TUI
go run cmd/poncho/main.go

# Maxiponcho TUI
go run cmd/maxiponcho/main.go

# Chain CLI
go build -o chain-cli cmd/chain-cli/main.go
./chain-cli "query"

# Vision CLI
go build -o vision-cli cmd/vision-cli/main.go
./vision-cli -query "analyze"
```

### 7.2 Testing

```bash
# Run all tests
go test ./... -v

# Specific package
go test ./pkg/utils -v

# Coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### 7.3 Creating a New Tool

**Step 1**: Create file in `pkg/tools/std/`

**Step 2**: Implement Tool interface:
```go
type MyTool struct{}

func (t *MyTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "my_tool",
        Description: "What this tool does",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "param1": map[string]interface{}{
                    "type": "string",
                    "description": "Description",
                },
            },
            "required": []string{"param1"},
        },
    }
}

func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Param1 string `json:"param1"`
    }
    json.Unmarshal([]byte(argsJSON), &args)

    result := map[string]interface{}{"result": "success"}
    jsonResult, _ := json.Marshal(result)
    return string(jsonResult), nil
}
```

**Step 3**: Register in `pkg/app/components.go`:
```go
func SetupTools(state *state.CoreState, wbClient *wb.Client, toolSet ToolSet) {
    registry := state.GetToolsRegistry()
    registry.Register(&MyTool{})
}
```

**Step 4**: Test using CLI utility (Rule 9):
```bash
./wb-tools-test
```

### 7.4 Debugging

**Enable in config**:
```yaml
app:
  debug_logs:
    enabled: true
    save_logs: true
    logs_dir: "./debug_logs"
```

**View logs**:
```bash
ls -la debug_logs/
cat debug_logs/debug_20260101_224928.json | jq .
```

---

## 8. Key Design Principles

### 8.1 Core Strengths

**1. "Raw In, String Out"**
Each tool knows how to parse its JSON. Makes system infinitely flexible.

**2. Registry Pattern**
Modular system. Build different binaries with different tool sets.

**3. LLM Abstraction**
Provider interface guarantees framework survives hype cycle.

**4. Error Resilience**
No panics. Correct error return is only way to make stable AI agent.

**5. Resource Localization**
CLI utilities work like Linux tools - self-contained artifacts.

### 8.2 Architecture Highlights

**1. Clean Separation**
Tools → Orchestrator → LLM → State → UI

**2. Thread-Safe Operations**
All runtime state protected by mutexes (CoreState, AppState, ToolsRegistry, ModelRegistry, etc.)

**3. LLM-Agnostic Design**
Works with any OpenAI-compatible API.

**4. Extensible Architecture**
Easy to add tools, LLM adapters, commands, CLI utilities.

### 8.3 Best Practices

1. Use existing components (Rule 0)
2. Follow Tool Interface (Rule 1)
3. Use YAML configuration (Rule 2)
4. Register all tools (Rule 3)
5. Use LLM abstraction (Rule 4)
6. Manage state properly (Rule 5)
7. Handle errors gracefully (Rule 7)
8. Extend through established points (Rule 8)
9. Test with CLI utilities (Rule 9)
10. Document public APIs (Rule 10)
11. Localize resources (Rule 11)

---

## Conclusion

Poncho AI is a **mature AI agent framework** with:

1. Clear separation of concerns
2. Thread-safe operations
3. LLM-agnostic design
4. Resilient error handling
5. Extensible architecture
6. Natural interface
7. Comprehensive debugging
8. Modular design

The framework follows **"Convention over Configuration"** - developers follow simple rules, framework handles complexity.

**Key Takeaway**: Poncho AI is a **framework** for building AI agents, providing infrastructure and patterns for developers to focus on business logic.

---

**Last Updated**: 2026-01-14
**Version**: 3.6 (Streaming, Expanded WB Tools, Chain Refactor)

**Maintainer**: Poncho AI Development Team
