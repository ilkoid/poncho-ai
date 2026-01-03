# Chain Pattern Design для Poncho AI

## Цель

**Полный рефакторинг Orchestrator с использованием Chain Pattern.**

⚠️ **Обратная совместимость НЕ требуется** — можно менять публичные API.

---

## Архитектурные принципы (из dev_manifest.md)

| Правило | Применение к Chain |
|---------|-------------------|
| **Rule 1** | Chain работает с Tool interface ("Raw In, String Out") |
| **Rule 2** | Chain конфигурируется через YAML (теперь обязательно) |
| **Rule 3** | Tools вызываются через Registry |
| **Rule 4** | LLM вызывается через `llm.Provider` |
| **Rule 5** | ChainContext thread-safe |
| **Rule 6** | `pkg/chain/` — библиотечный код |
| **Rule 7** | Все ошибки возвращаются, нет panic |
| **Rule 8** | Расширяемость через новые Step типы |
| **Rule 9** | CLI утилита `cmd/chain-cli/` (без TUI!) |
| **Rule 10** | Godoc на всех public API |
| **Rule 11** | Автономность — chain-cli хранит ресурсы рядом |

---

## YAML Конфигурация (Rule 2)

### config.yaml

```yaml
# Chain Configuration
chains:
  # Основная цепочка для агента
  react_agent:
    type: "react"
    description: "ReAct агент с tool calling"

    # Лимиты
    max_iterations: 10
    timeout: "120s"

    # Шаги цепочки (выполняются последовательно в цикле)
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
          parallel: false  # TODO: в будущем для parallel tools

    # Debug configuration (интеграция с DebugRecorder)
    debug:
      enabled: true
      save_logs: true
      logs_dir: "./debug_logs"
      include_tool_args: true
      include_tool_results: true
      max_result_size: 5000

    # Post-prompts для tools
    post_prompts_dir: "./prompts"

  # Будущие типы цепочек
  sequential_chain:
    type: "sequential"
    description: "Линейная цепочка без циклов"
    steps:
      - name: "step1"
        type: "llm"
      - name: "step2"
        type: "tools"

  # Branching chain (будет в Graph)
  conditional_chain:
    type: "conditional"
    description: "Цепочка с условным ветвлением"
    branches:
      - condition: "has_tool_calls"
        target: "tool_execution"
      - condition: "is_final"
        target: "output"
```

---

## DebugRecorder Интеграция

### Встроенная поддержка debug logging

```go
// pkg/chain/debug.go

// ChainDebugConfig — конфигурация debug логирования для Chain.
type ChainDebugConfig struct {
    Enabled     bool
    Recorder    *debug.Recorder
    IncludeArgs bool
    IncludeResults bool
    MaxResultSize int
}

// AttachDebug прикрепляет DebugRecorder к Chain.
func (c *ReActChain) AttachDebug(recorder *debug.Recorder) {
    c.debug = &ChainDebugConfig{
        Enabled:  true,
        Recorder: recorder,
    }
}

// StartDebug начинает запись новой сессии.
func (c *ReActChain) StartDebug(ctx context.Context, input ChainInput) error {
    if c.debug == nil || !c.debug.Enabled {
        return nil
    }

    debugCfg := input.State.Config.App.DebugLogs.GetDefaults()
    if !debugCfg.Enabled {
        return nil
    }

    recorder, err := debug.NewRecorder(debug.RecorderConfig{
        LogsDir:           debugCfg.LogsDir,
        IncludeToolArgs:   debugCfg.IncludeToolArgs,
        IncludeToolResults: debugCfg.IncludeToolResults,
        MaxResultSize:     debugCfg.MaxResultSize,
    })
    if err != nil {
        return err
    }

    recorder.Start(input.UserQuery)
    c.debug.Recorder = recorder
    return nil
}

// RecordIteration записывает итерацию в DebugRecorder.
func (c *ReActChain) RecordIteration(iter int, llmReq LLMRequest, llmResp LLMResponse, tools []ToolExecution) {
    if c.debug == nil || c.debug.Recorder == nil {
        return
    }

    c.debug.Recorder.StartIteration(iter)
    c.debug.Recorder.RecordLLMRequest(llmReq)
    c.debug.Recorder.RecordLLMResponse(llmResp)

    for _, tool := range tools {
        c.debug.Recorder.RecordToolExecution(debug.ToolExecution{
            Name:     tool.Name,
            Args:     tool.Args,
            Result:   tool.Result,
            Duration: tool.Duration.Milliseconds(),
            Success:  tool.Success,
            Error:    tool.Error,
        })
    }

    c.debug.Recorder.EndIteration()
}

// FinalizeDebug завершает запись и сохраняет лог.
func (c *ReActChain) FinalizeDebug(result string, duration time.Duration) (string, error) {
    if c.debug == nil || c.debug.Recorder == nil {
        return "", nil
    }

    return c.debug.Recorder.Finalize(result, duration)
}
```

