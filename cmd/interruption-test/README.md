# Interruption Test Utility

Утилита для тестирования механизма прерываний в Poncho AI.

## Возможности

- **TUI с панелью статистики** - отображает количество прерываний, запросов, время выполнения и статус
- **Ручные прерывания** - пользователь сам вводит команды прерывания
- **JSON-log** - статистика сохраняется в JSON при выходе (для автоматизации)
- **Автономность** - собственный config.yaml согласно dev_manifest

## Установка

Не требуется - утилита использует Go модули Poncho AI.

## Использование

### Запуск

```bash
cd cmd/interruption-test
go run main.go
```

### Интерфейс

```
┌─────────────────────────────────────────┐
│ AI Agent TUI with Interruptions        │
├─────────────────────────────────────────┤
│ [Agent output...]                      │
│ USER: Show me parent categories        │
│ AI: Thinking...                        │
│ ⏸️ Interruption (iter 2): todo add X  │
│ ✅ Done: ...                           │
├─────────────────────────────────────────┤
│ Interruptions: 2 | Queries: 1          │
│ Duration: 12s | Status: Success       │
├─────────────────────────────────────────┤
│ > [your input or interruption]         │
└─────────────────────────────────────────┘
```

## Прерывания

Во время выполнения агента можно ввести любой текст и нажать Enter:

- `todo: add <task>` - добавить задачу
- `todo: complete <N>` - завершить задачу
- `stop` - остановить выполнение
- `What are you doing?` - спросить статус
- Любой текст - задать вопрос агенту

## Выход

- `Ctrl+C` или `Esc` - выход с сохранением статистики

## JSON-log

При выходе статистика сохраняется в `./logs/test_YYYY-MM-DD_HHMMSS.json`:

```json
{
  "session_id": "2025-01-16_123456",
  "start_time": "2025-01-16T12:34:56Z",
  "end_time": "2025-01-16T12:35:08Z",
  "duration_ms": 12000,
  "queries": [
    {
      "query": "Show me parent categories",
      "start_time": "2025-01-16T12:34:56Z",
      "end_time": "2025-01-16T12:35:08Z",
      "interruptions": [
        {
          "timestamp": "2025-01-16T12:35:00Z",
          "iteration": 2,
          "message": "todo: add test task"
        }
      ],
      "iterations": 5,
      "status": "success"
    }
  ],
  "stats": {
    "total_queries": 1,
    "total_interruptions": 1,
    "success": true
  }
}
```

## Конфигурация

Утилита использует собственный `config.yaml` в своей директории.

### Минимальные требования

```yaml
app:
  json_log_dir: "./logs"
  json_log_enabled: true

models:
  default_reasoning: "glm-4.6"
  default_chat: "glm-4.6"
  definitions:
    glm-4.6:
      provider: "zai"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"

tools:
  plan_add_task:
    enabled: true
  plan_mark_done:
    enabled: true
```

## Переменные окружения

- `ZAI_API_KEY` - API ключ для Zai (или другой провайдер)

## Архитектура

```
cmd/interruption-test/
├── main.go       # Точка входа, TestModel со статистикой
├── stats.go      # SessionStats, QueryStats, JSON-log
├── config.yaml   # Собственная конфигурация
└── README.md     # Документация
```

### TestModel

Расширяет `InterruptionModel` через встраивание:

- Обрабатывает события для сбора статистики
- Добавляет панель статистики в View
- Сохраняет JSON-log при выходе

## Правила dev_manifest

✅ **Автономность** - собственный config.yaml
✅ **Точка входа** - только инициализация и оркестрация
✅ **CLI-утилита** - логи в stdout/stderr
✅ **ConfigPathFinder** - config.yaml только в своей директории
