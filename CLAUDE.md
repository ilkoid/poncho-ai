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
- `pkg/app/tool_setup.go` - **OCP: Config-driven tool setup** (2026-02-01)
- `pkg/app/presets.go` - Preset system for quick launch
- `pkg/config/utility.go` - Shared config types for cmd/ utilities (PromotionConfig, DownloadConfig, FeedbacksConfig, FunnelConfig, FunnelAggregatedConfig, WBClientConfig)
- `pkg/agent/Client` - Simple 2-line agent API (Facade)
- `pkg/events/` - Port & Adapter for UI decoupling
- `pkg/prompts/` - **OCP: Prompt loading with source pattern** (2026-02-01)
- `pkg/chain/bundle_resolver.go` - Token optimization (98% savings)
- Rule 6 Compliant: `pkg/` has NO imports from `internal/`

---

## Architecture Overview

### High-Level Structure

```
poncho-ai/
├── cmd/                    # Production utilities
│   ├── data-downloaders/   # Data collection from APIs
│   ├── data-analyzers/     # LLM-powered data analysis
│   ├── fix-utilities/      # Data fix/cleanup tools
│   └── test-utils/         # API debugging (not for production)
├── examples/              # Verification & demos (Rule 9)
│   ├── e2e-testing/        # E2E infrastructure
│   ├── api-demos/          # API demonstrations
│   └── feature-demos/      # Framework features (interruptions, etc.)
├── internal/              # App-specific logic (ui/)
├── pkg/                   # Reusable library packages
├── prompts/              # Prompt templates
└── config.yaml           # Main config
```

**Rule 9 Compliance**: Verification via `/examples` utilities, not unit tests initially.

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
| **9: Testing** | Use CLI utilities in `/examples` for verification |
  - Examples: `examples/wb-funnel-demo/` - autonomous WB Analytics v3 verification |
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

**Categories**: WB API (Content, Analytics v3, Feedbacks, Advertising), S3 (basic/batch/download), Vision, Planner.

**WB Analytics API v3 Tools** (2026-02-10):
- `get_wb_product_funnel` - Sales funnel with 15+ metrics (buyouts, cancellations, WB Club, stocks, ratings)
- `get_wb_product_funnel_history` - Daily funnel trends (1-7 days free, up to 365 with subscription)
- `get_wb_search_positions` - Product search visibility
- `get_wb_top_search_queries` - Top search queries by product
- `get_wb_top_organic_positions` - Organic search positions

**Rate Limits**: Two-level adaptive rate limiting for all downloaders (see [dev_limits.md](dev_limits.md)).
Analytics API: 3 req/min swagger. Content/Advertising API: higher limits (100-300/min).

**Tool Registration** (OCP Refactored - 2026-02-01):
Config-driven via `pkg/app/tool_setup.go` with factory pattern in `registerTool()`:

```go
// pkg/app/tool_setup.go
func SetupToolsFromConfig(
    st *state.CoreState,
    cfg *config.AppConfig,
    clients map[string]any,  // Dependency injection container
) error {
    // Iterates cfg.ToolCategories, calls registerTool() for each tool
}

func registerTool(name string, ...) error {
    switch name {
    case "search_wb_products":
        tool = std.NewWbProductSearchTool(client.(*wb.Client), toolCfg, cfg.WB)
    // ... 50+ tools covered
    }
}
```

**Adding Ozon** (without modifying core code):
1. Add `ozon` category to `config.yaml`
2. Add `ozon_client` to clients map
3. Add cases to `registerTool()` switch

**No setupOzonTools() function needed!**

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

**WB Client Factory**:
```go
// pkg/wb/client.go
client := wb.New(apiKey)                    // Simple creation
client, err := wb.NewFromConfig(cfg.WB)    // Config-based

// Adaptive rate limiting (MUST call before API methods)
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
client.SetAdaptiveParams(recoverAfter, probeAfter, maxBackoff)
```

**Important**: `NewFromConfig()` does NOT use `WBConfig.RateLimit`/`BurstLimit` fields.
Always call `SetRateLimit()` explicitly for adaptive behavior. See [dev_limits.md](dev_limits.md).

**HTTP Methods**:
- `Get()` → `doRequest()` — buffered (io.ReadAll + json.Unmarshal)
- `GetStream()` — streaming (json.Decoder, for large payloads like fullstats)
- `Post()` → `doRequest()` — for request bodies