---

## Текущая структура Orchestrator (для извлечения)

```
Orchestrator.Run()
│
├── 1. Append user message to history
│
├── 2. ReAct Loop (max 10 iterations)
│   │
│   ├── 2.1. BuildAgentContext(systemPrompt)
│   │
│   ├── 2.2. Determine LLM parameters
│   │       ├─ activePromptConfig → use post-prompt config
│   │       └─ reasoningConfig → use default config
│   │
│   ├── 2.3. llm.Generate(messages, opts..., toolDefs)
│   │
│   ├── 2.4. SanitizeLLMOutput(response.Content)
│   │
│   ├── 2.5. AppendMessage(response)
│   │
│   ├── 2.6. If len(ToolCalls) > 0
│   │       └─ For each ToolCall
│   │           ├─ executeTool(ctx, tc)
│   │           ├─ Check for post-prompt
│   │           └─ AppendMessage(tool result)
│   │
│   └── 2.7. Else (no ToolCalls) → Final response
│           ├─ Reset activePostPrompt
│           └─ Return response.Content
```

---

## Chain Pattern Design

### Основные типы

```go
// pkg/chain/chain.go

// Chain представляет последовательность шагов для выполнения.
// Chain является иммутабельным и thread-safe (Rule 5).
type Chain interface {
    // Execute выполняет цепочку и возвращает результат.
    Execute(ctx context.Context, input ChainInput) (ChainOutput, error)
}

// ChainInput — входные данные для цепочки.
type ChainInput struct {
    UserQuery string                 // Пользовательский запрос
    State     *app.GlobalState       // Глобальное состояние
    LLM       llm.Provider           // LLM провайдер (Rule 4)
    Registry  *tools.Registry        // Registry инструментов (Rule 3)
    Config    ChainConfig            // Конфигурация цепочки
}

// ChainOutput — результат выполнения цепочки.
type ChainOutput struct {
    Result        string            // Финальный ответ
    Iterations    int               // Количество итераций
    Duration      time.Duration     // Общее время выполнения
    FinalState    []llm.Message     // Финальное состояние истории
}
```

### Step — атомарная операция

```go
// pkg/chain/step.go

// Step представляет один шаг в цепочке.
// Все шаги должны быть thread-safe и возвращать ошибки (Rule 7).
type Step interface {
    // Name возвращает имя шага для логирования.
    Name() string

    // Execute выполняет шаг и возвращает:
    // - nextAction: что делать дальше (continue, break, branch)
    // - err: ошибка если произошла
    Execute(ctx context.Context, state *ChainContext) (nextAction NextAction, err error)
}

// NextAction определяет что делать после выполнения шага.
type NextAction int

const (
    ActionContinue NextAction = iota // Продолжить к следующему шагу
    ActionBreak                      // Прервать цепочку (успешно)
    ActionError                      // Прервать цепочку (с ошибкой)
    ActionBranch                     // Ветвление (будет в Graph)
)
```

### ChainContext — состояние выполнения цепочки

```go
// pkg/chain/context.go

// ChainContext содержит состояние выполнения цепочки.
// Thread-safe через sync.RWMutex (Rule 5).
type ChainContext struct {
    mu sync.RWMutex

    // Входные данные (неизменяемые)
    Input *ChainInput

    // Текущее состояние
    CurrentIteration int
    Messages         []llm.Message
    ActivePostPrompt string
    ActivePromptConfig *prompt.PromptConfig

    // LLM параметры текущей итерации
    ActualModel     string
    ActualTemp      float64
    ActualMaxTokens int
}

// GetMessages возвращает копию сообщений (thread-safe).
func (c *ChainContext) GetMessages() []llm.Message

// AppendMessage добавляет сообщение (thread-safe).
func (c *ChainContext) AppendMessage(msg llm.Message)

// SetActivePostPrompt устанавливает активный post-prompt (thread-safe).
func (c *ChainContext) SetActivePostPrompt(prompt string, config *prompt.PromptConfig)
```

---

## Конкретные Step реализации

### 1. LLMInvocationStep

