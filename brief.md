# Poncho AI: Framework Architecture Documentation

## Executive Summary

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct (Reasoning + Acting) pattern. The framework provides structured approach to agent development, handling routine tasks (prompt engineering, JSON validation, conversation history, task planning) while allowing developers to focus on business logic through isolated tools.

**Primary Use Case**: E-commerce automation for Wildberries marketplace with multimodal AI capabilities for processing fashion sketches and PLM data.

**Key Philosophy**: "Raw In, String Out" - tools receive raw JSON from LLM and return strings, ensuring maximum flexibility and minimal dependencies.

---

## 1. Architecture Overview

### 1.1 High-Level Structure

```
poncho-ai/
├── cmd/                    # Application entry points
│   ├── poncho/            # Main TUI application (primary interface)
│   └── orchestrator-test/ # CLI utility for testing orchestrator
├── internal/              # Application-specific logic
│   ├── agent/            # Orchestrator implementation (ReAct loop)
│   ├── app/              # Global state, command registry, types
│   └── ui/               # Bubble Tea TUI (Model-View-Update)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent interface (avoids circular imports)
│   ├── app/              # Component initialization (shared across entry points)
│   ├── classifier/       # File classification engine
│   ├── config/           # YAML configuration with ENV support
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer
│   ├── prompt/           # Prompt loading and rendering
│   ├── s3storage/        # S3-compatible storage client
│   ├── todo/             # Thread-safe task manager
│   ├── tools/            # Tool system (registry + std tools)
│   ├── utils/            # JSON sanitization utilities
│   └── wb/               # Wildberries API client
├── prompts/              # YAML prompt templates
└── config.yaml           # Main configuration file
```

### 1.2 Component Dependency Graph

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
│  └─────────────────┘    └──────────────────┘                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Core Architectural Components

### 2.1 Tool System (`pkg/tools/`)

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
- **LLM-Agnostic**: Tools work with any LLM provider through the interface

**Standard Tools** (`pkg/tools/std/`):
- `planner.go` - Task management (plan_add_task, plan_mark_done, plan_mark_failed, plan_clear)
- `wb_catalog.go` - Wildberries catalog integration (get_wb_parent_categories, get_wb_subjects, ping_wb_api)
- `s3_tools.go` - S3 storage operations

**Selective Tool Registration**:
- Tools are organized into categories using **bit flags** for token efficiency
- Each utility can register only the tools it needs (e.g., WB + Planner, WB only, Planner only)
- See `pkg/app/components.go` for `ToolSet` definition and `SetupTools()` implementation

### 2.2 Orchestrator (`internal/agent/orchestrator.go`)

**Purpose**: Coordinates interaction between LLM and Tools using ReAct pattern.

**Key Features**:
1. **ReAct Loop** (max 10 iterations for safety)
2. **Context Building**: Combines system prompt + files + todos + history
3. **JSON Sanitization**: Removes markdown wrappers from LLM responses
4. **Thread-Safe**: Uses mutex for concurrent access protection
5. **Resilience**: All errors returned as strings, no panics (Rule 7)

**Execution Flow**:
```
1. BuildAgentContext() → Collect all context
2. llm.Generate() → Call LLM with tool definitions
3. SanitizeLLMOutput() → Clean markdown wrappers
4. Execute Tools → For each ToolCall:
   a. CleanJsonBlock(tc.Args) → Sanitize JSON
   b. registry.Get(tc.Name) → Find tool
   c. tool.Execute(ctx, cleanArgs) → Run tool
   d. AppendMessage(result) → Add to history
   e. LoadToolPostPrompt(tc.Name) → Load post-prompt for this tool
   f. Set activePostPrompt → Activate for next iteration
   g. Loop back to step 2
5. Return final response (when no tool calls)
```

### 2.3 Tool Post-Prompts

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

### 2.4 Global State Management (`internal/app/state.go`)

**Purpose**: Thread-safe centralized state for the entire application.

**Components**:
```go
type GlobalState struct {
    Config          *config.AppConfig
    S3              *s3storage.Client
    Dictionaries    *wb.Dictionaries
    Todo            *todo.Manager           // Task planner
    CommandRegistry *CommandRegistry         // TUI commands
    ToolsRegistry   *tools.Registry          // Agent tools
    Orchestrator    agent.Agent              // Agent interface

    mu              sync.RWMutex             // Protects fields below
    History         []llm.Message            // Dialog history
    Files           map[string][]*FileMeta   // "Working Memory"
    CurrentArticleID string
    CurrentModel    string
    IsProcessing    bool
}
```

