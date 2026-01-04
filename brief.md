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
4. [Architecture Patterns](#4-architecture-patterns)
5. [Configuration System](#5-configuration-system)
6. [The 11 Immutable Rules](#6-the-11-immutable-rules)
7. [Dependencies and Technologies](#7-dependencies-and-technologies)
8. [Development Workflow](#8-development-workflow)
9. [Key Insights and Design Philosophy](#9-key-insights-and-design-philosophy)
10. [Documentation Files](#10-documentation-files)

---

## 1. Architecture Overview

### 1.1 High-Level Structure

```
poncho-ai/
├── cmd/                    # Application entry points
│   ├── poncho/            # Main TUI application (primary interface)
│   ├── maxiponcho/        # Wildberries PLM analysis TUI
│   ├── chain-cli/         # Chain Pattern testing CLI
│   ├── vision-cli/        # Vision AI standalone utility
│   ├── debug-test/        # Debug logs testing utility
│   ├── wb-tools-test/     # Wildberries tools interactive tester
│   └── maxiponcho-test/  # Maxiponcho testing CLI
├── internal/              # Application-specific logic
│   ├── agent/            # Orchestrator implementation (ReAct loop)
│   ├── app/              # Application state (AppState with embedded CoreState)
│   └── ui/               # Bubble Tea TUI (Model-View-Update)
├── pkg/                   # Reusable library packages
│   ├── agent/            # Agent interface (avoids circular imports)
│   ├── app/              # Component initialization (shared across entry points)
│   ├── chain/            # Chain Pattern implementation
│   ├── classifier/       # File classification engine
│   ├── config/           # YAML configuration with ENV support
│   ├── debug/            # Debug logging system
│   ├── factory/          # LLM provider factory
│   ├── llm/              # LLM abstraction layer
│   ├── prompt/           # Prompt loading and rendering
│   ├── s3storage/        # S3-compatible storage client
│   ├── state/            # Framework core state (CoreState) - NEW
│   ├── todo/             # Thread-safe task manager
│   ├── tools/            # Tool system (registry + std tools)
│   ├── utils/            # JSON sanitization utilities
│   └── wb/               # Wildberries API client
├── prompts/              # YAML prompt templates
├── debug_logs/           # Debug log files
└── config.yaml           # Main configuration file
```

### 1.2 Component Dependency Graph

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    LAYER ARCHITECTURE (Rule 6 Compliant)                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐       │
│  │   cmd/poncho    │───▶│  AppState        │◀───│ CommandRegistry │       │
│  │  (Entry Point)  │    │ (internal/app)   │    │  (App-specific)  │       │
│  └─────────────────┘    └──────────────────┘    └─────────────────┘       │
│           │                       │▲                                        │
│           │                       ││ Embeds                                 │
│           ▼                       ││                                        │
│  ┌─────────────────┐    ┌─────────┴───────────────┐    ┌─────────────────┐   │
│  │ Orchestrator    │◀───│   CoreState          │    │   UI (Bubble)   │   │
│  │ (internal/agent)│    │   (pkg/state)        │    │                 │   │
│  └─────────────────┘    └──────────────────────┘    └─────────────────┘   │
│           │                       │                                        │
│           ▼                       ▼                                        │
│  ┌─────────────────┐    ┌──────────────────┐                              │
│  │ LLM Provider    │◀───│  Tool Interface  │                              │
│  │ (pkg/llm)       │    │  (pkg/tools)     │                              │
│  └─────────────────┘    └──────────────────┘                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘

Architecture:
  - pkg/state/CoreState: Framework core (no internal/ imports)
  - internal/app/AppState: Application-specific (embeds CoreState)
  - pkg/chain: Works with CoreState (Rule 6 compliant)
  - pkg/app/components: Component initialization (acceptable internal/ import)
```

### 1.3 Data Flow Diagram

```
User Input (TUI/CLI)
        │
        ▼
┌───────────────────┐
│ Command Registry  │───▶ Known Commands (todo, load, render)
└───────────────────┘
        │
        ▼ Unknown commands
┌───────────────────┐
│  Orchestrator    │
│   (ReAct Loop)   │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Build Context    │───▶ System Prompt + History + Files + Todos
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  LLM Provider    │───▶ Response with Tool Calls
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Tool Execution  │───▶ Tool Results
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Update History  │───▶ Loop back or return final answer
└───────────────────┘
```

---

## 2. CLI Tools

### 2.1 Overview Table

| Tool | Purpose | Interface | Config Location | Key Features |
|------|---------|-----------|-----------------|--------------|
| **poncho** | Main TUI application | Bubble Tea | [`config.yaml`](config.yaml) | Full agent interface, file management, vision analysis |
| **maxiponcho** | Wildberries PLM analysis TUI | Bubble Tea | [`cmd/maxiponcho/config.yaml`](cmd/maxiponcho/config.yaml) | S3 integration, WB API, product analysis |
| **chain-cli** | Chain Pattern testing | CLI | `./config.yaml` (next to binary) | Debug logging, JSON output, model override |
| **vision-cli** | Standalone vision analysis | CLI | `./config.yaml` (next to binary) | Vision AI, multimodal queries |
| **debug-test** | Debug logs testing | CLI | `./config.yaml` (next to binary) | Debug recorder simulation, log inspection |
| **wb-tools-test** | Wildberries tools interactive tester | Interactive CLI | `./config.yaml` (next to binary) | Tool menu, argument input, result formatting |
| **maxiponcho-test** | Maxiponcho testing CLI | CLI | `./config.yaml` (next to binary) | Query execution, verbose output |

### 2.2 Detailed Tool Descriptions

#### 2.2.1 poncho - Main TUI Application

**Location**: [`cmd/poncho/main.go`](cmd/poncho/main.go)

**Purpose**: Primary interface for interacting with the Poncho AI agent through a terminal UI.

**Key Features**:
- Natural command interface (unknown commands delegated to agent)
- File management and vision analysis
- Todo/task planning integration
- Conversation history
- Real-time status updates

**Usage**:
```bash
# Run main TUI
go run cmd/poncho/main.go

# Build and run
go build -o poncho cmd/poncho/main.go
./poncho
```

**Initialization Flow**:
1. Load configuration using [`appcomponents.InitializeConfig()`](pkg/app/components.go)
2. Initialize all components (LLM, S3, WB client, orchestrator)
3. Create Bubble Tea model
4. Start TUI without AltScreen (allows text selection)

**Commands Available**:
- `todo` / `t` - Todo management (add, done, fail, clear, help)
- `load <id>` - Load article from S3
- `render <file>` - Render prompt template
- `ask <query>` - Query AI agent
- Any other text - Delegated to agent (natural interface)

#### 2.2.2 maxiponcho - Wildberries PLM Analysis TUI

**Location**: [`cmd/maxiponcho/main.go`](cmd/maxiponcho/main.go)

**Purpose**: Specialized TUI for analyzing Wildberries products using PLM data from S3 storage.

**Key Features**:
- S3 integration for loading product data
- Wildberries API integration
- Product analysis and description generation
- Category and subject mapping
- Same TUI interface as poncho

**Usage**:
```bash
# Run maxiponcho TUI
go run cmd/maxiponcho/main.go

# Build and run
go build -o maxiponcho cmd/maxiponcho/main.go
./maxiponcho
```

**Configuration**: Uses custom [`MaxiponchoConfigPathFinder`](cmd/maxiponcho/main.go:91) to locate config in `cmd/maxiponcho/` directory first.

#### 2.2.3 chain-cli - Chain Pattern Testing CLI

**Location**: [`cmd/chain-cli/main.go`](cmd/chain-cli/main.go)

**Purpose**: CLI utility for testing the Chain Pattern implementation without TUI.

**Key Features**:
- Test Chain Pattern execution
- Debug logging integration
- JSON output format
- Model override capability
- Configurable timeout

**Usage**:
```bash
# Basic usage
./chain-cli "найди все товары в категории Верхняя одежда"

# With debug logging
./chain-cli -debug "show me trace"

# With specific model
./chain-cli -model glm-4.6 "расскажи про Go"

# JSON output
./chain-cli -json "query" | jq .

# Show version
./chain-cli -version

# Show help
./chain-cli -help
```

**Flags**:
| Flag | Description | Default |
|------|-------------|---------|
| `-config` | Path to config.yaml | `./config.yaml` |
| `-model` | Override model name | from config |
| `-debug` | Enable debug logging | from config |
| `-no-color` | Disable colors | false |
| `-json` | Output in JSON format | false |
| `-version` | Show version | - |
| `-help` | Show help | - |

**Rule 11 Compliance**: Config must be next to binary; utility fails if not found.

#### 2.2.4 vision-cli - Standalone Vision Analysis

**Location**: [`cmd/vision-cli/main.go`](cmd/vision-cli/main.go)

**Purpose**: Standalone utility for analyzing images through Vision AI.

**Key Features**:
- Multimodal AI queries
- Image analysis
- Interactive or query mode
- Standalone distribution with config and prompts

**Usage**:
```bash
# Interactive mode
./vision-cli

# Query mode with flag
./vision-cli -query "опиши это изображение"

# With timeout
./vision-cli -query "analyze" -timeout 10m
```

**Flags**:
| Flag | Description | Default |
|------|-------------|---------|
| `-config` | Path to config.yaml | next to binary |
| `-query` | Query to execute | interactive mode |
| `-timeout` | Timeout for execution | 5 minutes |

**Rule 11 Compliance**: Strict behavior - fails if config or prompts not found next to binary.

#### 2.2.5 debug-test - Debug Logs Testing Utility

**Location**: [`cmd/debug-test/main.go`](cmd/debug-test/main.go)

**Purpose**: Demonstrate [`pkg/debug`](pkg/debug/) package functionality without running full orchestrator.

**Key Features**:
- Debug recorder simulation
- Mock LLM and tool execution
- Log file inspection
- JSON formatting

**Usage**:
```bash
# Run debug test
go run cmd/debug-test/main.go

# Build and run
go build -o debug-test cmd/debug-test/main.go
./debug-test
```

**What It Does**:
1. Loads configuration
2. Creates debug recorder
3. Simulates 3 iterations of agent execution
4. Records LLM requests/responses
5. Records tool executions
6. Saves and displays debug log

**Rule 9 Compliance**: CLI utility used instead of unit tests for verification.

#### 2.2.6 wb-tools-test - Wildberries Tools Interactive Tester

**Location**: [`cmd/wb-tools-test/main.go`](cmd/wb-tools-test/main.go)

**Purpose**: Interactive menu for testing Wildberries API tools.

**Key Features**:
- Interactive tool selection menu
- Argument input with JSON validation
- Result formatting
- Execution timing
- Error handling

**Usage**:
```bash
# Run interactive tester
go run cmd/wb-tools-test/main.go

# Build and run
go build -o wb-tools-test cmd/wb-tools-test/main.go
./wb-tools-test
```

**Available Tools**:

| # | Tool Name | Description | Args Required |
|---|-----------|-------------|---------------|
| 1 | `search_wb_products` | Search by supplier articles | Yes |
| 2 | `get_wb_parent_categories` | Parent categories WB | No |
| 3 | `get_wb_subjects` | Subjects by parentID | Yes |
| 4 | `get_wb_subjects_by_name` | Search subject by name | Yes |
| 5 | `get_wb_characteristics` | Characteristics for subject | Yes |
| 6 | `get_wb_tnved` | TNVED codes for subject | Yes |
| 7 | `get_wb_brands` | Brands for subject | Yes |
| 8 | `get_wb_colors` | Color fuzzy search | Yes |
| 9 | `get_wb_countries` | Countries reference | No |
| 10 | `get_wb_genders` | Genders reference | No |
| 11 | `get_wb_seasons` | Seasons reference | No |
| 12 | `get_wb_vat_rates` | VAT rates reference | No |
| 13 | `ping_wb_api` | WB API health check | No |

**Example Session**:
```
==========================================
   WB Tools Test Utility
==========================================
 1. search_wb_products     Поиск по артикулам
 2. get_wb_parent_categories Родительские категории WB
 3. get_wb_subjects        Предметы по parentID
...
  q. Выход
==========================================
Выбери инструмент (число или 'q' для выхода): 2

--- Tool: get_wb_parent_categories ---
Описание: Родительские категории WB

Выполняю: get_wb_parent_categories({})

Результат (45ms, 2847 bytes):
{
  "categories": [
    {"id": 1541, "name": "Верхняя одежда"},
    ...
  ]
}
```

#### 2.2.7 maxiponcho-test - Maxiponcho Testing CLI

**Location**: [`cmd/maxiponcho-test/main.go`](cmd/maxiponcho-test/main.go)

**Purpose**: Test maxiponcho functionality without TUI.

**Key Features**:
- Query execution
- Verbose mode
- Todo stats display
- Duration tracking

**Usage**:
```bash
# Default query
go run cmd/maxiponcho-test/main.go

# Custom query
go run cmd/maxiponcho-test/main.go -query "анализируй товар"

# Verbose mode
go run cmd/maxiponcho-test/main.go -query "test" -verbose
```

**Flags**:
| Flag | Description |
|------|-------------|
| `-query`, `-q` | Query to execute |
| `-verbose`, `-v` | Enable verbose output |

**Default Query**: "загрузи из S3 артикул 12612012, определи тип товара на WB и составь продающее описание"

---

## 3. Core Components

### 3.1 Tool System (`pkg/tools/`)

**Design Principle**: "Raw In, String Out"

All functionality is implemented as tools conforming to the [`Tool`](pkg/tools/registry.go) interface:

```go
type Tool interface {
    Definition() ToolDefinition              // Metadata for LLM
    Execute(ctx context.Context, argsJSON string) (string, error)  // Business logic
}
```

**Key Features**:
- **Registry Pattern**: [`Registry.Register()`](pkg/tools/registry.go) for dynamic tool registration
- **Thread-Safe**: All operations protected by `sync.RWMutex`
- **LLM-Agnostic**: Tools work with any LLM provider through the interface

**Standard Tools** ([`pkg/tools/std/`](pkg/tools/std/)):

| Tool | File | Purpose | Parameters |
|------|------|---------|------------|
| `plan_add_task` | [`planner.go`](pkg/tools/std/planner.go) | Add task to plan | `{"task": "description"}` |
| `plan_mark_done` | [`planner.go`](pkg/tools/std/planner.go) | Mark task as done | `{"id": 1}` |
| `plan_mark_failed` | [`planner.go`](pkg/tools/std/planner.go) | Mark task as failed | `{"id": 1}` |
| `plan_clear` | [`planner.go`](pkg/tools/std/planner.go) | Clear all tasks | `{}` |
| `get_wb_parent_categories` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get WB parent categories | `{}` |
| `get_wb_subjects` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get subjects by parent | `{"parent": 1541}` |
| `ping_wb_api` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Check WB API health | `{}` |
| `search_wb_products` | [`wb_products.go`](pkg/tools/std/wb_products.go) | Search by supplier articles | `{"supplierArticles": ["ABC-123"]}` |
| `get_wb_subjects_by_name` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Search subject by name | `{"name": "платье", "limit": 10}` |
| `get_wb_characteristics` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get characteristics | `{"subjectID": 105}` |
| `get_wb_tnved` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get TNVED codes | `{"subjectID": 105}` |
| `get_wb_brands` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get brands | `{"subjectID": 105}` |
| `get_wb_colors` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Search colors | `{"search": "красный", "top": 10}` |
| `get_wb_countries` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get countries | `{}` |
| `get_wb_genders` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get genders | `{}` |
| `get_wb_seasons` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get seasons | `{}` |
| `get_wb_vat_rates` | [`wb_catalog.go`](pkg/tools/std/wb_catalog.go) | Get VAT rates | `{}` |

**Selective Tool Registration**:

Tools are organized into categories using **bit flags** for token efficiency:

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

Each utility can register only the tools it needs, saving tokens:

```go
// TUI - all tools
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolsAll)

// WB-specific utility - only WB + Planner (~320 tokens saved)
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolWB|appcomponents.ToolPlanner)

// Planner-only - minimal token usage
components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolsMinimal)
```

### 3.2 Orchestrator (`internal/agent/orchestrator.go`)

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

### 3.3 Tool Post-Prompts

**Purpose**: Specialized system prompts that activate after tool execution to guide LLM response formatting.

**Concept**: After a tool completes, Orchestrator can load a custom post-prompt that replaces the default system prompt for the next LLM iteration. This allows tool-specific formatting and analysis instructions.

**Configuration** ([`prompts/tool_postprompts.yaml`](prompts/tool_postprompts.yaml)):

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

### 3.4 State Management (Repository Pattern + Optimization, 2026-01-04)

**Purpose**: Thread-safe centralized state with clear separation between framework core and application-specific logic using Repository Pattern.

#### Architecture: Repository Pattern

The state management system uses a layered repository architecture for clean separation of concerns:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Repository Architecture                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────┐ │
│  │ UnifiedStore    │◀───│ MessageRepo     │    │ AppState    │ │
│  │ (Base Interface)│    │ (History API)   │    │             │ │
│  └─────────────────┘    └─────────────────┘    └─────────────┘ │
│         │                        ▲                        ▲      │
│         │                        │                        │      │
│         ▼                        │                        │      │
│  ┌─────────────────┐             │                   ┌────┴──────┐│
│  │ FileRepository  │─────────────┘                   │CoreState ││
│  │ (Files API)     │             │                   │          ││
│  └─────────────────┘             │                   └─────────┘│
│                                   │                              │
│  ┌─────────────────┐             │                              │
│  │ TodoRepository  │─────────────┘                              │
│  │ (Plan API)      │                                           │
│  └─────────────────┘                                           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Repository Interfaces** ([`pkg/state/repository.go`](pkg/state/repository.go)):

```go
// UnifiedStore - базовый интерфейс для всех репозиториев
type UnifiedStore interface {
    Get(key string) (any, bool)
    Set(key string, value any) error
    Update(key string, fn func(any) any) error
    Delete(key string) error
    Exists(key string) bool
    List() []string
}

// MessageRepository - управление историей диалога
type MessageRepository interface {
    UnifiedStore
    Append(msg llm.Message) error
    GetHistory() []llm.Message
}

// FileRepository - управление файлами (Working Memory)
type FileRepository interface {
    UnifiedStore
    UpdateFileAnalysis(tag string, file *s3storage.FileMeta) error
    GetFiles() map[string][]*s3storage.FileMeta
}

// TodoRepository - управление планом действий
type TodoRepository interface {
    UnifiedStore
    AddTask(description string) (int, error)
    CompleteTask(id int) error
    FailTask(id int, reason string) error
    GetTodoString() string
}

// DictionaryRepository - доступ к справочникам
type DictionaryRepository interface {
    UnifiedStore
    GetDictionaries() *wb.Dictionaries
}

// StorageRepository - доступ к S3
type StorageRepository interface {
    UnifiedStore
    GetS3() *s3storage.Client
}
```

#### Framework Core: `pkg/state/CoreState`

**Location**: [`pkg/state/core.go`](pkg/state/core.go)

**Purpose**: Reusable framework core implementing all repository interfaces.

**Architecture**:

```go
type CoreState struct {
    // Dependencies (read-only after creation)
    Config          *config.AppConfig
    S3              *s3storage.Client
    Dictionaries    *wb.Dictionaries
    Todo            *todo.Manager
    ToolsRegistry   *tools.Registry

    // Thread-safe storage
    mu    sync.RWMutex
    store map[string]any  // Unified storage for all data
}
```

**Thread-Safe Methods** (post-optimization, 2026-01-04):

**UnifiedStore operations**:
- `Get(key) (any, bool)` - Retrieve value by key
- `Set(key, value) error` - Store value
- `Update(key, fn) error` - Atomic update with function
- `Delete(key) error` - Remove key
- `Exists(key) bool` - Check existence
- `List() []string` - List all keys

**MessageRepository operations**:
- `Append(msg llm.Message) error` - Add message to history
- `GetHistory() []llm.Message` - Get message history

**FileRepository operations**:
- `UpdateFileAnalysis(tag, file) error` - Add/update file analysis
- `GetFiles() map[string][]*FileMeta` - Get all files

**TodoRepository operations**:
- `AddTask(desc) (int, error)` - Add new task
- `CompleteTask(id) error` - Mark task done
- `FailTask(id, reason) error` - Mark task failed
- `GetTodoString() string` - Format plan as string

**Helper operations**:
- `BuildAgentContext(systemPrompt) []Message` - Assemble full LLM context
- `Get/SetDictionaries()`, `Get/SetS3()`, `Get/SetTodo()`, `Get/SetToolsRegistry()`

**"Working Memory" Pattern**: Vision model results are stored in `FileMeta.VisionDescription` and injected into prompts without re-sending images.

**Rule 6 Compliance**: `pkg/state` has NO imports from `internal/` - fully reusable framework logic.

#### Application State: `internal/app/AppState`

**Location**: [`internal/app/state.go`](internal/app/state.go)

**Purpose**: Application-specific logic (TUI commands, orchestrator, UI state).

**Components**:

```go
type AppState struct {
    *state.CoreState           // Embedded framework core

    CommandRegistry *CommandRegistry  // TUI commands
    Orchestrator     agent.Agent       // Agent interface (simplified)
    UserChoice       *userChoiceData   // Interactive UI

    mu               sync.RWMutex      // Protects fields below
    CurrentArticleID string            // WB workflow state
    CurrentModel     string            // UI state
    IsProcessing     bool              // UI spinner
}
```

**Application-Specific Methods**:
- `SetOrchestrator()`, `GetOrchestrator()` - Agent management
- `SetProcessing()`, `GetProcessing()` - UI processing state
- `SetCurrentArticle()`, `GetCurrentArticle()` - WB workflow
- `SetUserChoice()`, `GetUserChoice()`, `ClearUserChoice()` - Interactive UI

**Framework Methods Available via Composition**:
- All `CoreState` methods accessible directly (e.g., `state.Append()`, `state.AddTask()`)

#### Code Optimization (2026-01-04)

**Removed Unused Methods** (~300 lines eliminated):

| Removed Method | Location | Reason |
|----------------|----------|--------|
| `Clear()` | UnifiedStore | 0 usages - use `Set(key, nil)` |
| `GetRange(from, to)` | MessageRepository | 0 usages - unnecessary complexity |
| `GetLast(n)` | MessageRepository | 0 usages - use `GetHistory()` |
| `Trim(n)` | MessageRepository | 0 usages - unnecessary complexity |
| `ClearHistory()` | MessageRepository | 0 usages - use `Set(KeyHistory, [])` |
| `GetFilesByTag(tag)` | FileRepository | 0 usages - use `GetFiles()` |
| `ClearFiles()` | FileRepository | 0 usages - use `Set(KeyFiles, {})` |
| `ClearTodo()` | TodoRepository | 0 usages - use `Set(KeyTodo, NewManager())` |
| `ClearDictionaries()` | DictionaryRepository | 0 usages - dictionaries never cleared |
| `String()` | ChainContext | 0 usages - debugging only |
| `ClearHistory()` | Agent interface | 0 usages - unnecessary requirement |
| `ClearTodos()` | Components | 0 usages - replaced with direct Set() |

**Optimization Results**:

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| Methods in CoreState | ~40 | ~31 | **-9 methods** |
| Methods in ChainContext | 18 | 17 | **-1 method** |
| Agent interface methods | 3 | 2 | **-1 method** |
| Approx. lines of code | ~2800 | ~2500 | **-300 lines** |
| Unused methods | 13 | 0 | **-100%** |
| Compilation status | ✅ | ✅ | **no errors** |

**Updated Agent Interface** ([`pkg/agent/types.go`](pkg/agent/types.go)):

```go
// REFACTORED 2026-01-04: Removed ClearHistory() - not used
type Agent interface {
    Run(ctx context.Context, query string) (string, error)
    GetHistory() []llm.Message
}
```

#### Architecture Benefits

1. **Rule 6 Compliance**: `pkg/` no longer imports `internal/`
2. **Modularity**: Framework core can be used independently (HTTP API, gRPC, etc.)
3. **Reusability**: CoreState works across CLI, TUI, and future interfaces
4. **Testability**: Framework logic testable without application dependencies
5. **Interface Segregation**: Each repository provides only relevant methods
6. **Single Source of Truth**: Unified storage prevents data duplication

### 3.5 Command Registry (`internal/app/commands.go`)

**Purpose**: Extensible TUI command system for Bubble Tea.

**Pattern**: Command Pattern with asynchronous execution via `tea.Cmd`.

**Built-in Commands**:

| Command | Aliases | Purpose |
|---------|----------|---------|
| `todo` | `t` | Todo management (add, done, fail, clear, help) |
| `load` | - | Load article from S3 |
| `render` | - | Render prompt template |
| `ask` | - | Query AI agent |
| (unknown) | - | Delegate to agent (natural interface) |

**Registration Example**:

```go
registry.Register("todo", handler)
registry.Register("t", aliasHandler)  // Alias
```

### 3.6 LLM Abstraction (`pkg/llm/`)

**Purpose**: Provider-agnostic interface for AI models.

**Interface**:

```go
type Provider interface {
    Generate(ctx context.Context, messages []Message, tools ...any) (Message, error)
}
```

**Implementation**: OpenAI-compatible adapter covers 99% of modern APIs.

**Factory Pattern**: [`pkg/factory/llm_factory.go`](pkg/factory/llm_factory.go) creates providers from config.

**Message Format**: Supports text, tool calls, and images (for vision queries).

### 3.7 Component Initialization Pattern (`pkg/app/`)

**Purpose**: Provides reusable initialization logic across all entry points (TUI, CLI, HTTP, etc.).

**Key Structures**:

```go
// Components holds all initialized application dependencies
type Components struct {
    Config       *config.AppConfig
    State        *app.AppState        // Changed: GlobalState → AppState
    LLM          llm.Provider
    VisionLLM    llm.Provider
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

| Function | Purpose |
|----------|---------|
| `InitializeConfig(finder ConfigPathFinder)` | Loads configuration with auto-discovery |
| `Initialize(cfg, maxIters, systemPrompt, toolSet ToolSet)` | Creates all components with selective tool registration |
| `Execute(components, query, timeout)` | Runs agent query and returns results |
| `SetupTools(state, wbClient, toolSet ToolSet)` | Registers tools based on bit flags |

**Benefits**:
- **No duplication** - All entry points use same initialization logic
- **Testability** - `ConfigPathFinder` interface allows mocking
- **Consistency** - Same component setup across TUI, CLI, and future interfaces
- **Token efficiency** - Selective tool registration reduces LLM context size

### 3.8 Chain Pattern (`pkg/chain/`)

**Purpose**: Implements Chain of Responsibility pattern for flexible agent execution pipelines.

**Key Components**:

```go
// Chain represents a sequence of steps
type Chain interface {
    Execute(ctx context.Context, input ChainInput) (ChainOutput, error)
}

// Step represents an atomic operation
type Step interface {
    Name() string
    Execute(ctx context.Context, state *ChainContext) (NextAction, error)
}

// ChainContext holds execution state (thread-safe)
type ChainContext struct {
    mu sync.RWMutex
    Input *ChainInput
    CurrentIteration int
    Messages []llm.Message
    ActivePostPrompt string
    // ...
}
```

**Implemented Chains**:

| Chain | Type | Purpose |
|-------|------|---------|
| `ReActChain` | Loop | ReAct (Reasoning + Acting) pattern for agents |

**Implemented Steps**:

| Step | Purpose |
|------|---------|
| `LLMInvocationStep` | Call LLM with current context |
| `ToolExecutionStep` | Execute all tool calls from LLM response |

**YAML Configuration**:

```yaml
chains:
  react_agent:
    type: "react"
    max_iterations: 10
    timeout: "120s"
    steps:
      - name: "llm_invocation"
        type: "llm"
        config:
          system_prompt: "default"
          model: "${default_reasoning}"
          temperature: 0.5
          max_tokens: 2000
      - name: "tool_execution"
        type: "tools"
        config:
          continue_on_error: false
    debug:
      enabled: true
      save_logs: true
      logs_dir: "./debug_logs"
      include_tool_args: true
      include_tool_results: true
      max_result_size: 5000
```

**Benefits**:
- Modular execution pipeline
- Easy to add new step types
- YAML-configurable chains
- Debug integration built-in

### 3.9 Debug System (`pkg/debug/`)

**Purpose**: Comprehensive debug logging for agent execution.

**Key Components**:

```go
// Recorder captures execution details
type Recorder struct {
    config RecorderConfig
    runID string
    startTime time.Time
    iterations []Iteration
}

// Iteration represents one ReAct loop
type Iteration struct {
    Number int
    LLMRequest LLMRequest
    LLMResponse LLMResponse
    Tools []ToolExecution
}

// ToolExecution records tool call details
type ToolExecution struct {
    Name     string
    Args     string
    Result   string
    Duration int64
    Success  bool
    Error    string
}
```

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

**Output Format**:

```json
{
  "run_id": "20260101_224928",
  "timestamp": "2026-01-01T22:49:28Z",
  "query": "найди все товары в категории Верхняя одежда",
  "duration_ms": 205,
  "iterations": [
    {
      "number": 1,
      "llm_request": {
        "model": "glm-4.6",
        "temperature": 0.5,
        "max_tokens": 2000,
        "system_prompt_used": "default",
        "messages_count": 3
      },
      "llm_response": {
        "content": "Нужно найти категории на WB",
        "tool_calls": [
          {
            "id": "call_abc123",
            "name": "get_wb_parent_categories",
            "args": "{}"
          }
        ],
        "duration_ms": 50
      },
      "tools": [
        {
          "name": "get_wb_parent_categories",
          "args": "{}",
          "result": "{...}",
          "duration_ms": 45,
          "success": true
        }
      ]
    }
  ],
  "final_response": "В категории Верхняя одежда найдено 1240 товаров...",
  "success": true
}
```

**Benefits**:
- Full execution trace
- JSON format for analysis
- Configurable detail level
- Automatic file naming with timestamps

### 3.10 UI Components (`internal/ui/`)

**Purpose**: Bubble Tea-based terminal user interface.

**Architecture**: Model-View-Update (TEA) pattern

**Components**:

```go
// MainModel holds TUI state
type MainModel struct {
    state *app.GlobalState
    viewport viewport.Model
    input  textinput.Model
    status string
    // ...
}

// View renders UI to terminal
func (m MainModel) View() string { ... }

// Update handles events and returns commands
func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { ... }
```

**Key Features**:
- No AltScreen (allows text selection and copying)
- Scrollable viewport
- Real-time status updates
- Command input with history
- File management interface

---

## 4. Architecture Patterns

### 4.1 Repository Pattern

**Location**: [`pkg/state/repository.go`](pkg/state/repository.go)

**Purpose**: Unified storage with domain-specific interfaces for clean separation of concerns.

**Architecture**:

```go
// UnifiedStore - Base interface for CRUD operations
type UnifiedStore interface {
    Get(key string) (any, bool)
    Set(key string, value any) error
    Update(key string, fn func(any) any) error
    Delete(key string) error
    Exists(key string) bool
    List() []string
}

// Domain-specific repositories extend UnifiedStore
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

**Benefits**:
- Clean separation of concerns
- Easy to mock for testing
- No duplicate data storage
- Thread-safe by design (sync.RWMutex)
- Interface segregation - each interface exposes only relevant methods
- Single implementation ([`CoreState`](pkg/state/core.go)) implements all repositories

**Single Implementation**:

```go
type CoreState struct {
    mu    sync.RWMutex
    store map[string]any  // Unified storage for all data
}

// CoreState implements: UnifiedStore, MessageRepository,
// FileRepository, TodoRepository, DictionaryRepository, StorageRepository
```

### 4.2 ReAct Pattern (Reasoning + Acting)

**Location**: [`internal/agent/orchestrator.go`](internal/agent/orchestrator.go)

The agent loops between:
1. **Reasoning**: LLM analyzes context and decides which tool to use
2. **Acting**: Tool is executed and result is fed back to LLM
3. **Repeat**: Until LLM provides final answer or max iterations reached

**Implementation**:

```go
for i := 0; i < maxIters; i++ {
    // 1. Build context
    messages := state.BuildAgentContext(systemPrompt)

    // 2. Call LLM (Reasoning)
    response, err := llm.Generate(ctx, messages, toolDefs)

    // 3. Execute tools (Acting)
    for _, tc := range response.ToolCalls {
        result := tool.Execute(ctx, tc.Args)
        messages = append(messages, result)
    }

    // 4. Check for final answer
    if len(response.ToolCalls) == 0 {
        return response.Content
    }
}
```

### 4.3 Chain of Responsibility Pattern

**Location**: [`pkg/chain/`](pkg/chain/)

**Purpose**: Flexible execution pipeline where steps are chained together.

**Benefits**:
- Easy to add new step types
- Reusable step implementations
- Configurable via YAML
- Debug integration built-in

**Example**:

```go
chain := chain.NewReActChain(chain.ReActChainConfig{
    SystemPrompt:    defaultSystemPrompt(),
    ReasoningConfig: reasoningConfig,
    ToolPostPrompts: toolPostPrompts,
    MaxIterations:   10,
})

output, err := chain.Execute(ctx, input)
```

### 4.4 Dependency Inversion Principle

**Problem**: Circular import between [`internal/agent`](internal/agent/) (implementation) and [`internal/app`](internal/app/) (usage).

**Solution**: [`pkg/agent`](pkg/agent/) package defines `Agent` interface:
- [`internal/app`](internal/app/) imports [`pkg/agent`](pkg/agent/) (interface only)
- [`internal/agent`](internal/agent/) imports [`internal/app`](internal/app/) (concrete implementation)

### 4.5 Context Injection Pattern

**Purpose**: Automatically include Todo and File context in prompts.

**Implementation**: [`BuildAgentContext()`](internal/app/state.go) method combines:
1. System prompt
2. "Working Memory" (vision descriptions from analyzed files)
3. Todo plan context
4. Dialog history

**Benefit**: AI always sees current state without tool calls, saving tokens.

### 4.6 Registry Pattern

**Used In**:
- [`ToolsRegistry`](pkg/tools/registry.go) - Tool registration and discovery
- [`CommandRegistry`](internal/app/commands.go) - TUI command management

**Benefits**:
- Dynamic feature registration
- Decoupling of registration and usage
- Thread-safe concurrent access

**Example**:

```go
// Register tool
registry.Register(std.NewWbParentCategoriesTool(wbClient))

// Get tool
tool, err := registry.Get("get_wb_parent_categories")

// Execute tool
result, err := tool.Execute(ctx, argsJSON)
```

### 4.7 Factory Pattern

**Location**: [`pkg/factory/llm_factory.go`](pkg/factory/llm_factory.go)

**Purpose**: Create LLM providers from configuration.

**Benefits**:
- Centralized provider creation
- Easy to add new providers
- Configuration-driven

**Example**:

```go
provider, err := factory.CreateProvider(cfg.Models.Definitions["glm-4.6"])
```

### 4.8 Model-View-Update (TEA)

**Location**: [`internal/ui/`](internal/ui/) (Bubble Tea framework)

**Components**:
- **Model**: [`MainModel`](internal/ui/model.go) holds application state
- **View**: [`view()`](internal/ui/view.go) renders UI to terminal
- **Update**: [`update()`](internal/ui/update.go) handles events and returns commands

**Benefits**:
- Clear separation of concerns
- Predictable state management
- Easy to test

### 4.9 Strategy Pattern

**Location**: [`pkg/classifier/`](pkg/classifier/)

**Purpose**: File classification rules.

**Benefits**:
- Pluggable classification strategies
- Easy to add new rules
- Configuration-driven

### 4.10 Adapter Pattern

**Location**: [`pkg/llm/openai/`](pkg/llm/openai/)

**Purpose**: OpenAI-compatible API adapter.

**Benefits**:
- Works with any OpenAI-compatible API
- Single adapter covers 99% of providers
- Easy to add new adapters if needed

### 4.11 Template Method Pattern

**Location**: [`pkg/prompt/`](pkg/prompt/)

**Purpose**: Prompt rendering with Go templates.

**Benefits**:
- Reusable prompt templates
- Dynamic content injection
- YAML-based configuration

### 4.12 Command Pattern

**Location**: [`internal/app/commands.go`](internal/app/commands.go)

**Purpose**: TUI command handling.

**Benefits**:
- Extensible command system
- Asynchronous execution via `tea.Cmd`
- Natural command interface (unknown commands → agent)

---

## 5. Configuration System

### 5.1 YAML + ENV Variables

**Format**: [`config.yaml`](config.yaml) with `${VAR}` syntax for ENV variables.

**Structure**:

```yaml
# Models configuration
models:
  default_chat: "glm-4.6"
  default_reasoning: "glm-4.6"
  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5
      timeout: "120s"
      thinking: "enabled"

# S3 storage configuration
s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"

# File classification rules
file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.jpeg", "*.png"]
    required: true

# Wildberries API configuration
wb:
  api_key: "${WB_API_KEY}"

# Application settings
app:
  prompts_dir: "./prompts"
  debug_logs:
    enabled: true
    save_logs: true
    logs_dir: "./debug_logs"
    include_tool_args: true
    include_tool_results: true
    max_result_size: 5000

# Chain configuration
chains:
  react_agent:
    type: "react"
    max_iterations: 10
    timeout: "120s"
    steps:
      - name: "llm_invocation"
        type: "llm"
      - name: "tool_execution"
        type: "tools"
```

### 5.2 Required Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `ZAI_API_KEY` | AI provider API key | `sk-...` |
| `S3_ACCESS_KEY` | S3 storage access key | `YCAJE...` |
| `S3_SECRET_KEY` | S3 storage secret key | `YCN...` |
| `WB_API_KEY` | Wildberries API key | `...` |

### 5.3 Configuration Loading

**Auto-Discovery**: Configuration is loaded using [`ConfigPathFinder`](pkg/app/components.go) interface:

```go
type ConfigPathFinder interface {
    FindConfigPath() string
}
```

**Search Order** (for [`DefaultConfigPathFinder`](pkg/app/components.go)):
1. Current directory: `./config.yaml`
2. Project root: `./config.yaml`
3. Fallback: error

**Rule 11 Compliance**: For standalone utilities, config must be next to binary:

```go
type StandaloneConfigPathFinder struct {
    ConfigFlag string
}

func (f *StandaloneConfigPathFinder) FindConfigPath() string {
    // 1. Check next to binary
    if exePath, err := os.Executable(); err == nil {
        exeDir := filepath.Dir(exePath)
        cfgPath := filepath.Join(exeDir, "config.yaml")
        if _, err := os.Stat(cfgPath); err == nil {
            return cfgPath
        }
    }
    // 2. Fail if not found
    return "" // Will cause error
}
```

### 5.4 Prompt Configuration

**Location**: [`prompts/`](prompts/) directory

**Structure**:

```
prompts/
├── system.yaml                    # Default system prompt
├── tool_postprompts.yaml         # Tool-specific post-prompts
├── wb/
│   ├── parent_categories_analysis.yaml
│   ├── subjects_analysis.yaml
│   └── api_health_report.yaml
└── vision/
    └── image_analysis.yaml
```

**Example Prompt**:

```yaml
# prompts/system.yaml
name: "default"
content: |
  You are a helpful AI assistant with access to various tools.
  Use the tools to gather information and provide accurate answers.
  
  Available tools:
  {{range .Tools}}
  - {{.Name}}: {{.Description}}
  {{end}}
  
  Always use tools when you need to fetch data.
```

**Tool Post-Prompts**:

```yaml
# prompts/tool_postprompts.yaml
tools:
  get_wb_parent_categories:
    post_prompt: "wb/parent_categories_analysis.yaml"
    enabled: true

  get_wb_subjects:
    post_prompt: "wb/subjects_analysis.yaml"
    enabled: true
```

---

## 6. The 11 Immutable Rules

These rules are defined in [`dev_manifest.md`](dev_manifest.md) and must be followed by all developers.

### Rule 0: Code Reuse

**Principle**: Any development within the codebase should first use what has been developed, but if the existing solution hinders development, it can be replaced (refactoring).

**Implication**: Don't reinvent the wheel. Use existing components, patterns, and utilities. Refactor only when necessary.

### Rule 1: Tool Interface Contract

**Principle**: Never change the [`Tool`](pkg/tools/registry.go) interface. "Raw In, String Out" is immutable.

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

**Implication**: All tools must implement only this contract. Tools parse their own JSON.

**Why This Is Good**: This is the best solution for LLM tools. Attempting to make typed arguments at the interface level would lead to hell with `interface{}` and reflection inside the core. This way - each tool knows how to parse its JSON. This makes the system infinitely flexible.

### Rule 2: Configuration

**Principle**: All settings must be in YAML with ENV variable support. No hardcoded values in code.

**Implication**: Use [`config.yaml`](config.yaml) with `${VAR}` syntax. The [`AppConfig`](pkg/config/config.go) structure can be extended, but existing fields must not change.

### Rule 3: Registry Usage

**Principle**: All tools must be registered via [`Registry.Register()`](pkg/tools/registry.go). No direct calls bypassing the registry.

**Implication**: This turns Poncho from a simple script into a modular system. You can build a binary with one set of tools for admins, and another for users, just by changing `main.go`, without touching the core.

### Rule 4: LLM Abstraction

**Principle**: Work with AI models **only** through the [`Provider`](pkg/llm/llm.go) interface. No direct API calls to specific providers in business logic.

**Implication**: All AI model interaction goes through the abstraction layer. Vendor-specific details live only in adapter packages.

**Why This Is Good**: Critically important. Today OpenAI is popular, tomorrow DeepSeek, the day after tomorrow local Llama. The Provider interface guarantees that the framework will survive the hype cycle.

### Rule 5: State Management

**Principle**: State management through layered architecture with thread-safe access. No global variables.

**Architecture (2026-01-04 Refactoring)**:
- **`pkg/state/CoreState`**: Framework core (reusable e-commerce logic)
- **`internal/app/AppState`**: Application-specific state (embeds CoreState)
- **Repository Pattern**: Domain-specific interfaces with unified storage

**Repository Interfaces** ([`pkg/state/repository.go`](pkg/state/repository.go)):
- `UnifiedStore` - Base CRUD operations (Get, Set, Update, Delete, Exists, List)
- `MessageRepository` - Dialog history management (Append, GetHistory)
- `FileRepository` - Working memory (UpdateFileAnalysis, GetFiles)
- `TodoRepository` - Task plan management (AddTask, CompleteTask, FailTask, GetTodoString)
- `DictionaryRepository` - E-commerce data access (GetDictionaries)
- `StorageRepository` - S3 client access (GetS3)

**Single Implementation**: [`CoreState`](pkg/state/core.go) implements all repositories with unified storage (`map[string]any`).

**Implication**:
- Framework logic uses `CoreState` (no `internal/` imports)
- Application logic uses `AppState` (embeds `CoreState` via composition)
- All runtime state protected by mutexes
- No ad-hoc global variables for conversational/session state
- Interface segregation - each repository exposes only relevant methods
- Single source of truth - unified storage prevents data duplication

### Rule 6: Package Structure

**Principle**:
- [`pkg/`](pkg/) - Library code ready for reuse (NO imports from `internal/`)
- [`internal/`](internal/) - Application-specific logic
- [`cmd/`](cmd/) - Entry points (initialization and orchestration only)

**Rule 6 Compliance (Achieved 2026-01-04)**:
- ✅ `pkg/state` - NO `internal/` imports
- ✅ `pkg/chain` - NO `internal/` imports
- ✅ `pkg/agent` - NO `internal/` imports
- ⚠️ `pkg/app/components` - Imports `internal/` (acceptable for component initialization)

**Implication**: Clear separation between reusable library code and application-specific implementation. Framework is now modular and reusable.

### Rule 7: Error Handling

**Principle**: All errors must be returned up the stack. **No `panic()`** in business logic. Framework must be resilient to LLM hallucinations.

**Implication**: Tool errors are returned as strings to LLM. Orchestrator never panics. Framework is resilient against LLM hallucinations.

**Why This Is Good**: For AI agents, this is more important than for web applications. The model will make mistakes, will output broken JSON. The absence of panic and correct error return is the only way to make a stable robot.

### Rule 8: Extensibility

**Principle**: New features added only through:
- New tools in [`pkg/tools/std/`](pkg/tools/std/) or custom packages
- New LLM adapters in [`pkg/llm/`](pkg/llm/)
- Configuration extensions (no breaking changes)

**Implication**: Add features through the established extension points, not by modifying core framework code.

### Rule 9: Testing

**Principle**: Every tool must support mocking dependencies for unit tests. No direct HTTP calls without abstraction. **But!** No tests are written initially; instead, prepare a utility in `/cmd` for verifying functionality.

**Implication**: Use CLI utilities for verification instead of unit tests during development. Write tests only when necessary.

### Rule 10: Documentation

**Principle**: All public APIs must have godoc comments. Interface changes must be accompanied by updated usage examples.

**Implication**: Document all public APIs with clear examples.

### Rule 11: Application Resource Localization

**Principle**: Any application in [`/cmd`](cmd/) must be autonomous and store its resources nearby:

- **Prompts**: Default in `{app_dir}/prompts/` (flat structure without nested folders)
- **Config**: Default `{app_dir}/config.yaml` (searched next to executable)
- **Logs**: Default `{app_dir}/logs/` (or stdout for CLI utilities)

**Implication**: Each application in [`/cmd`](cmd/) must implement [`ConfigPathFinder`](pkg/app/components.go), which searches for `config.yaml` first in its directory, and only then uses fallback (current directory or project path).

**Why This Is Good**: This turns each utility in [`/cmd`](cmd/) into a self-contained artifact that can be copied to any directory and run independently of the main project. No confusion "where is the config?", no dependency on the project root. A CLI utility should work like any Linux tool - it carries everything it needs with it.

---

## 7. Dependencies and Technologies

### 7.1 Go Dependencies

| Package | Version | Purpose | Usage |
|---------|---------|---------|-------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework | Model-View-Update pattern for terminal UI |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling | Terminal styling and colors |
| `github.com/charmbracelet/bubbles` | v0.18.0 | TUI components | Text input, viewport, etc. |
| `github.com/minio/minio-go/v7` | v7.0.97 | S3 compatible storage | S3 storage client for PLM data |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML configuration | Config file parsing |
| `github.com/sashabaranov/go-openai` | v1.41.2 | OpenAI-compatible API | LLM provider abstraction |
| `golang.org/x/time/rate` | v0.14.0 | Rate limiting | API rate limiting |
| `github.com/fatih/color` | v1.17.0 | Terminal colors | Colored output in CLI |

### 7.2 External Services

| Service | Purpose | Configuration |
|---------|---------|---------------|
| **ZAI API** | LLM provider | `models.definitions.*.api_key` |
| **Yandex S3** | PLM data storage | `s3.endpoint`, `s3.access_key`, `s3.secret_key` |
| **Wildberries API** | E-commerce data | `wb.api_key` |

### 7.3 Technology Stack

**Language**: Go 1.21+

**Architecture**:
- **Pattern**: ReAct (Reasoning + Acting)
- **UI Framework**: Bubble Tea (TEA pattern)
- **Configuration**: YAML with ENV expansion
- **Storage**: S3-compatible (Yandex Cloud)
- **Logging**: Structured logging with JSON debug logs

**Key Design Decisions**:
- **"Raw In, String Out"** for tools - maximum flexibility
- **LLM-agnostic** - works with any OpenAI-compatible API
- **Thread-safe** - all shared state protected by mutexes
- **No panics** - resilient error handling
- **Modular** - clear separation of concerns

---

## 8. Development Workflow

### 8.1 Building and Running

**Main TUI Application**:

```bash
# Run directly
go run cmd/poncho/main.go

# Build binary
go build -o poncho cmd/poncho/main.go
./poncho
```

**Maxiponcho TUI**:

```bash
# Run directly
go run cmd/maxiponcho/main.go

# Build binary
go build -o maxiponcho cmd/maxiponcho/main.go
./maxiponcho
```

**Chain CLI**:

```bash
# Build binary
go build -o chain-cli cmd/chain-cli/main.go

# Run with query
./chain-cli "найди все товары в категории Верхняя одежда"

# With debug
./chain-cli -debug "show me trace"

# JSON output
./chain-cli -json "query" | jq .
```

**Vision CLI**:

```bash
# Build binary
go build -o vision-cli cmd/vision-cli/main.go

# Run interactive
./vision-cli

# Run with query
./vision-cli -query "опиши это изображение"
```

**Debug Test**:

```bash
# Run debug test
go run cmd/debug-test/main.go

# Build binary
go build -o debug-test cmd/debug-test/main.go
./debug-test
```

**WB Tools Test**:

```bash
# Run interactive tester
go run cmd/wb-tools-test/main.go

# Build binary
go build -o wb-tools-test cmd/wb-tools-test/main.go
./wb-tools-test
```

**Maxiponcho Test**:

```bash
# Run test
go run cmd/maxiponcho-test/main.go

# Build binary
go build -o maxiponcho-test cmd/maxiponcho-test/main.go
./maxiponcho-test -query "test query"
```

### 8.2 Testing

**Run All Tests**:

```bash
go test ./... -v
```

**Run Specific Package**:

```bash
go test ./pkg/utils -v
```

**Run Specific Test**:

```bash
go test ./pkg/utils -v -run TestCleanJsonBlock
```

**Coverage Report**:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### 8.3 Creating a New Tool

**Step 1**: Create file in [`pkg/tools/std/`](pkg/tools/std/) (e.g., `my_tool.go`)

**Step 2**: Implement [`Tool`](pkg/tools/registry.go) interface:

```go
package std

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/ilkoid/poncho-ai/pkg/tools"
)

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

**Important**: Tools without parameters MUST include `"required": []string{}`:

```go
Parameters: map[string]interface{}{
    "type":       "object",
    "properties": map[string]interface{}{},
    "required":   []string{}, // Required for LLM API compatibility
}
```

**Step 3**: Register in [`pkg/app/components.go`](pkg/app/components.go) `SetupTools()`:

```go
func SetupTools(state *state.CoreState, wbClient *wb.Client, toolSet ToolSet) {
    registry := state.GetToolsRegistry()

    // Register your tool
    if toolSet & ToolWB != 0 {
        registry.Register(&MyTool{/* deps */})
    }
}
```

**Note**: `SetupTools()` now accepts `*state.CoreState` (framework core) instead of `*app.GlobalState` (Rule 6 compliant).

**Step 4**: Test using CLI utility (Rule 9):

```bash
# Use wb-tools-test or create new test utility
./wb-tools-test
```

### 8.4 Creating a New CLI Utility

**Step 1**: Create directory in [`cmd/`](cmd/)

```bash
mkdir cmd/my-utility
```

**Step 2**: Create [`main.go`](cmd/my-utility/main.go):

```go
package main

import (
    "fmt"
    "os"

    appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
)

func main() {
    // 1. Load config (Rule 11: next to binary)
    cfg, cfgPath, err := appcomponents.InitializeConfig(&MyUtilityConfigPathFinder{})
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
        os.Exit(1)
    }

    // 2. Initialize components
    components, err := appcomponents.Initialize(cfg, 10, "")
    if err != nil {
        fmt.Fprintf(os.Stderr, "Initialization failed: %v\n", err)
        os.Exit(1)
    }

    // 3. Your utility logic here
    fmt.Println("Utility running with config:", cfgPath)
}

// MyUtilityConfigPathFinder implements ConfigPathFinder (Rule 11)
type MyUtilityConfigPathFinder struct{}

func (f *MyUtilityConfigPathFinder) FindConfigPath() string {
    // Search next to binary first
    if exePath, err := os.Executable(); err == nil {
        exeDir := filepath.Dir(exePath)
        cfgPath := filepath.Join(exeDir, "config.yaml")
        if _, err := os.Stat(cfgPath); err == nil {
            return cfgPath
        }
    }
    // Fallback
    return "config.yaml"
}
```

**Step 3**: Build and test:

```bash
go build -o my-utility cmd/my-utility/main.go
./my-utility
```

### 8.5 Debugging

**Enable Debug Logging**:

```yaml
# config.yaml
app:
  debug_logs:
    enabled: true
    save_logs: true
    logs_dir: "./debug_logs"
    include_tool_args: true
    include_tool_results: true
    max_result_size: 5000
```

**View Debug Logs**:

```bash
# List all debug logs
ls -la debug_logs/

# View specific log
cat debug_logs/debug_20260101_224928.json

# Pretty print with jq
cat debug_logs/debug_20260101_224928.json | jq .
```

**Debug Log Structure**:

```json
{
  "run_id": "20260101_224928",
  "timestamp": "2026-01-01T22:49:28Z",
  "query": "user query",
  "duration_ms": 205,
  "iterations": [...],
  "final_response": "answer",
  "success": true
}
```

**Use debug-test Utility**:

```bash
# Simulate debug logging
./debug-test
```

---

## 9. Key Insights and Design Philosophy

### 9.1 Core Strengths

**1. "Raw In, String Out" Philosophy**

This is the best solution for LLM tools. Attempting to make typed arguments at the interface level would lead to hell with `interface{}` and reflection inside the core. This way - each tool knows how to parse its JSON. This makes the system infinitely flexible.

**2. Registry Pattern**

This turns Poncho from a simple script into a modular system. You can build a binary with one set of tools for admins, and another for users, just by changing `main.go`, without touching the core.

**3. LLM Abstraction**

Critically important. Today OpenAI is popular, tomorrow DeepSeek, the day after tomorrow local Llama. The Provider interface guarantees that the framework will survive the hype cycle.

**4. Error Handling and Resilience**

For AI agents, this is more important than for web applications. The model will make mistakes, will output broken JSON. The absence of panic and correct error return is the only way to make a stable robot.

**5. Resource Localization (Rule 11)**

This turns each utility in [`/cmd`](cmd/) into a self-contained artifact that can be copied to any directory and run independently of the main project. No confusion "where is the config?", no dependency on the project root. A CLI utility should work like any Linux tool - it carries everything it needs with it.

### 9.2 Design Philosophy

**"Convention over Configuration"**

Developers follow simple rules, and the framework handles the complexity of prompt engineering, validation, and orchestration.

**Key Principles**:
- **Simplicity**: Clear, simple interfaces
- **Flexibility**: Easy to extend and customize
- **Resilience**: Graceful error handling
- **Modularity**: Clear separation of concerns
- **Testability**: Easy to test and debug

### 9.3 Architecture Highlights

**1. Clean Separation of Concerns**

```
Tools (Business Logic)
    ↓
Orchestrator (Coordination)
    ↓
LLM Provider (AI)
    ↓
Global State (Management)
    ↓
UI (Presentation)
```

**2. Thread-Safe Operations**

All runtime state is protected by mutexes:
- [`CoreState`](pkg/state/core.go) - Framework core state
- [`AppState`](internal/app/state.go) - Application state (embeds CoreState)
- [`ToolsRegistry`](pkg/tools/registry.go) - Tool registration and lookup
- [`TodoManager`](pkg/todo/manager.go) - Atomic task operations
- [`CommandRegistry`](internal/app/commands.go) - Command registration

**3. LLM-Agnostic Design**

Works with any OpenAI-compatible API through the [`Provider`](pkg/llm/llm.go) interface.

**4. Resilient Error Handling**

No panics in business logic. All errors returned up the stack. Framework is resilient against LLM hallucinations.

**5. Extensible Architecture**

Easy to add:
- New tools in [`pkg/tools/std/`](pkg/tools/std/) or custom packages
- New LLM adapters in [`pkg/llm/`](pkg/llm/)
- New commands in [`internal/app/commands.go`](internal/app/commands.go)
- New CLI utilities in [`cmd/`](cmd/)

### 9.4 Best Practices

**1. Always Use Existing Components**

Rule 0: Any development should first use what has been developed. Don't reinvent the wheel.

**2. Follow the Tool Interface**

Rule 1: Never change the [`Tool`](pkg/tools/registry.go) interface. "Raw In, String Out" is immutable.

**3. Use Configuration**

Rule 2: All settings in YAML with ENV support. No hardcoded values.

**4. Register Tools**

Rule 3: All tools registered via [`Registry.Register()`](pkg/tools/registry.go). No direct calls bypassing registry.

**5. Use LLM Abstraction**

Rule 4: Work with AI models only through [`Provider`](pkg/llm/llm.go) interface.

**6. Manage State Properly**

Rule 5: State management through layered architecture:
- Framework logic uses [`CoreState`](pkg/state/core.go)
- Application logic uses [`AppState`](internal/app/state.go) (embeds CoreState)

**7. Handle Errors Gracefully**

Rule 7: Return errors up the stack. No `panic()` in business logic.

**8. Extend Through Established Points**

Rule 8: New features through tools, adapters, or config extensions.

**9. Test with CLI Utilities**

Rule 9: Use CLI utilities for verification instead of unit tests during development.

**10. Document Public APIs**

Rule 10: All public APIs must have godoc comments.

**11. Localize Resources**

Rule 11: Each application in [`/cmd`](cmd/) must be autonomous with resources nearby.

---

## 10. Documentation Files

### 10.1 Core Documentation

| File | Purpose |
|------|---------|
| [`brief.md`](brief.md) | This file - comprehensive project documentation |
| [`dev_manifest.md`](dev_manifest.md) | The 11 Immutable Rules for development |
| [`CLAUDE.md`](CLAUDE.md) | AI assistant context and instructions |
| [`.gitignore`](.gitignore) | Git ignore patterns |

### 10.2 Design Documents

| File | Purpose |
|------|---------|
| [`CHAIN_PATTERN_DESIGN.md`](CHAIN_PATTERN_DESIGN.md) | Chain Pattern design and implementation plan |
| [`REFACTORING_PLAN.md`](REFACTORING_PLAN.md) | Refactoring plans and strategies |
| [`IMPROVEMENT_PLAN.md`](IMPROVEMENT_PLAN.md) | Improvement plans and feature requests |
| [`REASONING_MODEL_TZ.md`](REASONING_MODEL_TZ.md) | Reasoning model specifications |
| [`CMD_CHAIN_CLI_PLAN.md`](CMD_CHAIN_CLI_PLAN.md) | Chain CLI implementation plan |

### 10.3 Component Documentation

| File | Purpose |
|------|---------|
| [`docs/eino_comparison.md`](docs/eino_comparison.md) | Comparison with Eino framework |

### 10.4 Tool Documentation

| File | Purpose |
|------|---------|
| [`cmd/vision-cli/README.md`](cmd/vision-cli/README.md) | Vision CLI usage guide |
| [`cmd/vision-cli/conf.yaml.bak.md`](cmd/vision-cli/conf.yaml.bak.md) | Vision CLI configuration backup |

---

## Conclusion

Poncho AI represents a **mature AI agent framework** with:

1. **Clear separation of concerns** - Tools, Orchestrator, State, UI
2. **Thread-safe operations** - All runtime state protected
3. **LLM-agnostic design** - Works with any OpenAI-compatible API
4. **Resilient error handling** - No panics, graceful degradation
5. **Extensible architecture** - Easy to add tools, commands, LLM providers
6. **Natural interface** - TUI with seamless AI integration
7. **Comprehensive debugging** - JSON debug logs with full execution trace
8. **Multiple CLI tools** - Specialized utilities for different use cases
9. **Modular design** - Clean package structure with clear responsibilities
10. **Production-ready** - Includes logging, error handling, and testing utilities

The framework follows the principle of **"Convention over Configuration"** - developers follow simple rules, and the framework handles the complexity of prompt engineering, validation, and orchestration.

**Key Takeaway**: Poncho AI is not just a tool, but a **framework** for building AI agents. It provides the infrastructure, patterns, and conventions that allow developers to focus on business logic while the framework handles the complexity of AI agent orchestration.

---

## Quick Reference

### Common Commands

```bash
# Run main TUI
go run cmd/poncho/main.go

# Run maxiponcho TUI
go run cmd/maxiponcho/main.go

# Test Chain Pattern
go run cmd/chain-cli/main.go "query"

# Test vision analysis
go run cmd/vision-cli/main.go -query "analyze"

# Test debug logging
go run cmd/debug-test/main.go

# Test WB tools interactively
go run cmd/wb-tools-test/main.go

# Test maxiponcho
go run cmd/maxiponcho-test/main.go -query "test"

# Run all tests
go test ./... -v

# Build all binaries
go build -o poncho cmd/poncho/main.go
go build -o maxiponcho cmd/maxiponcho/main.go
go build -o chain-cli cmd/chain-cli/main.go
go build -o vision-cli cmd/vision-cli/main.go
go build -o debug-test cmd/debug-test/main.go
go build -o wb-tools-test cmd/wb-tools-test/main.go
go build -o maxiponcho-test cmd/maxiponcho-test/main.go
```

### Configuration Files

| File | Purpose |
|------|---------|
| [`config.yaml`](config.yaml) | Main configuration file |
| [`config.yaml.example`](config.yaml.example) | Configuration template |
| [`cmd/maxiponcho/config.yaml`](cmd/maxiponcho/config.yaml) | Maxiponcho-specific config |

### Key Directories

| Directory | Purpose |
|-----------|---------|
| [`cmd/`](cmd/) | Application entry points |
| [`internal/`](internal/) | Application-specific logic |
| [`pkg/`](pkg/) | Reusable library packages |
| [`prompts/`](prompts/) | Prompt templates |
| [`debug_logs/`](debug_logs/) | Debug log files |
| [`docs/`](docs/) | Additional documentation |

### Environment Variables

```bash
# Set required environment variables
export ZAI_API_KEY="your-zai-api-key"
export S3_ACCESS_KEY="your-s3-access-key"
export S3_SECRET_KEY="your-s3-secret-key"
export WB_API_KEY="your-wb-api-key"
```

---

**Last Updated**: 2026-01-04 (Repository Pattern + Code Optimization: -300 lines)

**Version**: 2.1

**Maintainer**: Poncho AI Development Team

---

## Architecture Evolution History

| Phase | Date | Changes |
|-------|------|---------|
| **Phase 1** | 2025-12-XX | Monolithic Orchestrator implementation |
| **Phase 2** | 2025-12-XX | Chain Pattern introduction |
| **Phase 3** | 2025-12-XX | Debug System integration |
| **Phase 4** | 2026-01-04 | **Repository Pattern + Optimization** |
| | | - Unified storage architecture (`map[string]any`) |
| | | - Domain-specific repository interfaces |
| | | - Removed ~300 lines of unused code |
| | | - Simplified Agent interface (removed ClearHistory) |
| | | - Single source of truth for state management |