```go
// pkg/chain/llm_step.go

// LLMInvocationStep вызывает LLM с текущим контекстом.
type LLMInvocationStep struct {
    systemPrompt string
    reasoningConfig llm.GenerateOptions
    chatConfig      llm.GenerateOptions
}

func (s *LLMInvocationStep) Execute(ctx context.Context, state *ChainContext) (NextAction, error) {
    // 1. Формируем messages
    systemPrompt := s.systemPrompt
    if state.ActivePostPrompt != "" {
        systemPrompt = state.ActivePostPrompt
    }
    messages := state.Input.State.BuildAgentContext(systemPrompt)

    // 2. Определяем параметры
    var opts []any
    if state.ActivePromptConfig != nil {
        // Используем post-prompt config
        opts = buildOptsFromConfig(state.ActivePromptConfig)
    } else {
        // Используем reasoning config
        opts = buildOptsFromConfig(s.reasoningConfig)
    }

    // 3. Добавляем tools definitions
    toolDefs := state.Input.Registry.GetDefinitions()
    opts = append(opts, toolDefs)

    // 4. Вызываем LLM через Provider (Rule 4)
    response, err := state.Input.LLM.Generate(ctx, messages, opts...)
    if err != nil {
        return ActionError, fmt.Errorf("llm generation failed: %w", err)
    }

    // 5. Санитизируем и сохраняем
    response.Content = utils.SanitizeLLMOutput(response.Content)
    state.AppendMessage(response)

    return ActionContinue, nil
}
```

### 2. ToolExecutionStep

```go
// pkg/chain/tool_step.go

// ToolExecutionStep выполняет все tool calls из последнего LLM ответа.
type ToolExecutionStep struct {
    toolPostPrompts *prompt.ToolPostPromptConfig
    promptsDir      string
}

func (s *ToolExecutionStep) Execute(ctx context.Context, state *ChainContext) (NextAction, error) {
    // 1. Получаем последний message
    lastMsg := state.GetMessages()[len(state.GetMessages())-1]

    // 2. Если нет tool calls — пропускаем
    if len(lastMsg.ToolCalls) == 0 {
        return ActionBreak, nil // Успешное завершение
    }

    // 3. Выполняем каждый tool
    for _, tc := range lastMsg.ToolCalls {
        // 3.1. Получаем tool через Registry (Rule 3)
        tool, err := state.Input.Registry.Get(tc.Name)
        if err != nil {
            return ActionError, fmt.Errorf("tool not found: %w", err)
        }

        // 3.2. Выполняем tool (Raw In, String Out — Rule 1)
        cleanArgs := utils.CleanJsonBlock(tc.Args)
        result, err := tool.Execute(ctx, cleanArgs)
        if err != nil {
            return ActionError, fmt.Errorf("tool execution failed: %w", err)
        }

        // 3.3. Добавляем результат в историю
        state.AppendMessage(llm.Message{
            Role:       llm.RoleTool,
            ToolCallID: tc.ID,
            Content:    result,
        })

        // 3.4. Проверяем post-prompt
        s.loadPostPrompt(tc.Name, state)
    }

    return ActionContinue, nil
}
```

### 3. PostPromptStep

```go
// pkg/chain/postprompt_step.go

// PostPromptStep загружает и активирует post-prompt для tools.
// Логика уже встроена в ToolExecutionStep, но может быть отдельным шагом.
type PostPromptStep struct {
    toolPostPrompts *prompt.ToolPostPromptConfig
    promptsDir      string
}
```

---

## ReActChain — готовая цепочка

```go
// pkg/chain/react.go

// ReActChain реализует ReAct (Reasoning + Acting) паттерн.
// Это основная цепочка для Orchestrator.
type ReActChain struct {
    llmStep         *LLMInvocationStep
    toolStep        *ToolExecutionStep
    maxIterations   int
}

// NewReActChain создает новую ReAct цепочку.
func NewReActChain(cfg ReActChainConfig) *ReActChain {
    return &ReActChain{
        llmStep:       NewLLMInvocationStep(cfg.SystemPrompt, cfg.ReasoningConfig, cfg.ChatConfig),
        toolStep:      NewToolExecutionStep(cfg.ToolPostPrompts, cfg.PromptsDir),
        maxIterations: cfg.MaxIterations,
    }
}

// Execute выполняет ReAct цикл.
func (c *ReActChain) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
    startTime := time.Now()

    // 1. Создаём контекст
    state := NewChainContext(input)
    state.AppendMessage(llm.Message{
        Role:    llm.RoleUser,
        Content: input.UserQuery,
    })

    // 2. ReAct цикл
    for i := 0; i < c.maxIterations; i++ {
        state.CurrentIteration = i + 1

        // 2.1. LLM invocation
        action, err := c.llmStep.Execute(ctx, state)
        if err != nil {
            return ChainOutput{}, err
        }

        // 2.2. Tool execution
        action, err = c.toolStep.Execute(ctx, state)
        if err != nil {
            return ChainOutput{}, err
        }

        // 2.3. Если ActionBreak — финальный ответ
        if action == ActionBreak {
            lastMsg := state.GetMessages()[len(state.GetMessages())-1]
            return ChainOutput{
                Result:     lastMsg.Content,
                Iterations: i + 1,
                Duration:   time.Since(startTime),
                FinalState: state.GetMessages(),
            }, nil
        }

        // 2.4. ActionContinue → следующая итерация
    }

    // 3. Превышен лимит итераций
    return ChainOutput{}, fmt.Errorf("max iterations exceeded")
}
```

