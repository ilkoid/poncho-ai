# Техническое задание: Reasoning Model + Prompt-based Model Override

## Текущая ситуация

### Проблемы

1. **Один LLM провайдер для всего**: Orchestrator использует только `llmProvider` (Chat LLM)
2. **Параметры игнорируются**: В `pkg/llm/openai/client.go:88-91` при создании API запроса НЕ используются `Temperature`, `MaxTokens` из `ModelDef`
3. **Нет гибкости**: Нельзя переключаться между reasoning/chat моделями в зависимости от задачи

### Как работает сейчас

```
config.yaml:
  models:
    default_chat: "glm-4.6"
    default_vision: "glm-4.6v"
    definitions:
      glm-4.6: {temperature: 0.5, max_tokens: 2000}

pkg/app/components.go:
  llmProvider = factory.NewLLMProvider(defs["glm-4.6"])
  orchestrator.LLM = llmProvider  ← только один провайдер

pkg/llm/openai/client.go:
  req := openai.ChatCompletionRequest{
      Model: c.model,  ← только модель, параметры проигнорированы!
  }
```

## Требования

### 1. Reasoning модель для Orchestrator по умолчанию

```yaml
# config.yaml
models:
  default_reasoning: "glm-4.6-reasoning"
  default_chat: "glm-4.6-chat"
  definitions:
    glm-4.6-reasoning:
      model_name: "glm-4.6"
      temperature: 0.1
      thinking: "enabled"
    glm-4.6-chat:
      model_name: "glm-4.6"
      temperature: 0.7
```

### 2. Chat модель в промптах

```yaml
# prompts/sketch_description_prompt.yaml
config:
  model: "glm-4.6-chat"  # переопределяет модель
  temperature: 0.7       # переопределяет параметры
  max_tokens: 2000

messages:
  - role: system
    content: "Ты - эксперт по анализу эскизов..."
```

### 3. Приоритет параметров

```
Промпт (самый высокий)
   ↓ переопределяет
config.yaml (дефолтные)
   ↓ используется если
API defaults
```

### 4. Flow работы

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        НОВЫЙ FLOW                                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Инициализация:                                                              │
│  ├── reasoningLLM = NewLLMProvider("glm-4.6-reasoning")                     │
│  ├── chatLLM = NewLLMProvider("glm-4.6-chat")                               │
│  └── orchestrator.LLM = reasoningLLM  ← по умолчанию                        │
│                                                                              │
│  ReAct Loop:                                                                 │
│  ├── Iteration N:                                                           │
│  │   └── reasoningLLM.Generate()  ← планирование, tool selection            │
│  │                                                                          │
│  ├── Tool executed → post-prompt загружается:                               │
│  │   ├── prompts/xxx.yaml имеет config.model = "glm-4.6-chat"               │
│  │   └── config.temperature = 0.7                                           │
│  │                                                                          │
│  └── Iteration N+1:                                                         │
│      ├── chatLLM.WithOverrides(temp=0.7, maxTokens=2000)                    │
│      └── chatLLM.Generate()  ← формирует ответ пользователю                 │
│                                                                              │
│  Следующая итерация → обратно на reasoningLLM                               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Архитектура (соблюдая dev_manifest.md)

### Принципы

**Правило 4**: "Тупой клиент" - Client не должен хранить состояние модели.
**Правило 2**: Все настройки в YAML конфиге.
**Правило 11**: Промпты рядом с приложением.

### Структуры данных

```go
// pkg/config/config.go
type ModelsConfig struct {
    DefaultReasoning string              `yaml:"default_reasoning"`
    DefaultChat      string              `yaml:"default_chat"`
    DefaultVision    string              `yaml:"default_vision"`
    Definitions      map[string]ModelDef `yaml:"definitions"`
}

// pkg/llm/provider.go - ИНТЕРФЕЙС (единственная точка взаимодействия)
type Provider interface {
    // Generate вызывает LLM с опциональными runtime-параметрами
    Generate(ctx context.Context, messages []Message, opts ...GenerateOption) (Message, error)
}

// pkg/llm/options.go - НОВЫЙ ФАЙЛ
type GenerateOptions struct {
    Model       string
    Temperature float64
    MaxTokens   int
    Format      string // "json_object" или ""
}

type GenerateOption func(*GenerateOptions)

func WithModel(model string) GenerateOption
func WithTemperature(temp float64) GenerateOption
func WithMaxTokens(tokens int) GenerateOption
func WithFormat(format string) GenerateOption

// pkg/llm/openai/client.go - ТУПОЙ КЛИЕНТ
type Client struct {
    api         *openai.Client
    baseModel   string           // дефолтная модель из конфига
    baseTemp    float64          // дефолтная температура
    baseMaxTok  int              // дефолтные max_tokens
    // НЕ хранит состояние между вызовами!
}

// Generate применяет opts поверх дефолтных значений
func (c *Client) Generate(ctx context.Context, messages []Message, opts ...GenerateOption) (Message, error) {
    options := c.defaultOptions()  // из config.yaml
    for _, opt := range opts {
        opt(&options)               // переопределение из промпта
    }
    req := openai.ChatCompletionRequest{
        Model:       options.Model,
        Temperature: options.Temperature,
        MaxTokens:   options.MaxTokens,
        // ...
    }
}
```

