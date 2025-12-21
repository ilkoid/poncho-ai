# Todo Framework Function Architecture v3.0

## Гибридный подход: ReAct (Reasoning + Acting) для Todo List в Poncho AI

Документ описывает гибридную реализацию Todo List для AI-агента в цикле рассуждений-действий, сочетающую преимущества Core Logic и Tool подхода в архитектуре Poncho AI.

## Философия гибридного подхода

**Основная идея**: Разделение ответственности между уровнем данных (Core) и уровнем управления (Tools).

- **Для LLM**: Todo - это инструменты для управления планом (add_task, complete_task)
- **Для Фреймворка**: Todo - это часть состояния с автоматической инъекцией в контекст
- **Для UI**: Todo - это визуализация текущего состояния плана

Это позволяет экономить токены, сохранять гибкость и обеспечивать синхронизацию между AI и пользователем.

## 1. Уровень данных - Todo Manager

**Файл:** [`pkg/todo/manager.go`](pkg/todo/manager.go)

Создаем отдельный пакет для избежания циклических зависимостей:

```go
package todo

import (
    "fmt"
    "strings"
    "sync"
    "time"
)

type TaskStatus string
const (
    StatusPending TaskStatus = "PENDING"
    StatusDone    TaskStatus = "DONE"
    StatusFailed  TaskStatus = "FAILED"
)

type Task struct {
    ID          int                    `json:"id"`
    Description string                 `json:"description"`
    Status      TaskStatus             `json:"status"`
    CreatedAt   time.Time              `json:"created_at"`
    CompletedAt *time.Time             `json:"completed_at,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Manager - потокобезопасное хранилище задач
type Manager struct {
    mu     sync.RWMutex
    tasks  []Task
    nextID int
}

func NewManager() *Manager {
    return &Manager{
        tasks:  make([]Task, 0),
        nextID: 1,
    }
}

// Методы для Tools
func (m *Manager) Add(description string, metadata ...map[string]interface{}) int {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    var meta map[string]interface{}
    if len(metadata) > 0 {
        meta = metadata[0]
    }
    
    task := Task{
        ID:          m.nextID,
        Description: description,
        Status:      StatusPending,
        CreatedAt:   time.Now(),
        Metadata:    meta,
    }
    
    m.tasks = append(m.tasks, task)
    m.nextID++
    return task.ID
}

func (m *Manager) Complete(id int) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    for i := range m.tasks {
        if m.tasks[i].ID == id {
            if m.tasks[i].Status != StatusPending {
                return fmt.Errorf("задача %d уже выполнена или провалена", id)
            }
            m.tasks[i].Status = StatusDone
            now := time.Now()
            m.tasks[i].CompletedAt = &now
            return nil
        }
    }
    return fmt.Errorf("задача %d не найдена", id)
}

func (m *Manager) Fail(id int, reason string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    for i := range m.tasks {
        if m.tasks[i].ID == id {
            if m.tasks[i].Status != StatusPending {
                return fmt.Errorf("задача %d уже выполнена или провалена", id)
            }
            m.tasks[i].Status = StatusFailed
            if m.tasks[i].Metadata == nil {
                m.tasks[i].Metadata = make(map[string]interface{})
            }
            m.tasks[i].Metadata["error"] = reason
            return nil
        }
    }
    return fmt.Errorf("задача %d не найдена", id)
}

func (m *Manager) Clear() {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.tasks = make([]Task, 0)
    m.nextID = 1
}

// Метод для Context Injection - превращает лист в строку для промпта
func (m *Manager) String() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    if len(m.tasks) == 0 {
        return "Нет активных задач"
    }
    
    var result strings.Builder
    result.WriteString("ТЕКУЩИЙ ПЛАН:\n")
    
    pending := 0
    done := 0
    failed := 0
    
    for _, task := range m.tasks {
        status := "[ ]"
        switch task.Status {
        case StatusDone:
            status = "[✓]"
            done++
        case StatusFailed:
            status = "[✗]"
            failed++
        default:
            pending++
        }
        
        result.WriteString(fmt.Sprintf("%s %d. %s\n", status, task.ID, task.Description))
        
        if task.Status == StatusFailed && task.Metadata != nil {
            if err, ok := task.Metadata["error"].(string); ok {
                result.WriteString(fmt.Sprintf("    Ошибка: %s\n", err))
            }
        }
    }
    
    result.WriteString(fmt.Sprintf("\nСтатистика: %d выполнено, %d в работе, %d провалено", 
        done, pending, failed))
    
    return result.String()
}

