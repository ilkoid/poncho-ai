# Plan: TUI Todo Agent Utility (`cmd/todo-agent/`)

## Overview
Новая автономная TUI утилита с нуля для управления задачами через AI агента. Пользователь вводит запрос, агент создаёт план через todo tools и выполняет его с помощью всех доступных инструментов.

**User Workflow:**
1. Утилита инициализирует все компоненты
2. TUI интерфейс принимает запрос пользователя
3. Агент анализирует запрос и создаёт todo список
4. Агент выполняет задачи с использованием tools
5. TUI показывает прогресс, todo list и tool execution trace
6. Результат сохраняется в JSON лог в `logs/`

## Requirements Summary
- **Location**: `cmd/todo-agent/`
- **Config**: `cmd/todo-agent/config.yaml` (local, standalone)
- **Prompts**: `cmd/todo-agent/prompts/` (local, standalone)
- **Logs**: `cmd/todo-agent/logs/` (JSON trace logs)
- **Tools**: All working tools (24 tools - 9 stubs excluded)
- **TUI Features**: ReAct cycle + Todo List widget + Tool execution trace
- **No CLI args**: All config from YAML

---

## Architecture

### Directory Structure
```
cmd/todo-agent/
├── main.go                 # Entry point
├── config.yaml            # Local configuration
├── prompts/
│   └── system.yaml        # System prompt for agent
├── logs/                  # JSON debug logs (created at runtime)
└── README.md              # Usage documentation
```

### Component Architecture
```
main.go
    │
    ├── config.Load("./config.yaml")
    │
    ├── agent.New(agent.Config{ConfigPath: "./config.yaml"})
    │   ├── ModelRegistry (from config)
    │   ├── ToolsRegistry (all 24 working tools)
    │   └── CoreState
    │
    ├── events.NewChanEmitter(100)
    │
    ├── TUI Model (custom BubbleTea)
    │   ├── View: Input prompt
    │   ├── View: Todo list (from CoreState)
    │   └── View: Tool execution trace
    │
    └── debug.NewRecorder()
        └── logs/debug_TIMESTAMP.json
```

---

## Files to Create

### 1. `cmd/todo-agent/main.go`

**Purpose**: Entry point, initializes all components, runs TUI

**Code Pattern** (based on `cmd/poncho/main.go`):
```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/ilkoid/poncho-ai/pkg/agent"
    "github.com/ilkoid/poncho-ai/pkg/config"
    "github.com/ilkoid/poncho-ai/pkg/debug"
    "github.com/ilkoid/poncho-ai/pkg/events"
    "github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
    // 1. Load local config (Rule 11: рядом с бинарником)
    cfg, err := config.Load("./config.yaml")
    if err != nil {
        log.Fatalf("Config load failed: %v", err)
    }

    // 2. Create agent client
    client, err := agent.New(agent.Config{ConfigPath: "./config.yaml"})
    if err != nil {
        log.Fatalf("Agent creation failed: %v", err)
    }

    // 3. Setup event emitter (Port & Adapter)
    emitter := events.NewChanEmitter(100)
    client.SetEmitter(emitter)

    // 4. Create debug recorder
    recorder, err := debug.NewRecorder(debug.RecorderConfig{
        LogsDir:           "./logs",
        IncludeToolArgs:   true,
        IncludeToolResults: true,
        MaxResultSize:     5000,
    })
    if err != nil {
        log.Fatalf("Debug recorder creation failed: %v", err)
    }

    // 5. Get subscriber for TUI
    sub := client.Subscribe()

    // 6. Create custom TUI model
    model := NewTodoAgentModel(client, sub, recorder)

    // 7. Run TUI
    p := tea.NewProgram(model)
    if _, err := p.Run(); err != nil {
        log.Fatalf("TUI error: %v", err)
    }
}
```

### 2. `cmd/todo-agent/model.go`

**Purpose**: Custom BubbleTea model with Todo List + Tool Trace views

**Key Components**:
```go
type todoAgentModel struct {
    // Agent components
    client   *agent.Client
    sub      *events.Subscriber
    recorder *debug.Recorder

    // TUI state
    input    string           // User input buffer
    loading  bool             // Agent processing state
    todos    []todo.Task      // Current todo list
    trace    []ToolExecEntry  // Tool execution trace
    output   string           // Agent response
    err      error            // Error state

    // UI dimensions
    width    int
    height   int
}

type ToolExecEntry struct {
    ToolName string
    Args     string
    Result   string
    Duration time.Duration
    Status   string  // "running", "done", "error"
}
```