**Thread-Safe Methods**:
- `AppendMessage()` - Add message to history
- `GetHistory()` - Return copy of history
- `UpdateFileAnalysis()` - Store vision analysis results
- `SetCurrentArticle()` - Update current article context
- `BuildAgentContext()` - Assemble full context for LLM

**"Working Memory" Pattern**: Vision model results are stored in `FileMeta.VisionDescription` and injected into prompts without re-sending images.

### 2.4 Command Registry (`internal/app/commands.go`)

**Purpose**: Extensible TUI command system for Bubble Tea.

**Pattern**: Command Pattern with asynchronous execution via `tea.Cmd`.

**Built-in Commands**:
- `todo` / `t` - Todo management (add, done, fail, clear, help)
- `load <id>` - Load article from S3
- `render <file>` - Render prompt template
- `ask <query>` - Query AI agent
- Unknown commands → delegate to agent (natural interface)

**Registration Example**:
```go
registry.Register("todo", handler)
registry.Register("t", aliasHandler)  // Alias
```

### 2.5 LLM Abstraction (`pkg/llm/`)

**Purpose**: Provider-agnostic interface for AI models.

**Interface**:
```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, tools ...any) (Message, error)
}
```

**Implementation**: OpenAI-compatible adapter covers 99% of modern APIs.

**Factory Pattern**: `pkg/factory/llm_factory.go` creates providers from config.

**Message Format**: Supports text, tool calls, and images (for vision queries).

### 2.6 Component Initialization Pattern (`pkg/app/`)

**Purpose**: Provides reusable initialization logic across all entry points (TUI, CLI, HTTP, etc.).

**Key Structures**:
```go
// Components holds all initialized application dependencies
type Components struct {
    Config       *config.AppConfig
    State        *app.GlobalState
    LLM          llm.Provider
    WBClient     *wb.Client
    Orchestrator *agent.Orchestrator
}

// ExecutionResult contains the result of an agent query
type ExecutionResult struct {
    Response   string        // Final agent response
    TodoString string        // Formatted todo list state
    TodoStats  TodoStats     // Task statistics
    History    []llm.Message // Message history
    Duration   time.Duration // Execution time
}
```

**Key Functions**:
- `InitializeConfig(finder ConfigPathFinder)` - Loads configuration with auto-discovery
- `Initialize(cfg, maxIters, systemPrompt, toolSet ToolSet)` - Creates all components with selective tool registration
- `Execute(components, query, timeout)` - Runs agent query and returns results
- `SetupTools(state, wbClient, toolSet ToolSet)` - Registers tools based on bit flags

**ToolSet Bit Flags**:
```go
type ToolSet int

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

**Usage Example**:
```go
// Any entry point can use the same initialization code
cfg, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})

// Register only WB + Planner tools (saves ~320 tokens vs ToolsAll)
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolWB|appcomponents.ToolPlanner)

result, err := appcomponents.Execute(components, userQuery, 2*time.Minute)
```

**Benefits**:
- **No duplication** - All entry points use same initialization logic
- **Testability** - `ConfigPathFinder` interface allows mocking
- **Consistency** - Same component setup across TUI, CLI, and future interfaces
- **Token efficiency** - Selective tool registration reduces LLM context size

---

## 3. Key Architectural Patterns

### 3.1 ReAct Pattern (Reasoning + Acting)

**Location**: `internal/agent/orchestrator.go`

The agent loops between:
1. **Reasoning**: LLM analyzes context and decides which tool to use
2. **Acting**: Tool is executed and result is fed back to LLM
3. **Repeat**: Until LLM provides final answer or max iterations reached

### 3.2 Dependency Inversion Principle

**Problem**: Circular import between `internal/agent` (implementation) and `internal/app` (usage).

**Solution**: `pkg/agent` package defines `Agent` interface:
- `internal/app` imports `pkg/agent` (interface only)
- `internal/agent` imports `internal/app` (concrete implementation)

### 3.3 Context Injection Pattern

**Purpose**: Automatically include Todo and File context in prompts.

**Implementation**: `BuildAgentContext()` method combines:
1. System prompt
2. "Working Memory" (vision descriptions from analyzed files)
3. Todo plan context
4. Dialog history

**Benefit**: AI always sees current state without tool calls, saving tokens.

### 3.4 Registry Pattern

**Used In**:
- `ToolsRegistry` - Tool registration and discovery
- `CommandRegistry` - TUI command management

**Benefits**:
- Dynamic feature registration
- Decoupling of registration and usage
- Thread-safe concurrent access

### 3.5 Model-View-Update (TEA)

**Location**: `internal/ui/` (Bubble Tea framework)

- **Model**: `MainModel` holds application state
- **View**: `view()` renders UI to terminal
- **Update**: `update()` handles events and returns commands

---

## 4. Critical Implementation Details

### 4.1 Tool Registration with ToolSet

All tools are registered in `pkg/app/components.go` `SetupTools()` using **bit flags** for selective registration:

```go
func SetupTools(state *app.GlobalState, wbClient *wb.Client, toolSet ToolSet) {
    registry := state.GetToolsRegistry()

    // Wildberries tools
    if toolSet & ToolWB != 0 {
        registry.Register(std.NewWbParentCategoriesTool(wbClient))
        registry.Register(std.NewWbSubjectsTool(wbClient))
        registry.Register(std.NewWbPingTool(wbClient))
    }

    // Ozon tools (future)
    if toolSet & ToolOzon != 0 {
        // TODO: Add Ozon tools
    }

    // S3 tools (future)
    if toolSet & ToolS3 != 0 {
        // TODO: Add S3 tools
    }

    // Planner tools
    if toolSet & ToolPlanner != 0 {
        registry.Register(std.NewPlanAddTaskTool(state.Todo))
        registry.Register(std.NewPlanMarkDoneTool(state.Todo))
        registry.Register(std.NewPlanMarkFailedTool(state.Todo))
        registry.Register(std.NewPlanClearTool(state.Todo))
    }
}
```

**Entry Point Usage**:
```go
// TUI - all tools
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolsAll)