**Demo Mode**: `client.IsDemoKey()` returns `true` for `demo_key`, enabling mock responses.

**Thread Safety**:
- CoreState: `sync.RWMutex`
- ModelRegistry: `sync.RWMutex`
- WB Client: `sync.RWMutex` (rate limiters map + adaptive state map)

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

### Prompt System (`pkg/prompts/`)
**OCP Refactored** (2026-02-01): Source pattern with fallback chain.

**PromptSource Interface**:
```go
type PromptSource interface {
    Load(promptID string) (*PromptFile, error)
}
```

**Source Registry** with fallback chain:
1. File sources (YAML files from `cfg.App.PromptsDir`)
2. Default source (Go defaults)

**Implementations**:
- `FileSource` - YAML files (`<base_dir>/<promptID>.yaml`)
- `DefaultSource` - Go hardcoded defaults (fallback)
- `APISource` - HTTP REST API (example)
- `DatabaseSource` - SQL database (example)

**Usage**:
```go
registry, _ := prompts.CreateSourceRegistry(cfg)
file, err := registry.Load("agent_system")
// Fallback: file.yaml → Go default
```

---

## Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Facade** | `pkg/agent/Client` | Simple 2-line API |
| **Port & Adapter** | `pkg/events/`, `pkg/tui/` | UI decoupling |
| **Callback** | `pkg/tui/` | Business logic injection (Rule 6) |
| **Repository** | `pkg/state/` | Unified storage |
| **Registry** | `pkg/tools/`, `pkg/models/` | Registration/discovery |
| **Factory** | `pkg/models/`, `pkg/app/tool_setup.go` | LLM/tool creation |
| **Adapter** | `pkg/prompts/registry_factory.go` | Source pattern adapters |
| **Options** | `pkg/llm/` | Runtime parameter overrides |
| **Dependency Injection** | `pkg/app/`, `pkg/tools/std/` | DI for WB client |
| **ReAct** | `pkg/chain/` | Agent reasoning |
| **Chain of Responsibility** | `pkg/chain/` | Modular execution |
| **Template-Execution** | `pkg/chain/` | Immutable + runtime state |
| **Observer** | `pkg/chain/` | Cross-cutting concerns |
| **Streaming** | `pkg/llm/StreamingProvider` | Real-time responses |
| **Fallback** | `pkg/prompts/source_registry.go` | Prompt source chain |
| **Source** | `pkg/prompts/` | OCP: Extensible prompt loading |

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

# Feature demo: Interruptions with TUI
cd examples/feature-demos/interruption-test && go run main.go [config_path]

# WB Analytics Funnel Demo (API v3 verification)
cd examples/api-demos/wb-funnel-demo
go run main.go                              # Mock mode (demo_key)
WB_API_KEY=your_key go run main.go         # Real API
WB_API_KEY=your_key go run main.go --nmIds 123456 --days 30  # Custom args

# E2E Mock Collector (create snapshot database)
cd examples/e2e-testing/e2e-mock-collector
go run main.go --days 7 --output ../e2e-snapshot.db

# Download utilities (production)
cd cmd/data-downloaders/download-wb-sales && go run main.go --days 7
cd cmd/data-downloaders/download-wb-promotion && go run main.go --begin 2025-01-01 --end 2025-01-31
cd cmd/data-downloaders/download-wb-feedbacks && go run . --days=7
cd cmd/data-downloaders/download-wb-funnel && go run . --days=7
cd cmd/data-downloaders/download-wb-funnel-agg && go run . --days=7

