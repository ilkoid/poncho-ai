// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// StepExecutor — интерфейс для исполнителей шагов в ReAct цикле.
//
// # StepExecutor Pattern (PHASE 3 REFACTOR)
//
// StepExecutor separates execution logic from data (ReActExecution).
// This enables:
//   - Adding new execution strategies without modifying ReActCycle
//   - Composing steps into different pipelines (sequential, branching)
//   - Testing executors in isolation
//
// # Implementations
//
// ReActExecutor: Classic ReAct loop (LLM → Tools → Repeat)
// Future: ReflectionExecutor, ValidationExecutor, ParallelExecutor, etc.
//
// # Thread Safety
//
// StepExecutor implementations receive isolated ReActExecution instances,
// so concurrent execution is inherently safe.
type StepExecutor interface {
	// Execute выполняет пайплайн шагов и возвращает результат.
	//
	// Принимает ReActExecution как контейнер runtime состояния,
	// но не создаёт его — этим занимается ReActCycle.Execute().
	Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error)
}

// ReActExecutor — базовая реализация StepExecutor для классического ReAct цикла.
//
// # Architecture (PHASE 3-4 REFACTOR)
//
// Separation of concerns:
//   - ReActExecution = pure data container (runtime state)
//   - ReActExecutor = execution logic (iteration loop)
//   - Observers = cross-cutting concerns (debug, events)
//
// # Iteration Loop
//
// For each iteration (up to MaxIterations):
//   1. Notify observers: OnIterationStart
//   2. Execute LLMInvocationStep
//   3. Send events via IterationObserver
//   4. Check ExecutionSignal
//      - SignalFinalAnswer/SignalNeedUserInput → BREAK
//      - SignalNone → continue if no tool calls
//   5. If tool calls: Execute ToolExecutionStep
//   6. Send events via IterationObserver
//   7. Notify observers: OnIterationEnd
//
// # Observer Notifications (PHASE 4)
//
// Lifecycle:
//   - OnStart: Before execution begins
//   - OnIterationStart: Before each iteration
//   - OnIterationEnd: After each iteration
//   - OnFinish: After execution completes (success or error)
//
// # Thread Safety
//
// Thread-safe when used with isolated ReActExecution instances.
// Each Execute() call uses its own execution state, enabling concurrent execution.
type ReActExecutor struct {
	// observers — список наблюдателей за выполнением (Phase 4)
	observers []ExecutionObserver

	// iterationObserver — наблюдатель для событий внутри итерации (PHASE 4)
	iterationObserver *EmitterIterationObserver
}

// ExecutionObserver — интерфейс для наблюдения за выполнением (PHASE 4).
//
// # Observer Pattern (PHASE 4 REFACTOR)
//
// ExecutionObserver isolates cross-cutting concerns from core orchestration.
// Instead of calling Emit() or debug methods directly, executor notifies observers
// of lifecycle events and delegates concerns to observer implementations.
//
// # Implementations
//
// ChainDebugRecorder: Records debug logs for each execution
//   - OnStart: Starts debug recording
//   - OnIterationStart: Starts new iteration in debug log
//   - OnIterationEnd: Ends iteration in debug log
//   - OnFinish: Finalizes debug log and writes to file
//
// EmitterObserver: Sends final events to UI
//   - OnStart: (no action)
//   - OnIterationStart: (no action)
//   - OnIterationEnd: (no action)
//   - OnFinish: Sends EventDone or EventError
//
// # Thread Safety
//
// Observer implementations must be thread-safe as they may be called
// from concurrent Execute() executions (each with isolated ReActExecution).
//
// # Lifecycle Contract
//
// 1. OnStart is called once at the beginning of execution
// 2. OnIterationStart/OnIterationEnd are called for each iteration
// 3. OnFinish is called once at the end (success or error)
type ExecutionObserver interface {
	OnStart(ctx context.Context, exec *ReActExecution)
	OnIterationStart(iteration int)
	OnIterationEnd(iteration int)
	OnFinish(result ChainOutput, err error)
}

// NewReActExecutor создаёт новый ReActExecutor.
func NewReActExecutor() *ReActExecutor {
	return &ReActExecutor{
		observers:         make([]ExecutionObserver, 0),
		iterationObserver: nil, // Будет установлен через SetIterationObserver
	}
}

// AddObserver добавляет наблюдателя за выполнением.
//
// PHASE 3 REFACTOR: Подготовка к Phase 4 (изоляция debug и events).
// Thread-safe: вызывается до Execute(), не требует синхронизации.
func (e *ReActExecutor) AddObserver(observer ExecutionObserver) {
	e.observers = append(e.observers, observer)
}

