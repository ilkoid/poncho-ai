# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a Go-based LLM-agnostic, tool-centric framework designed for building AI agents. It provides a structured approach to agent development, handling routine tasks like prompt engineering, JSON validation, conversation history, and task planning, while allowing developers to focus on implementing business logic through isolated tools.

The framework is specifically designed for e-commerce automation (Wildberries marketplace integration) with multimodal AI capabilities for processing fashion sketches and PLM data.

## Architecture

The project follows a modular architecture with clear separation of concerns:

```
poncho-ai/
├── cmd/              # Application entry points
│   ├── poncho/               # Main TUI application (primary interface)
│   └── orchestrator-test/    # CLI utility for testing orchestrator
├── internal/         # Application-specific logic (UI, state, commands)
│   ├── agent/               # Orchestrator implementation (ReAct loop)
│   ├── app/         # Global state, command registry, types
│   └── ui/          # Bubble Tea TUI components (model, view, update, styles)
├── pkg/              # Reusable library packages
│   ├── agent/                # Agent interface (avoids circular imports)
│   ├── app/         # Component initialization (shared across entry points)
│   ├── classifier/  # File classification engine
│   ├── config/      # YAML configuration with ENV support
│   ├── factory/     # LLM provider factory
│   ├── llm/         # LLM abstraction layer
│   ├── prompt/      # Prompt loading and rendering
│   ├── s3storage/   # S3-compatible storage client
│   ├── wb/          # Wildberries API client
│   ├── todo/        # Task manager for agent planning
│   └── tools/       # Tool system (registry + std tools)
├── prompts/         # YAML prompt templates
└── config.yaml      # Main configuration file
```

### Core Components

#### 1. Tool System (`pkg/tools/`)
- All functionality is implemented as tools conforming to the `Tool` interface
- "Raw In, String Out" principle - tools receive raw JSON from LLM and return strings
- Registry pattern allows dynamic tool registration and discovery
- Standard tools in `pkg/tools/std/`:
  - `planner.go` - Task management (plan_add_task, plan_mark_done, plan_mark_failed, plan_clear)
  - `s3_tools.go` - S3 storage operations
  - `wb_catalog.go` - Wildberries catalog integration including ping_wb_api

#### 2. LLM Abstraction (`pkg/llm/`)
- Provider-agnostic interface for AI models
- OpenAI-compatible adapter covers 99% of modern APIs
- Factory pattern for dynamic provider creation via `pkg/factory/`
- Support for "thinking" mode in compatible models
- Message format supports: text, tool calls, images (for vision queries)

#### 3. Component Initialization (`pkg/app/`)
- **Purpose**: Provides reusable initialization logic across all entry points (TUI, CLI, HTTP, etc.)
- **Components struct**: Holds all initialized dependencies (Config, State, LLM, WBClient, Orchestrator)
- **Key Functions**:
  - `InitializeConfig(finder)` - Loads configuration with auto-discovery
  - `Initialize(cfg, maxIters, systemPrompt, toolSet ToolSet)` - Creates all components with selective tool registration
  - `Execute(components, query, timeout)` - Runs agent query and returns results
  - `SetupTools(state, wbClient, toolSet ToolSet)` - Registers tools based on bit flags
- **ToolSet Bit Flags** for selective registration:
  ```go
  const (
      ToolWB      ToolSet = 1 << iota  // Wildberries tools
      ToolOzon                          // Ozon tools (future)
      ToolPlanner                       // Task planner tools
      ToolS3                            // S3 tools (future)

      // Predefined combinations
      ToolsWBWithPlanner  = ToolWB | ToolPlanner
      ToolsMarketplaces   = ToolWB | ToolOzon
      ToolsAll            = ToolWB | ToolOzon | ToolPlanner | ToolS3
      ToolsMinimal        = ToolPlanner
  )
  ```
- **Benefits**: No duplication, testability via `ConfigPathFinder` interface, consistency across entry points, token efficiency through selective tool registration

#### 4. Global State Management (`internal/app/state.go`)
- Thread-safe centralized state with `sync.RWMutex`
- Components: Config, S3 client, Dictionaries, Todo Manager, Registries
- File metadata with vision descriptions ("Working Memory")
- `BuildAgentContext()` - Assembles full context for LLM (system prompt + files + todos + history)