// Методы для UI
func (m *Manager) GetTasks() []Task {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    tasks := make([]Task, len(m.tasks))
    copy(tasks, m.tasks)
    return tasks
}

func (m *Manager) GetStats() (pending, done, failed int) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    for _, task := range m.tasks {
        switch task.Status {
        case StatusDone:
            done++
        case StatusFailed:
            failed++
        default:
            pending++
        }
    }
    return
}
```

## 2. Интеграция в Global State

**Файл:** [`internal/app/state.go`](internal/app/state.go)

```go
import "github.com/poncho-ai/pkg/todo"

type GlobalState struct {
    mu    sync.RWMutex
    // ... существующие поля ...
    Todo  *todo.Manager // <--- Добавляем Todo Manager
}

func NewState(...) *GlobalState {
    return &GlobalState{
        // ... существующая инициализация ...
        Todo: todo.NewManager(),
    }
}

// Обновляем логику сборки контекста для ReAct цикла
func (s *GlobalState) BuildAgentContext(systemPrompt string) []llm.Message {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // 1. Базовый системный промпт
    messages := []llm.Message{
        {Role: llm.RoleSystem, Content: systemPrompt},
    }

    // 2. Контекст файлов (как было раньше)
    if len(s.Files) > 0 {
        var fileContext strings.Builder
        fileContext.WriteString("ДОСТУПНЫЕ ФАЙЛЫ:\n")
        for _, file := range s.Files {
            fileContext.WriteString(fmt.Sprintf("- %s (%s)\n", file.Path, file.Type))
        }
        messages = append(messages, llm.Message{
            Role:    llm.RoleSystem,
            Content: fileContext.String(),
        })
    }

    // 3. Контекст плана (НОВОЕ - Context Injection)
    // Агент всегда видит свой план перед глазами без вызова инструментов
    todoContext := s.Todo.String()
    messages = append(messages, llm.Message{
        Role:    llm.RoleSystem,
        Content: todoContext,
    })

    // 4. История диалога
    messages = append(messages, s.History...)

    return messages
}

// Вспомогательные методы для работы с Todo
func (s *GlobalState) AddTodoTask(description string, metadata ...map[string]interface{}) int {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.Todo.Add(description, metadata...)
}

func (s *GlobalState) CompleteTodoTask(id int) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.Todo.Complete(id)
}

func (s *GlobalState) FailTodoTask(id int, reason string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.Todo.Fail(id, reason)
}
```

## 3. Реализация Tools для управления планом

**Файл:** [`pkg/tools/std/planner.go`](pkg/tools/std/planner.go)

```go
package std

import (
    "context"
    "encoding/json"
    "fmt"
    "strconv"
    
    "github.com/poncho-ai/pkg/todo"
    "github.com/poncho-ai/pkg/tools"
)

type PlannerTool struct {
    manager *todo.Manager
}

func NewPlannerTool(manager *todo.Manager) *PlannerTool {
    return &PlannerTool{manager: manager}
}

// Tool: plan_add_task
func (p *PlannerTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name: "plan_add_task",
        Description: "Добавляет новую задачу в план действий",
        ArgsSchema: map[string]interface{}{
            "description": map[string]interface{}{
                "type":        "string",
                "description": "Описание задачи для выполнения",
            },
            "metadata": map[string]interface{}{
                "type":        "object",
                "description": "Дополнительные метаданные (опционально)",
            },
        },
    }
}

func (p *PlannerTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Description string                 `json:"description"`
        Metadata    map[string]interface{} `json:"metadata,omitempty"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
    }
    
    if args.Description == "" {
        return "", fmt.Errorf("описание задачи не может быть пустым")
    }
    
    id := p.manager.Add(args.Description, args.Metadata)
    return fmt.Sprintf("✅ Задача добавлена в план (ID: %d): %s", id, args.Description), nil
}

// Tool: plan_mark_done
func (p *PlannerTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name: "plan_mark_done",
        Description: "Отмечает задачу как выполненную",
        ArgsSchema: map[string]interface{}{
            "task_id": map[string]interface{}{
                "type":        "integer",
                "description": "ID задачи для отметки выполнения",
            },
        },
    }
}

func (p *PlannerTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        TaskID int `json:"task_id"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
    }
    
    if err := p.manager.Complete(args.TaskID); err != nil {
        return "", fmt.Errorf("ошибка отметки задачи: %w", err)
    }
    
    return fmt.Sprintf("✅ Задача %d отмечена как выполненная", args.TaskID), nil
}

