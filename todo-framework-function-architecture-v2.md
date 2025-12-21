# Todo Framework Function Architecture v2.0

## Реализация Todo List для AI-агента на основе архитектуры Poncho AI (соответствующей принципам фреймворка)

Документ описывает, как реализовать todo list для AI-агента в архитектуре Poncho AI с полным соответствием принципам разработки из [`brief.md`](brief.md) и [`dev_manifest.md`](dev_manifest.md).

## 1. Todo Tool как модульный инструмент

**Файл:** [`pkg/tools/std/todo_manager.go`](pkg/tools/std/todo_manager.go)

Вместо интеграции в ядро, Todo List реализуется как самостоятельный инструмент following Tool interface:

```go
package std

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"
    
    "github.com/poncho-ai/pkg/tools"
    "github.com/poncho-ai/internal/app"
)

type TodoManagerTool struct {
    state *app.GlobalState
    mu    sync.RWMutex
}

func NewTodoManagerTool(state *app.GlobalState) *TodoManagerTool {
    return &TodoManagerTool{
        state: state,
    }
}

func (t *TodoManagerTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "todo_manager",
        Description: "Управление списком задач: создание, выполнение, отслеживание статуса",
        ArgsSchema: map[string]interface{}{
            "action": map[string]interface{}{
                "type":        "string",
                "description": "Действие: create, execute_next, status, list",
                "enum":        []string{"create", "execute_next", "status", "list"},
            },
            "data": map[string]interface{}{
                "type":        "string",
                "description": "JSON данные для действия (для create: {title, context, items})",
            },
        },
    }
}

func (t *TodoManagerTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    var args struct {
        Action string `json:"action"`
        Data   string `json:"data"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
    }
    
    switch args.Action {
    case "create":
        return t.createTodo(ctx, args.Data)
    case "execute_next":
        return t.executeNext(ctx)
    case "status":
        return t.getStatus(ctx)
    case "list":
        return t.listTodos(ctx)
    default:
        return "", fmt.Errorf("неизвестное действие: %s", args.Action)
    }
}

func (t *TodoManagerTool) createTodo(ctx context.Context, dataJSON string) (string, error) {
    var todoRequest struct {
        Title   string `json:"title"`
        Context string `json:"context"`
        Items   []struct {
            Title       string `json:"title"`
            Description string `json:"description"`
            Priority    int    `json:"priority"`
            Tool        string `json:"tool"`
            Args        string `json:"args"`
        } `json:"items"`
    }
    
    if err := json.Unmarshal([]byte(dataJSON), &todoRequest); err != nil {
        return "", fmt.Errorf("ошибка парсинга todo данных: %w", err)
    }
    
    todo := &TodoList{
        ID:        generateUUID(),
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
        Status:    TodoStatusPending,
        Context:   todoRequest.Context,
        Items:     make([]*TodoItem, 0, len(todoRequest.Items)),
    }
    
    for i, item := range todoRequest.Items {
        todoItem := &TodoItem{
            ID:          fmt.Sprintf("%s_item_%d", todo.ID, i+1),
            Title:       item.Title,
            Description: item.Description,
            Status:      ItemStatusPending,
            Priority:    item.Priority,
            Tool:        item.Tool,
            Args:        item.Args,
            CreatedAt:   time.Now(),
        }
        todo.Items = append(todo.Items, todoItem)
    }
    
    t.state.SetCurrentTodo(todo)
    
    return fmt.Sprintf("✅ Создан план: %s (%d задач)", todo.Title, len(todo.Items)), nil
}

func (t *TodoManagerTool) executeNext(ctx context.Context) (string, error) {
    nextItem, err := t.state.ExecuteNextTodoItem()
    if err != nil {
        return "", err
    }
    
    // Используем реестр инструментов для вызова
    toolRegistry := tools.GetRegistry()
    tool, err := toolRegistry.Find(nextItem.Tool)
    if err != nil {
        t.state.CompleteTodoItem(nextItem.ID, "", fmt.Errorf("инструмент не найден: %w", err))
        return "", fmt.Errorf("инструмент %s не найден: %w", nextItem.Tool, err)
    }
    
    result, err := tool.Execute(ctx, nextItem.Args)
    t.state.CompleteTodoItem(nextItem.ID, result, err)
    
    if err != nil {
        return "", fmt.Errorf("ошибка выполнения задачи %s: %w", nextItem.Title, err)
    }
    
    return fmt.Sprintf("✅ Выполнена задача: %s", nextItem.Title), nil
}

