# Poncho AI - Project Memory

## Project Overview

**Poncho AI** - это LLM-agnostic, Tool-centric framework на Go для создания AI-агентов с упором на автоматизацию работы с маркетплейсами (Wildberries, Ozon) и обработкой мультимодальных данных.

### Key Value Proposition
- **Raw In, String Out** - инструменты принимают JSON и возвращают строки
- **TUI-first подход** на базе Bubble Tea для высокопроизводительного интерфейса
- **Реестр инструментов** - модульное расширение функциональности
- **Context Injection** - автоматическая инъекция контекста в промпты для экономии токенов

## Architecture

### Directory Structure

```
poncho-ai/
├── cmd/                    # Entry points (приложения)
├── internal/               # Application-specific logic
│   ├── app/               # GlobalState, CommandRegistry
│   └── ui/                # Bubble Tea UI (model, view, update)
├── pkg/                    # Reusable library packages
│   ├── classifier/        # File classification engine
│   ├── config/            # YAML configuration with ENV support
│   ├── factory/           # LLM provider factory
│   ├── llm/               # LLM abstraction layer
│   │   └── openai/        # OpenAI-compatible adapter
│   ├── prompt/            # Prompt template system
│   ├── s3storage/         # S3-compatible storage client
│   ├── todo/              # Todo manager for agent planning
│   ├── tools/             # Tool registry and types
│   │   ├── std/           # Standard tools (s3, wb_catalog, planner)
│   │   └── types.go       # Tool interface
│   └── wb/                # Wildberries API client
├── prompts/                # YAML prompt templates
├── cfg/                    # Configuration files
├── _docs/                  # Documentation
└── _input/                 # External inputs (API docs, etc.)
```

### Core Interfaces

#### Tool Interface (pkg/tools/types.go)
```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```
**Never change this interface** - it's the foundation of the framework.

#### LLM Provider (pkg/llm/provider.go)
```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, tools ...any) (Message, error)
}
```

## Key Components

### GlobalState (internal/app/state.go)
Thread-safe central store with:
- `History []llm.Message` - Conversation history
- `Files map[string][]*FileMeta` - Working memory (file analysis results)
- `Todo *todo.Manager` - Task planner
- `CommandRegistry *CommandRegistry` - TUI commands
- `ToolsRegistry *tools.Registry` - Agent tools
- `Config *config.AppConfig` - App configuration
- `S3 *s3storage.Client` - Storage client
- `Dictionaries *wb.Dictionaries` - WB reference data

Key method: `BuildAgentContext(systemPrompt string) []llm.Message` - aggregates system prompt, file analysis, todo context, and history.

### Tool Registry (pkg/tools/registry.go)
Thread-safe service locator pattern:
- `Register(tool Tool)` - Register a tool
- `Get(name string) (Tool, error)` - Retrieve by name
- `GetDefinitions() []ToolDefinition` - Get all for LLM

### Todo Manager (pkg/todo/manager.go)
Thread-safe task planner with:
- Task statuses: PENDING, DONE, FAILED
- Metadata support for each task
- `String()` method for context injection
- Methods: Add, Complete, Fail, Clear

### Standard Tools (pkg/tools/std/)
- **s3_tools.go** - S3 operations (list, upload, download images)
- **wb_catalog.go** - Wildberries catalog operations
- **planner.go** - Todo management (plan_add_task, plan_mark_done, plan_mark_failed, plan_clear)

### LLM Factory (pkg/factory/llm_factory.go)
Creates providers based on config:
- Supports: "zai", "openai", "deepseek" (all via OpenAI-compatible adapter)
- Dynamic provider creation from YAML config

## Configuration (pkg/config/config.go)

YAML-based with ENV variable support (`${VAR}` syntax):

```yaml
models:
  default_vision: "glm-4.6v-flash"
  default_chat: "glm-4.5"
  definitions:
    glm-4.6v-flash:
      provider: "zai"
      model_name: "glm-4v-flash"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.zai.ai/v1"
      max_tokens: 4096
      temperature: 0.7
      timeout: 60s

s3:
  endpoint: "${S3_ENDPOINT}"
  bucket: "${S3_BUCKET}"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"

file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.png"]
    required: true
  - tag: "plm"
    patterns: ["*_spec.json"]
    required: false

wb:
  api_key: "${WB_API_KEY}"
```

## Design Patterns Used

1. **Registry Pattern** - Tool registration and discovery
2. **Factory Pattern** - LLM provider creation
3. **Strategy Pattern** - File classification strategies
4. **Command Pattern** - TUI command registry
5. **Adapter Pattern** - OpenAI API compatibility
6. **Template Method** - Prompt rendering
7. **TEA (Model-View-Update)** - UI architecture

## Architectural Rules (from dev_manifest.md)

1. **Tool Interface** - Never change `Tool` interface
2. **Configuration** - All settings in YAML with ENV support, no hardcoded values
3. **Registry** - All tools registered through `Registry.Register()`
4. **LLM Abstraction** - Work with AI models only through `Provider` interface
5. **State** - Use `GlobalState` with thread-safe access, no global variables
6. **Package Structure**:
   - `pkg/` - Library code ready for reuse
   - `internal/` - Application-specific logic
   - `cmd/` - Entry points and orchestration only
7. **Error Handling** - Return errors up the stack, no `panic()` in business logic
8. **Extensibility** - Add features through new tools, LLM adapters, or config extensions

## Environment Variables Required

- `ZAI_API_KEY` - For ZAI AI provider
- `S3_ACCESS_KEY` / `S3_SECRET_KEY` - S3 storage credentials
- `WB_API_KEY` - Wildberries API key

## Key Dependencies

```
github.com/charmbracelet/bubbletea v1.3.10    # TUI framework
github.com/charmbracelet/lipgloss v1.1.0      # TUI styling
github.com/minio/minio-go/v7 v7.0.97          # S3 client
github.com/sashabaranov/go-openai v1.41.2     # OpenAI SDK
gopkg.in/yaml.v3 v3.0.1                       # YAML parsing
golang.org/x/time/rate v0.14.0                # Rate limiting
github.com/nfnt/resize                        # Image processing
```

## Development Commands

```bash
# Build main TUI app
go run cmd/poncho/main.go

# Run tests
go test ./...

# Build all binaries
go build ./cmd/...
```

## Important Notes

### Context Injection Pattern
- File analysis results are stored in `FileMeta.VisionDescription`
- `BuildAgentContext()` injects these into system prompts
- Avoids re-sending images to LLM (token savings)

### Thread Safety
- `GlobalState` uses `sync.RWMutex` for History, Files, IsProcessing
- `Registry` uses `sync.RWMutex` for tools map
- `Todo.Manager` uses `sync.RWMutex` for tasks slice

### Agent Execution Loop
1. Build context via `BuildAgentContext()`
2. Call LLM via `Provider.Generate()`
3. Sanitize response (clean JSON from markdown)
4. Route to tool execution or return text
5. Add result to history via `AppendMessage()`

### Tool Development
Create new tools in `pkg/tools/std/`:
1. Implement `Tool` interface
2. Register in main.go via `registry.Register()`
3. Follow "Raw In, String Out" principle

## Files to Reference

- `CLAUDE.md` - Development guidance for Claude Code
- `brief.md` - Comprehensive architecture documentation (Russian)
- `dev_manifest.md` - Architectural rules (Russian)
- `config.yaml` - Main configuration file

## Recent Changes

Based on git history:
- Cleaned up old command executables
- Refactored with short implementation files
- Added agentic todo list implementation
- Updated .gitignore for security