#### 5. Command Registry (`internal/app/commands.go`)
- Extensible TUI command system
- Built-in todo commands: `todo`, `t` (alias)
- Asynchronous execution via Bubble Tea commands

#### 6. Task Manager (`pkg/todo/`)
- Thread-safe task tracking for agent planning
- Task statuses: PENDING, DONE, FAILED
- Automatic context injection into prompts via `String()` method
- Integrated with agent tools for dynamic planning

#### 7. Configuration (`pkg/config/`)
- YAML-based configuration with ENV variable support (`${VAR}` syntax)
- Sections: models, tools, s3, image_processing, app, file_rules, wb
- Type-safe configuration structures with validation

#### 8. Prompt System (`pkg/prompt/`)
- YAML-based prompt templates with Go template syntax
- Support for config section (model params) and messages section
- Variables: `{{.Variable}}` for template rendering
- Multi-message prompts with system/user/assistant roles

## Key Architectural Rules (from dev_manifest.md)

### The 10 Immutable Rules

1. **Tool Interface Contract**: Never change the `Tool` interface:
   ```go
   type Tool interface {
       Definition() ToolDefinition
       Execute(ctx context.Context, argsJSON string) (string, error)
   }
   ```

2. **Configuration**: All settings in YAML with ENV variable support. No hardcoded values.

3. **Registry Usage**: All tools registered via `Registry.Register()`. No direct calls bypassing registry.

4. **LLM Abstraction**: Work with AI models only through `Provider` interface. No direct API calls.

5. **State Management**: Use `GlobalState` with thread-safe access. No global variables.

6. **Package Structure**:
   - `pkg/` - Library code ready for reuse
   - `internal/` - Application-specific logic
   - Entry points - Initialization and orchestration only

7. **Error Handling**: Return errors up the stack. No `panic()` in business logic. Framework must be resilient to LLM hallucinations.

8. **Extensibility**: Add features through:
   - New tools in `pkg/tools/std/` or custom packages
   - New LLM adapters in `pkg/llm/`
   - Configuration extensions (no breaking changes)

9. **Testing**: All tools must support mocking dependencies. No direct HTTP calls without abstraction.

10. **Documentation**: All public APIs must have godoc comments.

## Tool Development

### Creating a New Tool

1. Create a new file in `pkg/tools/std/` or a custom package
2. Implement the `Tool` interface
3. Register the tool via `registry.Register(tool)`

Example:
```go
type MyTool struct {
    // Dependencies injected via constructor
}

func (t *MyTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "my_tool",
        Description: "What this tool does",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "param1": map[string]interface{}{
                    "type": "string",
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

    // Business logic here - use ctx for cancellation

    result := map[string]interface{}{
        "result": "success",
        "data": args.Param1,
    }
    jsonResult, _ := json.Marshal(result)
    return string(jsonResult), nil
}
```

### Available Standard Tools

**Planner Tools** (`pkg/tools/std/planner.go`):
- `plan_add_task` - Add new task to plan
- `plan_mark_done` - Mark task as completed
- `plan_mark_failed` - Mark task as failed with reason
- `plan_clear` - Clear entire plan

**S3 Tools** (`pkg/tools/std/s3_tools.go`):
- File upload/download operations
- Image processing integration

**Wildberries Tools** (`pkg/tools/std/wb_catalog.go`):
- `get_wb_parent_categories` - Get list of parent categories (e.g., "Женщинам", "Электроника")
- `get_wb_subjects` - Get subjects (subcategories) for a given parent category
- `ping_wb_api` - Check Wildberries Content API availability and diagnose issues

## Configuration

### config.yaml Structure

```yaml
# Models Configuration
models:
  default_chat: "glm-4.6"
  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"
      max_tokens: 2000
      temperature: 0.5
      timeout: "120s"
      thinking: "enabled"  # Enables thinking mode

# S3 Storage
s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"
  use_ssl: true

# File Classification Rules
file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.jpeg", "*.png"]
    required: true
  - tag: "plm_data"
    patterns: ["*.json"]
    required: true

# Wildberries API
wb:
  api_key: "${WB_API_KEY}"
```

## Building and Running

```bash
# Main TUI application (primary interface)
go run cmd/poncho/main.go

# Build the main application
go build -o poncho cmd/poncho/main.go

# Orchestrator test CLI utility
go run cmd/orchestrator-test/main.go -query "show parent categories"
go run cmd/orchestrator-test/main.go -verbose -query "complex task"

# Build orchestrator-test
go build -o orchestrator-test cmd/orchestrator-test/main.go
```

