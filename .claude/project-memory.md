# Poncho AI - Project Memory

## Project Overview

**Poncho AI** - Go-based LLM-agnostic, tool-centric framework для AI-агентов с ReAct pattern. Основной use case: автоматизация e-commerce (Wildberries) с мультимодальными возможностями.

### Key Value Proposition
- **Raw In, String Out** - инструменты принимают JSON, возвращают строки
- **Repository Pattern** - `CoreState` (pkg/) + `AppState` (internal/)
- **Chain Pattern** - модульная архитектура с composable steps
- **Tool Post-Prompts** - специализированные промпты + параметр override
- **Context Injection** - автоматическая инъекция контекста

## Architecture

### Directory Structure

```
poncho-ai/
├── cmd/                    # Entry points (autonomous utilities)
│   ├── poncho/            # Main TUI application
│   ├── chain-cli/         # Chain Pattern CLI
│   ├── debug-test/        # Debug logging test
│   ├── wb-tools-test/     # WB tools test
│   ├── vision-cli/        # Vision analysis CLI
│   └── maxiponcho/        # Fashion PLM analyzer
├── internal/              # Application-specific logic
│   ├── agent/            # Orchestrator (ReAct loop)
│   ├── app/              # AppState (embeds CoreState)
│   └── ui/               # Bubble Tea TUI
├── pkg/                   # Reusable library (Rule 6: no internal/ imports)
│   ├── agent/            # Agent interface
│   ├── app/              # Component initialization
│   ├── chain/            # Chain Pattern (modular execution)
│   ├── classifier/       # File classification
│   ├── config/           # YAML + ENV configuration
│   ├── debug/            # JSON trace recording
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction + Options Pattern
│   ├── prompt/           # Prompt loading + post-prompts
│   ├── s3storage/        # S3-compatible storage
│   ├── state/            # CoreState (Repository Pattern)
│   ├── todo/             # Thread-safe task manager
│   ├── tools/            # Tool registry + std tools
│   ├── utils/            # JSON sanitization
│   └── wb/               # Wildberries API client
├── config.yaml           # Main configuration
└── prompts/              # YAML prompt templates
```

### Core Interfaces

#### Tool Interface (pkg/tools/types.go) - Rule 1
```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

#### LLM Provider with Options Pattern (pkg/llm/)
```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}

// Runtime overrides: WithModel, WithTemperature, WithMaxTokens, WithFormat
```

#### Agent Interface (pkg/agent/types.go)
```go
type Agent interface {
    Run(ctx context.Context, query string) (string, error)
    GetHistory() []llm.Message
}
```

#### Repository Interfaces (pkg/state/repository.go)
```go
type UnifiedStore interface { Get/Set/Update/Delete/Exists/List }
type MessageRepository interface { UnifiedStore + Append/GetHistory }
type FileRepository interface { UnifiedStore + UpdateFileAnalysis/GetFiles }
type TodoRepository interface { UnifiedStore + AddTask/CompleteTask/FailTask }
```

## Key Components

### State Management (Repository Pattern)

**CoreState** (pkg/state/core.go) - Framework core, reusable:
- Config, S3, Dictionaries, Todo, ToolsRegistry
- Thread-safe: History, Files (UnifiedStore with sync.RWMutex)
- Methods: BuildAgentContext, all repository operations

**AppState** (internal/app/state.go) - Application-specific:
- Embeds *CoreState via composition
- CommandRegistry, Orchestrator, UserChoice
- CurrentArticleID, CurrentModel, IsProcessing

### Tool Registry (pkg/tools/registry.go)
Thread-safe: Register(name, tool), Get(name), GetDefinitions()

### Standard Tools (pkg/tools/std/)
**WB Content API**: search_wb_products, get_wb_parent_categories, get_wb_subjects, ping_wb_api
**WB Feedbacks API**: get_wb_feedbacks, get_wb_questions, get_wb_unanswered_*_counts
**WB Dictionaries**: wb_colors, wb_countries, wb_genders, wb_seasons, wb_vat_rates
**S3**: list_s3_files, read_s3_object, read_s3_image, classify_and_download_s3_files
**Planner**: plan_add_task, plan_mark_done, plan_mark_failed, plan_clear

### Chain Pattern (pkg/chain/)
Modular step-based execution:
- **ReActCycle**: LLMInvocationStep + ToolExecutionStep
- **ChainContext**: Thread-safe execution context
- **ChainDebugRecorder**: JSON trace recording

### Tool Post-Prompts (pkg/prompts/)
Specialized prompts activated after tool execution:
- Override model parameters (model, temperature, max_tokens)
- Active for one iteration only
- Configured in config.yaml: `post_prompt: "wb/analysis.yaml"`

## Configuration (config.yaml)

YAML + ENV support (`${VAR}`):

```yaml
models:
  default_reasoning: "glm-4.6"    # Orchestrator (planning)
  default_chat: "glm-4.6"          # Chat responses
  default_vision: "glm-4.6v-flash" # Vision analysis
  definitions:
    glm-4.6:
      provider: "zai"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5