# Analyze utilities (LLM-powered)
cd cmd/data-analyzers/analyze-wb-feedbacks && OPENROUTER_API_KEY=sk-or-... go run . --days=30
```

### cmd/ Directory

Production utilities organized by purpose:

**`data-downloaders/`** - Data collection from external APIs
- `download-all-articles` - S3 mass download
- `download-wb-sales` - WB Sales/Analytics → SQLite
- `download-wb-promotion` - WB Promotion → SQLite
- `download-wb-feedbacks` - WB Feedbacks/Questions → SQLite
- `download-wb-funnel` - WB Analytics v3 funnel → SQLite
- `download-wb-funnel-agg` - WB Analytics v3 aggregated funnel → SQLite

**`data-analyzers/`** - LLM-powered data analysis
- `analyze-wb-feedbacks` - Feedback quality analysis via OpenRouter (two-level LLM aggregation)

**`fix-utilities/`** - Data fix/cleanup tools
- `fix-fake-png` - Fix PNG/JSON file naming

**`test-utils/`** - API debugging utilities (not for production)
- `test-wb-*` - Various WB API testing tools

### examples/ Directory

Verification and demonstration utilities per Rule 9:

**`e2e-testing/`** - E2E infrastructure
- `e2e-mock-collector` - Collects real data to SQLite
- `e2e-snapshot-test` - SnapshotDBClient verification
- `e2e-real-test` - API vs SQLite comparison
- `e2e-v2-test` - V2 transformation verification

**`api-demos/`** - API demonstrations
- `wb-funnel-demo` - WB Analytics API v3 (15+ metrics)
- `wb-list-products` - Seller products listing
- `wb-service-demo` - WbService layer demo

**`db-inspectors/`** - SQLite database inspection tools
- `funnel-db-inspector` - Funnel data queries
- `sales-db-inspector` - Sales data queries

**`feature-demos/`** - Framework features
- `interruption-test` - Interruption mechanism with TUI

---

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | LLM provider |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | Storage |
| `WB_API_KEY` | Wildberries API (Content, Analytics, Advertising) |
| `WB_API_ANALYTICS_AND_PROMO_KEY` | Analytics + Advertising API (alternative key) |
| `WB_API_FEEDBACK_KEY` | Wildberries Feedbacks API (separate key) |
| `WB_STAT_API_KEY` | Wildberries Statistics API (optional) |
| `OPENROUTER_API_KEY` | OpenRouter (LLM gateway for analyzers) |

---

## WB Analytics API v3

### Migration from API v2 (2026-02-10)

**What Changed**: Migrated from `/api/v2/nm-report/detail` to `/api/analytics/v3/sales-funnel/products`

**New Metrics** (15+ additions):
| Metric | API v2 | API v3 |
|--------|--------|--------|
| Views | ✅ | ✅ |
| Cart | ✅ | ✅ |
| Orders | ✅ | ✅ |
| **Buyouts** | ❌ | ✅ |
| **Cancellations** | ❌ | ✅ |
| **Wishlist** | ❌ | ✅ |
| **Order Sums** | ❌ | ✅ |
| **Average Price** | ❌ | ✅ |
| **WB Club** | ❌ | ✅ |
| **Stocks (WB/MP)** | ❌ | ✅ |
| **Product Rating** | ❌ | ✅ |
| **Feedback Rating** | ❌ | ✅ |
| **Time to Ready** | ❌ | ✅ |
| **Localization %** | ❌ | ✅ |

**Updated Tools**:
- `get_wb_product_funnel` - Sales funnel with full v3 metrics
- `get_wb_product_funnel_history` - Daily trends (1-7 days free, 365 with subscription)
- `get_wb_search_positions` - Search visibility
- `get_wb_top_search_queries` - Top search queries
- `get_wb_top_organic_positions` - Organic positions

**Rate Limits** (Analytics API — CRITICAL):
```
Period: 1 minute
Limit:  3 requests
Interval: 20 seconds
Burst:  3 requests
```
> ⚠️ Note: Content API uses higher limits (`rate_limit: 100`). Analytics tools override this in config.yaml.

**Post-Prompts**:
- `prompts/wb/analytics/product_funnel.ru.yaml` - Funnel analysis with insights
- `prompts/wb/analytics/funnel_history.ru.yaml` - Daily trends with seasonality

**Implementation**: [pkg/tools/std/wb_analytics.go](pkg/tools/std/wb_analytics.go)

---

## Key Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/minio/minio-go/v7` - S3 client
- `github.com/sashabaranov/go-openai` - OpenAI API
- `github.com/mattn/go-sqlite3` - SQLite driver (CGo)
- `golang.org/x/time/rate` - Rate limiting
- `gopkg.in/yaml.v3` - YAML config parsing

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

## OCP Refactoring (2026-02-01)

**Open/Closed Principle**: Open for extension, closed for modification.

### Tool Categories — Config-Driven (No Interface)

**Rationale**: Only 1 category type exists → interface not justified (dev_solid.md principle).

**Adding Ozon now requires only:**
1. Add `ozon` section to `config.yaml`
2. Add `ozon_client` to clients map in `Initialize()`
3. Add cases to `registerTool()` switch in `pkg/app/tool_setup.go`

