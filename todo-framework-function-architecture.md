# Todo Framework Function Architecture

## Реализация Todo List для AI-агента на основе архитектуры Poncho AI

Документ описывает, как реализовать todo list для AI-агента в текущей архитектуре фреймворка Poncho AI, с указанием конкретных файлов и пакетов для реализации.

## 1. Структура Todo в GlobalState

**Файл:** [`internal/app/state.go`](internal/app/state.go:42)

На основе существующей `GlobalState` и `FileMeta` из текущей архитектуры:

```go
type GlobalState struct {
    // ... существующие поля ...
    CurrentTodo *TodoList       // Текущий todo лист
    TodoHistory []*TodoList     // История выполненных todo
    ActiveTask  *TodoItem       // Активная задача сейчас
}

type TodoList struct {
    ID        string      `json:"id"`
    CreatedAt time.Time   `json:"created_at"`
    UpdatedAt time.Time   `json:"updated_at"`
    Status    TodoStatus  `json:"status"` // pending, in_progress, completed, failed
    Context   string      `json:"context"` // Контекст создания (запрос пользователя)
    Items     []*TodoItem `json:"items"`
}

type TodoItem struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    Description string     `json:"description"`
    Status      ItemStatus `json:"status"` // pending, in_progress, completed, failed
    Priority    int        `json:"priority"` // 1-5
    Tool        string     `json:"tool,omitempty"` // Какой инструмент нужен
    Args        string     `json:"args,omitempty"` // Аргументы для инструмента
    Result      string     `json:"result,omitempty"` // Результат выполнения
    Error       string     `json:"error,omitempty"` // Ошибка если была
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

## 2. Заставляем LLM выдать Todo - правильный промпт

**Файл:** [`internal/app/state.go`](internal/app/state.go:129) (расширение метода `BuildAgentContext`)

На основе существующего метода `BuildAgentContext` из текущей архитектуры:

```go
func (s *GlobalState) BuildTodoPrompt(userRequest string) []llm.Message {
    // Базовый контекст как в BuildAgentContext
    baseContext := s.BuildAgentContext(s.buildTodoSystemPrompt())
    
    // Добавляем специфичный для todo промпт
    todoPrompt := fmt.Sprintf(`
ПОЛЬЗОВАТЕЛЬСКИЙ ЗАПРОС: %s

ТВОЯ ЗАДАЧА: Создай структурированный план выполнения в формате JSON.

ДОСТУПНЫЕ ИНСТРУМЕНТЫ:
- read_s3_image_base64(file="path") - прочитать изображение
- get_wb_categories() - получить категории WB
- analyze_product_data(article_id) - анализ данных товара
- generate_description(specs) - генерация описания

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
    },
    {
      "title": "Получить категории WB",
      "description": "Найти подходящие категории для женской одежды",
      "priority": 3,
      "tool": "get_wb_categories",
      "args": ""
    }
  ]
}

ОТВЕЧАЙ ТОЛЬКО JSON БЕЗ ДОПОЛНИТЕЛЬНЫХ КОММЕНТАРИЕВ.
`, userRequest)

    // Добавляем todo промпт к базовому контексту
    messages := append(baseContext, llm.Message{
        Role:    llm.RoleUser,
        Content: todoPrompt,
    })
    
    return messages
}
```

## 3. Парсинг и сохранение Todo в State

**Файл:** [`internal/app/state.go`](internal/app/state.go:80) (добавление новых потокобезопасных методов)

На основе существующих потокобезопасных методов из `state.go`:

```go
// Создание todo из ответа LLM
func (s *GlobalState) CreateTodoFromLLMResponse(userRequest, llmResponse string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Парсим JSON ответ
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
    
    if err := json.Unmarshal([]byte(llmResponse), &todoRequest); err != nil {
        return fmt.Errorf("ошибка парсинга todo JSON: %w", err)
    }
    
    // Создаем TodoList
    todo := &TodoList{
        ID:        generateUUID(),
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
        Status:    TodoStatusPending,
        Context:   todoRequest.Context,
        Items:     make([]*TodoItem, 0, len(todoRequest.Items)),
    }
    
    // Конвертируем items
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
    
    // Сохраняем в state
    s.CurrentTodo = todo
    
    // Добавляем сообщение в историю
    s.History = append(s.History, llm.Message{
        Role:    llm.RoleSystem,
        Content: fmt.Sprintf("✅ Создан план выполнения: %s (%d задач)", todo.Title, len(todo.Items)),
    })
    
    return nil
}

// Выполнение следующей задачи из todo
func (s *GlobalState) ExecuteNextTodoItem() (*TodoItem, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.CurrentTodo == nil {
        return nil, fmt.Errorf("нет активного todo листа")
    }
    
    // Находим следующую pending задачу с наивысшим приоритетом
    var nextItem *TodoItem
    highestPriority := 0
    
    for _, item := range s.CurrentTodo.Items {
        if item.Status == ItemStatusPending && item.Priority > highestPriority {
            nextItem = item
            highestPriority = item.Priority
        }
    }
    
    if nextItem == nil {
        return nil, fmt.Errorf("нет невыполненных задач")
    }
    
    // Помечаем как выполняющуюся
    nextItem.Status = ItemStatusInProgress
    s.ActiveTask = nextItem
    s.CurrentTodo.UpdatedAt = time.Now()
    
    return nextItem, nil
}