### Flow работы (соблюдая "тупой клиент")

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        НОВЫЙ FLOW (dev_manifest compliant)                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Инициализация (pkg/app/components.go):                                     │
│  ├── Создаётся ОДИН Provider с дефолтными параметрами                      │
│  └── Orchestrator хранит только Provider                                    │
│                                                                              │
│  config.yaml определяет дефолты:                                            │
│    models:                                                                  │
│      default_reasoning: "glm-4.6"  {temperature: 0.1}                       │
│      default_chat: "glm-4.6"      {temperature: 0.7}                        │
│                                                                              │
│  ReAct Loop (internal/agent/orchestrator.go):                               │
│  ├── Iteration N (размышление):                                             │
│  │   └── llm.Generate(ctx, messages,                                       │
│  │         WithModel("glm-4.6"),                                           │
│  │         WithTemperature(0.1))  ← reasoning параметры                    │
│  │                                                                          │
│  ├── Tool executed → post-prompt загружается:                               │
│  │   └── prompts/xxx.yaml:                                                 │
│  │       config:                                                            │
│  │         model: "glm-4.6"                                                │
│  │         temperature: 0.7  # runtime override                            │
│  │                                                                          │
│  └── Iteration N+1 (ответ пользователю):                                    │
│      └── llm.Generate(ctx, messages,                                       │
│            WithModel("glm-4.6"),                                           │
│            WithTemperature(0.7))  ← промпт переопределил                    │
│                                                                              │
│  Ключевой момент: ОДИН Provider, параметры передаются при каждом вызове!     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Новые файлы и методы

```go
// pkg/llm/options.go - НОВЫЙ ФАЙЛ
package llm

type GenerateOptions struct {
    Model       string
    Temperature float64
    MaxTokens   int
    Format      string
}

type GenerateOption func(*GenerateOptions)

func WithModel(model string) GenerateOption {
    return func(o *GenerateOptions) { o.Model = model }
}

func WithTemperature(temp float64) GenerateOption {
    return func(o *GenerateOptions) { o.Temperature = temp }
}

func WithMaxTokens(tokens int) GenerateOption {
    return func(o *GenerateOptions) { o.MaxTokens = tokens }
}

func WithFormat(format string) GenerateOption {
    return func(o *GenerateOptions) { o.Format = format }
}

// pkg/llm/provider.go - ОБНОВИТЬ ИНТЕРФЕЙС
type Provider interface {
    Generate(ctx context.Context, messages []Message, opts ...GenerateOption) (Message, error)
}

// pkg/llm/openai/client.go - ОБНОВИТЬ
type Client struct {
    api              *openai.Client
    baseConfig       GenerateOptions  // дефолты из config.yaml
}

func (c *Client) Generate(ctx context.Context, messages []Message, opts ...GenerateOption) (Message, error) {
    // 1. Начинаем с дефолтов
    options := c.baseConfig

    // 2. Применяем runtime overrides
    for _, opt := range opts {
        opt(&options)
    }

    // 3. Формируем запрос
    req := openai.ChatCompletionRequest{
        Model:       options.Model,
        Temperature: options.Temperature,
        MaxTokens:   options.MaxTokens,
        Messages:    messages,
    }
    // ...
}

// internal/agent/orchestrator.go - ОБНОВИТЬ
type Orchestrator struct {
    llm                    llm.Provider  // ОДИН провайдер
    reasoningConfig        llm.GenerateOptions  // из config.yaml
    chatConfig             llm.GenerateOptions  // из config.yaml
    activePromptConfig     *prompt.PromptConfig  // из промпта
    // ...
}

func (o *Orchestrator) callLLM(messages []llm.Message, toolDefs []tools.ToolDefinition) (llm.Message, error) {
    // Определяем параметры для этого вызова
    var opts []llm.GenerateOption

    if o.activePromptConfig != nil {
        // Промпт переопределяет всё
        opts = []llm.GenerateOption{
            llm.WithModel(o.activePromptConfig.Model),
            llm.WithTemperature(o.activePromptConfig.Temperature),
            llm.WithMaxTokens(o.activePromptConfig.MaxTokens),
        }
    } else {
        // Дефолтные reasoning параметры
        opts = []llm.GenerateOption{
            llm.WithModel(o.reasoningConfig.Model),
            llm.WithTemperature(o.reasoningConfig.Temperature),
            llm.WithMaxTokens(o.reasoningConfig.MaxTokens),
        }
    }

    return o.llm.Generate(ctx, messages, opts...)
}

// pkg/prompt/postprompt.go - ОБНОВИТЬ
func (cfg *ToolPostPromptConfig) GetToolPromptFile(toolName string, promptsDir string) (*PromptFile, error) {
    // Загружает весь PromptFile с Config
}
```