## Testing

```bash
# Run all tests
go test ./... -v

# Run tests for specific package
go test ./pkg/utils -v

# Run a specific test
go test ./pkg/utils -v -run TestCleanJsonBlock

# Run tests with coverage
go test ./... -cover

# Generate coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Data Flow (End-to-End Request Processing)

Understanding how a user request flows through the system:

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
│  5. LLM GENERATE                                                             │
│     │ llmProvider.Generate(ctx, messages, toolDefs)                          │
│     │ - Returns response with ToolCalls[{Name, Args, ID}]                   │
│     ▼                                                                        │
│  6. SANITIZE RESPONSE                                                        │
│     │ utils.SanitizeLLMOutput(response.Content)                              │
│     │ - Removes markdown wrappers, trims whitespace                          │
│     ▼                                                                        │
│  7. EXECUTE TOOLS (if ToolCalls present)                                     │
│     │ For each ToolCall:                                                     │
│     │   a. registry.Get(tc.Name) → find tool                                │
│     │   b. utils.CleanJsonBlock(tc.Args) → sanitize JSON                    │
│     │   c. tool.Execute(ctx, cleanArgs) → "Raw In, String Out"              │
│     │   d. state.AppendMessage(result) → add to history                     │
│     │   e. Loop back to step 4                                               │
│     ▼                                                                        │
│  8. FINAL RESPONSE (no ToolCalls)                                            │
│     │ Return response.Content to user                                        │
│     ▼                                                                        │
│  9. TUI DISPLAY                                                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Key Points in the Flow:

- **Thread-Safety**: All state operations use `sync.RWMutex` in GlobalState
- **Resilience**: Tool errors are returned as strings, never panic (Rule 7)
- **JSON Sanitization**: LLM responses are cleaned of markdown wrappers before parsing
- **Context Injection**: Todos and Files are automatically included in prompts
- **Tool Discovery**: LLM sees all registered tools via `registry.GetDefinitions()`

## Working with Prompts

### Tool Post-Prompts

**Purpose**: Specialized system prompts that activate after tool execution to guide LLM response formatting.

**Concept**: After a tool completes, Orchestrator can load a custom post-prompt that replaces the default system prompt for the next LLM iteration. This allows tool-specific formatting and analysis instructions.

**Configuration** (`prompts/tool_postprompts.yaml`):
```yaml
tools:
  get_wb_parent_categories:
    post_prompt: "wb/parent_categories_analysis.yaml"
    enabled: true

  get_wb_subjects:
    post_prompt: "wb/subjects_analysis.yaml"
    enabled: true

  ping_wb_api:
    post_prompt: "wb/api_health_report.yaml"
    enabled: true
```

**Flow**:
```
Iteration N:
  Tool: get_wb_parent_categories → Result
  LoadToolPostPrompt("get_wb_parent_categories") → wb/parent_categories_analysis.yaml
  activePostPrompt = <post-prompt content>

Iteration N+1:
  BuildAgentContext(activePostPrompt) ← System prompt replaced!
  LLM gets: [SYSTEM] <post-prompt> + [HISTORY with tool result]
  LLM formats response according to post-prompt instructions
  activePostPrompt = "" ← Reset after response
```

**Scope**: Post-prompts are active for **one iteration** only (from tool execution to final LLM response).

**Benefits**:
- Tool-specific formatting without changing LLM calls
- Separation of concerns: tools return data, prompts define presentation
- Easy to customize per-tool behavior via YAML

**Testing Post-Prompts**:
A CLI utility exists for testing post-prompts: `cmd/prompt-test/main.go`

```bash
# Build the utility
go build -o prompt-test cmd/prompt-test/main.go

# Test a specific tool with post-prompt
./prompt-test ping_wb_api "проверь API"
./prompt-test get_wb_parent_categories "покажи категории"
./prompt-test get_wb_subjects "покажи подкатегории"
```

The utility:
1. Executes the specified tool
2. Loads the post-prompt for that tool
3. Calls LLM with: post-prompt + tool result + user query
4. Returns the formatted response

### Prompt YAML Format

```yaml
config:
  model: "zai-vision/glm-4.5v"
  temperature: 0.1
  max_tokens: 2000

