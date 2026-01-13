# План действий для Фазы 1
## Update Todos

Создаём ReActExecution struct (runtime state)

Рефакторим ReActCycle (удаляем mu, runtime поля)

Обновляем Execute() — создаём execution per call

Пишем concurrent execution test (race detector)

Запускаем go test -race для проверки
Детальное ТЗ для Фазы 1 (на основе плана)
## 1. Вводим ReActExecution в новый файл
Файл: pkg/chain/execution.go (новый)


// ReActExecution — runtime состояние выполнения ReAct цикла.
//
// Создаётся на каждый вызов Execute(), не разделяется между goroutines.
// ReActCycle (template) → создаёт → ReActExecution (runtime)
type ReActExecution struct {
    // Context
    ctx      context.Context
    chainCtx *ChainContext

    // Steps (локальные экземпляры для этого выполнения)
    llmStep  *LLMInvocationStep
    toolStep *ToolExecutionStep

    // Cross-cutting concerns (локальные)
    emitter        events.Emitter
    debugRecorder *ChainDebugRecorder

    // Configuration
    streamingEnabled bool
    startTime       time.Time
}

// NewReActExecution создаёт execution для одного вызова Execute()
func NewReActExecution(
    ctx context.Context,
    input ChainInput,
    llmStep *LLMInvocationStep,
    toolStep *ToolExecutionStep,
    emitter events.Emitter,
    debugRecorder *ChainDebugRecorder,
    streamingEnabled bool,
) *ReActExecution {
    // ...
}

// Run выполняет ReAct цикл (основная логика из ReActCycle.Execute)
func (e *ReActExecution) Run(config ReActCycleConfig) (ChainOutput, error) {
    // Логика из текущего ReActCycle.Execute(), но:
    // - БЕЗ sync.Mutex (execution не шарится)
    // - Использует e.llmStep, e.toolStep, e.emitter (локальные)
}
## 2. Рефакторим ReActCycle
Файл: pkg/chain/react.go

Удаляем:


// УДАЛИТЬ из ReActCycle:
mu            sync.Mutex  // ← Строка 49
emitter       events.Emitter // ← Строка 51
debugRecorder *ChainDebugRecorder // ← Строка 50
streamingEnabled bool         // ← Строка 46
Оставляем (immutable template):


type ReActCycle struct {
    // Dependencies (immutable)
    modelRegistry *models.Registry
    registry      *tools.Registry
    state         *state.CoreState
    
    // Configuration
    config        ReActCycleConfig
    defaultModel  string
    promptsDir    string

    // Steps (immutable, создаются один раз)
    llmStep  *LLMInvocationStep  // ← Переместить логику emitter в execution
    toolStep *ToolExecutionStep
}
Обновляем setter'ы:


// SetEmitter теперь сохраняет emitter для создания execution
// НЕ устанавливает в llmStep напрямую
func (c *ReActCycle) SetEmitter(emitter events.Emitter) {
    // Сохраняем для передачи в ReActExecution
    c.config.DefaultEmitter = emitter
}

// AttachDebug аналогично
func (c *ReActCycle) AttachDebug(recorder *ChainDebugRecorder) {
    c.config.DefaultDebugRecorder = recorder
}
## 3. Новый Execute()
Файл: pkg/chain/react.go


// Execute выполняет ReAct цикл (Thread-safe через immutability).
func (c *ReActCycle) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
    // 1. Валидация (read-only, без блокировки)
    if err := c.validateDependencies(); err != nil {
        return ChainOutput{}, fmt.Errorf("invalid dependencies: %w", err)
    }

    // 2. Создаём execution (runtime state)
    execution := NewReActExecution(
        ctx,
        input,
        c.llmStep,      // Шаблон шага
        c.toolStep,     // Шаблон шага
        c.config.DefaultEmitter,
        c.config.DefaultDebugRecorder,
        c.config.StreamingEnabled,
    )

    // 3. Запускаем execution (без mutex!)
    return execution.Run(c.config)
}
## 4. Клонирование шагов
Проблема: llmStep и toolStep сейчас mutable (хранят emitter, debugRecorder).

Решение:


// В NewReActExecution:
execLLMStep := &LLMInvocationStep{
    modelRegistry:  templateLlmStep.modelRegistry,   // Shared (immutable)
    defaultModel:   templateLlmStep.defaultModel,    // Shared (immutable)
    registry:       templateLlmStep.registry,        // Shared (immutable)
    systemPrompt:   templateLlmStep.systemPrompt,    // Shared (immutable)
    emitter:        executionEmitter,                // Local
    debugRecorder:  executionDebugRecorder,          // Local
}
## 5. Concurrent execution test
Файл: pkg/chain/react_race_test.go (новый)


// TestConcurrentExecution проверяет что несколько Execute()
// могут работать одновременно без data races.
func TestConcurrentExecution(t *testing.T) {
    // Setup
    cycle := setupTestCycle(t)
    ctx := context.Background()

    // Запускаем 10 параллельных выполнений
    var wg sync.WaitGroup
    errors := make(chan error, 10)

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            input := ChainInput{UserQuery: fmt.Sprintf("query %d", idx)}
            _, err := cycle.Execute(ctx, input)
            errors <- err
        }(i)
    }

    wg.Wait()
    close(errors)

    // Проверяем что нет ошибок
    for err := range errors {
        if err != nil {
            t.Errorf("Concurrent execution failed: %v", err)
        }
    }
}
## 6. Race detector

# Запускаем с race detector
go test -race -v ./pkg/chain/... -run TestConcurrentExecution

# Ожидаем: NO DATA RACES
Acceptance Criteria (из плана)
 Два параллельных Run() не блокируют друг друга
 go test -race без ошибок
 Поведение идентично baseline при single execution
 Public API не изменился (Agent, Chain, Run, Execute)
Риски
Риск	Последствие	Митигация
Shared state в шагах	Data race	Клонировать шаги в execution
CoreState mutable	Race condition в истории	Уже thread-safe (RWMutex)
Emitter не thread-safe	Race в events	Проверить ChanEmitter на thread-safety
Изменение порядка events	UI сломается	Проверить что порядок не изменился