---

## Интеграция с существующим Orchestrator

### Обратная совместимость

```go
// internal/agent/orchestrator.go

type Orchestrator struct {
    // ... существующие поля ...

    // chain — внутренняя цепочка (опционально)
    chain *chain.ReActChain
}

// New создает Orchestrator с_chain или без него.
func New(cfg Config) (*Orchestrator, error) {
    // ... существующая валидация ...

    orch := &Orchestrator{
        // ... существующие поля ...
    }

    // Создаём internal chain для использования
    orch.chain = chain.NewReActChain(chain.ReActChainConfig{
        SystemPrompt:     cfg.SystemPrompt,
        ReasoningConfig:  cfg.ReasoningConfig,
        ChatConfig:       cfg.ChatConfig,
        ToolPostPrompts:  cfg.ToolPostPrompts,
        PromptsDir:       "", // берём из state
        MaxIterations:    cfg.MaxIters,
    })

    return orch, nil
}

// Run использует internal chain или fallback на старую логику.
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
    // Используем chain
    input := chain.ChainInput{
        UserQuery: userQuery,
        State:     o.state,
        LLM:       o.llm,
        Registry:  o.registry,
        Config: chain.ChainConfig{
            PromptsDir: o.state.Config.App.PromptsDir,
        },
    }

    output, err := o.chain.Execute(ctx, input)
    if err != nil {
        return "", err
    }

    return output.Result, nil
}
```

**Преимущества:**
- ✅ Внешний API не изменяется
- ✅ Существующий код работает без изменений
- ✅ Chain можно тестировать отдельно
- ✅ Постепенный рефакторинг

---

## Конфигурация (Rule 2)

```yaml
# config.yaml

# Chain Configuration (опционально)
chains:
  default:
    type: "react"
    max_iterations: 10
    steps:
      - name: "llm_invocation"
        config:
          temperature: 0.5
          max_tokens: 2000
      - name: "tool_execution"
        config:
          continue_on_error: false

  # Будущие chain типы
  sequential:
    type: "sequential"
    steps: [...]

  conditional:
    type: "conditional"
    branches:
      - condition: "has_tool_calls"
        target: "tool_execution"
      - condition: "final"
        target: "output"
```

---

## Тестирование (Rule 9)

```go
// cmd/chain-test/main.go

func main() {
    // 1. Создаём mock LLM
    mockLLM := &MockLLM{
        responses: []llm.Message{
            {Content: "Need to use tool", ToolCalls: []llm.ToolCall{...}},
            {Content: "Final answer"},
        },
    }

    // 2. Создаём mock registry
    registry := tools.NewRegistry()
    registry.Register(&MockTool{})

    // 3. Создаём chain
    reactChain := chain.NewReActChain(chain.ReActChainConfig{
        SystemPrompt: defaultSystemPrompt(),
        ReasoningConfig: llm.GenerateOptions{...},
        ChatConfig: llm.GenerateOptions{...},
        MaxIterations: 10,
    })

    // 4. Выполняем
    input := chain.ChainInput{
        UserQuery: "What's the weather?",
        LLM:       mockLLM,
        Registry:  registry,
    }

    output, err := reactChain.Execute(context.Background(), input)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Result: %s\n", output.Result)
    fmt.Printf("Iterations: %d\n", output.Iterations)
}
```

---

## Преимущества Chain Pattern

| До | После |
|----|-------|
| `Run()` — 300+ строк монолитического кода | `Run()` делегирует `chain.Execute()` |
| Логика branching смешана с бизнес-логикой | Каждый Step — отдельная сущность |
| Сложно тестировать отдельные части | Каждый Step тестируется отдельно |
| Сложно добавить новый тип execution | Создаёшь новый Step и добавляешь в Chain |
| Debug logging захардкожен | Debug через ChainDebugConfig |
| Нет YAML конфигурации цепочек | Полная YAML конфигурация |

---

## Структура пакетов

```
pkg/
├── chain/
│   ├── chain.go              # Chain interface, ChainInput/Output
│   ├── context.go            # ChainContext (thread-safe state)
│   ├── step.go               # Step interface, NextAction
│   ├── react.go              # ReActChain реализация
│   ├── config.go             # YAML конфигурация цепочек
│   ├── debug.go              # DebugRecorder интеграция
│   ├── llm_step.go           # LLMInvocationStep
│   ├── tool_step.go          # ToolExecutionStep
│   └── loader.go             # Загрузка chain из YAML

cmd/
└── chain-cli/                # CLI утилита БЕЗ TUI (Rule 9, Rule 11)
    └── main.go               # Тестирование chain через CLI
```

---

## CLI Утилита (Rule 9, Rule 11)

