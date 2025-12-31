# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct (Reasoning + Acting) pattern. The framework provides structured approach to agent development, handling routine tasks (prompt engineering, JSON validation, conversation history, task planning) while allowing developers to focus on business logic through isolated tools.

**Primary Use Case**: E-commerce automation for Wildberries marketplace with multimodal AI capabilities for processing fashion sketches and PLM data.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings, ensuring maximum flexibility and minimal dependencies.

---

## Architecture Overview

### High-Level Structure

```
poncho-ai/
├── cmd/                    # Application entry points (autonomous utilities)
│   ├── poncho/            # Main TUI application (primary interface)
│   ├── maxiponcho/        # Fashion PLM analyzer (TUI)
│   ├── vision-cli/        # CLI utility for vision analysis
│   └── wb-tools-test/     # CLI utility for testing WB tools
├── internal/              # Application-specific logic
│   ├── agent/            # Orchestrator implementation (ReAct loop)
│   ├── app/              # Global state, command registry, types
│   └── ui/               # Bubble Tea TUI (Model-View-Update)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent interface (avoids circular imports)
│   ├── app/              # Component initialization (shared across entry points)
│   ├── config/           # YAML configuration with ENV support
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer + options pattern
│   ├── prompt/           # Prompt loading and rendering + post-prompts
│   ├── s3storage/        # S3-compatible storage client
│   ├── state/            # State types (avoid circular imports)
│   ├── todo/             # Thread-safe task manager
│   ├── tools/            # Tool system (registry + std tools)
│   ├── utils/            # JSON sanitization utilities
│   └── wb/               # Wildberries API client
└── config.yaml           # Main configuration file
```

### Component Dependency Graph

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           LAYER ARCHITECTURE                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐       │
│  │   cmd/poncho    │───▶│  GlobalState     │◀───│ CommandRegistry │       │
│  │  (Entry Point)  │    │  (internal/app)  │    │                 │       │
│  └─────────────────┘    └──────────────────┘    └─────────────────┘       │
│           │                       │                       │                │
│           ▼                       ▼                       ▼                │
│  ┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐       │
│  │ Orchestrator    │◀───│ ToolsRegistry    │    │   UI (Bubble)   │       │
│  │ (internal/agent)│    │ (pkg/tools)      │    │                 │       │
│  └─────────────────┘    └──────────────────┘    └─────────────────┘       │
│           │                       │                                        │
│           ▼                       ▼                                        │
│  ┌─────────────────┐    ┌──────────────────┐                              │
│  │ LLM Provider    │◀───│  Tool Interface  │                              │
│  │ (pkg/llm)       │    │  (pkg/tools)     │                              │
│  │ + Options       │    │                  │                              │
│  └─────────────────┘    └──────────────────┘                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## The 11 Immutable Rules (from dev_manifest.md)

### Rule 0: Code Reuse
- Any development must first use existing solutions
- Refactoring is acceptable if existing code hinders development
- Applies to `/cmd`, `/internal`, and `/pkg`

### Rule 1: Tool Interface Contract
**NEVER change** the `Tool` interface:
```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```
"Raw In, String Out" principle is immutable.

### Rule 2: Configuration
All settings in YAML with ENV variable support. No hardcoded values.

### Rule 3: Registry Usage
All tools registered via `Registry.Register()`. No direct calls bypassing registry.

### Rule 4: LLM Abstraction
Work with AI models **only** through `Provider` interface. No direct API calls.

**Important**: Single Provider with Options Pattern - NOT multiple clients.

### Rule 5: State Management
Use `GlobalState` with thread-safe access. No global variables.

### Rule 6: Package Structure
- `pkg/` - Library code ready for reuse
- `internal/` - Application-specific logic
- `cmd/` - Entry points (initialization and orchestration only)

### Rule 7: Error Handling
Return errors up the stack. **No `panic()`** in business logic. Framework must be resilient to LLM hallucinations.

### Rule 8: Extensibility
Add features through:
- New tools in `pkg/tools/std/` or custom packages
- New LLM adapters in `pkg/llm/`
- Configuration extensions (no breaking changes)

### Rule 9: Testing
All tools must support mocking dependencies. No direct HTTP calls without abstraction.
**No tests initially** - create CLI utility in `/cmd` for verification instead.

### Rule 10: Documentation
All public APIs must have godoc comments.

### Rule 11: Resource Localization
Each application in `/cmd` must be **autonomous** and store resources nearby:
- **Config**: `{app_dir}/config.yaml` (searches binary directory first)
- **Prompts**: `{app_dir}/prompts/` (flat structure)

---

## Core Architectural Components