**No `setupOzonTools()` function needed!**

```yaml
# config.yaml
tool_categories:
  ozon:
    enabled: true
    client: ozon_client
    tools:
      - search_ozon_products
      - get_ozon_categories
```

```go
// pkg/app/tool_setup.go - registerTool()
switch name {
// ... existing cases ...
case "search_ozon_products":
    tool = std.NewOzonProductSearchTool(client.(*ozon.Client), toolCfg, cfg.Ozon)
}
```

**Files**: [pkg/app/tool_setup.go](pkg/app/tool_setup.go), [config.yaml](config.yaml)

### Prompt Loading — Source Pattern (Interface Justified)

**Rationale**: ≥3 implementations (File, Database, API) → interface justified.

**Fallback Chain**: File sources (YAML) → Default source (Go code)

```yaml
# config.yaml
prompt_sources:
  - type: file
    config:
      base_dir: "${PROMPTS_DIR:-./prompts}"
  # Optional: Database source
  # - type: database
  #   config:
  #     connection_string: "${DB_URL}"
  #     table: "prompts"
```

**Adding new prompt source:**
1. Implement `Load(promptID) (*PromptData, error)` in `pkg/prompts/sources/`
2. Add adapter in `pkg/prompts/registry_factory.go` (3 lines)
3. Add type to `config.yaml`

**No loader functions need modification!**

**Files**:
- [pkg/prompts/source.go](pkg/prompts/source.go) - PromptSource interface
- [pkg/prompts/types.go](pkg/prompts/types.go) - PromptFile, ErrNotFound
- [pkg/prompts/source_registry.go](pkg/prompts/source_registry.go) - SourceRegistry with fallback
- [pkg/prompts/registry_factory.go](pkg/prompts/registry_factory.go) - Factory + adapters
- [pkg/prompts/sources/file_source.go](pkg/prompts/sources/file_source.go) - YAML files
- [pkg/prompts/sources/default_source.go](pkg/prompts/sources/default_source.go) - Go defaults
- [pkg/prompts/sources/api_source.go](pkg/prompts/sources/api_source.go) - HTTP API (example)
- [pkg/prompts/sources/database_source.go](pkg/prompts/sources/database_source.go) - SQL DB (example)

### OCP Benefits Summary

| Aspect | Before | After |
|--------|--------|-------|
| **Adding new e-commerce API** | Modify `SetupTools()`, add `setupXxxTools()` | Add to config.yaml, add switch cases |
| **Adding new prompt source** | Rewrite loader functions | Implement PromptSource interface |
| **Configuration** | Partially hardcoded | Fully declarative YAML |
| **Rule 6 compliance** | Partial | Full (pkg/ independent) |
| **Interface justification** | N/A | Source: ≥3 impl, Category: no interface |

**YAML-first Philosophy**: All configuration through YAML, Go code provides sensible defaults.

---

## Testing

```bash
# Run all tests
go test ./...

# Run specific package
go test ./pkg/wb/ -v
go test ./cmd/data-downloaders/download-wb-sales/ -v

# Run specific test
go test ./pkg/wb/ -run TestAdaptiveRateLimit_ReducesOn429 -v

# Run with coverage
go test ./... -coverprofile=coverage.out
```

### Test Layers

**Unit tests** (`pkg/`): Test internal logic with mocked dependencies.
- `pkg/wb/client_adaptive_test.go` — Adaptive rate limiting: 429 recovery, toolID mismatch detection, retry behavior. Uses `mockHTTPClient` (implements `HTTPClient` interface) to exercise real retry loop + `adaptiveReduce()` without network.
- `pkg/chain/*_test.go` — ReAct cycle components (6 files)
- `pkg/tui/*_test.go` — TUI primitives (7 files)

**E2E tests** (`cmd/`): Test downloader logic with mock clients + SQLite.
- `cmd/data-downloaders/download-wb-sales/e2e_test.go` — Download, resume mode, retry on failure
- `cmd/data-downloaders/download-wb-promotion/e2e_test.go` — Campaign loading, stats hierarchy, resume

### Test Patterns