```go
// cmd/chain-cli/main.go

func main() {
    // 1. Загружаем конфиг
    cfg := loadConfig()

    // 2. Создаём chain из YAML
    chain, err := chain.LoadFromYAML("react_agent", cfg)
    if err != nil {
        log.Fatal(err)
    }

    // 3. Подключаем debug если включен
    if cfg.Chains["react_agent"].Debug.Enabled {
        recorder, _ := debug.NewRecorder(...)
        chain.AttachDebug(recorder)
    }

    // 4. Выполняем
    input := chain.ChainInput{
        UserQuery: os.Args[1], // Из командной строки
        LLM:       createLLM(cfg),
        Registry:  createRegistry(cfg),
    }

    output, err := chain.Execute(context.Background(), input)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println("Result:", output.Result)
    fmt.Println("Iterations:", output.Iterations)
    fmt.Println("Duration:", output.Duration)

    // 5. Debug лог сохранен автоматически
}
```

**Использование:**
```bash
# Запуск с запросом из командной строки
./chain-cli "найди все товары в категории Верхняя одежда"

# Debug лог будет в ./debug_logs/debug_YYYYMMDD_HHMMSS.json
```

---

## План реализации (Полный рефакторинг)

| Этап | Описание | Время |
|------|----------|-------|
| **1** | Создать `pkg/chain/` с базовыми типами | 1 день |
| **2** | Реализовать YAML загрузку (`config.go`, `loader.go`) | 1 день |
| **3** | Реализовать `LLMInvocationStep` с debug | 1 день |
| **4** | Реализовать `ToolExecutionStep` с debug | 1 день |
| **5** | Реализовать `ReActChain` с debug интеграцией | 1 день |
| **6** | Полный рефакторинг `Orchestrator` (без обратной совместимости) | 1 день |
| **7** | Создать `cmd/chain-cli/` для тестирования | 1 день |
| **8** | Тестирование и фиксы | 1 день |

**Итого: 7-8 дней**

---

## Новый Orchestrator (после рефакторинга)

```go
// internal/agent/orchestrator.go

// Orchestrator — тонкая обёртка над Chain.
// Вся логика перенесена в ReActChain.
type Orchestrator struct {
    chain  *chain.ReActChain
    config *config.AppConfig
}

// New создает Orchestrator с chain из YAML.
func New(cfg Config) (*Orchestrator, error) {
    // Валидация
    if cfg.LLM == nil {
        return nil, fmt.Errorf("LLM is required")
    }

    // Загружаем chain из YAML конфигурации
    reactChain, err := chain.LoadFromYAML("react_agent", cfg.Config)
    if err != nil {
        return nil, fmt.Errorf("failed to load chain: %w", err)
    }

    // Настраиваем chain
    reactChain.SetLLM(cfg.LLM)
    reactChain.SetRegistry(cfg.Registry)
    reactChain.SetState(cfg.State)

    // Подключаем debug если включен
    if cfg.Config.Chains["react_agent"].Debug.Enabled {
        debugCfg := cfg.Config.App.DebugLogs.GetDefaults()
        if debugCfg.Enabled {
            recorder, err := debug.NewRecorder(...)
            if err == nil {
                reactChain.AttachDebug(recorder)
            }
        }
    }

    return &Orchestrator{
        chain:  reactChain,
        config: cfg.Config,
    }, nil
}

// Run выполняет запрос через Chain.
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
    input := chain.ChainInput{
        UserQuery: userQuery,
        State:     o.config.State, // Извлекаем из Orchestrator
    }

    output, err := o.chain.Execute(ctx, input)
    if err != nil {
        return "", err
    }

    return output.Result, nil
}
```

**Результат:**
- ✅ Orchestrator = ~50 строк (было 300+)
- ✅ Вся логика в тестируемых Chain/Step
- ✅ YAML конфигурация цепочек
- ✅ Debug интегрирован

---

## Следующие шаги после Chain

После реализации Chain можно добавить:

1. **SequentialChain** — линейная цепочка без циклов (из YAML)
2. **ConditionalChain** — условное ветвление (Graph precursor)
3. **ParallelChain** — параллельное выполнение шагов
4. **CallbackChain** — AOP/hooks на каждом шаге

---

## YAML Schema для Chain

```yaml
# config.yaml - полная схема

chains:
  react_agent:
    type: "react"
    description: "ReAct агент"

    # Лимиты выполнения
    max_iterations: 10
    timeout: "120s"

    # Шаги цикла ReAct
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

    # Debug конфигурация
    debug:
      enabled: true
      save_logs: true
      logs_dir: "./debug_logs"
      include_tool_args: true
      include_tool_results: true
      max_result_size: 5000

    # Post-prompts
    post_prompts_dir: "./prompts"
```

---

## Структура файлов для реализации

