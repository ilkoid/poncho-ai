# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct pattern.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings.

**Architecture**:
- `pkg/state/CoreState` - Framework core (reusable, includes e-commerce helpers)
- `internal/ui/` - TUI-specific (stores UI state separately from CoreState)
- `pkg/chain/ReActCycle` - Implements both Chain and Agent interfaces
- `pkg/app/components.go` - Returns `*state.CoreState` (Rule 6 compliant)
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
│   └── wb-tools-test/     # CLI utility for testing WB tools
├── internal/              # Application-specific logic
│   └── ui/               # Bubble Tea TUI (Model-View-Update)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent interface (avoids circular imports)
│   ├── app/              # Component initialization (shared across entry points)
│   ├── chain/            # Chain Pattern + ReAct implementation (modular agent execution)
│   ├── classifier/       # File classification engine
│   ├── config/           # YAML configuration with ENV support
│   ├── debug/            # JSON debug logging system
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer + options pattern
│   ├── models/           # Model Registry (centralized LLM provider management)
│   ├── prompt/           # Prompt loading and rendering + post-prompts
│   ├── s3storage/        # S3-compatible storage client
│   ├── state/            # Framework core state (CoreState)
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

### ReActCycle (`pkg/chain/`)

Implements both **Chain** and **Agent** interfaces:

```go
// Chain - full control
output, err := reactChain.Execute(ctx, chain.ChainInput{...})

// Agent - simple
result, err := reactCycle.Run(ctx, query)
history := reactCycle.GetHistory()
```

**Note**: `internal/agent/orchestrator.go` was DELETED. Use ReActCycle instead.

### Simple Agent API (`pkg/agent/`)

**NEW (2026-01-08)**: Ultra-simple API for creating AI agents in **2 lines**.

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

**Architecture**: Facade pattern over `ReActCycle`. See `cmd/simple-agent/` and `cmd/wb-ping-util-v2/` for examples.

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

**Step Interface**:
```go
type Step interface {
    Name() string
    Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error)
}
```

**ReActCycle**: Composable steps (LLMInvocationStep, ToolExecutionStep) with debug support via `ChainDebugRecorder`.

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
| Repository | `pkg/state/` | Unified storage with domain interfaces |
| Registry | `pkg/tools/`, `pkg/models/` | Tool and Model registration/discovery |
| Factory | `pkg/factory/` | LLM provider creation |
| Options | `pkg/llm/` | Runtime parameter overrides |
| Command | `internal/ui/` | TUI command handling (local, not in pkg/) |
| ReAct | `pkg/chain/` | Agent reasoning loop |
| Chain of Responsibility | `pkg/chain/` | Modular step-based execution |
| Recorder | `pkg/debug/` | JSON trace recording |

---

## Building and Running

```bash
# Main TUI
go run cmd/poncho/main.go

# Chain CLI
go run cmd/chain-cli/main.go "show categories"

# Debug test
go run cmd/debug-test/main.go
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