tools:
  get_wb_parent_categories:
    enabled: true
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5
    post_prompt: "wb/parent_categories_analysis.yaml"  # Override params

s3:
  endpoint: "storage.yandexcloud.net"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"
```

## Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| Repository | `pkg/state/` | Unified storage with domain interfaces |
| Registry | `pkg/tools/` | Tool registration/discovery |
| Factory | `pkg/factory/` | LLM provider creation |
| Options | `pkg/llm/` | Runtime parameter overrides |
| Strategy | `pkg/classifier/` | File classification |
| Command | `internal/app/` | TUI commands |
| Adapter | `pkg/llm/openai/` | OpenAI compatibility |
| Chain of Responsibility | `pkg/chain/` | Modular step execution |
| Template Method | `pkg/prompt/` | Prompt rendering |
| Composition | `internal/app/` | AppState embeds CoreState |
| TEA (MVU) | `internal/ui/` | Bubble Tea TUI |

## The 11 Immutable Rules (dev_manifest.md)

| Rule | Description |
|------|-------------|
| 0: Code Reuse | Use existing solutions first |
| 1: Tool Interface | NEVER change Tool interface |
| 2: Configuration | All settings in YAML + ENV |
| 3: Registry | All tools via Registry.Register() |
| 4: LLM Abstraction | Work only through Provider interface + Options Pattern |
| 5: State Management | Repository Pattern, thread-safe, no globals |
| 6: Package Structure | `pkg/` (reusable), `internal/` (app-specific), `cmd/` (entry points) |
| 7: Error Handling | Return errors, NO panic() in business logic |
| 8: Extensibility | Add through tools/LLM adapters/config |
| 9: Testing | CLI utilities in `/cmd` for verification |
| 10: Documentation | Public APIs must have godoc |
| 11: Resource Localization | Each app autonomous, stores resources nearby |

## Environment Variables

Required: `ZAI_API_KEY`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `WB_API_KEY`

## Key Dependencies

```
github.com/charmbracelet/bubbletea v1.3.10   # TUI framework
github.com/charmbracelet/lipgloss v1.1.0     # TUI styling
github.com/minio/minio-go/v7 v7.0.97         # S3 client
github.com/sashabaranov/go-openai v1.41.2    # OpenAI SDK
gopkg.in/yaml.v3 v3.0.1                      # YAML parsing
golang.org/x/time/rate v0.14.0               # Rate limiting
```

## Development Commands

```bash
# Main TUI app
go run cmd/poncho/main.go

# Chain CLI (modular execution + debug)
go run cmd/chain-cli/main.go "show categories"
./chain-cli -debug "show categories"  # With JSON trace

# Other CLIs
go run cmd/debug-test/main.go        # Test debug system
go run cmd/wb-tools-test/main.go     # Test WB tools
go run cmd/vision-cli/main.go -article 12345
go run cmd/maxiponcho/main.go        # Fashion PLM analyzer

# Build all
go build ./cmd/...
```

## Important Notes

### Thread-Safety Guarantees
- CoreState: All fields protected by sync.RWMutex
- AppState: Application fields protected by sync.RWMutex
- ToolsRegistry: Concurrent registration/lookup
- TodoManager: Atomic task operations
- wb.Client.limiters: Map protected by sync.RWMutex

### Tool Development
1. Create file in `pkg/tools/std/`
2. Implement `Tool` interface (Raw In, String Out)
3. Add config to `config.yaml` under `tools:`
4. Add tool name to `getAllKnownToolNames()` in `pkg/app/components.go`

### Rule 6 Compliance (Package Structure)
- `pkg/state/` has NO imports from `internal/` ✅
- `pkg/chain/` works with CoreState only ✅
- Framework core reusable across CLI, TUI, HTTP, gRPC

## Files to Reference

- `CLAUDE.md` - Development guidance for Claude Code (comprehensive)
- `brief.md` - Architecture documentation (Russian)
- `dev_manifest.md` - Architectural rules (Russian)
- `config.yaml` - Main configuration

## Recent Changes (2026-01-04)

- ✅ **Repository Pattern** - Unified storage with domain interfaces
- ✅ **Chain Pattern** - Modular step-based execution
- ✅ **Tool Post-Prompts** - Specialized prompts with param override
- ✅ **Debug System** - JSON trace recording
- ✅ **Code Optimization** - Removed ~300 lines of unused methods
- ✅ **Rule 6 Compliance** - `pkg/` no longer imports `internal/`
- ✅ **Options Pattern** - Runtime LLM parameter overrides
- ✅ **Dual Architecture** - Orchestrator (TUI) + Chain (CLI)