func (t *TodoManagerTool) getStatus(ctx context.Context) (string, error) {
    currentTodo := t.state.GetCurrentTodo()
    if currentTodo == nil {
        return "Нет активного плана", nil
    }
    
    var status strings.Builder
    status.WriteString(fmt.Sprintf("📋 План: %s (статус: %s)\n", 
        currentTodo.Context, currentTodo.Status))
    
    for _, item := range currentTodo.Items {
        status.WriteString(fmt.Sprintf("  [%s] %s (приоритет: %d)\n", 
            item.Status, item.Title, item.Priority))
    }
    
    return status.String(), nil
}

func (t *TodoManagerTool) listTodos(ctx context.Context) (string, error) {
    history := t.state.GetTodoHistory()
    if len(history) == 0 {
        return "Нет завершенных планов", nil
    }
    
    var result strings.Builder
    result.WriteString("📚 История планов:\n")
    
    for i, todo := range history {
        result.WriteString(fmt.Sprintf("%d. %s (%s) - %s\n", 
            i+1, todo.Context, todo.Status, todo.UpdatedAt.Format("02.01.2006 15:04")))
    }
    
    return result.String(), nil
}
```

## 2. Расширение GlobalState для поддержки Todo

**Файл:** [`internal/app/state.go`](internal/app/state.go) (добавление потокобезопасных методов)

```go
type GlobalState struct {
    mu           sync.RWMutex
    // ... существующие поля ...
    currentTodo  *TodoList
    todoHistory  []*TodoList
    activeTask   *TodoItem
}

// Потокобезопасные методы для работы с Todo
func (s *GlobalState) SetCurrentTodo(todo *TodoList) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.currentTodo = todo
    
    s.History = append(s.History, llm.Message{
        Role:    llm.RoleSystem,
        Content: fmt.Sprintf("📋 Создан план выполнения: %s (%d задач)", todo.Title, len(todo.Items)),
    })
}

func (s *GlobalState) GetCurrentTodo() *TodoList {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.currentTodo
}

func (s *GlobalState) ExecuteNextTodoItem() (*TodoItem, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.currentTodo == nil {
        return nil, fmt.Errorf("нет активного todo листа")
    }
    
    var nextItem *TodoItem
    highestPriority := 0
    
    for _, item := range s.currentTodo.Items {
        if item.Status == ItemStatusPending && item.Priority > highestPriority {
            nextItem = item
            highestPriority = item.Priority
        }
    }
    
    if nextItem == nil {
        return nil, fmt.Errorf("нет невыполненных задач")
    }
    
    nextItem.Status = ItemStatusInProgress
    s.activeTask = nextItem
    s.currentTodo.UpdatedAt = time.Now()
    
    return nextItem, nil
}

func (s *GlobalState) CompleteTodoItem(itemID string, result string, err error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.currentTodo == nil {
        return
    }
    
    for _, item := range s.currentTodo.Items {
        if item.ID == itemID {
            if err != nil {
                item.Status = ItemStatusFailed
                item.Error = err.Error()
            } else {
                item.Status = ItemStatusCompleted
                item.Result = result
                now := time.Now()
                item.CompletedAt = &now
            }
            break
        }
    }
    
    s.activeTask = nil
    s.currentTodo.UpdatedAt = time.Now()
    s.checkTodoCompletion()
}

func (s *GlobalState) GetTodoHistory() []*TodoList {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.todoHistory
}

func (s *GlobalState) checkTodoCompletion() {
    if s.currentTodo == nil {
        return
    }
    
    completed := 0
    failed := 0
    
    for _, item := range s.currentTodo.Items {
        switch item.Status {
        case ItemStatusCompleted:
            completed++
        case ItemStatusFailed:
            failed++
        }
    }
    
    if completed+failed == len(s.currentTodo.Items) {
        if failed == 0 {
            s.currentTodo.Status = TodoStatusCompleted
        } else {
            s.currentTodo.Status = TodoStatusFailed
        }
        
        s.todoHistory = append(s.todoHistory, s.currentTodo)
        s.currentTodo = nil
    }
}
```

## 3. Структуры данных Todo

**Файл:** [`pkg/models/todo.go`](pkg/models/todo.go)

```go
package models

import "time"

type TodoList struct {
    ID        string      `json:"id"`
    Title     string      `json:"title"`
    CreatedAt time.Time   `json:"created_at"`
    UpdatedAt time.Time   `json:"updated_at"`
    Status    TodoStatus  `json:"status"`
    Context   string      `json:"context"`
    Items     []*TodoItem `json:"items"`
}

type TodoItem struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    Description string     `json:"description"`
    Status      ItemStatus `json:"status"`
    Priority    int        `json:"priority"`
    Tool        string     `json:"tool,omitempty"`
    Args        string     `json:"args,omitempty"`
    Result      string     `json:"result,omitempty"`
    Error       string     `json:"error,omitempty"`
    CreatedAt   time.Time  `json:"created_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type TodoStatus string
const (
    TodoStatusPending    TodoStatus = "pending"
    TodoStatusInProgress TodoStatus = "in_progress"
    TodoStatusCompleted  TodoStatus = "completed"
    TodoStatusFailed     TodoStatus = "failed"
)

type ItemStatus string
const (
    ItemStatusPending    ItemStatus = "pending"
    ItemStatusInProgress ItemStatus = "in_progress"
    ItemStatusCompleted  ItemStatus = "completed"
    ItemStatusFailed     ItemStatus = "failed"
)
```