```go
// Mock HTTP client (for testing wb.Client internals — rate limiting, retries)
mockHTTP := &mockHTTPClient{responses: []*mockResponse{
    {status: 429, header: map[string]string{"X-Ratelimit-Retry": "1"}},
    {status: 200, body: `[]`},
}}
client := wb.New("test_key")
client.SetHTTPClient(mockHTTP)
client.SetRateLimit("tool_id", 6000, 100, 3000, 100)
// Verify: client.RateLimiters()["tool_id"] after 429

// Mock service client (for testing downloader logic — skips HTTP entirely)
mockClient := wb.NewMockClient()
mockClient.AddMockSales(rows...)
mockClient.SetFailCount(2) // fail first 2 requests, succeed on 3rd

// App-level mock (implements PromotionClient interface)
mock := NewMockPromotionClient()
PopulateMockData(mock, 5, 7) // 5 campaigns, 7 days
```

### Key: Why Two Mock Levels

| Mock | What it tests | Skips |
|------|--------------|-------|
| `mockHTTPClient` (HTTPClient) | `doRequest()` retry loop, `adaptiveReduce()`, `getOrCreateLimiter()` | Service layer |
| `MockClient` / `MockPromotionClient` (Service) | Downloader batch logic, DB save, resume mode | Rate limiting |

Service-level mocks are **faster** but **cannot catch rate limiting bugs** (they bypass HTTP).
HTTP-level mocks exercise the real retry loop and catch configuration/runtime mismatches.

---

## E2E Testing Infrastructure

### SnapshotDBClient (`pkg/wb/snapshot_client.go`)

**Purpose**: Fast, deterministic E2E tests without API rate limits.

SnapshotDBClient reads data from SQLite database instead of WB API.

**Services**:
- `Sales()` - funnel metrics, sales data
- `Advertising()` - campaigns, daily stats
- `Feedbacks()` - feedbacks, questions

**Required Tables**:
```
sales, funnel_metrics_daily, products,
campaigns, campaign_stats_daily,
feedbacks_items, questions_items
```

**Usage**:
```go
client, _ := wb.NewSnapshotDBClient("e2e-snapshot.db")
defer client.Close()

// Use like regular Service
svc := wb.NewServiceFromSnapshot(client)
funnel, _ := svc.Sales().GetFunnel(ctx, nmIDs, dateFrom, dateTo)
```

**Files**:
- [pkg/wb/snapshot_client.go](pkg/wb/snapshot_client.go) - Main client
- [pkg/wb/snapshot_sales.go](pkg/wb/snapshot_sales.go) - SalesService impl
- [pkg/wb/snapshot_advertising.go](pkg/wb/snapshot_advertising.go) - AdvertisingService impl
- [pkg/wb/snapshot_feedbacks.go](pkg/wb/snapshot_feedbacks.go) - FeedbackService impl

### E2E Mock Collector (`examples/e2e-mock-collector/`)

**Purpose**: Collect real data from WB APIs and store in SQLite for testing.

**Collection Order** (CRITICAL):
1. **Sales FIRST** - extracts real nmIDs for the period
2. Funnel metrics (uses nmIDs from sales)
3. Search positions, organic positions
4. Campaigns and daily stats
5. Feedbacks (answered + unanswered)
6. Questions (answered + unanswered)

**Rate Limits**:
| API | Delay | Limit |
|-----|-------|-------|
| Analytics | 21s | 3 req/min |
| Adverts | 21s | 3 req/min |
| Feedbacks | 333ms | 3 req/sec |
| Content | 0.6s | 100 req/min |

**Usage**:
```bash
cd examples/e2e-mock-collector
go run main.go --days 7 --output ../e2e-snapshot.db
```

### Download Utilities (`cmd/download-*`)

All downloaders use **two-level adaptive rate limiting** (see [dev_limits.md](dev_limits.md)):
start at desired rate, auto-reduce on 429, recover to api floor, probe desired again.

| Utility | Purpose | Config |
|---------|---------|--------|
| `download-wb-sales` | Sales + funnel metrics by period | `config.yaml` |
| `download-wb-promotion` | Campaigns + daily stats (4-level hierarchy) | `config.yaml` |
| `download-wb-feedbacks` | Feedbacks + questions (39 fields) | `config.yaml` |
| `download-wb-funnel` | Analytics v3 funnel (daily per product) | `config.yaml` |
| `download-wb-funnel-agg` | Analytics v3 aggregated funnel | `config.yaml` |
| `download-all-articles` | S3 article processing | — |