messages:
  - role: "system"
    content: |
      You are a fashion sketch analyzer.

  - role: "user"
    content: |
      Analyze this sketch: {{.ArticleID}}

      IMAGE: {{.ImageURL}}
```

### Loading and Rendering

```go
// Load prompt file
pf, err := prompt.Load("prompts/sketch_description_prompt.yaml")
if err != nil {
    return err
}

// Prepare data for template
data := struct {
    ArticleID string
    ImageURL  string
}{
    ArticleID: "WB123456",
    ImageURL:  "s3://bucket/image.jpg",
}

// Render messages
messages, err := pf.RenderMessages(data)
if err != nil {
    return err
}
```

## Common Patterns

### Agent Loop (ReAct Pattern)

The full ReAct cycle is implemented in `internal/agent/orchestrator.go`:

```go
// 1. Build context (thread-safe)
messages := state.BuildAgentContext(systemPrompt)

// 2. Get tool definitions for function calling
toolDefs := registry.GetDefinitions()

// 3. Call LLM
response, err := llmProvider.Generate(ctx, messages, toolDefs)
if err != nil {
    return "", fmt.Errorf("llm generation failed: %w", err)
}

// 4. Sanitize LLM response (remove markdown wrappers)
response.Content = utils.SanitizeLLMOutput(response.Content)

// 5. Add assistant response to history
state.AppendMessage(response)

// 6. Execute tools if present
for _, tc := range response.ToolCalls {
    // 6a. Sanitize JSON arguments (LLM may return ```json {...}`)
    cleanArgs := utils.CleanJsonBlock(tc.Args)

    // 6b. Get tool from registry
    tool, err := registry.Get(tc.Name)
    if err != nil {
        result := fmt.Sprintf("Tool not found: %v", err)
        state.AppendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: result})
        continue
    }

    // 6c. Execute tool (Raw In, String Out)
    result, err := tool.Execute(ctx, cleanArgs)
    if err != nil {
        result = fmt.Sprintf("Tool execution error: %v", err)
    }

    // 6d. Add result to history
    state.AppendMessage(llm.Message{
        Role:       llm.RoleTool,
        ToolCallID: tc.ID,
        Content:    result,
    })
}

// 7. If no tool calls, return final answer
if len(response.ToolCalls) == 0 {
    return response.Content, nil
}
```

### JSON Sanitization

LLMs often wrap JSON in markdown code blocks. Use utilities from `pkg/utils`:

```go
import "github.com/ilkoid/go-workspace/src/poncho-ai/pkg/utils"

// Clean ```json {...}``` wrapper from tool arguments
cleanArgs := utils.CleanJsonBlock(tc.Args)

// Full sanitization for LLM responses (removes all code blocks, trims spaces)
cleanContent := utils.SanitizeLLMOutput(response.Content)

// Extract JSON object from mixed text
jsonStr := utils.ExtractJSON("Some text: {\"key\": \"value\"} and more")
// Returns: {"key": "value"}
```

### Creating a New Entry Point

Using `pkg/app` for creating new entry points (CLI, HTTP, etc.):

```go
package main

import (
    "fmt"
    "log"
    "time"

    appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
)

func main() {
    // 1. Initialize config (auto-discovers config.yaml)
    cfg, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Config loaded from %s", cfgPath)

    // 2. Initialize all components with selective tool registration
    // Use ToolWB | ToolPlanner for WB-specific utilities (saves ~320 tokens)
    components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolWB|appcomponents.ToolPlanner)
    if err != nil {
        log.Fatal(err)
    }

    // 3. Execute query
    result, err := appcomponents.Execute(components, "show categories", 2*time.Minute)
    if err != nil {
        log.Fatal(err)
    }

    // 4. Use results
    fmt.Println("Response:", result.Response)
    fmt.Println("Todo stats:", result.TodoStats.Total, "tasks")
    fmt.Println("Duration:", result.Duration)
}
```

**ToolSet Examples**:
```go
// All tools (TUI, general-purpose)
appcomponents.ToolsAll

// WB + Planner only (token-efficient)
appcomponents.ToolWB | appcomponents.ToolPlanner

// Planner only (minimal token usage)
appcomponents.ToolsMinimal
```

### Thread-Safe State Operations

All state operations in `internal/app/state.go` are protected by `sync.RWMutex`:

```go
// Add message to history
state.AppendMessage(llm.Message{
    Role:    llm.RoleUser,
    Content: userInput,
})