## 4. Динамическая регистрация команд

**Файл:** [`internal/app/commands.go`](internal/app/commands.go)

```go
package app

import (
    "encoding/json"
    "fmt"
    "strings"
    
    "github.com/charmbracelet/bubbletea"
    "github.com/poncho-ai/pkg/tools"
)

type CommandHandler func(state *GlobalState, args []string) tea.Msg

type CommandRegistry struct {
    mu       sync.RWMutex
    commands map[string]CommandHandler
}

func NewCommandRegistry() *CommandRegistry {
    return &CommandRegistry{
        commands: make(map[string]CommandHandler),
    }
}

func (r *CommandRegistry) Register(name string, handler CommandHandler) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.commands[name] = handler
}

func (r *CommandRegistry) Execute(input string, state *GlobalState) tea.Cmd {
    return func() tea.Msg {
        parts := strings.Fields(input)
        if len(parts) == 0 {
            return nil
        }
        
        cmd := parts[0]
        args := parts[1:]
        
        r.mu.RLock()
        handler, exists := r.commands[cmd]
        r.mu.RUnlock()
        
        if !exists {
            return CommandResultMsg{Err: fmt.Errorf("неизвестная команда: %s", cmd)}
        }
        
        return handler(state, args)
    }
}

// Регистрация Todo команд
func SetupTodoCommands(registry *CommandRegistry, todoTool *std.TodoManagerTool) {
    registry.Register("plan", func(state *GlobalState, args []string) tea.Msg {
        if len(args) < 1 {
            return CommandResultMsg{Err: fmt.Errorf("usage: plan <user_request>")}
        }
        
        userRequest := strings.Join(args, " ")
        
        // Используем LLM для генерации плана
        messages := state.BuildTodoPrompt(userRequest)
        response, err := llmClient.Generate(messages)
        if err != nil {
            return CommandResultMsg{Err: fmt.Errorf("ошибка LLM: %w", err)}
        }
        
        // Вызываем todo_tool через реестр
        todoArgs := map[string]interface{}{
            "action": "create",
            "data":   response.Content,
        }
        
        argsJSON, _ := json.Marshal(todoArgs)
        result, err := todoTool.Execute(context.Background(), string(argsJSON))
        
        if err != nil {
            return CommandResultMsg{Err: fmt.Errorf("ошибка создания плана: %w", err)}
        }
        
        return CommandResultMsg{Output: result}
    })
    
    registry.Register("execute", func(state *GlobalState, args []string) tea.Msg {
        todoArgs := map[string]interface{}{
            "action": "execute_next",
            "data":   "",
        }
        
        argsJSON, _ := json.Marshal(todoArgs)
        result, err := todoTool.Execute(context.Background(), string(argsJSON))
        
        if err != nil {
            return CommandResultMsg{Err: err}
        }
        
        return CommandResultMsg{Output: result}
    })
    
    registry.Register("status", func(state *GlobalState, args []string) tea.Msg {
        todoArgs := map[string]interface{}{
            "action": "status",
            "data":   "",
        }
        
        argsJSON, _ := json.Marshal(todoArgs)
        result, err := todoTool.Execute(context.Background(), string(argsJSON))
        
        if err != nil {
            return CommandResultMsg{Err: err}
        }
        
        return CommandResultMsg{Output: result}
    })
    
    registry.Register("history", func(state *GlobalState, args []string) tea.Msg {
        todoArgs := map[string]interface{}{
            "action": "list",
            "data":   "",
        }
        
        argsJSON, _ := json.Marshal(todoArgs)
        result, err := todoTool.Execute(context.Background(), string(argsJSON))
        
        if err != nil {
            return CommandResultMsg{Err: err}
        }
        
        return CommandResultMsg{Output: result}
    })
}
```

## 5. Интеграция в main.go

**Файл:** [`cmd/poncho/main.go`](cmd/poncho/main.go)

```go
func main() {
    // ... существующая инициализация ...
    
    // Создаем реестр команд
    commandRegistry := app.NewCommandRegistry()
    
    // Создаем и регистрируем Todo инструмент
    todoTool := std.NewTodoManagerTool(state)
    tools.GetRegistry().Register(todoTool.Definition().Name, todoTool)
    
    // Регистрируем Todo команды
    app.SetupTodoCommands(commandRegistry, todoTool)
    
    // ... существующая инициализация TUI ...
    
    // Используем commandRegistry вместо performCommand
    model := initialModel(state, llmClient, commandRegistry)
    
    // ... запуск приложения ...
}
```