### 1. Tool System (`pkg/tools/`)

**Design Principle**: "Raw In, String Out"

All functionality is implemented as tools conforming to the `Tool` interface:

```go
type Tool interface {
    Definition() ToolDefinition              // Metadata for LLM
    Execute(ctx context.Context, argsJSON string) (string, error)  // Business logic
}
```

**Key Features**:
- **Registry Pattern**: `Registry.Register(tool)` for dynamic tool registration
- **Thread-Safe**: All operations protected by `sync.RWMutex`
- **YAML-Driven**: Tools enabled/disabled via `config.yaml`
- **Validation**: Tool definitions validated against JSON Schema

**Standard Tools** (`pkg/tools/std/`):

| Category | Tools |
|----------|-------|
| **WB Content API** | `search_wb_products`, `get_wb_parent_categories`, `get_wb_subjects`, `ping_wb_api` |
| **WB Feedbacks API** | `get_wb_feedbacks`, `get_wb_questions`, `get_wb_new_feedbacks_questions`, `get_wb_unanswered_*_counts` |
| **WB Dictionaries** | `wb_colors`, `wb_countries`, `wb_genders`, `wb_seasons`, `wb_vat_rates` |
| **S3 Basic** | `list_s3_files`, `read_s3_object`, `read_s3_image` |
| **S3 Batch** | `classify_and_download_s3_files`, `analyze_article_images_batch` |
| **Planner** | `plan_add_task`, `plan_mark_done`, `plan_mark_failed`, `plan_clear` |

### 2. LLM Abstraction with Options Pattern (`pkg/llm/`)

**Options Pattern for Runtime Parameter Overrides**

The framework supports runtime parameter overrides through functional options:

```go
type GenerateOptions struct {
    Model       string
    Temperature float64
    MaxTokens   int
    Format      string  // "json_object" or ""
}

type GenerateOption func(*GenerateOptions)

func WithModel(model string) GenerateOption
func WithTemperature(temp float64) GenerateOption
func WithMaxTokens(tokens int) GenerateOption
func WithFormat(format string) GenerateOption
```

**Provider Interface**:
```go
type Provider interface {
    // Supports both tools and runtime options
    Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}
```

**Usage Examples**:
```go
// Default (uses config.yaml values)
llm.Generate(ctx, messages)

// Override model
llm.Generate(ctx, messages, llm.WithModel("glm-4.6"))

// With tools + options
llm.Generate(ctx, messages, toolDefs, llm.WithTemperature(0.5))
```

### 3. Orchestrator (`internal/agent/orchestrator.go`)

**Purpose**: Coordinates interaction between LLM and Tools using ReAct pattern.

**Key Fields**:
```go
type Orchestrator struct {
    llm                llm.Provider           // Single provider (Rule 4)
    registry           *tools.Registry
    state              *app.GlobalState
    systemPrompt       string
    toolPostPrompts    *prompt.ToolPostPromptConfig

    // Model parameter management
    reasoningConfig    llm.GenerateOptions   // From config.yaml
    chatConfig         llm.GenerateOptions   // From config.yaml
    activePromptConfig *prompt.PromptConfig  // From post-prompt
    activePostPrompt   string                // Current post-prompt text
}
```

**Execution Flow**:
```
1. BuildAgentContext() → Collect all context
2. Determine LLM parameters:
   - If activePromptConfig → use prompt config (highest priority)
   - Else → use reasoningConfig (default)
3. llm.Generate() with opts + tool definitions
4. SanitizeLLMOutput() → Clean markdown wrappers
5. Execute Tools → For each ToolCall:
   a. CleanJsonBlock(tc.Args) → Sanitize JSON
   b. registry.Get(tc.Name) → Find tool
   c. tool.Execute(ctx, cleanArgs) → Run tool
   d. AppendMessage(result) → Add to history
   e. LoadToolPostPrompt(tc.Name) → Load PromptFile
   f. Set activePromptConfig → Activate for next iteration
   g. Loop back to step 2
6. Return final response (when no tool calls)
7. Reset activePostPrompt and activePromptConfig
```

### 4. Tool Post-Prompts

**Purpose**: Specialized system prompts that activate after tool execution to guide LLM response formatting AND override model parameters.

**Configuration** (`config.yaml`):
```yaml
tools:
  get_wb_parent_categories:
    enabled: true
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/parent/all"
    rate_limit: 100
    burst: 5
    post_prompt: "wb/parent_categories_analysis.yaml"
```

**Prompt File** (`prompts/wb/parent_categories_analysis.yaml`):
```yaml
config:
  model: "glm-4.6"       # Override model
  temperature: 0.7       # Override temperature
  max_tokens: 2000       # Override max_tokens

messages:
  - role: system
    content: |
      Format the parent categories as a structured table...
```