**BubbleTea Messages**:
```go
type userInputMsg string
type agentRunMsg   struct{ query string; result string }
type agentErrorMsg error
type todoUpdateMsg []todo.Task
type toolEventMsg  events.Event
```

**View Layout**:
```
┌─────────────────────────────────────────────────────┐
│ Todo Agent v1.0                     [logs/todo.log] │
├─────────────────────────────────────────────────────┤
│                                                     │
│  Todo List:                          Tool Trace:    │
│  □ [1/3] Analyze S3 files              ✓ list_s3   │
│  □ [2/3] Download images               → download   │
│  □ [3/3] Generate report               (running)    │
│                                                     │
│  Agent Response:                                    │
│  Starting task execution...                        │
│                                                     │
├─────────────────────────────────────────────────────┤
│ > Enter your task or command:                      │
└─────────────────────────────────────────────────────┘
```

### 3. `cmd/todo-agent/config.yaml`

**Purpose**: Local configuration with all 33 tools enabled

**Structure**:
```yaml
# Models Configuration
models:
  default_reasoning: "glm-4.6"
  default_chat: "glm-4.6"
  default_vision: "glm-4.6v-flash"
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
      parallel_tool_calls: false

# S3 Configuration
s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"
  use_ssl: true

# Image Settings
image_processing:
  max_width: 800
  quality: 90

# App Settings
app:
  debug: true
  prompts_dir: "./prompts"
  debug_logs:
    enabled: true
    save_logs: true
    logs_dir: "./logs"
    include_tool_args: true
    include_tool_results: true
    max_result_size: 5000

# File Classification Rules
file_rules:
  - tag: "sketch"
    patterns: ["*.jpg", "*.jpeg", "*.png"]
    required: true
  - tag: "plm_data"
    patterns: ["*.json"]
    required: true

# Wildberries API Defaults
wb:
  api_key: "${WB_API_KEY}"
  base_url: "https://content-api.wildberries.ru"
  rate_limit: 100
  burst_limit: 5
  retry_attempts: 3
  timeout: "30s"

# Tools Configuration - 24 WORKING TOOLS (9 stubs excluded)
tools:
  # === WB Content API Tools ===
  get_wb_parent_categories:
    enabled: true
    description: "Возвращает список родительских категорий Wildberries"
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/parent/all"
    rate_limit: 100
    burst: 5

  get_wb_subjects:
    enabled: true
    description: "Возвращает список предметов для категории с пагинацией"
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/all"
    rate_limit: 100
    burst: 5

  get_wb_subjects_by_name:
    enabled: true
    description: "Ищет предметы по подстроке в названии"
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5

  get_wb_characteristics:
    enabled: true
    description: "Возвращает характеристики для предмета"
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5

  get_wb_tnved:
    enabled: true
    description: "Возвращает коды ТНВЭД для предмета"
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5

  get_wb_brands:
    enabled: true
    description: "Возвращает бренды для предмета"
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5

  ping_wb_api:
    enabled: true
    description: "Проверяет доступность Wildberries Content API"
    endpoint: "https://content-api.wildberries.ru"
    path: "/ping"
    rate_limit: 100
    burst: 5

  # === WB Dictionary Tools ===
  wb_colors:
    enabled: true
    description: "Ищет цвета в справочнике Wildberries"

  wb_countries:
    enabled: true
    description: "Возвращает справочник стран производства"

  wb_genders:
    enabled: true
    description: "Возвращает справочник значений пола"

  wb_seasons:
    enabled: true
    description: "Возвращает справочник сезонов"

  wb_vat_rates:
    enabled: true
    description: "Возвращает справочник ставок НДС"

  # === S3 Basic Tools ===
  list_s3_files:
    enabled: true
    description: "Возвращает список файлов в S3 по пути"

  read_s3_object:
    enabled: true
    description: "Читает содержимое файла из S3"

  read_s3_image:
    enabled: true
    description: "Скачивает и оптимизирует изображение из S3"

  # === Planner Tools ===
  plan_add_task:
    enabled: true
    description: "Добавляет новую задачу в план"

  plan_mark_done:
    enabled: true
    description: "Отмечает задачу как выполненную"

  plan_mark_failed:
    enabled: true
    description: "Отмечает задачу как проваленную"

  plan_clear:
    enabled: true
    description: "Очищает весь план"

  plan_set_tasks:
    enabled: true
    description: "Создаёт или заменяет весь план задач"
```

### 4. `cmd/todo-agent/prompts/system.yaml`

**Purpose**: Specialized short system prompt for todo agent