```
pkg/chain/
├── chain.go              # Chain interface, ChainInput/Output
├── context.go            # ChainContext (thread-safe)
├── step.go               # Step interface, NextAction
├── react.go              # ReactChain реализация
├── config.go             # ChainConfig struct + YAML parsing
├── loader.go             # LoadFromYAML функция
├── debug.go              # DebugRecorder интеграция
├── llm_step.go           # LLMInvocationStep
└── tool_step.go          # ToolExecutionStep

cmd/chain-cli/
├── main.go               # CLI утилита
└── config.yaml           # Локальная конфигурация (Rule 11)

internal/agent/
└── orchestrator.go       # Полный рефакторинг (~50 строк)
```

---

## cmd/chain-cli/ — Детальный план CLI утилиты

### Назначение

CLI утилита для тестирования Chain Pattern **без TUI интерфейса**.
Простая командная строка для верификации работы цепочек.

### Спецификация CLI

#### Использование

```bash
# Базовое использование
./chain-cli "найди все товары в категории Верхняя одежда"

# С указанием chain типа
./chain-cli -chain sequential "какой сегодня день?"

# С указанием config файла
./chain-cli -config /path/to/config.yaml "тестовый запрос"

# С выводом debug информации
./chain-cli -debug "show me trace"

# С указанием модели
./chain-cli -model glm-4.6 "расскажи про Go"

# JSON вывод
./chain-cli -json "query" | jq .

# Версия и помощь
./chain-cli -version
./chain-cli -help
```

#### Флаги

| Флаг | Описание | Дефолт |
|------|----------|--------|
| `-chain` | Тип цепочки (`react`, `sequential`) | `react` |
| `-config` | Путь к config.yaml | `./config.yaml` (по Rule 11) |
| `-model` | Переопределить модель | из config |
| `-debug` | Включить debug логирование | из config |
| `-no-color` | Отключить цвета в выводе | false |
| `-json` | Вывод в JSON формате | false |
| `-version` | Показать версию | - |
| `-help`, `-h` | Показать справку | - |

---

### Структура файлов

```
cmd/chain-cli/
├── main.go                 # Парсинг флагов, точка входа
├── config.go               # Загрузка конфигурации (Rule 11)
├── runner.go               # Выполнение Chain
├── output.go               # Форматирование вывода (human/JSON)
└── testdata/               # Тестовые конфиги
    ├── minimal-config.yaml  # Минимальная конфигурация
    └── mock-config.yaml     # Конфигурация с mock LLM
```

---

### Human-readable формат вывода

```
=== Chain Execution ===

Query: найди все товары в категории Верхняя одежда

Iteration 1:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Called: get_wb_parent_categories
  → Duration: 50ms

Iteration 2:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Called: get_wb_subjects
  → Duration: 40ms

Iteration 3:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Final answer
  → Duration: 60ms

=== Result ===
В категории Верхняя одежда найдено 1240 товаров в 12 подкатегориях.

=== Summary ===
Iterations: 3
Duration: 205ms
Debug log: ./debug_logs/debug_20250101_143022.json
```

---

### JSON формат вывода

```json
{
  "query": "найди все товары в категории Верхняя одежда",
  "result": "В категории Верхняя одежда найдено 1240 товаров...",
  "iterations": 3,
  "duration_ms": 205,
  "debug_log": "./debug_logs/debug_20250101_143022.json",
  "success": true
}
```

---

### main.go — код парсинга аргументов