**Flow**: Post-prompts are active for **one iteration** only.

### 5. Global State Management (`internal/app/state.go`)

**Purpose**: Thread-safe centralized state for the entire application.

**Components**:
```go
type GlobalState struct {
    Config          *config.AppConfig
    S3              *s3storage.Client
    Dictionaries    *wb.Dictionaries
    Todo            *todo.Manager
    CommandRegistry *CommandRegistry
    ToolsRegistry   *tools.Registry
    Orchestrator    agent.Agent

    mu              sync.RWMutex     // Protects fields below
    History         []llm.Message
    Files           map[string][]*state.FileMeta  // "Working Memory"
    CurrentArticleID string
    CurrentModel    string
    IsProcessing    bool
}
```

**"Working Memory" Pattern**: Vision model results are stored in `FileMeta.VisionDescription` and injected into prompts without re-sending images.

### 6. Configuration (`config.yaml`)

**Structure** with reasoning/chat separation:

```yaml
# Models Configuration
models:
  default_reasoning: "glm-4.6"    # For orchestrator (planning, tool selection)
  default_chat: "glm-4.6"          # For chat responses
  default_vision: "glm-4.6v-flash" # For vision analysis

  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5
      timeout: "120s"

# Tools Configuration (YAML-driven registration)
tools:
  get_wb_parent_categories:
    enabled: true
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/parent/all"
    rate_limit: 100
    burst: 5
    post_prompt: "wb/parent_categories_analysis.yaml"

# S3 Storage
s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"

# File Classification Rules
file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.jpeg", "*.png"]
    required: true

# Wildberries API
wb:
  api_key: "${WB_API_KEY}"
  base_url: "https://content-api.wildberries.ru"
  rate_limit: 100
  burst_limit: 5
  retry_attempts: 3
  timeout: "30s"
```

**Helper Methods**:
```go
cfg.GetReasoningModel(name)  // Returns ModelDef for reasoning
cfg.GetChatModel(name)       // Returns ModelDef for chat
cfg.GetVisionModel(name)     // Returns ModelDef for vision
```

---

## Request Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         REQUEST FLOW DIAGRAM                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. USER INPUT                                                               │
│     │ "покажи родительские категории товаров на wb"                          │
│     ▼                                                                        │
│  2. TUI (Bubble Tea)                                                         │
│     │ update.go: Unknown command → delegates to agent                        │
│     ▼                                                                        │
│  3. ORCHESTRATOR.Run()                                                       │
│     │ - Appends user message to history (thread-safe)                        │
│     │ - Starts ReAct loop (max 10 iterations)                               │
│     ▼                                                                        │
│  4. BUILD CONTEXT                                                            │
│     │ state.BuildAgentContext(systemPrompt)                                  │
│     │ - System prompt + File metadata + Todos + History                      │
│     ▼                                                                        │
│  5. DETERMINE LLM PARAMETERS                                                 │
│     │ if activePromptConfig != nil:                                          │
│     │     opts = [WithModel(prompt.model), WithTemp(prompt.temp)]           │
│     │ else:                                                                  │
│     │     opts = [WithModel(reasoning.model), WithTemp(reasoning.temp)]     │
│     ▼                                                                        │
│  6. LLM GENERATE                                                             │
│     │ llm.Generate(ctx, messages, opts..., toolDefs)                         │
│     │ - Applies opts to baseConfig                                          │
│     │ - Returns response with ToolCalls[{Name, Args, ID}]                   │
│     ▼                                                                        │
│  7. SANITIZE RESPONSE                                                        │
│     │ utils.SanitizeLLMOutput(response.Content)                              │
│     │ - Removes markdown wrappers, trims whitespace                          │
│     ▼                                                                        │
│  8. EXECUTE TOOLS (if ToolCalls present)                                     │
│     │ For each ToolCall:                                                     │
│     │   a. registry.Get(tc.Name) → find tool                                │
│     │   b. utils.CleanJsonBlock(tc.Args) → sanitize JSON                    │
│     │   c. tool.Execute(ctx, cleanArgs) → "Raw In, String Out"              │
│     │   d. tool uses client.Get(toolID, endpoint, rateLimit, burst, ...)   │
│     │   e. state.AppendMessage(result) → add to history                     │
│     │   f. LoadToolPostPrompt(tc.Name) → Load PromptFile                    │
│     │   g. Set activePostPrompt and activePromptConfig                      │
│     │   h. Loop back to step 4                                               │
│     ▼                                                                        │
│  9. FINAL RESPONSE (no ToolCalls)                                            │
│     │ Reset activePostPrompt and activePromptConfig                          │
│     │ Return response.Content to user                                        │
│     ▼                                                                        │
│  10. TUI DISPLAY                                                             │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Key Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Registry** | `pkg/tools/registry.go` | Tool registration and discovery |
| **Factory** | `pkg/factory/llm_factory.go` | LLM provider creation |
| **Options** | `pkg/llm/options.go` | Runtime parameter overrides |
| **Component Initialization** | `pkg/app/components.go` | Reusable initialization across entry points |
| **Strategy** | `pkg/classifier/` | File classification rules |
| **Command** | `internal/app/commands.go` | TUI command handling |
| **Template Method** | `pkg/prompt/` | Prompt rendering with Go templates |
| **Context Injection** | `internal/app/state.go` | Automatic context inclusion in prompts |
| **Adapter** | `pkg/llm/openai/` | OpenAI-compatible API adapter |
| **Model-View-Update** | `internal/ui/` | Bubble Tea TUI architecture |
| **ReAct** | `internal/agent/orchestrator.go` | Agent reasoning and acting loop |