## План реализации (dev_manifest compliant)

### Phase 1: Options Pattern для LLM

**Цель**: Добавить возможность передавать параметры при каждом вызове, не создавая новых клиентов.

1. **pkg/llm/options.go** - НОВЫЙ ФАЙЛ
   - [ ] Создать `GenerateOptions` struct
   - [ ] Создать `GenerateOption` type
   - [ ] Реализовать `WithModel`, `WithTemperature`, `WithMaxTokens`, `WithFormat`

2. **pkg/llm/provider.go**
   - [ ] Обновить интерфейс: `Generate(opts ...GenerateOption)`
   - [ ] Обратная совместимость: opts опциональны

3. **pkg/llm/openai/client.go**
   - [ ] Изменить `Client` для хранения `baseConfig GenerateOptions`
   - [ ] Обновить `NewClient` для заполнения `baseConfig` из `ModelDef`
   - [ ] Обновить `Generate()` для применения opts к baseConfig
   - [ ] Использовать итоговые значения в API запросе

### Phase 2: Config для reasoning/chat

4. **pkg/config/config.go**
   - [ ] Добавить `DefaultReasoning string` в `ModelsConfig`
   - [ ] Валидация: проверить что `default_reasoning` существует в `definitions`
   - [ ] Хелпер `GetReasoningModel() (ModelDef, bool)`

### Phase 3: Orchestrator с параметрами

5. **internal/agent/orchestrator.go**
   - [ ] Добавить поля:
     - `reasoningConfig llm.GenerateOptions` (из config.yaml)
     - `chatConfig llm.GenerateOptions` (из config.yaml)
     - `activePromptConfig *prompt.PromptConfig` (из промпта)
   - [ ] Изменить `Run()` для передачи opts в `Generate()`
   - [ ] Логика выбора opts: reasoning → prompt override → reasoning

6. **pkg/app/components.go**
   - [ ] Создать ОДИН провайдер (не два!)
   - [ ] Сформировать `reasoningConfig` и `chatConfig` из `cfg.Models.Definitions`
   - [ ] Передать в `agent.Config` вместе с провайдером

### Phase 4: Промпты с конфигом

7. **pkg/prompt/postprompt.go**
   - [ ] Добавить метод `GetToolPromptFile(toolName, promptsDir) (*PromptFile, error)`
   - [ ] Возвращает весь `PromptFile` включая `Config`

8. **internal/agent/orchestrator.go**
   - [ ] В `executeToolIteration` загружать `PromptFile` вместо текста
   - [ ] Сохранять `pf.Config` в `activePromptConfig`
   - [ ] Сбрасывать после следующей итерации

### Phase 5: Тестирование