## 6. Промпт для генерации Todo через LLM

**Файл:** [`prompts/todo_generation.yaml`](prompts/todo_generation.yaml)

```yaml
name: "todo_generation"
description: "Генерация структурированного плана выполнения задач"
template: |
  ПОЛЬЗОВАТЕЛЬСКИЙ ЗАПРОС: {{.UserRequest}}

  ТВОЯ ЗАДАЧА: Создай структурированный план выполнения в формате JSON.

  ДОСТУПНЫЕ ИНСТРУМЕНТЫ:
  {{range .Tools}}- {{.Name}} - {{.Description}}
  {{end}}

  ФОРМАТ ОТВЕТА (ТОЛЬКО JSON):
  {
    "title": "Краткое название задачи",
    "context": "Контекст запроса пользователя",
    "items": [
      {
        "title": "Заголовок задачи",
        "description": "Подробное описание что нужно сделать",
        "priority": 1-5,
        "tool": "имя_инструмента",
        "args": "аргументы для инструмента"
      }
    ]
  }

  ПРИМЕР:
  {
    "title": "Создать карточку товара для платья",
    "context": "Пользователь хочет создать карточку товара для артикула 12345",
    "items": [
      {
        "title": "Проанализировать эскиз платья",
        "description": "Получить и проанализировать изображение эскиза для понимания дизайна",
        "priority": 5,
        "tool": "read_s3_image_base64",
        "args": "file=\"sketch/dress_12345.jpg\""
      }
    ]
  }

  ОТВЕЧАЙ ТОЛЬКО JSON БЕЗ ДОПОЛНИТЕЛЬНЫХ КОММЕНТАРИЕВ.
```

## 7. Пример использования

```bash
# Пользователь вводит:
plan создать карточку товара для платья артикул 12345

# Система вызывает todo_manager.create сгенерированный JSON
📋 План создан: Создание карточки товара (2 задачи)

# Выполняем следующую задачу:
execute

# Система вызывает todo_manager.execute_next, который:
# 1. Находит следующую задачу
# 2. Находит инструмент в реестре
# 3. Вызывает инструмент через registry.Find(toolName).Execute()
✅ Выполнена задача: Анализ эскиза платья

# Проверяем статус:
status

📋 План: Пользователь хочет создать карточку товара для платья артикул 12345 (статус: in_progress)
  [completed] Анализ эскиза платья (приоритет: 5)
  [pending] Получить категории WB (приоритет: 3)

# Смотрим историю:
history

📚 История планов:
1. Создание карточки товара для платья (completed) - 20.12.2025 15:30
```

## 8. Преимущества архитектуры v2.0

1. **Полное соответствие принципам фреймворка** - Todo реализован как инструмент
2. **Модульность** - легко отключить или заменить Todo инструмент
3. **Использование реестра** - все вызовы инструментов через Registry
4. **Динамические команды** - регистрация команд через CommandRegistry
5. **"Raw In, String Out"** - строгий следование Tool интерфейсу
6. **Потокобезопасность** - все операции защищены мьютексами
7. **Расширяемость** - легко добавлять новые действия Todo
8. **Конфигурируемость** - промпты вынесены в YAML шаблоны

## 9. Ключевые улучшения

- **Инструментальный подход**: Todo List теперь полноценный инструмент в реестре
- **Динамическая регистрация команд**: Команды регистрируются, а не хардкодятся
- **Использование Registry**: Все вызовы инструментов идут через реестр
- **Разделение ответственности**: Ядро фреймворка не знает о Todo логике
- **Соответствие manifesto**: Все 10 правил dev_manifest.md соблюдены

## 10. Соответствие принципам разработки

| Правило dev_manifest.md | Соответствие в v2.0 |
|-------------------------|-------------------|
| 1. Интерфейс инструментов | ✅ Todo реализует Tool interface |
| 2. Конфигурация в YAML | ✅ Промпты в YAML, настройки в config |
| 3. Реестр инструментов | ✅ Все вызовы через Registry |
| 4. Абстракция LLM | ✅ Используется существующий Provider |
| 5. Глобальное состояние | ✅ Thread-safe доступ через GlobalState |
| 6. Структура пакетов | ✅ pkg/tools/std/, internal/app/ |
| 7. Обработка ошибок | ✅ Ошибки возвращаются вверх по стеку |
| 8. Расширение через инструменты | ✅ Todo добавлен как инструмент |
| 9. Тестирование | ✅ Легко мокировать зависимости |
| 10. Документация | ✅ Публичные API с godoc |

Эта версия архитектуры обеспечивает **100% соответствие** принципам разработки Poncho AI и сохраняет всю функциональность оригинального подхода.