// SetIterationObserver устанавливает наблюдатель для событий внутри итерации.
//
// PHASE 4 REFACTOR: Изоляция логики отправки событий из core orchestration.
// Thread-safe: вызывается до Execute(), не требует синхронизации.
func (e *ReActExecutor) SetIterationObserver(observer *EmitterIterationObserver) {
	e.iterationObserver = observer
}

// Execute выполняет ReAct цикл.
//
// PHASE 3 REFACTOR: Основная логика из ReActExecution.Run(),
// но теперь в отдельном компоненте (StepExecutor).
//
// PHASE 4 REFACTOR: Изоляция debug и events через observer pattern.
// Execute() больше не содержит прямых вызовов Emit или debug методов.
//
// Итерация:
//   ├─ LLMInvocationStep
//   ├─ Отправка событий через iterationObserver (EventThinking, EventToolCall)
//   ├─ Проверка сигнала (SignalFinalAnswer, SignalNeedUserInput)
//   ├─ Если tool calls:
//   │  └─ ToolExecutionStep
//   └─ Иначе: break
//
// Thread-safe: Использует изолированный ReActExecution.
func (e *ReActExecutor) Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error) {
	// Initialize execution
	if err := e.initializeExecution(ctx, exec); err != nil {
		return e.notifyFinishWithError(exec, err)
	}

	// ReAct loop
	iterations := 0
	for iterations = 0; iterations < exec.config.MaxIterations; iterations++ {
		e.notifyIterationStart(iterations)

		// LLM step
		llmResult, lastMsg, err := e.executeLLMStep(ctx, exec, iterations)
		if err != nil {
			return e.notifyFinishWithError(exec, err)
		}

		// Check for final answer or user input signal
		if llmResult.Signal == SignalFinalAnswer || llmResult.Signal == SignalNeedUserInput {
			exec.finalSignal = llmResult.Signal
			e.notifyIterationEnd(iterations)
			break
		}

		// Check for tool calls
		if len(lastMsg.ToolCalls) == 0 {
			if exec.finalSignal == SignalNone {
				exec.finalSignal = SignalFinalAnswer
			}
			e.notifyIterationEnd(iterations)
			break
		}

		// Tool execution
		toolResult, err := e.handleToolExecution(ctx, exec, iterations)
		if err != nil {
			return e.notifyFinishWithError(exec, err)
		}

		// Check for interruption during tool execution
		if toolResult.Signal == SignalUserInterruption {
			if err := e.handleToolInterruption(ctx, exec, toolResult.Interruption, iterations); err != nil {
				return e.notifyFinishWithError(exec, err)
			}
			continue
		}

		// Check for interruption between iterations
		if err := e.checkUserInterruption(ctx, exec, iterations); err != nil {
			return e.notifyFinishWithError(exec, err)
		}

		e.notifyIterationEnd(iterations)
	}

	return e.finalizeExecution(ctx, exec, iterations)
}

// initializeExecution инициализирует выполнение ReAct цикла.
//
// Уведомляет наблюдателей и добавляет user message в историю.
func (e *ReActExecutor) initializeExecution(ctx context.Context, exec *ReActExecution) error {
	// Notify observers: OnStart
	for _, obs := range e.observers {
		obs.OnStart(ctx, exec)
	}

	// Добавляем user message в историю
	if err := exec.chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: exec.chainCtx.Input.UserQuery,
	}); err != nil {
		return fmt.Errorf("failed to append user message: %w", err)
	}

	return nil
}

// executeLLMStep выполняет LLM шаг итерации.
//
// Возвращает (llmResult, lastMessage, error).
func (e *ReActExecutor) executeLLMStep(ctx context.Context, exec *ReActExecution, iteration int) (StepResult, *llm.Message, error) {
	// LLM Invocation
	llmResult := exec.llmStep.Execute(ctx, exec.chainCtx)

	// Обрабатываем результат
	if llmResult.Action == ActionError || llmResult.Error != nil {
		err := llmResult.Error
		if err == nil {
			err = fmt.Errorf("LLM step failed")
		}
		return StepResult{}, nil, err
	}

	// Отправляем события через iterationObserver
	lastMsg := exec.chainCtx.GetLastMessage()

	shouldSendThinking := true
	if exec.emitter != nil && exec.streamingEnabled {
		shouldSendThinking = false
	}

	if shouldSendThinking && e.iterationObserver != nil {
		e.iterationObserver.EmitThinking(ctx, lastMsg.Content)
	}

	if e.iterationObserver != nil {
		for _, tc := range lastMsg.ToolCalls {
			e.iterationObserver.EmitToolCall(ctx, tc)
		}
	}

	return llmResult, lastMsg, nil
}