---

## Building and Running

```bash
# Main TUI application (primary interface)
go run cmd/poncho/main.go
go build -o poncho cmd/poncho/main.go

# WB Tools Test CLI
go run cmd/wb-tools-test/main.go -query "show categories"

# Vision CLI
go run cmd/vision-cli/main.go -article 12345

# Maxiponcho (Fashion PLM analyzer)
go run cmd/maxiponcho/main.go
```

---

## Creating a New Tool

1. Create file in `pkg/tools/std/` (e.g., `my_tool.go`)
2. Implement `Tool` interface
3. Add tool config to `config.yaml` under `tools:`
4. Add tool name to `getAllKnownToolNames()` in `pkg/app/components.go`

```go
type MyTool struct {
    client    *wb.Client
    toolID    string
    endpoint  string
    rateLimit int
    burst     int
}

func NewMyTool(client *wb.Client, cfg config.ToolConfig) *MyTool {
    return &MyTool{
        client:    client,
        toolID:    "my_tool",
        endpoint:  cfg.Endpoint,
        rateLimit: cfg.RateLimit,
        burst:     cfg.Burst,
    }
}

func (t *MyTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        t.toolID,
        Description: "What this tool does",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "param1": map[string]interface{}{
                    "type":        "string",
                    "description": "Description of param1",
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
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }

    result := map[string]interface{}{
        "result": "success",
        "data": args.Param1,
    }
    jsonResult, _ := json.Marshal(result)
    return string(jsonResult), nil
}
```

**Important**: Tools without parameters MUST include `"required": []string{}`.

---

## Environment Variables

Required:
- `ZAI_API_KEY` - ZAI AI provider API key
- `S3_ACCESS_KEY` - S3 storage access key
- `S3_SECRET_KEY` - S3 storage secret key
- `WB_API_KEY` - Wildberries API key

---

## Key Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework (Model-View-Update) |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling |
| `github.com/minio/minio-go/v7` | v7.0.97 | S3 compatible storage |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML configuration |
| `github.com/sashabaranov/go-openai` | v1.41.2 | OpenAI-compatible API |
| `golang.org/x/time/rate` | v0.14.0 | Rate limiting |

---

## Important Implementation Notes

### Per-Tool Rate Limiting

The `wb.Client` stores a map of limiters, each keyed by tool ID:

```go
type Client struct {
    apiKey        string
    httpClient    HTTPClient
    retryAttempts int

    mu       sync.RWMutex
    limiters map[string]*rate.Limiter // tool ID → limiter
}
```

This allows:
- `get_wb_feedbacks` to have 60 req/min limit
- `get_wb_parent_categories` to have 100 req/min limit
- Each tool gets its own rate limiter instance

### Thread-Safety Guarantees

The following components are thread-safe:
- `GlobalState` - all fields protected by `sync.RWMutex`
- `ToolsRegistry` - concurrent tool registration and lookup
- `TodoManager` - atomic task operations
- `CommandRegistry` - safe command registration
- `wb.Client.limiters` - map protected by `sync.RWMutex`

### Error Handling (Rule 7)

**Principle**: No panics in business logic. All errors returned up the stack.

**Implementation**:
```go
// Tool errors are returned as strings to LLM
result, err := tool.Execute(ctx, cleanArgs)
if err != nil {
    return fmt.Sprintf("Tool execution error: %v", err)
}

// Orchestrator never panics
return "", fmt.Errorf("llm generation failed: %w", err)
```

**Benefit**: Framework is resilient against LLM hallucinations.