// WB-specific utility - only WB + Planner
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolWB|appcomponents.ToolPlanner)

// Planner-only - minimal token usage
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolsMinimal)
```

### 4.2 JSON Sanitization

**Problem**: LLMs often return JSON wrapped in markdown code blocks: `````json {...}`````

**Solution**: Two-layer sanitization in Orchestrator:

1. **Response Sanitization** (`orchestrator.go:162`):
   ```go
   response.Content = utils.SanitizeLLMOutput(response.Content)
   ```

2. **Argument Sanitization** (`orchestrator.go:210`):
   ```go
   cleanArgs := utils.CleanJsonBlock(tc.Args)
   ```

**Utilities** (`pkg/utils/`):
- `CleanJsonBlock()` - Removes ````json` and ```` wrappers
- `SanitizeLLMOutput()` - Full sanitization (removes code blocks, trims spaces)
- `ExtractJSON()` - Extracts JSON object from mixed text

### 4.3 Thread-Safety Guarantees

Components protected by `sync.RWMutex`:
- `GlobalState` - All runtime fields
- `ToolsRegistry` - Tool registration and lookup
- `TodoManager` - Atomic task operations
- `CommandRegistry` - Command registration

### 4.4 Error Handling (Rule 7)

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

---

## 5. Configuration System

### 5.1 YAML + ENV Variables

**Format**: `config.yaml` with `${VAR}` syntax for ENV variables.

**Structure**:
```yaml
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
      thinking: "enabled"

s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"

file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.jpeg", "*.png"]
    required: true

wb:
  api_key: "${WB_API_KEY}"

app:
  prompts_dir: "./prompts"
```

### 5.2 Required Environment Variables

- `ZAI_API_KEY` - AI provider API key
- `S3_ACCESS_KEY`, `S3_SECRET_KEY` - S3 storage credentials
- `WB_API_KEY` - Wildberries API key

---

## 6. TUI Integration (Bubble Tea)

### 6.1 Natural Command Interface

**Innovation**: Unknown commands are automatically delegated to the agent.

```
User input: "покажи родительские категории"
↓
CommandRegistry: Not found
↓
Orchestrator.Run("покажи родительские категории")
↓
Agent processes request with tools
```

**Benefits**:
- No need to prefix every query with "ask"
- Natural conversational interface
- Seamless integration of commands and AI

### 6.2 Asynchronous Command Execution

**Pattern**: Commands return `tea.Cmd` for async execution.

```go
func performCommand(input string, state *app.GlobalState) tea.Cmd {
    return func() tea.Msg {
        // Execute with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()

        // Process command
        result, err := doWork(ctx, input)

        // Return result message
        return app.CommandResultMsg{Output: result, Err: err}
    }
}
```

---

## 7. The 10 Immutable Rules (from dev_manifest.md)

### Rule 1: Tool Interface Contract
**Never change** the `Tool` interface. "Raw In, String Out" is immutable.