// handleToolExecution выполняет tool execution шаг.
//
// Возвращает (toolResult, error).
func (e *ReActExecutor) handleToolExecution(ctx context.Context, exec *ReActExecution, iteration int) (StepResult, error) {
	// Tool Execution
	toolResult := exec.toolStep.Execute(ctx, exec.chainCtx)

	utils.Debug("Tool execution completed",
		"iteration", iteration+1,
		"action", toolResult.Action,
		"signal", toolResult.Signal,
		"error", toolResult.Error,
		"will_continue", toolResult.Action == ActionContinue)

	if toolResult.Action == ActionError || toolResult.Error != nil {
		err := toolResult.Error
		if err == nil {
			err = fmt.Errorf("tool execution failed")
		}
		return StepResult{}, err
	}

	// Отправляем EventToolResult через iterationObserver
	if e.iterationObserver != nil {
		for _, tr := range exec.toolStep.GetToolResults() {
			e.iterationObserver.EmitToolResult(ctx, tr.Name, tr.Result, time.Duration(tr.Duration)*time.Millisecond)
		}
	}

	return toolResult, nil
}

// handleToolInterruption обрабатывает прерывание во время tool execution.
//
// Добавляет interruption message и устанавливает interruption handler.
func (e *ReActExecutor) handleToolInterruption(ctx context.Context, exec *ReActExecution, interruptionMsg string, iteration int) error {
	interruptMsg := fmt.Sprintf(`🛑 USER INTERRUPTION

The user has interrupted the execution with the following message:

--- USER MESSAGE ---
%s
-------------------

Previous tool result is available in context. Please address the interruption and decide whether to continue or stop execution.`, interruptionMsg)

	if err := exec.chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: interruptMsg,
	}); err != nil {
		return fmt.Errorf("failed to append interruption message: %w", err)
	}

	promptsDir := exec.chainCtx.Input.Config.PostPromptsDir
	interruptionPath := exec.chainCtx.Input.Config.InterruptionPrompt

	interruptPrompt, promptConfig := loadInterruptionPrompt(promptsDir, interruptionPath)
	exec.chainCtx.SetActivePostPrompt(interruptPrompt, promptConfig)

	promptSource := "default"
	if interruptionPath != "" {
		promptSource = "yaml:" + interruptionPath
	}

	if e.iterationObserver != nil {
		e.iterationObserver.EmitUserInterruption(ctx, interruptionMsg, iteration+1, promptSource)
	}

	return nil
}

// checkUserInterruption проверяет прерывание между итерациями.
//
// Возвращает error если произошла ошибка или nil если продолжаем.
func (e *ReActExecutor) checkUserInterruption(ctx context.Context, exec *ReActExecution, iteration int) error {
	if exec.chainCtx.Input.UserInputChan == nil {
		return nil
	}

	select {
	case userInput := <-exec.chainCtx.Input.UserInputChan:
		return e.handleToolInterruption(ctx, exec, userInput, iteration)

	case <-ctx.Done():
		return ctx.Err()

	default:
		return nil
	}
}

// finalizeSession финализирует выполнение и возвращает результат.
//
// Формирует ChainOutput и уведомляет наблюдателей.
func (e *ReActExecutor) finalizeExecution(ctx context.Context, exec *ReActExecution, iterations int) (ChainOutput, error) {
	lastMsg := exec.chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(exec.startTime).Milliseconds())

	// Отправляем EventMessage с полным результатом
	if e.iterationObserver != nil {
		e.iterationObserver.EmitMessage(ctx, result)
	}

	output := ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(exec.startTime),
		FinalState: exec.chainCtx.GetMessages(),
		DebugPath:  "",
		Signal:     exec.finalSignal,
	}

	// Notify observers: OnFinish
	for _, obs := range e.observers {
		obs.OnFinish(output, nil)
	}

	// Fill DebugPath from ChainDebugRecorder
	for _, obs := range e.observers {
		if debugRec, ok := obs.(*ChainDebugRecorder); ok {
			output.DebugPath = debugRec.GetLogPath()
			break
		}
	}

	return output, nil
}

// Helper methods for observer notifications

func (e *ReActExecutor) notifyIterationStart(iteration int) {
	for _, obs := range e.observers {
		obs.OnIterationStart(iteration + 1)
	}
}

func (e *ReActExecutor) notifyIterationEnd(iteration int) {
	for _, obs := range e.observers {
		obs.OnIterationEnd(iteration + 1)
	}
}

// notifyFinishWithError завершает выполнение с ошибкой и уведомляет наблюдателей.
func (e *ReActExecutor) notifyFinishWithError(exec *ReActExecution, err error) (ChainOutput, error) {
	// Debug финализация теперь обрабатывается ChainDebugRecorder.OnFinish

	// Notify observers: OnFinish with error (EmitterObserver отправит EventError, ChainDebugRecorder финализирует)
	for _, obs := range e.observers {
		obs.OnFinish(ChainOutput{}, err)
	}

	return ChainOutput{}, err
}

// Ensure ReActExecutor implements StepExecutor
var _ StepExecutor = (*ReActExecutor)(nil)
