# План рефакторинга `chain.ReActCycle`
## Техническое задание для Go-разработчика (Senior / Principal)

---

## 1. Цели рефакторинга

### Основные цели
1. Повысить **масштабируемость исполнения** (устранить single-flight ограничение).
2. Снизить **связанность `ReActCycle`** и предотвратить рост God Object.
3. Подготовить архитектуру к:
   - добавлению новых шагов (Reflection, Validation, Memory, Guardrails),
   - альтернативным стратегиям исполнения (sequential / branching),
   - более строгой типизации управляющих сигналов.

### Не цели (важно)
- Не менять внешний public API (`Agent`, `Chain`, `Run`, `Execute`) на первой фазе.
- Не менять семантику ReAct.
- Не ломать совместимость с существующими YAML-конфигами.

---

## 2. Текущее состояние (baseline)

### Ключевые ограничения
- `ReActCycle.Execute()` удерживает `sync.Mutex` на весь цикл выполнения.
- `ReActCycle` одновременно:
  - orchestrator,
  - runtime container,
  - event dispatcher,
  - debug coordinator.
- Step abstraction формально присутствует, но фактически не используется как pipeline.
- Управляющие сигналы (`UserChoiceRequest`) передаются через string-маркеры.

---

## 3. Фаза 1 — Разделение template и execution state (КРИТИЧЕСКАЯ)

### Цель фазы
Убрать глобальный mutex с длительного выполнения и обеспечить **concurrent-safe execution**.

### Основная идея
Разделить:
- **immutable template** (`ReActCycle`)
- **execution-local runtime** (`ReActExecution`)

---

### 3.1 Введение `ReActExecution`

#### Новая структура
```go
type ReActExecution struct {
    ctx        context.Context
    chainCtx   *ChainContext

    llmStep    *LLMInvocationStep
    toolStep   *ToolExecutionStep

    emitter        events.Emitter
    debugRecorder *ChainDebugRecorder

    streamingEnabled bool
    startTime time.Time
}
Требования
Создаётся на каждый вызов Execute()

Не хранится в ReActCycle

Не разделяется между goroutine

3.2 Изменения в ReActCycle
До
go
Копировать код
type ReActCycle struct {
    mu sync.Mutex
    llmStep *LLMInvocationStep
    toolStep *ToolExecutionStep
    emitter events.Emitter
    ...
}
После
go
Копировать код
type ReActCycle struct {
    // immutable dependencies
    modelRegistry *models.Registry
    toolsRegistry *tools.Registry
    state         *state.CoreState
    config        ReActCycleConfig

    // defaults
    defaultModel string
    promptsDir   string
}
Удалить из ReActCycle
sync.Mutex

runtime mutable поля (emitter, debugRecorder, streamingEnabled)

3.3 Новый flow Execute()
text
Копировать код
Execute()
 ├─ validate dependencies (read-only)
 ├─ build ChainContext
 ├─ build ReActExecution
 ├─ run execution loop
 └─ produce ChainOutput
Acceptance criteria
Два параллельных Run() не блокируют друг друга.

Поведение идентично baseline при single execution.

4. Фаза 2 — Чёткая модель управляющих сигналов
Цель фазы
Убрать неявные string-маркеры и повысить типобезопасность.

4.1 Введение ExecutionSignal
go
Копировать код
type ExecutionSignal int

const (
    SignalNone ExecutionSignal = iota
    SignalFinalAnswer
    SignalNeedUserInput
    SignalError
)
4.2 Обновление NextAction
До
go
Копировать код
type NextAction int
После
go
Копировать код
type StepResult struct {
    Action NextAction
    Signal ExecutionSignal
}
4.3 Обновление Step.Execute
go
Копировать код
Execute(ctx context.Context, chainCtx *ChainContext) (StepResult, error)
Acceptance criteria
UserChoiceRequest больше не передаётся через текст ответа.

UI получает типизированный сигнал.

5. Фаза 3 — Реализация реального Step Pipeline
Цель фазы
Использовать Step abstraction по назначению.

5.1 Новый интерфейс Executor
go
Копировать код
type StepExecutor interface {
    Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error)
}
5.2 Базовая реализация ReActExecutor
text
Копировать код
Iteration:
 ├─ LLMInvocationStep
 ├─ if tool calls:
 │    └─ ToolExecutionStep
 └─ else:
      └─ break
5.3 Подготовка к расширениям
ReflectionStep

ValidationStep

PostProcessingStep

Acceptance criteria
Добавление нового шага не требует изменения ReActCycle.

6. Фаза 4 — Изоляция Debug и Events
Цель фазы
Убрать cross-cutting concerns из core orchestration.

6.1 Debug hooks
go
Копировать код
type ExecutionObserver interface {
    OnStart(exec *ReActExecution)
    OnIterationStart(n int)
    OnIterationEnd(n int)
    OnFinish(result ChainOutput)
}
ChainDebugRecorder становится реализацией ExecutionObserver.

6.2 Events как Observer
EmitterObserver

StreamingObserver

Acceptance criteria
Execute() не содержит прямых вызовов Emit.

7. Фаза 5 — Документация и контрактность
Обязательные артефакты
ADR: Single-flight → concurrent execution

Godoc:

lifecycle ReActCycle

distinction Template vs Execution

Diagram (sequence / flow)

8. Риски и контрольные точки
Основные риски
Незаметное изменение порядка событий

Потеря streaming semantics

Debug recorder regressions

Контроль
Golden tests на:

iterations count

emitted events order

debug artifacts

Race detector (-race) обязателен

9. Итоговое состояние (target architecture)
text
Копировать код
ReActCycle (immutable template)
        ↓
ReActExecution (runtime)
        ↓
StepExecutor
        ↓
Observers (Debug, Events, Streaming)
10. Оценка сложности
Фаза	Сложность	Риск
Фаза 1	High	Medium
Фаза 2	Medium	Low
Фаза 3	Medium	Medium
Фаза 4	Low	Low
Фаза 5	Low	Low

Заключение
Рефакторинг эволюционный, без переписывания framework.
После выполнения:

ReActCycle станет масштабируемым,

архитектура будет готова к росту,

код станет проще сопровождать senior-командой.