// Update file analysis result ("Working Memory")
state.UpdateFileAnalysis("sketch", "dress.jpg", "Red midi dress with V-neck")

// Manage todos (thread-safe)
state.AddTodoTask("Analyze fashion sketch")
state.CompleteTodoTask(1)
state.FailTodoTask(1, "Image not found")

// Get current article context
articleID, files := state.GetCurrentArticle()

// Get thread-safe history copy
history := state.GetHistory()
```

## Environment Variables

Required:
- `ZAI_API_KEY` - ZAI AI provider API key
- `S3_ACCESS_KEY` - S3 storage access key
- `S3_SECRET_KEY` - S3 storage secret key
- `WB_API_KEY` - Wildberries API key

## Dependencies

Key external dependencies:
- `github.com/charmbracelet/bubbletea` - TUI framework (Model-View-Update)
- `github.com/charmbracelet/lipgloss` - TUI styling
- `github.com/minio/minio-go/v7` - S3 compatible storage client
- `gopkg.in/yaml.v3` - YAML configuration parsing
- `golang.org/x/time/rate` - Rate limiting for API calls

## Design Patterns Used

- **Registry Pattern** - Tool and command management
- **Factory Pattern** - LLM provider creation
- **Component Initialization** - Reusable initialization across entry points via `pkg/app`
- **Strategy Pattern** - File classification rules
- **Command Pattern** - TUI command handling
- **Template Method** - Prompt rendering with Go templates
- **Context Injection** - Todo/Files automatically included in prompts
- **Adapter Pattern** - OpenAI-compatible API adapter
- **Model-View-Update** - Bubble Tea TUI architecture

## File Naming Convention

Files ending with `_short.go` (e.g., `state_short.go`, `planner_short.go`) contain alternative implementations or utility functions. The main implementation is in the base file.

## Important Implementation Notes

### Tool Registration

All standard tools are registered in `pkg/app/components.go` `SetupTools()` using **bit flags** for selective registration:

```go
func SetupTools(state *app.GlobalState, wbClient *wb.Client, toolSet ToolSet) {
    registry := state.GetToolsRegistry()

    // Wildberries инструменты
    if toolSet & ToolWB != 0 {
        registry.Register(std.NewWbParentCategoriesTool(wbClient))
        registry.Register(std.NewWbSubjectsTool(wbClient))
        registry.Register(std.NewWbPingTool(wbClient))
    }

    // Planner инструменты (обязательны для управления задачами)
    if toolSet & ToolPlanner != 0 {
        registry.Register(std.NewPlanAddTaskTool(state.Todo))
        registry.Register(std.NewPlanMarkDoneTool(state.Todo))
        registry.Register(std.NewPlanMarkFailedTool(state.Todo))
        registry.Register(std.NewPlanClearTool(state.Todo))
    }
}
```

**Important: JSON Schema for Tools Without Parameters**

Tools without parameters MUST include `"required": []string{}` for LLM API compatibility:

```go
Parameters: map[string]interface{}{
    "type":       "object",
    "properties": map[string]interface{}{},
    "required":   []string{}, // Required for LLM API compatibility
}
```

For custom tools, register them in `SetupTools()` under the appropriate category flag:

```go
if toolSet & ToolWB != 0 {
    registry.Register(&MyCustomWBTool{...})
}
```

### JSON Sanitization in Orchestrator

The orchestrator automatically sanitizes LLM responses (see `internal/agent/orchestrator.go`):

1. **Response sanitization** (line 162): `utils.SanitizeLLMOutput(response.Content)` - removes markdown code blocks from final responses
2. **Argument sanitization** (line 210): `utils.CleanJsonBlock(tc.Args)` - removes markdown wrappers from tool call arguments

This ensures resilience against LLM hallucinations that return ````json {...}``` instead of pure JSON.

### Thread-Safety Guarantees

The following components are thread-safe:
- `GlobalState` - all fields protected by `sync.RWMutex`
- `ToolsRegistry` - concurrent tool registration and lookup
- `TodoManager` - atomic task operations
- `CommandRegistry` - safe command registration

### Circular Import Resolution

The `pkg/agent` package defines the `Agent` interface to avoid circular imports between `internal/agent` (implementation) and packages that need to reference agents (like tools that need to trigger agent actions).
