# План рефакторинга: Удаление зависимости `pkg/app` от `internal/`

## Цель

Убрать импорт `internal/app` из `pkg/app/components.go` для соблюдения **Rule 6**:
- `pkg/` = library code (без зависимостей от `internal/`)
- `internal/` = application-specific код

---

## Текущая архитектура (проблема)

```
pkg/app/components.go
├── import "internal/app"     ← НАРУШЕНИЕ Rule 6
├── State *app.AppState       ← Тип из internal/
└── state := app.NewAppState() ← Вызов из internal/
```

**Используется в:**
- `pkg/app/components.go`: `GetTodoString()`, `GetTodoStats()` (делегируют в CoreState)
- `cmd/poncho/main.go`: установка `Orchestrator`, `CurrentModel`
- `cmd/maxiponcho/main.go`: установка `Orchestrator`, `CurrentModel`

---

## Стратегия

**Replace, don't remove** - заменить `*app.AppState` на `*state.CoreState` в `pkg/app`,
а `AppState` оставить только для TUI приложений.

```
Было:                    Станет:
├── pkg/app/             ├── pkg/app/
│   └── components.go    │   └── components.go (State → CoreState)
│       State *app.AppState  → State *state.CoreState
│
└── internal/app/        └── internal/app/
    └── state.go             └── state.go (AppState для TUI)
```

---

## Фаза 1: Подготовка (без breaking changes)

**Цель:** Создать foundation для изменений, не ломая существующий код.

### Задачи

- [ ] **1.1** Создать интерфейс `StateReader` в `pkg/state/`
  - Файл: `pkg/state/interface.go`
  - Методы: `GetTodoString()`, `GetTodoStats()`, `GetToolsRegistry()`
  - Цель: абстрагировать потребность в конкретном типе

- [ ] **1.2** Реализовать `StateReader` в `CoreState`
  - Файл: `pkg/state/core.go`
  - Уже есть методы, осталось добавить интерфейс

- [ ] **1.3** Создать тип-обёртку `ComponentsState` в `pkg/app/`
  - Файл: `pkg/app/types.go`
  - Хранит `*state.CoreState`
  - Предоставляет методы для установки `Orchestrator`, `CurrentModel` (для TUI)

**Критерий завершения:** Компиляция проходит, тесты проходят.

**Риски:** Минимальные - это добавление нового кода.

---

## Фаза 2: Рефакторинг `pkg/app/components.go`

**Цель:** Заменить `*app.AppState` на `*state.CoreState`.

### Задачи

- [ ] **2.1** Изменить тип поля `State` в `Components`
  - Файл: `pkg/app/components.go:40`
  - Было: `State *app.AppState`
  - Станет: `State *state.CoreState`

- [ ] **2.2** Удалить импорт `internal/app`
  - Файл: `pkg/app/components.go:20`
  - Удалить: `"github.com/ilkoid/poncho-ai/internal/app"`

- [ ] **2.3** Изменить создание State
  - Файл: `pkg/app/components.go:207`
  - Было: `state := app.NewAppState(cfg)`
  - Станет: `state := state.NewCoreState(cfg)`

- [ ] **2.4** Обновить использование State
  - Файл: `pkg/app/components.go`
  - Методы `GetTodoString()`, `GetTodoStats()` - уже работают с CoreState
  - Проверить все вызовы `c.State.*`

**Критерий завершения:** `pkg/app` не импортирует `internal/`.

**Риски:** Сломаются `cmd/` приложения.

---

## Фаза 3: Обновление `cmd/` приложений

**Цель:** Адаптировать приложения к новым типам.

### Задачи

- [ ] **3.1** Обновить `cmd/poncho/main.go`
  - Файл: `cmd/poncho/main.go`
  - Создать `AppState` отдельно от `Components`
  - Установить `Orchestrator` в `AppState`, не в `Components.State`

- [ ] **3.2** Обновить `cmd/maxiponcho/main.go`
  - Файл: `cmd/maxiponcho/main.go`
  - Аналогично poncho

- [ ] **3.3** Обновить CLI утилиты (chain-cli, wb-ping-util, etc.)
  - Файлы: `cmd/chain-cli/main.go`, `cmd/wb-ping-util/main.go`, etc.
  - Заменить `components.State` на `components.State` (работает, т.к. CoreState)
  - Проверить что нет обращения к TUI-полям

**Критерий завершения:** Все `cmd/` приложения компилируются.

**Риски:** Ничего - это адаптация к новому API.

---

## Фаза 4: Валидация и тестирование

**Цель:** Убедиться что всё работает.

### Задачи

- [ ] **4.1** Проверить компиляцию
  ```bash
  go build ./cmd/poncho
  go build ./cmd/maxiponcho
  go build ./cmd/chain-cli
  go build ./cmd/wb-ping-util
  ```

- [ ] **4.2** Запустить TUI приложения
  ```bash
  ./poncho  # проверить что UI работает
  ./maxiponcho  # проверить что UI работает
  ```

- [ ] **4.3** Запустить CLI утилиты
  ```bash
  ./chain-cli "show categories"
  ./wb-ping-util
  ```

- [ ] **4.4** Проверить что нет импортов `internal/` в `pkg/`
  ```bash
  grep -r "internal/" pkg/  # должно быть пусто
  ```

**Критерий завершения:** Все приложения работают, `pkg/` чист от `internal/`.

---

## Фаза 5: Cleanup (опционально)

**Цель:** Убрать временный код.

### Задачи

- [ ] **5.1** Удалить `StateReader` если не нужен
- [ ] **5.2** Удалить `ComponentsState` если не нужен
- [ ] **5.3** Обновить документацию

---

## Rollback стратегия

Если что-то пошло не так:

```bash
# Откатить изменения
git checkout HEAD -- pkg/app/components.go
git checkout HEAD -- cmd/poncho/main.go
git checkout HEAD -- cmd/maxiponcho/main.go
```

---

## Критерии успеха

1. ✅ `pkg/app` не импортирует `internal/`
2. ✅ Все `cmd/` приложения компилируются
3. ✅ TUI приложения работают
4. ✅ CLI утилиты работают
5. ✅ Rule 6 соблюдается: `pkg/` = library code

---

## Файлы для изменения

### Основные:
- `pkg/app/components.go` - убрать импорт, заменить тип
- `pkg/state/core.go` - добавить интерфейс (опционально)
- `pkg/state/interface.go` - новый файл (опционально)

### Второстепенные:
- `cmd/poncho/main.go` - адаптация
- `cmd/maxiponcho/main.go` - адаптация
- `cmd/chain-cli/main.go` - адаптация
- `cmd/wb-ping-util/main.go` - адаптация

---

## Оценка трудозатрат

| Фаза | Время | Сложность |
|------|-------|-----------|
| Фаза 1 | 1 час | Low |
| Фаза 2 | 2 часа | Medium |
| Фаза 3 | 2 часа | Medium |
| Фаза 4 | 1 час | Low |
| **Итого** | **6 часов** | **Medium** |

---

## Дополнительные улучшения (post-refactor)

После завершения основного рефакторинга:

1. **Создать `pkg/agent`** с простым API (как обсуждали ранее)
2. **Вынести WB tools** в `contrib/wildberries/`
3. **Добавить examples** в `examples/`
4. **Написать README.md** для библиотеки