**Content**:
```yaml
config:
  model: "glm-4.6"
  temperature: 0.5
  max_tokens: 2000

messages:
  - role: system
    content: |
      Ты Todo Agent - AI ассистент для планирования и выполнения задач.

      При получении запроса пользователя:
      1. Сначала создай план задач через plan_set_tasks
      2. Выполняй задачи последовательно через доступные tools
      3. Отмечай выполненные задачи через plan_mark_done
      4. При ошибке отметь задачу через plan_mark_failed с причиной
      5. По завершению предоставь краткий отчёт

      Доступные инструменты:
      - S3: работа с файлами (list_s3_files, read_s3_object, read_s3_image)
      - WB Catalog: категории и предметы (get_wb_parent_categories, get_wb_subjects, ping_wb_api)
      - WB Characteristics: характеристики, ТНВЭД, бренды (get_wb_characteristics, get_wb_tnved, get_wb_brands, get_wb_subjects_by_name)
      - WB Dictionary: справочники (wb_colors, wb_countries, wb_genders, wb_seasons, wb_vat_rates)
      - Planner: управление задачами (plan_set_tasks, plan_mark_done, plan_mark_failed)

      Всегда начинай с создания плана. Пиши ответы на русском языке.
```

---

## Implementation Steps

### Step 1: Create Directory Structure
```bash
mkdir -p cmd/todo-agent/prompts
mkdir -p cmd/todo-agent/logs
touch cmd/todo-agent/README.md
```

### Step 2: Create `config.yaml`
- Copy structure above
- Enable all 33 tools (`enabled: true`)
- Set local paths (`./prompts`, `./logs`)

### Step 3: Create `prompts/system.yaml`
- Short specialized prompt for todo agent
- Include tool descriptions
- Russian language

### Step 4: Implement `main.go`
- Load local config
- Create agent client
- Setup event emitter
- Create debug recorder
- Initialize TUI model
- Run BubbleTea program

### Step 5: Implement `model.go`
- `todoAgentModel` struct
- `Init()` method
- `Update()` method with message routing:
  - `tea.KeyMsg` → handle user input
  - `userInputMsg` → trigger agent run
  - `toolEventMsg` → update tool trace
  - `todoUpdateMsg` → update todo list
- `View()` method with layout:
  - Header (title + log file)
  - Todo list (left panel)
  - Tool trace (right panel)
  - Agent output (bottom area)
  - Input prompt (footer)

### Step 6: Integration with `pkg/events`
- Subscribe to agent events
- Map events to TUI updates:
  - `EventThinking` → show loading spinner
  - `EventToolCall` → add to tool trace (status: running)
  - `EventToolResult` → update tool trace (status: done)
  - `EventMessage` → update output
  - `EventError` → show error
  - `EventDone` → finalize

### Step 7: Debug Logging
- Wrap agent calls with recorder
- Save to `logs/debug_TIMESTAMP.json`
- Display log path in TUI header

### Step 8: Create `README.md`
- Usage instructions
- Environment variables
- Example queries

---

## Critical Files Reference

| File | Purpose |
|------|---------|
| `pkg/agent/agent.go` | Agent client facade |
| `pkg/events/events.go` | Port interfaces (Emitter, Subscriber) |
| `pkg/events/chan_emitter.go` | Standard emitter implementation |
| `pkg/debug/recorder.go` | JSON debug logging |
| `internal/ui/model.go` | Reference TUI implementation |
| `cmd/poncho/main.go` | Reference entry point |
| `pkg/app/components.go` | Tool registration patterns |

---

## Verification Steps

### 1. Build
```bash
cd cmd/todo-agent
go build -o todo-agent
./todo-agent
```

### 2. Test Basic Query
Input: `проверь доступность WB API`
Expected:
- Todo list created
- `ping_wb_api` tool called
- Result displayed
- Log file created

### 3. Test Multi-Step Query
Input: `загрузи артикул 12345, проанализируй эскизы и создай отчёт`
Expected:
- Plan created (3-5 tasks)
- Tools called sequentially
- Todo list updates in real-time
- Tool trace shows all executions

### 4. Verify Debug Log
```bash
ls -la logs/
cat logs/debug_*.json | jq '.summary'
```

### 5. Test All Tools
Input: `покажи все доступные инструменты`
Expected: Agent lists all 24 working tools

---

## Success Criteria

✅ **Standalone**: All resources in `cmd/todo-agent/`
✅ **Max Code Reuse**: Uses `pkg/agent`, `pkg/events`, `pkg/debug`
✅ **All Tools Enabled**: 24 working tools available (9 stubs excluded)
✅ **TUI Features**: Input + Todo List + Tool Trace
✅ **Debug Logging**: JSON logs in `logs/`
✅ **No CLI Args**: Config from YAML only
✅ **Specialized Prompt**: Short system prompt for todo agent