**Example**:
```bash
# Download 7 days of sales data
cd cmd/download-wb-sales
go run main.go --days 7 --output sales.db

# Download promotion data with resume
cd cmd/download-wb-promotion
go run main.go --begin 2025-01-01 --end 2025-01-31 --resume
```

---

## Adaptive Rate Limiting (2026-03-28)

Two-level rate limiting in `pkg/wb/client.go` allows exceeding documented API limits
while safely handling 429 responses. Full developer guide: [dev_limits.md](dev_limits.md).

**Setup** (required for all downloaders):
```go
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
client.SetAdaptiveParams(recoverAfter, probeAfter, maxBackoff)
```

**Recovery cycle**: `desired` → 429 → `api floor` (after 5 OKs) → `probe desired` (after 10 OKs) → repeat.

**Key internals**:
- `getOrCreateLimiter()` — returns pre-set limiter, ignores inline rateLimit/burst params (fallback only)
- `adaptiveReduce()` — computes rate from `X-Ratelimit-Retry` header, exponential backoff
- `adaptiveRecoverOK()` — two-phase recovery with `probed` flag (one-shot probe per 429 cycle)
- `GetStream()` — streaming decode for large payloads (avoids io.ReadAll)

**Common pitfalls** (see [dev_limits.md](dev_limits.md) Pitfalls section):
1. Forgetting `SetRateLimit()` → adaptive state created lazily with `apiFloor=0`, limiter never reduces
2. `NewFromConfig()` ignores `WBConfig.RateLimit` — must call `SetRateLimit()` explicitly
3. Phase 2 probe spam without `probed` flag — fixed, but check if adding new downloader
4. Config fields exist but aren't wired → dead code (checklist in dev_limits.md)
5. Computing rate from `X-Ratelimit-Retry` is wrong — header is backoff hint, not rate indicator
6. **ToolID mismatch** — `SetRateLimit("tool_A")` + `Get("tool_B")` creates separate limiter with no adaptive state. Verify with: `grep -rn "SetRateLimit" cmd/ && grep -rn 'client.Get\|client.Post' cmd/`

**Testing** (see `pkg/wb/client_adaptive_test.go`):
```go
// SetHTTPClient() — inject mock for testing wb.Client internals
client.SetHTTPClient(mockHTTP)
// RateLimiters() — inspect current rate per toolID for assertions
rates := client.RateLimiters()
```

---

## WB API Endpoints Reference

### Statistics API (`statistics-api.wildberries.ru`)
| Endpoint | Purpose | Rate Limit |
|----------|---------|------------|
| `/api/v5/supplier/reportDetailByPeriod` | Sales realization report (87 fields) | 100/min |

### Content API (`suppliers-api.wildberries.ru`)
| Endpoint | Purpose | Rate Limit |
|----------|---------|------------|
| `/api/v3/stocks` | Product stocks | 100/min |
| `/api/v2/parent-categories` | Category tree | 100/min |
| `/api/v2/cards/filter` | Product list | 100/min |

### Analytics API (`analytics-api.wildberries.ru`)
| Endpoint | Purpose | Rate Limit |
|----------|---------|------------|
| `/api/analytics/v3/sales-funnel/products` | Product funnel | 3/min |
| `/api/analytics/v3/sales-funnel/products/history` | Daily trends | 3/min |
| `/api/v2/search-positions` | Search positions | 3/min |

### Advertising API (`advert-api.wildberries.ru`)
| Endpoint | Purpose | Rate Limit |
|----------|---------|------------|
| `/adv/v1/promotion/count` | Campaign list | 100/min |
| `/adv/v3/fullstats` | Daily campaign stats | 20/min |

### Feedbacks API (`feedbacks-api.wildberries.ru`)
| Endpoint | Purpose | Rate Limit |
|----------|---------|------------|
| `/api/v1/feedbacks` | Product feedbacks | 3 req/sec, burst 6 |
| `/api/v1/questions` | Customer questions | 3 req/sec, burst 6 |

**API Keys**:
- `WB_API_KEY` - Content, Analytics, Advertising APIs
- `WB_API_FEEDBACK_KEY` - Feedbacks API (separate key)
- `WB_STAT_API_KEY` - Statistics API (optional, for sales data)

---

**Last Updated**: 2026-03-28
**Version**: 12.0 (testing infrastructure, adaptive rate limiting tests, toolID mismatch pitfall)
