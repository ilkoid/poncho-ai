# Техническое задание: Унифицированное In-Memory State Management

**Дата:** 2026-01-04
**Статус:** Черновик
**Автор:** AI Assistant
**Версия:** 1.0

---

## 1. Цели и задачи

### 1.1. Основная цель

Создать **унифицированное in-memory хранилище состояния** для AI-агента, которое:

- ✅ Не имеет жёстких зависимостей (S3, WB, etc.)
- ✅ Предоставляет единообразный CRUD интерфейс для всех типов данных
- ✅ Поддерживает domain-specific операции для каждого типа данных
- ✅ Является thread-safe
- ✅ Сохраняет полный контекст общения с LLM и tools между вызовами

### 1.2. Ключевые требования

1. **Отсутствие обратной совместимости** — API разрабатывается с нуля
2. **Единый источник правды** — история диалога хранится в одном месте
3. **Интерфейсная архитектура** — работа через абстракции, а не конкретные типы
4. **Dependency Injection** — все зависимости передаются извне
5. **Тестируемость** — все интерфейсы можно мокать

---

## 2. Архитектура

### 2.1. Диаграмма пакетов

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         НОВАЯ АРХИТЕКТУРА STATE                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  pkg/state/                                                                  │
│  │                                                                          │
│  ├─ repository.go           # Интерфейсы репозиториев                       │
│  │   ├─ UnifiedStore        # Базовый CRUD                                 │
│  │   ├─ MessageRepository   # История сообщений                            │
│  │   ├─ FileRepository      # Файлы (Working Memory)                       │
│  │   ├─ TodoRepository      # Задачи планировщика                          │
│  │   ├─ DictionaryRepository # Справочники (WB, Ozon)                       │
│  │   └─ StorageRepository   # S3/файловое хранилище (опционально)          │
│  │                                                                          │
│  ├─ core.go                 # Реализация CoreState                          │
│  │   └─ CoreState implements all repositories                              │
│  │                                                                          │
│  ├─ keys.go                 # Константы ключей                             │
│  └─ errors.go               # Ошибки пакета                                 │
│                                                                              │
│  pkg/chain/                                                                  │
│  ├─ context.go              # ChainContext использует CoreState              │
│  │   └─ ❌ УБРАТЬ дублирующую messages                                    │
│  └─ react.go                # ReActChain без собственной истории            │
│                                                                              │
│  internal/app/                                                               │
│  └─ state.go                # AppState embeds CoreState                      │
│                                                                              │
│  cmd/*/                                                                          │
│  └─ Переписать для использования новых интерфейсов                          │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2. Иерархия интерфейсов

```go
// UnifiedStore — базовый CRUD для всех типов
type UnifiedStore interface {
    Get(key string) (any, bool)
    Set(key string, value any) error
    Update(key string, fn func(any) any) error
    Delete(key string) error
    Exists(key string) bool
    List() []string
    Clear() error
}

// MessageRepository — спецификация для истории сообщений
type MessageRepository interface {
    UnifiedStore

    Append(msg llm.Message) error
    GetHistory() []llm.Message
    GetRange(from, to int) []llm.Message
    GetLast(n int) []llm.Message
    Trim(n int) error
    ClearHistory() error
}

// FileRepository — спецификация для файлов
type FileRepository interface {
    UnifiedStore

    Set(tag string, files []*s3storage.FileMeta) error
    Get(tag string) ([]*s3storage.FileMeta, bool)
    GetAll() map[string][]*s3storage.FileMeta
    UpdateAnalysis(tag, filename, description string) error
    Tags() []string
    Clear() error
}

// TodoRepository — спецификация для задач
type TodoRepository interface {
    UnifiedStore

    Add(description string, meta ...map[string]any) (int, error)
    Complete(id int) error
    Fail(id int, reason string) error
    Get(id int) (todo.Task, bool)
    GetAll() []todo.Task
    Stats() (pending, done, failed int)
    Clear() error
}

// DictionaryRepository — спецификация для справочников
type DictionaryRepository interface {
    UnifiedStore

    Set(dicts *wb.Dictionaries) error
    Get() (*wb.Dictionaries, error)
    Has() bool
}
```

### 2.3. Поток данных (фикс проблемы дублирования)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         ПОТОК ДАННЫХ (НОВЫЙ)                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  User Query                                                                  │
│     │                                                                        │
│     ▼                                                                        │
│  Orchestrator.Run(query)                                                     │
│     │                                                                        │
│     ├─> CoreState.Append(user message)  ✅  Единственное хранилище          │
│     │                                                                        │
│     ▼                                                                        │
│  ReActChain.Execute(input)                                                   │
│     │                                                                        │
│     ├─> ChainContext { state: CoreState }  ✅  Ссылка на CoreState          │
│     │                                                                        │
│     ├─> LLMInvocationStep                                                   │
│     │   └─> BuildContextMessages()                                          │
│     │       └─> CoreState.BuildAgentContext()  ✅  Использует History       │
│     │                                                                        │
│     ├─> ToolExecutionStep                                                   │
│     │   └─> CoreState.Append(tool result)  ✅  Сюда же                      │
│     │                                                                        │
│     └─> Return output  ✅  История уже в CoreState                          │
│                                                                              │
│  ─────────────────────────────────────────────────────────────────────────   │
│                                                                              │
│  Следующий вызов                                                             │
│     │                                                                        │
│     └─> CoreState.History содержит полный контекст  ✅                       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Техническое задание по файлам

### Фаза 1: Новые интерфейсы (pkg/state/repository.go)

**Задача:** Создать файл с определениями всех интерфейсов.

**Содержание:**

```go
// Package state/repository предоставляет интерфейсы для работы с состоянием
package state

import (
    "github.com/ilkoid/poncho-ai/pkg/llm"
    "github.com/ilkoid/poncho-ai/pkg/s3storage"
    "github.com/ilkoid/poncho-ai/pkg/todo"
    "github.com/ilkoid/poncho-ai/pkg/wb"
)

// ========== BASE INTERFACE ==========

// UnifiedStore представляет базовый CRUD интерфейс для хранения данных.
//
// Все репозитории наследуют этот интерфейс, обеспечивая минимальный
// набор операций: создание, чтение, обновление, удаление.
type UnifiedStore interface {
    // Get возвращает значение по ключу.
    // Возвращает (value, true) если ключ существует, (nil, false) иначе.
    Get(key string) (any, bool)

    // Set сохраняет значение по ключу (атомарная операция).
    Set(key string, value any) error

    // Update атомарно обновляет значение по ключу с помощью функции.
    // Функция fn принимает текущее значение и возвращает новое.
    Update(key string, fn func(any) any) error

    // Delete удаляет значение по ключу.
    Delete(key string) error

    // Exists проверяет наличие ключа.
    Exists(key string) bool

    // List возвращает список всех ключей.
    List() []string

    // Clear удаляет все данные из хранилища.
    Clear() error
}

// ========== DOMAIN-SPECIFIC INTERFACES ==========

// MessageRepository определяет интерфейс для работы с историей сообщений.
//
// Используется для хранения диалога между пользователем и AI-агентом.
// Поддерживает операции append, range, trim специфичные для логов сообщений.
type MessageRepository interface {
    UnifiedStore

    // Append добавляет сообщение в конец истории.
    Append(msg llm.Message) error

    // GetHistory возвращает полную историю сообщений.
    // Возвращает копию слайса для thread-safety.
    GetHistory() []llm.Message

    // GetRange возвращает сообщения в диапазоне [from, to).
    // from включительно, to исключительно.
    GetRange(from, to int) []llm.Message

    // GetLast возвращает последние N сообщений.
    // Если N > длины истории — возвращается вся история.
    GetLast(n int) []llm.Message

    // Trim оставляет только последние N сообщений, удаляя старые.
    // Используется для ограничения размера истории.
    Trim(n int) error

    // ClearHistory очищает всю историю.
    ClearHistory() error
}

// FileRepository определяет интерфейс для работы с файлами ("рабочая память").
//
// Файлы организованы по тегам (sketch, plm_data, marketing и т.д.).
// Каждый файл может содержать результат vision-анализа в VisionDescription.
type FileRepository interface {
    UnifiedStore

    // Set сохраняет файлы под указанным тегом.
    // Если тег уже существует — заменяет файлы полностью.
    Set(tag string, files []*s3storage.FileMeta) error

    // Get возвращает файлы по тегу.
    // Возвращает (files, true) если тег существует, (nil, false) иначе.
    Get(tag string) ([]*s3storage.FileMeta, bool)

    // GetAll возвращает все файлы по всем тегам.
    GetAll() map[string][]*s3storage.FileMeta

    // UpdateAnalysis обновляет результат vision-анализа для файла.
    // Ищет файл по (tag, filename) и обновляет VisionDescription.
    UpdateAnalysis(tag, filename, description string) error

    // Tags возвращает список всех тегов.
    Tags() []string

    // Clear удаляет все файлы.
    Clear() error
}

// TodoRepository определяет интерфейс для работы с задачами планировщика.
//
// Используется для многошаговых задач в ReAct цикле.
type TodoRepository interface {
    UnifiedStore

    // Add добавляет новую задачу.
    // Возвращает ID созданной задачи.
    Add(description string, metadata ...map[string]any) (int, error)

    // Complete отмечает задачу выполненной.
    Complete(id int) error

    // Fail отмечает задачу проваленной с указанием причины.
    Fail(id int, reason string) error

    // Get возвращает задачу по ID.
    Get(id int) (todo.Task, bool)

    // GetAll возвращает все задачи.
    GetAll() []todo.Task

    // Stats возвращает статистику: (pending, done, failed).
    Stats() (pending, done, failed int)

    // Clear удаляет все задачи.
    Clear() error
}

// DictionaryRepository определяет интерфейс для работы со справочниками.
//
// Справочники содержат данные маркетплейсов (WB, Ozon): цвета, страны, полы и т.д.
type DictionaryRepository interface {
    UnifiedStore

    // Set сохраняет справочники.
    Set(dicts *wb.Dictionaries) error

    // Get возвращает справочники.
    Get() (*wb.Dictionaries, error)

    // Has проверяет что справочники загружены.
    Has() bool
}

// StorageRepository определяет интерфейс для работы с S3/файловым хранилищем.
//
// Это опциональная зависимость — может быть nil если приложение не работает
// с файловыми хранилищами.
type StorageRepository interface {
    UnifiedStore

    // SetClient устанавливает S3 клиент.
    SetClient(client *s3storage.Client) error

    // GetClient возвращает S3 клиент.
    // Возвращает ошибку если клиент не настроен.
    GetClient() (*s3storage.Client, error)

    // Has проверяет что клиент настроен.
    Has() bool
}
```

**Критерии приёмки:**
- [ ] Все интерфейсы определены
- [ ] Godoc комментарии на всех public методах
- [ ] Нет зависимостей от реализации (только pkg/ типы)

---

### Фаза 2: Реализация CoreState (pkg/state/core.go)

**Задача:** Переписать CoreState с реализацией всех интерфейсов.

**Требования:**

1. **Конструктор без зависимостей:**
```go
func NewCoreState(cfg *config.AppConfig) *CoreState
```

2. **Внутренняя структура:**
```go
type CoreState struct {
    mu     sync.RWMutex
    store  map[string]any  // Унифицированное хранилище
    Config *config.AppConfig  // Конфигурация (неизменяемая)
}

// Compile-time проверки
var (
    _ MessageRepository    = (*CoreState)(nil)
    _ FileRepository       = (*CoreState)(nil)
    _ TodoRepository       = (*CoreState)(nil)
    _ DictionaryRepository = (*CoreState)(nil)
    _ StorageRepository    = (*CoreState)(nil)
    _ UnifiedStore         = (*CoreState)(nil)
)
```

3. **Thread-safety:** Все операции защищены `sync.RWMutex`

4. **Возврат копий:** Get-методы возвращают копии данных

**Псевдокод ключевых методов:**

```go
// UnifiedStore implementation
func (s *CoreState) Get(key string) (any, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    val, ok := s.store[key]
    return val, ok
}

func (s *CoreState) Set(key string, value any) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.store[key] = value
    return nil
}

// MessageRepository implementation
func (s *CoreState) Append(msg llm.Message) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    history, _ := s.store["history"].([]llm.Message)
    s.store["history"] = append(history, msg)
    return nil
}

func (s *CoreState) GetHistory() []llm.Message {
    s.mu.RLock()
    defer s.mu.RUnlock()
    history, _ := s.store["history"].([]llm.Message)
    result := make([]llm.Message, len(history))
    copy(result, history)
    return result
}

// FileRepository implementation
func (s *CoreState) Set(tag string, files []*s3storage.FileMeta) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    filesMap, _ := s.store["files"].(map[string][]*s3storage.FileMeta)
    if filesMap == nil {
        filesMap = make(map[string][]*s3storage.FileMeta)
    }
    filesCopy := make([]*s3storage.FileMeta, len(files))
    copy(filesCopy, files)
    filesMap[tag] = filesCopy
    s.store["files"] = filesMap
    return nil
}

// TodoRepository implementation
func (s *CoreState) Add(description string, metadata ...map[string]any) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    todoMgr, _ := s.store["todo"].(*todo.Manager)
    if todoMgr == nil {
        todoMgr = todo.NewManager()
        s.store["todo"] = todoMgr
    }
    return todoMgr.Add(description, metadata...), nil
}

// StorageRepository implementation
func (s *CoreState) SetClient(client *s3storage.Client) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.store["s3_client"] = client
    return nil
}

func (s *CoreState) GetClient() (*s3storage.Client, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    val, ok := s.store["s3_client"]
    if !ok || val == nil {
        return nil, errors.New("S3 client not configured")
    }
    return val.(*s3storage.Client), nil
}
```

**BuildAgentContext — сбор контекста для LLM:**

```go
// BuildAgentContext собирает полный контекст для генеративного запроса.
//
// Включает:
// 1. Системный промпт
// 2. "Рабочую память" (результаты анализа файлов)
// 3. Контекст плана (Todo Manager)
// 4. Историю диалога
func (s *CoreState) BuildAgentContext(systemPrompt string) []llm.Message {
    s.mu.RLock()
    defer s.mu.RUnlock()

    messages := make([]llm.Message, 0, 10)

    // 1. Системный промпт
    messages = append(messages, llm.Message{
        Role:    llm.RoleSystem,
        Content: systemPrompt,
    })

    // 2. Файловый контекст
    if filesMap, ok := s.store["files"].(map[string][]*s3storage.FileMeta); ok {
        var fileContext strings.Builder
        for tag, files := range filesMap {
            for _, f := range files {
                if f.VisionDescription != "" {
                    fileContext.WriteString(fmt.Sprintf("- [%s] %s: %s\n", tag, f.Filename, f.VisionDescription))
                }
            }
        }
        if fileContext.Len() > 0 {
            messages = append(messages, llm.Message{
                Role:    llm.RoleSystem,
                Content: "\nКОНТЕКСТ АРТИКУЛА:\n" + fileContext.String(),
            })
        }
    }

    // 3. Todo контекст
    if todoMgr, ok := s.store["todo"].(*todo.Manager); ok {
        messages = append(messages, llm.Message{
            Role:    llm.RoleSystem,
            Content: todoMgr.String(),
        })
    }

    // 4. История диалога
    if history, ok := s.store["history"].([]llm.Message); ok {
        messages = append(messages, history...)
    }

    return messages
}
```

**Критерии приёмки:**
- [ ] CoreState реализует все 6 интерфейсов
- [ ] Конструктор не требует S3 клиента
- [ ] Все операции thread-safe
- [ ] Get-методы возвращают копии
- [ ] BuildAgentContext работает
- [ ] Компиляция проходит без ошибок

---

### Фаза 3: Константы ключей (pkg/state/keys.go)

**Задача:** Определить константы для ключей хранилища.

```go
package state

const (
    // Core keys
    HistoryKey       = "history"
    FilesKey         = "files"
    TodoKey          = "todo"
    DictionariesKey  = "dictionaries"
    S3ClientKey      = "s3_client"
    ToolsRegistryKey = "tools_registry"

    // Optional keys (для расширения)
    SessionIDKey     = "session_id"
    UserProfileKey   = "user_profile"
    CacheKey         = "cache"
)
```

---

### Фаза 4: Ошибки пакета (pkg/state/errors.go)

**Задача:** Определить ошибки пакета.

```go
package state

import "errors"

var (
    // ErrKeyNotFound возвращается когда ключ не найден
    ErrKeyNotFound = errors.New("key not found")

    // ErrS3NotConfigured возвращается когда S3 клиент не настроен
    ErrS3NotConfigured = errors.New("S3 client not configured")

    // ErrDictionariesNotLoaded возвращается когда справочники не загружены
    ErrDictionariesNotLoaded = errors.New("dictionaries not loaded")

    // ErrTodoNotInitialized возвращается когда todo менеджер не инициализирован
    ErrTodoNotInitialized = errors.New("todo manager not initialized")
)
```

---

### Фаза 5: ChainContext без дублирования (pkg/chain/context.go)

**Задача:** Переписать ChainContext чтобы использовать CoreState.History.

**Было:**
```go
type ChainContext struct {
    mu     sync.RWMutex
    Input  *ChainInput
    messages []llm.Message  // ❌ Дублирует CoreState.History
    // ...
}
```

**Стало:**
```go
type ChainContext struct {
    mu     sync.RWMutex
    Input  *ChainInput
    state  *state.CoreState  // ✅ Ссылка на единственный источник правды

    currentIteration int
    activePostPrompt   string
    activePromptConfig *prompt.PromptConfig
    actualModel        string
    actualTemperature  float64
    actualMaxTokens    int
}

func NewChainContext(input ChainInput) (*ChainContext, error) {
    if input.State == nil {
        return nil, errors.New("ChainInput.State is required")
    }
    return &ChainContext{
        Input: &input,
        state: input.State,
    }, nil
}

// BuildContextMessages использует CoreState
func (c *ChainContext) BuildContextMessages(systemPrompt string) []llm.Message {
    return c.state.BuildAgentContext(systemPrompt)
}

// AppendMessage добавляет в CoreState
func (c *ChainContext) AppendMessage(msg llm.Message) {
    c.state.Append(msg)
}

// GetLastMessage использует CoreState
func (c *ChainContext) GetLastMessage() *llm.Message {
    history := c.state.GetHistory()
    if len(history) == 0 {
        return nil
    }
    return &history[len(history)-1]
}

// ❌ УДАЛИТЬ: GetMessages, SetMessages (не нужны)
```

**Критерии приёмки:**
- [ ] `messages` поле удалено
- [ ] `state *state.CoreState` добавлен
- [ ] BuildContextMessages использует state.BuildAgentContext()
- [ ] AppendMessage использует state.Append()
- [ ] GetMessages/SetMessages удалены

---

### Фаза 6: Обновление ReActChain (pkg/chain/react.go)

**Задача:** Убрать дублирующее добавление сообщения.

**Было (строки 146-152):**
```go
chainCtx := NewChainContext(input)
chainCtx.AppendMessage(llm.Message{
    Role:    llm.RoleUser,
    Content: input.UserQuery,
})
```

**Стало:**
```go
chainCtx, err := NewChainContext(input)
if err != nil {
    return ChainOutput{}, err
}
// ❌ УБРАТЬ AppendMessage — сообщение уже добавлено в Orchestrator.Run()
```

**Критерии приёмки:**
- [ ] Дублирующий AppendMessage удалён
- [ ] NewChainContext проверяет state на nil
- [ ] Ошибка обрабатывается

---

### Фаза 7: Обновление AppState (internal/app/state.go)

**Задача:** Убрать s3Client из конструктора.

**Было:**
```go
func NewAppState(cfg *config.AppConfig, s3Client *s3storage.Client) *AppState {
    coreState := state.NewCoreState(cfg, s3Client)
    // ...
}
```

**Стало:**
```go
func NewAppState(cfg *config.AppConfig) *AppState {
    coreState := state.NewCoreState(cfg)
    // ...
    return &AppState{
        CoreState:       coreState,
        CommandRegistry: NewCommandRegistry(),
        CurrentArticleID: "NONE",
        CurrentModel:     cfg.Models.DefaultVision,
        IsProcessing:     false,
    }
}

// Опциональный сеттер для S3
func (s *AppState) SetS3(client *s3storage.Client) {
    s.CoreState.SetClient(client)
}
```

**Критерии приёмки:**
- [ ] Конструктор принимает только cfg
- [ ] SetS3 метод добавлен
- [ ] Компиляция проходит

---

### Фаза 8: Обновление Components (pkg/app/components.go)

**Задача:** Сделать S3 опциональным.

**Было (строки 143-149):**
```go
s3Client, err := s3storage.New(cfg.S3)
if err != nil {
    return nil, fmt.Errorf("failed to create S3 client: %w", err)
}
utils.Info("S3 client initialized", "bucket", cfg.S3.Bucket)
```

**Стало:**
```go
// S3 — опциональная зависимость
var s3Client *s3storage.Client
if cfg.S3.Endpoint != "" && cfg.S3.Bucket != "" {
    s3Client, err = s3storage.New(cfg.S3)
    if err != nil {
        utils.Warn("S3 client creation failed, continuing without S3", "error", err)
        s3Client = nil
    } else {
        utils.Info("S3 client initialized", "bucket", cfg.S3.Bucket)
    }
} else {
    utils.Info("S3 not configured, running without storage")
}

// Создаём AppState без S3
state := app.NewAppState(cfg)
if s3Client != nil {
    state.SetS3(s3Client)
}
```

**Защита S3-инструментов:**

```go
// S3 Basic Tools
if toolCfg, exists := getToolCfg("list_s3_files"); exists && toolCfg.Enabled {
    s3Client, err := state.GetS3()
    if err != nil {
        utils.Warn("S3 tool 'list_s3_files' skipped - S3 not configured")
        continue
    }
    if err := register("list_s3_files", std.NewS3ListTool(s3Client)); err != nil {
        return err
    }
}
```

**Критерии приёмки:**
- [ ] S3 опциональный при создании
- [ ] Логирование информативное
- [ ] S3-инструменты пропускаются если S3 не настроен
- [ ] Компиляция проходит

---

### Фаза 9: Перепись утилит для тестирования

**Задача:** Переписать утилиты в `/cmd` для проверки нового API.

#### 9.1. `cmd/chain-cli/main.go`

```go
func main() {
    // 1. Загрузка конфига
    cfg, _ := config.Load("config.yaml")

    // 2. Создаём CoreState БЕЗ S3
    coreState := state.NewCoreState(cfg)

    // 3. Опционально S3
    if cfg.S3.Endpoint != "" {
        s3Client, _ := s3storage.New(cfg.S3)
        coreState.SetClient(s3Client)
    }

    // 4. Создаём Chain
    reactChain := chain.NewReActChain(chainConfig)
    reactChain.SetLLM(llmProvider)
    reactChain.SetRegistry(registry)
    reactChain.SetState(coreState)

    // 5. Выполняем
    input := chain.ChainInput{
        UserQuery: query,
        State:     coreState,
        LLM:       llmProvider,
        Registry:  registry,
    }

    output, _ := reactChain.Execute(ctx, input)

    // 6. Проверяем что история сохранилась
    history := coreState.GetHistory()
    fmt.Printf("History length: %d\n", len(history))
}
```

#### 9.2. `cmd/poncho/main.go`

```go
func main() {
    // ... инициализация ...

    // Создаём AppState БЕЗ S3
    components.State = app.NewAppState(cfg)

    // S3 опционально
    if s3Client != nil {
        components.State.SetS3(s3Client)
    }

    // ... остальное ...
}
```

#### 9.3. Новая утилита `cmd/state-test/main.go`

```go
// Утилита для тестирования нового State API

func main() {
    cfg, _ := config.Load("config.yaml")
    state := state.NewCoreState(cfg)

    // Тест 1: UnifiedStore
    fmt.Println("=== UnifiedStore Test ===")
    state.Set("test_key", "test_value")
    if val, ok := state.Get("test_key"); ok {
        fmt.Printf("✓ Get works: %v\n", val)
    }

    // Тест 2: MessageRepository
    fmt.Println("\n=== MessageRepository Test ===")
    state.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
    state.Append(llm.Message{Role: llm.RoleAssistant, Content: "hi there"})
    history := state.GetHistory()
    fmt.Printf("✓ History length: %d\n", len(history))

    last := state.GetLast(1)
    fmt.Printf("✓ Last message: %s\n", last[0].Content)

    // Тест 3: FileRepository
    fmt.Println("\n=== FileRepository Test ===")
    files := []*s3storage.FileMeta{
        {Filename: "test.jpg", VisionDescription: "test description"},
    }
    state.Set("sketch", files)

    state.UpdateAnalysis("sketch", "test.jpg", "updated description")
    retrieved, _ := state.Get("sketch")
    fmt.Printf("✓ File analysis updated: %s\n", retrieved[0].VisionDescription)

    // Тест 4: TodoRepository
    fmt.Println("\n=== TodoRepository Test ===")
    id, _ := state.Add("Task 1")
    state.Add("Task 2")
    state.Complete(id)

    pending, done, _ := state.Stats()
    fmt.Printf("✓ Stats: %d pending, %d done\n", pending, done)

    // Тест 5: BuildAgentContext
    fmt.Println("\n=== BuildAgentContext Test ===")
    messages := state.BuildAgentContext("You are helpful assistant")
    fmt.Printf("✓ Context messages: %d\n", len(messages))
}
```

**Критерии приёмки:**
- [ ] `chain-cli` работает без S3
- [ ] `poncho` работает с опциональным S3
- [ ] `state-test` создаётся и проходит все тесты
- [ ] История сохраняется между вызовами

---

## 4. Порядок реализации

### Порядок по зависимостям:

1. **Фаза 1** — `repository.go` (интерфейсы)
2. **Фаза 2** — `core.go` (реализация)
3. **Фаза 3** — `keys.go` (константы)
4. **Фаза 4** — `errors.go` (ошибки)
5. **Фаза 5** — `context.go` (chain context)
6. **Фаза 6** — `react.go` (chain)
7. **Фаза 7** — `AppState` (internal/app)
8. **Фаза 8** — `components.go` (pkg/app)
9. **Фаза 9** — Утилиты (/cmd)

### Временная оценка:

| Фаза | Оценка | Приоритет |
|------|--------|----------|
| 1-4 (interfaces) | 2-3 часа | P0 |
| 5-6 (chain) | 1-2 часа | P0 |
| 7-8 (integration) | 2-3 часа | P0 |
| 9 (testing) | 2-3 часа | P0 |
| **Итого** | **7-11 часов** | |

---

## 5. Критерии завершения

### Функциональные требования:

- [ ] CoreState создаётся без зависимостей
- [ ] Все интерфейсы реализованы
- [ ] История диалога сохраняется в одном месте
- [ ] ChainContext не дублирует историю
- [ ] S3 опциональный

### Нефункциональные требования:

- [ ] Все операции thread-safe
- [ ] Get-методы возвращают копии
- [ ] Compile-time проверки интерфейсов
- [ ] Godoc на всех public API
- [ ] Утилита для тестирования работает

### Тестирование:

```bash
# 1. Компиляция
go build ./...

# 2. Утилита тестирования
go run cmd/state-test/main.go
# Ожидаемый вывод:
# ✓ UnifiedStore Test
# ✓ Get works: test_value
# ✓ MessageRepository Test
# ✓ History length: 2
# ✓ Last message: hi there
# ✓ FileRepository Test
# ✓ File analysis updated: updated description
# ✓ TodoRepository Test
# ✓ Stats: 1 pending, 1 done
# ✓ BuildAgentContext Test
# ✓ Context messages: 4

# 3. Chain CLI без S3
go run cmd/chain-cli/main.go "hello"
# Ожидается: работает без S3, история сохраняется

# 4. TUI с опциональным S3
go run cmd/poncho/main.go
# Ожидается: работает если S3 не настроен
```

---

## 6. Риски и митигация

| Риск | Вероятность | Влияние | Митигация |
|------|------------|---------|-----------|
| Compile-time проверки не пройдут | Средняя | Высокое | Добавить в конец каждой фазы |
| Утилиты не работают после рефакторинга | Высокая | Среднее | Писать их параллельно с фазами |
| Thread-safety нарушена | Средняя | Высокое | Unit тесты на конкурентность |
| История теряется между вызовами | Средняя | Высокое | Интеграционные тесты |

---

## 7. Следующие шаги после реализации

1. **Performance тестирование** — измерить overhead от interface{} vs конкретные типы
2. **Persistence** — добавить опциональный слой персистентности (если нужно)
3. **Metrics** — добавить метрики использования state
4. **Оптимизация** — sync.Pool для частых аллокаций

---

**Статус:** Готов к реализации
**Следующее действие:** Фаза 1 — Создать `pkg/state/repository.go`