// Завершение задачи
func (s *GlobalState) CompleteTodoItem(itemID string, result string, err error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    for _, item := range s.CurrentTodo.Items {
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
    
    s.ActiveTask = nil
    s.CurrentTodo.UpdatedAt = time.Now()
    
    // Проверяем, завершен ли весь todo
    s.checkTodoCompletion()
}

func (s *GlobalState) checkTodoCompletion() {
    completed := 0
    failed := 0
    
    for _, item := range s.CurrentTodo.Items {
        switch item.Status {
        case ItemStatusCompleted:
            completed++
        case ItemStatusFailed:
            failed++
        }
    }
    
    if completed+failed == len(s.CurrentTodo.Items) {
        if failed == 0 {
            s.CurrentTodo.Status = TodoStatusCompleted
        } else {
            s.CurrentTodo.Status = TodoStatusFailed
        }
        
        // Перемещаем в историю
        s.TodoHistory = append(s.TodoHistory, s.CurrentTodo)
        s.CurrentTodo = nil
    }
}
```

## 4. Интеграция в performCommand

На основе `performCommand` из текущей архитектуры:

```go
func performCommand(input string, state *app.GlobalState) tea.Cmd {
    return func() tea.Msg {
        parts := strings.Fields(input)
        if len(parts) == 0 {
            return nil
        }
        
        cmd := parts[0]
        args := parts[1:]
        
        switch cmd {
        // ... существующие команды ...
        
        case "plan":
            if len(args) < 1 {
                return CommandResultMsg{Err: fmt.Errorf("usage: plan <user_request>")}
            }
            
            userRequest := strings.Join(args, " ")
            
            // 1. Строим промпт для todo
            messages := state.BuildTodoPrompt(userRequest)
            
            // 2. Отправляем в LLM
            response, err := llmClient.Generate(messages)
            if err != nil {
                return CommandResultMsg{Err: fmt.Errorf("ошибка LLM: %w", err)}
            }
            
            // 3. Сохраняем todo в state
            if err := state.CreateTodoFromLLMResponse(userRequest, response.Content); err != nil {
                return CommandResultMsg{Err: fmt.Errorf("ошибка сохранения todo: %w", err)}
            }
            
            return CommandResultMsg{Output: fmt.Sprintf("📋 План создан: %d задач", len(state.CurrentTodo.Items))}
            
        case "execute":
            // Выполняем следующую задачу из todo
            nextItem, err := state.ExecuteNextTodoItem()
            if err != nil {
                return CommandResultMsg{Err: err}
            }
            
            // Выполняем инструмент
            result, err := executeTool(nextItem.Tool, nextItem.Args)
            state.CompleteTodoItem(nextItem.ID, result, err)
            
            if err != nil {
                return CommandResultMsg{Err: fmt.Errorf("ошибка выполнения задачи: %w", err)}
            }
            
            return CommandResultMsg{Output: fmt.Sprintf("✅ Задача выполнена: %s", nextItem.Title)}
            
        case "status":
            // Показываем статус текущего todo
            if state.CurrentTodo == nil {
                return CommandResultMsg{Output: "Нет активного плана"}
            }
            
            var status strings.Builder
            status.WriteString(fmt.Sprintf("📋 План: %s (статус: %s)\n", 
                state.CurrentTodo.Context, state.CurrentTodo.Status))
            
            for _, item := range s.CurrentTodo.Items {
                status.WriteString(fmt.Sprintf("  [%s] %s (приоритет: %d)\n", 
                    item.Status, item.Title, item.Priority))
            }
            
            return CommandResultMsg{Output: status.String()}
        }
    }
}
```

## 5. Пример использования

```bash
# Пользователь вводит:
plan создать карточку товара для платья артикул 12345

# LLM возвращает JSON:
{
  "title": "Создание карточки товара",
  "context": "Пользователь хочет создать карточку товара для платья артикул 12345",
  "items": [
    {
      "title": "Анализ эскиза",
      "description": "Проанализировать дизайн платья по эскизу",
      "priority": 5,
      "tool": "read_s3_image_base64",
      "args": "file=\"sketch/dress_12345.jpg\""
    }
  ]
}

# Система сохраняет в state и показывает:
📋 План создан: 1 задач

# Выполняем:
execute

# Результат:
✅ Задача выполнена: Анализ эскиза

# Проверяем статус:
status

# Результат:
📋 План: Пользователь хочет создать карточку товара для платья артикул 12345 (статус: completed)
  [completed] Анализ эскиза (приоритет: 5)
```

## 6. Преимущества такого подхода

1. **Структурированность** - четкий план выполнения
2. **Отслеживание** - история всех задач и их статусов
3. **Приоритеты** - важные задачи выполняются первыми
4. **Интеграция** - использует существующую архитектуру фреймворка
5. **Потокобезопасность** - все операции защищены мьютексами
6. **Масштабируемость** - легко добавлять новые типы задач и инструменты
7. **Переиспользование** - использует существующие `BuildAgentContext`, `Tool` интерфейс и `performCommand`

## 7. Ключевые моменты реализации

- **LLM должна отвечать строго JSON** без дополнительных комментариев
- Используем существующий `BuildAgentContext` для формирования контекста
- Потокобезопасное сохранение через `sync.RWMutex`
- Интеграция через новые команды `plan`, `execute`, `status` в `performCommand`
- Поддержка приоритетов и детальных статусов задач
- Автоматическое отслеживание завершения всего todo листа