// Tool: plan_mark_failed
func (p *PlannerTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name: "plan_mark_failed",
        Description: "Отмечает задачу как проваленную с указанием причины",
        ArgsSchema: map[string]interface{}{
            "task_id": map[string]interface{}{
                "type":        "integer",
                "description": "ID задачи для отметки провала",
            },
            "reason": map[string]interface{}{
                "type":        "string",
                "description": "Причина провала задачи",
            },
        },
    }
}

func (p *PlannerTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        TaskID int    `json:"task_id"`
        Reason string `json:"reason"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
    }
    
    if err := p.manager.Fail(args.TaskID, args.Reason); err != nil {
        return "", fmt.Errorf("ошибка отметки задачи: %w", err)
    }
    
    return fmt.Sprintf("❌ Задача %d отмечена как проваленная: %s", args.TaskID, args.Reason), nil
}

// Tool: plan_clear
func (p *PlannerTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name: "plan_clear",
        Description: "Очищает весь план действий",
        ArgsSchema:  map[string]interface{}{},
    }
}

func (p *PlannerTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    p.manager.Clear()
    return "🗑️ План действий очищен", nil
}
```

## 4. Визуализация в UI

**Файл:** [`internal/ui/view.go`](internal/ui/view.go)

```go
import (
    "github.com/charmbracelet/lipgloss"
    "github.com/poncho-ai/pkg/todo"
)

// Стили для Todo
var (
    todoBorderStyle = lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("62")).
        Padding(0, 1).
        MarginRight(1)

    todoTitleStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("212")).
        MarginBottom(1)

    taskPendingStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("251"))

    taskDoneStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("46")).
        Strikethrough(true)

    taskFailedStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("196")).
        Strikethrough(true)

    statsStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("244")).
        Italic(true).
        MarginTop(1)
)

func renderTodoPanel(manager *todo.Manager, width int) string {
    tasks := manager.GetTasks()
    pending, done, failed := manager.GetStats()
    
    if len(tasks) == 0 {
        return todoBorderStyle.Width(width).Render(
            todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ") + "\n" +
            taskPendingStyle.Render("Нет активных задач"),
        )
    }
    
    var content strings.Builder
    content.WriteString(todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ"))
    content.WriteString("\n\n")
    
    for _, task := range tasks {
        var statusIcon string
        var taskStyle lipgloss.Style
        
        switch task.Status {
        case todo.StatusDone:
            statusIcon = "✓"
            taskStyle = taskDoneStyle
        case todo.StatusFailed:
            statusIcon = "✗"
            taskStyle = taskFailedStyle
        default:
            statusIcon = "○"
            taskStyle = taskPendingStyle
        }
        
        content.WriteString(fmt.Sprintf("%s %d. %s\n", 
            statusIcon, task.ID, 
            taskStyle.Render(task.Description)))
        
        if task.Status == todo.StatusFailed && task.Metadata != nil {
            if err, ok := task.Metadata["error"].(string); ok {
                content.WriteString(fmt.Sprintf("   %s\n", 
                    taskFailedStyle.Render("Ошибка: "+err)))
            }
        }
    }
    
    content.WriteString("\n")
    content.WriteString(statsStyle.Render(
        fmt.Sprintf("Выполнено: %d | В работе: %d | Провалено: %d", 
            done, pending, failed)))
    
    return todoBorderStyle.Width(width).Render(content.String())
}

func (m MainModel) View() string {
    // ... существующий код ...
    
    // Добавляем Todo панель справа или снизу
    todoPanel := renderTodoPanel(m.appState.Todo, 40)
    
    // Комбинируем с основным интерфейсом
    return lipgloss.JoinHorizontal(lipgloss.Top, 
        mainContent,
        todoPanel,
    )
}
```

## 5. Команды для прямого управления из UI

**Файл:** [`internal/app/commands.go`](internal/app/commands.go)

```go
// Добавляем команды для прямого управления Todo из интерфейса
func SetupTodoCommands(registry *CommandRegistry, state *GlobalState) {
    registry.Register("todo", func(state *GlobalState, args []string) tea.Cmd {
        return func() tea.Msg {
            if len(args) == 0 {
                return CommandResultMsg{Output: state.Todo.String()}
            }
            
            subcommand := args[0]
            
            switch subcommand {
            case "add":
                if len(args) < 2 {
                    return CommandResultMsg{Err: fmt.Errorf("usage: todo add <description>")}
                }
                description := strings.Join(args[1:], " ")
                id := state.AddTodoTask(description)
                return CommandResultMsg{Output: fmt.Sprintf("✅ Добавлена задача %d: %s", id, description)}
                
            case "done":
                if len(args) < 2 {
                    return CommandResultMsg{Err: fmt.Errorf("usage: todo done <id>")}
                }
                id, err := strconv.Atoi(args[1])
                if err != nil {
                    return CommandResultMsg{Err: fmt.Errorf("неверный ID задачи: %w", err)}
                }
                if err := state.CompleteTodoTask(id); err != nil {
                    return CommandResultMsg{Err: err}
                }
                return CommandResultMsg{Output: fmt.Sprintf("✅ Задача %d выполнена", id)}
                
            case "clear":
                state.Todo.Clear()
                return CommandResultMsg{Output: "🗑️ План очищен"}
                
            default:
                return CommandResultMsg{Err: fmt.Errorf("неизвестная подкоманда: %s", subcommand)}
            }
        }
    })
}
```

## 6. Интеграция в main.go

**Файл:** [`cmd/poncho/main.go`](cmd/poncho/main.go)

```go
func main() {
    // ... существующая инициализация ...
    
    // Создаем состояние с Todo Manager
    state := app.NewState(...)
    
    // Создаем и регистрируем Planner инструменты
    plannerTool := std.NewPlannerTool(state.Todo)
    tools.GetRegistry().Register("plan_add_task", plannerTool)
    tools.GetRegistry().Register("plan_mark_done", plannerTool)
    tools.GetRegistry().Register("plan_mark_failed", plannerTool)
    tools.GetRegistry().Register("plan_clear", plannerTool)
    
    // Регистрируем UI команды
    commandRegistry := app.NewCommandRegistry()
    app.SetupTodoCommands(commandRegistry, state)
    
    // ... запуск приложения ...
}
```

## 7. Пример использования в ReAct цикле

```bash
# Пользователь: "Создай карточку товара для платья"

# LLM автоматически видит в контексте:
ТЕКУЩИЙ ПЛАН:
Нет активных задач

# LLM вызывает инструмент:
plan_add_task({"description": "Проанализировать эскиз платья"})

# Результат:
✅ Задача добавлена в план (ID: 1): Проанализировать эскиз платья

# LLM видит обновленный контекст:
ТЕКУЩИЙ ПЛАН:
[ ] 1. Проанализировать эскиз платья

# LLM вызывает следующий инструмент:
plan_add_task({"description": "Получить категории WB"})

# И так далее...
```

## 8. Преимущества гибридного подхода

### 🎯 **Экономия токенов и шагов**
- Агент не тратит шаги на вызов `read_todo`
- План автоматически инжектится в каждый промпт
- Агент всегда знает текущее состояние без дополнительных запросов

### 🔄 **Реальное время**
- Пользователь видит план в UI синхронно с AI
- Возможность ручного вмешательства через команды `/todo add/done`
- Мгновенная синхронизация между AI и интерфейсом

### 🏗️ **Разделение ответственности**
- **LLM**: Логика планирования (что делать)
- **Framework**: Хранение и отображение (как хранить)
- **UI**: Визуализация (как показывать)

### 🔧 **Масштабируемость**
- Легкое добавление новых типов задач
- Расширение метаданных задач
- Интеграция с другими инструментами

### 📊 **Соответствие принципам Poncho AI**

| Принцип | Соответствие |
|---------|-------------|
| Tool Interface | ✅ Все операции через инструменты |
| Registry Pattern | ✅ Инструменты регистрируются |
| State Management | ✅ Thread-safe GlobalState |
| Context Injection | ✅ Автоматическая инъекция в промпт |
| UI Integration | ✅ Визуализация в TUI |
| Error Handling | ✅ Стандартная обработка ошибок |

## 9. Ключевые архитектурные решения

1. **Раздельный пакет `pkg/todo`**: Избегает циклических зависимостей
2. **Context Injection**: План всегда виден AI без вызова инструментов
3. **Двойной интерфейс**: Управление через инструменты + UI команды
4. **Метаданные задач**: Гибкое расширение функциональности
5. **Потокобезопасность**: Все операции защищены мьютексами

## 10. Заключение

Гибридный подход сочетает лучшие мировые:
- **Эффективность** Core подхода (экономия токенов)
- **Гибкость** Tool подхода (управление из LLM)
- **Интерактивность** UI подхода (синхронизация с пользователем)

Это идеальная реализация Todo List для ReAct цикла в архитектуре Poncho AI, обеспечивающая seamless интеграцию между рассуждениями AI и действиями фреймворка.