### Rule 2: Configuration
All settings in YAML with ENV support. No hardcoded values.

### Rule 3: Registry Usage
All tools registered via `Registry.Register()`. No direct calls bypassing registry.

### Rule 4: LLM Abstraction
Work with AI models **only** through `Provider` interface. No direct API calls.

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

### Rule 10: Documentation
All public APIs must have godoc comments.

---

## 8. Architecture Auditing Guidelines

This section defines the methodology for auditing code against Poncho AI architectural principles.

### 8.1 Auditor Role and Objectives

The auditor is responsible for:
- Understanding **what** a piece of code or change is trying to do
- Evaluating **how** it does it against architectural principles
- Identifying **violations or risks** to long-term maintainability
- Proposing **concrete, minimal refactorings** that align with principles

**Think like a framework maintainer** who must protect architectural integrity while enabling new features.

### 8.2 Key Architectural Dimensions

When reviewing code, audit along these dimensions:

#### Dimension 1: Tooling Layer (Tools as First-Class Citizens)

**Compliance Criteria**:
- Business logic invoked by LLM is implemented as tools
- Tools implement stable `Tool` interface with:
  - **Definition**: Metadata + JSON schema for arguments
  - **Execute**: Takes `argsJSON string`, returns `string` result
- Tools **parse their own JSON** ("Raw In, String Out")
- Tools do NOT call LLM APIs directly; only domain logic and I/O

**Violations**:
- Direct LLM calls in tools
- Custom argument deserialization in framework
- Business logic outside of tool interface

#### Dimension 2: LLM Abstraction Layer

**Compliance Criteria**:
- All AI model interaction goes through `Provider` abstraction
- No direct HTTP calls to specific AI vendors in business code
- Vendor-specific details live only in adapter packages

**Violations**:
- Direct OpenAI/Anthropic/etc. API calls in tools or business logic
- Hardcoded API endpoints or credentials in code

#### Dimension 3: Dynamic Prompt Engineering & Tool Exposure

**Compliance Criteria**:
- System prompts and tool descriptions generated dynamically from tool definitions
- Individual tools do NOT manually edit system prompts
- Context constructed centrally (system prompt + history + working memory + tools)

**Violations**:
- Hardcoded tool descriptions in prompts
- Tools modifying their own system prompts
- Fragmented context building across multiple locations

#### Dimension 4: Execution Pipeline (ReAct Loop)

**Compliance Criteria**:
- Agent loop follows clear pipeline:
  1. Build context (system prompt, history, working memory, tool list)
  2. Call LLM once for next step
  3. Sanitize and validate response (especially JSON blocks)
  4. Route to tool via registry by name
  5. Execute tool and capture output
  6. Append result to conversation history

**Violations**:
- Bypassing tool registry for execution
- Skipping JSON sanitization
- Manual context building in individual tools

#### Dimension 5: State Management & GlobalState

**Compliance Criteria**:
- Session-level state lives in central `GlobalState` with thread-safe access
- **No ad-hoc global variables** for conversational/session state
- Shared mutable state protected by mutexes

**Violations**:
- Package-level global variables holding session data
- Unprotected concurrent access to state
- Duplicated state across multiple locations

#### Dimension 6: Config, Registry, and Extensibility

**Compliance Criteria**:
- Configuration externalized (YAML with ENV expansion), not hardcoded
- Tools registered in central registry, discovered by name
- No manual tool lookups bypassing registry
- New features added via:
  - New tools in appropriate packages
  - New LLM adapters
  - Backwards-compatible config extensions

**Violations**:
- Hardcoded configuration values
- Direct tool instantiation bypassing registry
- Breaking changes to core framework contracts

#### Dimension 7: Error Handling, Resilience, and Testing

**Compliance Criteria**:
- Errors propagated up call stack; no `panic` in business logic
- Explicit handling for LLM failure modes (invalid JSON, markdown wrappers)
- External dependencies abstracted for mocking

**Violations**:
- Using `panic` for normal error conditions
- No JSON sanitization before parsing
- Direct HTTP calls without interface abstraction

### 8.3 Planner/To-Do Feature Auditing

For planner/To-Do list features, verify:
- **Storage**: Integrated into shared `GlobalState` with safe concurrent access
- **LLM Manipulation**: Via tools (add_task, complete_task, list_tasks) respecting Tool interface
- **Context Injection**: Plan injected into LLM context via standard context-building logic
- **UI Separation**: UI layers only display plan, no embedded business/agent logic

### 8.4 Audit Output Format

When responding to audit requests, use this structure:

#### 1. High-Level Summary (3-6 sentences)
- What this code/change is doing
- Overall alignment (good / mixed / problematic)

#### 2. Findings by Dimension
For each relevant dimension:
- State: **Compliant / Partially compliant / Non-compliant / Unclear**
- Provide **specific evidence** (file names, function names, snippets)
- Explain why this is good/bad with respect to principles

#### 3. Explicit Rule Violations
If core rules are broken:
- "Violation: [short name of the rule]"
- Show relevant code location
- Propose how to fix while preserving behavior

#### 4. Refactoring Suggestions
Propose concrete, minimal changes to:
- Move logic into tools or adapters
- Restore usage of global state and registries
- Improve resilience and testability

Prefer **small, local refactorings** over large rewrites.

#### 5. Optional: Quick Scores
Provide coarse scores (0-100) for:
- Architecture compliance
- Extensibility
- Resilience / error handling

Include a sentence explaining the main factor affecting each score.

### 8.5 Auditor Style and Tone

- Be precise, technical, and concrete
- Always connect comments to specific architectural principles and rules
- Assume reader is a Go developer familiar with the project
- Goal: help evolve system **without eroding framework design**

---

## 9. Development Workflow

### 9.1 Building and Running

```bash
# Main TUI application (primary interface)
go run cmd/poncho/main.go

# Build binary
go build -o poncho cmd/poncho/main.go
./poncho

# Orchestrator test CLI utility
go run cmd/orchestrator-test/main.go -query "покажи родительские категории"

# Build orchestrator-test
go build -o orchestrator-test cmd/orchestrator-test/main.go
./orchestrator-test -help
```

### 9.2 Testing

```bash
# Run all tests
go test ./... -v

# Run specific package
go test ./pkg/utils -v

# Run specific test
go test ./pkg/utils -v -run TestCleanJsonBlock

# Coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### 9.3 Creating a New Tool

1. Create file in `pkg/tools/std/` (e.g., `my_tool.go`)
2. Implement `Tool` interface
3. Register in `pkg/app/components.go` `SetupTools()`

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

    // Business logic here

    result := map[string]interface{}{
        "result": "success",
        "data": args.Param1,
    }
    jsonResult, _ := json.Marshal(result)
    return string(jsonResult), nil
}
```

**Important: JSON Schema for Tools Without Parameters**
```go
// Tools without parameters MUST include "required": []string{}
Parameters: map[string]interface{}{
    "type":       "object",
    "properties": map[string]interface{}{},
    "required":   []string{}, // Required for LLM API compatibility
}
```

4. Register in `SetupTools()`:
```go
if toolSet & ToolWB != 0 {
    registry.Register(&MyTool{/* deps */})
}
```

---

## 10. Design Patterns Summary

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Registry** | `pkg/tools/registry.go` | Tool registration and discovery |
| **Factory** | `pkg/factory/llm_factory.go` | LLM provider creation |
| **Component Initialization** | `pkg/app/components.go` | Reusable initialization across entry points |
| **Strategy** | `pkg/classifier/` | File classification rules |
| **Command** | `internal/app/commands.go` | TUI command handling |
| **Template Method** | `pkg/prompt/` | Prompt rendering with Go templates |
| **Context Injection** | `internal/app/state.go` | Automatic context inclusion in prompts |
| **Adapter** | `pkg/llm/openai/` | OpenAI-compatible API adapter |
| **Model-View-Update** | `internal/ui/` | Bubble Tea TUI architecture |
| **ReAct** | `internal/agent/orchestrator.go` | Agent reasoning and acting loop |

---

## 11. Key Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework (Model-View-Update) |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling |
| `github.com/minio/minio-go/v7` | v7.0.97 | S3 compatible storage |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML configuration |
| `github.com/sashabaranov/go-openai` | v1.41.2 | OpenAI-compatible API |
| `golang.org/x/time/rate` | v0.14.0 | Rate limiting |

---

## 12. Conclusion

Poncho AI represents a **mature AI agent framework** with:

1. **Clear separation of concerns** - Tools, Orchestrator, State, UI
2. **Thread-safe operations** - All runtime state protected
3. **LLM-agnostic design** - Works with any OpenAI-compatible API
4. **Resilient error handling** - No panics, graceful degradation
5. **Extensible architecture** - Easy to add tools, commands, LLM providers
6. **Natural interface** - TUI with seamless AI integration

The framework follows the principle of **"Convention over Configuration"** - developers follow simple rules, and the framework handles the complexity of prompt engineering, validation, and orchestration.