9. **cmd/*/main.go**
   - [ ] Обновить все вызовы `Generate()` для совместимости (без opts)

10. **Интеграционные тесты**
    - [ ] Запустить vision-cli с новым config.yaml
    - [ ] Проверить переключение reasoning → chat
    - [ ] Проверить что параметры из промпта применяются

## Файлы для изменения

```
pkg/llm/options.go         (Phase 1 - НОВЫЙ)
pkg/llm/provider.go         (Phase 1)
pkg/llm/openai/client.go    (Phase 1)
pkg/config/config.go        (Phase 2)
internal/agent/orchestrator.go (Phase 3, 4, 5, 8)
pkg/app/components.go      (Phase 3, 6)
pkg/prompt/postprompt.go    (Phase 4, 7)
config.yaml                 (пример)
prompts/*.yaml              (примеры)
```

## Тест кейсы

### TC1: Базовый reasoning → chat переключение

```yaml
# config.yaml
models:
  default_reasoning: "glm-4.6-reasoning"
  default_chat: "glm-4.6-chat"
```

**Ожидание:**
- Первая итерация: используется `glm-4.6-reasoning`
- После tool: переключается на `glm-4.6-chat`
- Следующий запрос: обратно на `glm-4.6-reasoning`

### TC2: Параметры из промпта переопределяют config

```yaml
# config.yaml
glm-4.6-chat:
  temperature: 0.5
  max_tokens: 2000

# prompts/sketch.yaml
config:
  model: "glm-4.6-chat"
  temperature: 0.9  # переопределение
  max_tokens: 4000  # переопределение
```

**Ожидание:**
- API запрос использует `temperature: 0.9`, `max_tokens: 4000`

### TC3: Нет post-prompt → используется reasoning

```yaml
tools:
  some_tool:
    enabled: true
    # нет post_prompt
```

**Ожидание:**
- Все итерации используют `reasoningLLM`

### TC4: Vision анализ не затронут

**Ожидание:**
- `analyze_article_images_batch` всё ещё использует `visionLLM` напрямую
- Не участвует в переключении reasoning/chat

### TC5: Обратная совместимость

**Ожидание:**
- Если `default_reasoning` не указан → используется `default_chat`
- Старые конфиги продолжают работать

## Файлы для изменения

```
pkg/config/config.go                    (Phase 1)
pkg/llm/openai/client.go                (Phase 1)
pkg/llm/provider.go                     (Phase 1 - опционально)
pkg/app/components.go                   (Phase 2)
internal/agent/orchestrator.go          (Phase 2, 3, 5, 7)
pkg/prompt/postprompt.go                (Phase 3, 6)
config.yaml                             (пример конфига)
prompts/*/sketch_description_prompt.yaml (пример промпта)
```

## Open questions

1. **Обратная совместимость Provider.Generate()**: Старый код вызывает `Generate(ctx, messages, tools)` - нужно сделать `tools` совместимым с `opts`?
   - *Решение*: `tools` передаётся как variadic arg, проверяем тип: если `[]ToolDefinition` - tools, иначе - opts. Или использовать `tools ...any` как сейчас, opts в том же variadic.

2. **Format из промпта**: Как использовать `format: "json_object"`?
   - *Решение*: Передавать в API запрос как `ResponseFormat: {type: "json_object"}` если указано

3. **Ошибка если модель в промпте не найдена**: Что делать если `model` в промпте не существует в definitions?
   - *Решение*: Log warning, fall back на reasoning config

4. **Vision промпты**: Использовать ли эту логику для `analyze_article_images_batch`?
   - *Решение*: Нет, vision пока остаётся как есть (архитектурное исключение из Правила 4)

5. **Пустые параметры в промпте**: Если в промпте `temperature: 0` - это значит "не переопределять" или "использовать 0"?
   - *Решение*: Использовать sentinel values (0 для temp, -1 для maxTokens) = "не переопределять"

## Проверка соответствия dev_manifest.md

| Правило | Соответствие | Notes |
|---------|--------------|-------|
| 0. Переиспользование кода | ✅ | Используем существующий Provider, не дублируем |
| 1. Tool интерфейс | ✅ | Не трогаем, "Raw In, String Out" остаётся |
| 2. Конфигурация в YAML | ✅ | Все новые настройки в config.yaml |
| 3. Registry инструментов | ✅ | Не трогаем |
| 4. LLM абстракция | ✅ | ОДИН Provider, options pattern (не multiple clients!) |
| 5. GlobalState | ✅ | Не трогаем |
| 6. Структура пакетов | ✅ | pkg/ - библиотеки, internal/ - логика |
| 7. Обработка ошибок | ✅ | Возвращаем ошибки, не panic |
| 8. Расширение через tools | ✅ | Не трогаем |
| 9. Тестирование через cmd/ | ✅ | vision-cli для тестирования |
| 10. Godoc | ✅ | Добавить комментарии к новым функциям |
| 11. Локализация ресурсов | ✅ | Промпты в {app_dir}/prompts/ |

## Критерии приёма

- [ ] **Правило 4**: ОДИН Provider, не множественные клиенты
- [ ] **Правило 2**: Все настройки в config.yaml
- [ ] Options pattern работает: `Generate(opts...)` переопределяет дефолты
- [ ] Reasoning модель используется по умолчанию в orchestrator
- [ ] Post-prompt с `config.model` переключает параметры
- [ ] Параметры из промпта переопределяют config.yaml
- [ ] Обратная совместимость: старый код работает без opts
- [ ] Vision анализ не затронут
- [ ] Все тесты проходят (vision-cli работает)
- [ ] Godoc комментарии добавлены
- [ ] Документация обновлена