```go
// cmd/chain-cli/main.go

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/debug"
)

// Version — версия утилиты (заполняется при сборке)
var Version = "dev"

func main() {
	// 1. Парсим флаги
	chainType := flag.String("chain", "react", "Type of chain (react, sequential)")
	configPath := flag.String("config", findConfigPath(), "Path to config.yaml")
	modelName := flag.String("model", "", "Override model name")
	debugFlag := flag.Bool("debug", false, "Enable debug logging")
	noColor := flag.Bool("no-color", false, "Disable colors in output")
	jsonOutput := flag.Bool("json", false, "Output in JSON format")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	// 2. Обработка специальных флагов
	if *showVersion {
		fmt.Printf("chain-cli version %s\n", Version)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: query argument is required")
		fmt.Fprintln(os.Stderr, "Usage: chain-cli [flags] \"query\"")
		fmt.Fprintln(os.Stderr, "Run 'chain-cli -help' for more information")
		os.Exit(1)
	}

	userQuery := flag.Arg(0)

	// 3. Загружаем конфигурацию
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 4. Создаём компоненты
	llmProvider := createLLMProvider(cfg, *modelName)
	registry := createToolsRegistry(cfg)
	state := createGlobalState(cfg)

	// 5. Загружаем chain из YAML
	chainConfig := cfg.Chains[*chainType]
	reactChain := chain.NewReActChain(chain.ReActChainConfig{
		SystemPrompt:     getSystemPrompt(cfg),
		ReasoningConfig:  getReasoningConfig(cfg),
		ChatConfig:       getChatConfig(cfg),
		ToolPostPrompts:  getToolPostPrompts(cfg),
		PromptsDir:       cfg.App.PromptsDir,
		MaxIterations:    chainConfig.MaxIterations,
	})

	// Настраиваем chain
	reactChain.SetLLM(llmProvider)
	reactChain.SetRegistry(registry)
	reactChain.SetState(state)

	// 6. Подключаем debug если включен
	if *debugFlag || chainConfig.Debug.Enabled {
		recorder, err := createDebugRecorder(chainConfig.Debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create debug recorder: %v\n", err)
		} else {
			reactChain.AttachDebug(recorder)
		}
	}

	// 7. Выполняем chain
	input := chain.ChainInput{
		UserQuery: userQuery,
		State:     state,
		LLM:       llmProvider,
		Registry:  registry,
		Config:    chainConfig,
	}

	ctx, cancel := context.WithTimeout(context.Background(), chainConfig.Timeout)
	defer cancel()

	output, err := reactChain.Execute(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 8. Выводим результат
	if *jsonOutput {
		printJSON(output, *noColor)
	} else {
		printHuman(output, *noColor)
	}

	// 9. Debug лог уже сохранён
	if output.DebugPath != "" {
		fmt.Fprintf(os.Stderr, "Debug log: %s\n", output.DebugPath)
	}
}

// findConfigPath находит config.yaml (Rule 11)
func findConfigPath() string {
	// 1. Рядом с бинарником
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		configPath := filepath.Join(exeDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}

	// 2. Текущая директория
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// 3. Fallback на проект
	return "/Users/ilkoid/dev/go/poncho-ai/config.yaml"
}
```

---

### output.go — форматирование вывода

```go
// cmd/chain-cli/output.go

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

// printHuman выводит результат в human-readable формате.
func printHuman(output chain.ChainOutput, noColor bool) {
	if noColor {
		color.NoColor = true
	}

	fmt.Println(color.YellowString("=== Chain Execution ==="))
	fmt.Println()
	fmt.Printf("Query: %s\n", output.State.UserQuery)
	fmt.Println()

	for i := 1; i <= output.Iterations; i++ {
		fmt.Println(color.CyanString(fmt.Sprintf("Iteration %d:", i)))
		// TODO: Подробности каждой итерации
		fmt.Println()
	}

	fmt.Println(color.GreenString("=== Result ==="))
	fmt.Println(output.Result)
	fmt.Println()

	fmt.Println(color.YellowString("=== Summary ==="))
	fmt.Printf("Iterations: %d\n", output.Iterations)
	fmt.Printf("Duration: %dms\n", output.Duration.Milliseconds())
	if output.DebugPath != "" {
		fmt.Printf("Debug log: %s\n", output.DebugPath)
	}
}

// printJSON выводит результат в JSON формате.
func printJSON(output chain.ChainOutput, noColor bool) {
	result := map[string]interface{}{
		"query":       output.State.UserQuery,
		"result":      output.Result,
		"iterations":  output.Iterations,
		"duration_ms": output.Duration.Milliseconds(),
		"success":     true,
	}

	if output.DebugPath != "" {
		result["debug_log"] = output.DebugPath
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.Encode(result)
}
```

---

### runner.go — выполнение Chain

```go
// cmd/chain-cli/runner.go

package main

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/debug"
)

// Runner выполняет Chain и возвращает результат.
type Runner struct {
	chain      chain.Chain
	debug      *debug.Recorder
	startTime  time.Time
}

// NewRunner создает новый Runner.
func NewRunner(c chain.Chain, debug *debug.Recorder) *Runner {
	return &Runner{
		chain: c,
		debug: debug,
	}
}

// Run выполняет цепочку с замером времени.
func (r *Runner) Run(ctx context.Context, input chain.ChainInput) (chain.ChainOutput, error) {
	r.startTime = time.Now()

	// Start debug если включен
	if r.debug != nil {
		r.debug.Start(input.UserQuery)
	}

	// Execute
	output, err := r.chain.Execute(ctx, input)
	if err != nil {
		// Finalize debug с ошибкой
		if r.debug != nil {
			r.debug.Finalize("", time.Since(r.startTime))
		}
		return chain.ChainOutput{}, err
	}

	// Finalize debug успешно
	if r.debug != nil {
		debugPath, _ := r.debug.Finalize(output.Result, time.Since(r.startTime))
		output.DebugPath = debugPath
	}

	return output, nil
}
```

---

### TestData для тестирования

```yaml
# cmd/chain-cli/testdata/minimal-config.yaml

models:
  default_reasoning: "glm-4.6"
  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5

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
    debug:
      enabled: true
      save_logs: true
      logs_dir: "./debug_logs"

tools:
  plan_add_task:
    enabled: true
  plan_mark_done:
    enabled: true
```

---

### Зависимости CLI утилиты от Chain

| Компонент | Статус | Где реализуется |
|-----------|--------|-----------------|
| `chain.Chain` interface | ⏳ TBD | `pkg/chain/chain.go` |
| `chain.NewReActChain()` | ⏳ TBD | `pkg/chain/react.go` |
| `chain.ChainInput/Output` | ⏳ TBD | `pkg/chain/chain.go` |
| `debug.Recorder` | ✅ Done | `pkg/debug/recorder.go` |
| `config.Load()` | ✅ Done | `pkg/config/config.go` |
| `llm.Provider` | ✅ Done | `pkg/llm/` |
| `tools.Registry` | ✅ Done | `pkg/tools/registry.go` |

---

## План реализации (Полный)

### Этап 1: Базовые типы Chain (1 день)

**Файлы:**
- `pkg/chain/chain.go` — Chain interface, ChainInput/Output, ChainConfig
- `pkg/chain/context.go` — ChainContext (thread-safe state)
- `pkg/chain/step.go` — Step interface, NextAction

**Проверка:** `go build ./pkg/chain/...`

---

### Этап 2: YAML конфигурация (1 день)

**Файлы:**
- `pkg/chain/config.go` — ChainConfig struct + YAML теги
- `pkg/chain/loader.go` — LoadFromYAML функция

**Проверка:** `go test ./pkg/chain/... -v`

---

### Этап 3: Debug интеграция (1 день)

**Файлы:**
- `pkg/chain/debug.go` — DebugRecorder integration в Chain

**Проверка:** `go test ./pkg/chain/... -run TestDebug`

---

### Этап 4: LLMInvocationStep (1 день)

**Файлы:**
- `pkg/chain/llm_step.go` — LLMInvocationStep с debug recording

**Проверка:** `go test ./pkg/chain/... -run TestLLMStep`

---

### Этап 5: ToolExecutionStep (1 день)

**Файлы:**
- `pkg/chain/tool_step.go` — ToolExecutionStep с debug recording

**Проверка:** `go test ./pkg/chain/... -run TestToolStep`

---

### Этап 6: ReActChain (1 день)

**Файлы:**
- `pkg/chain/react.go` — ReActChain реализация

**Проверка:** `go test ./pkg/chain/... -run TestReActChain`

---

### Этап 7: Полный рефакторинг Orchestrator (1 день)

**Файлы:**
- `internal/agent/orchestrator.go` — Рефакторинг до ~50 строк

**Проверка:** `go build ./cmd/poncho/...`

---

### Этап 8: CLI утилита (1 день)

**Файлы:**
- `cmd/chain-cli/main.go` — Точка входа
- `cmd/chain-cli/config.go` — Поиск config.yaml (Rule 11)
- `cmd/chain-cli/runner.go` — Выполнение Chain
- `cmd/chain-cli/output.go` — Форматирование вывода
- `cmd/chain-cli/testdata/minimal-config.yaml` — Тестовая конфигурация

**Проверка:**
```bash
go build -o chain-cli cmd/chain-cli/*.go
./chain-cli -version
./chain-cli -help
./chain-cli "тест"
```

---

### Этап 9: Интеграция и тестирование (1 день)

**Проверки:**
- [ ] Компиляция всех пакетов
- [ ] CLI утилита работает с реальным LLM
- [ ] Debug логи сохраняются
- [ ] JSON вывод корректен
- [ ] TUI (poncho) работает с новым Orchestrator

---

## Итоговая структура

```
pkg/
├── chain/
│   ├── chain.go              # Chain interface, ChainInput/Output
│   ├── context.go            # ChainContext (thread-safe)
│   ├── step.go               # Step interface, NextAction
│   ├── config.go             # ChainConfig + YAML parsing
│   ├── loader.go             # LoadFromYAML
│   ├── debug.go              # DebugRecorder integration
│   ├── react.go              # ReActChain
│   ├── llm_step.go           # LLMInvocationStep
│   └── tool_step.go          # ToolExecutionStep

cmd/
├── chain-cli/
│   ├── main.go               # CLI entry point
│   ├── config.go             # Config loader (Rule 11)
│   ├── runner.go             # Chain execution
│   ├── output.go             # Output formatting
│   └── testdata/
│       └── minimal-config.yaml

internal/agent/
└── orchestrator.go           # Refactored (~50 lines)
```

---

## Решение

**План реализации:**

1. ✅ Базовый Chain interface
2. ✅ YAML конфигурация Chain (`config.go`, `loader.go`)
3. ✅ DebugRecorder интеграция (`debug.go`)
4. ✅ LLMInvocationStep
5. ✅ ToolExecutionStep
6. ✅ ReActChain
7. ✅ Полный рефакторинг Orchestrator
8. ✅ CLI утилита `cmd/chain-cli/`

**Всего: 7-9 дней**

Начать реализацию?
