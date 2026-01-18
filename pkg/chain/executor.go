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
	// 0. Notify observers: OnStart
	for _, obs := range e.observers {
		obs.OnStart(ctx, exec)
	}

	// 1. Добавляем user message в историю
	if err := exec.chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: exec.chainCtx.Input.UserQuery,
	}); err != nil {
		return e.notifyFinishWithError(exec, fmt.Errorf("failed to append user message: %w", err))
	}

	// 2. Debug запись теперь обрабатывается ChainDebugRecorder observer
	// (была добавлена в ReActCycle.Execute())

	// 3. ReAct цикл
	iterations := 0
	for iterations = 0; iterations < exec.config.MaxIterations; iterations++ {
		// Notify observers: OnIterationStart
		for _, obs := range e.observers {
			obs.OnIterationStart(iterations + 1)
		}

		// 3a. LLM Invocation
		llmResult := exec.llmStep.Execute(ctx, exec.chainCtx)

		// Обрабатываем результат
		if llmResult.Action == ActionError || llmResult.Error != nil {
			err := llmResult.Error
			if err == nil {
				err = fmt.Errorf("LLM step failed")
			}
			return e.notifyFinishWithError(exec, err)
		}

		// 3b. Отправляем события через iterationObserver (PHASE 4)
		lastMsg := exec.chainCtx.GetLastMessage()

		// Проверяем: был ли streaming?
		shouldSendThinking := true
		if exec.emitter != nil && exec.streamingEnabled {
			// Streaming был включен, EventThinkingChunk уже отправляли
			shouldSendThinking = false
		}

		if shouldSendThinking && e.iterationObserver != nil {
			e.iterationObserver.EmitThinking(ctx, lastMsg.Content)
		}

		// Отправляем EventToolCall для каждого tool call
		if e.iterationObserver != nil {
			for _, tc := range lastMsg.ToolCalls {
				e.iterationObserver.EmitToolCall(ctx, tc)
			}
		}

		// 3c. Проверяем сигнал от LLM шага
		if llmResult.Signal == SignalFinalAnswer || llmResult.Signal == SignalNeedUserInput {
			exec.finalSignal = llmResult.Signal
			// Notify observers: OnIterationEnd (для записи финального LLM call с post-prompt)
			for _, obs := range e.observers {
				obs.OnIterationEnd(iterations + 1)
			}
			break
		}

		// 3d. Проверяем есть ли tool calls
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			if exec.finalSignal == SignalNone {
				exec.finalSignal = SignalFinalAnswer
			}
			// Notify observers: OnIterationEnd (для записи финального LLM call с post-prompt)
			for _, obs := range e.observers {
				obs.OnIterationEnd(iterations + 1)
			}
			break
		}

		// 3e. Tool Execution
		toolResult := exec.toolStep.Execute(ctx, exec.chainCtx)

		// DEBUG: логируем результат tool execution
		utils.Debug("Tool execution completed",
			"iteration", iterations+1,
			"action", toolResult.Action,
			"signal", toolResult.Signal,
			"error", toolResult.Error,
			"will_continue", toolResult.Action == ActionContinue)

		// Обрабатываем результат
		if toolResult.Action == ActionError || toolResult.Error != nil {
			err := toolResult.Error
			if err == nil {
				err = fmt.Errorf("tool execution failed")
			}
			return e.notifyFinishWithError(exec, err)
		}

		// 3e-a. ПРОВЕРКА: прерывание во время tool execution (быстрая реакция)
		if toolResult.Signal == SignalUserInterruption {
			// Пользователь прервал выполнение между tool calls
			interruptMsg := fmt.Sprintf(`🛑 USER INTERRUPTION

The user has interrupted the execution with the following message:

--- USER MESSAGE ---
%s
-------------------

Previous tool result is available in context. Please address the interruption and decide whether to continue or stop execution.`, toolResult.Interruption)

			// Добавляем interruption message как user message
			if err := exec.chainCtx.AppendMessage(llm.Message{
				Role:    llm.RoleUser,
				Content: interruptMsg,
			}); err != nil {
				return e.notifyFinishWithError(exec, fmt.Errorf("failed to append interruption message: %w", err))
			}

			// Загружаем interruption handler промпт
			promptsDir := exec.chainCtx.Input.Config.PostPromptsDir
			interruptionPath := exec.chainCtx.Input.Config.InterruptionPrompt

			interruptPrompt, promptConfig := loadInterruptionPrompt(promptsDir, interruptionPath)

			// Устанавливаем interruption handler как активный post-prompt
			// Это заменит любой предыдущий post-prompt
			exec.chainCtx.SetActivePostPrompt(interruptPrompt, promptConfig)

			// Логируем прерывание
			promptSource := "default"
			if interruptionPath != "" {
				promptSource = "yaml:" + interruptionPath
			}

			// Отправляем EventUserInterruption через iterationObserver
			if e.iterationObserver != nil {
				e.iterationObserver.EmitUserInterruption(ctx, toolResult.Interruption, iterations+1, promptSource)
			}

			// Продолжаем следующую итерацию (interruption handler обработает)
			continue
		}

		// Отправляем EventToolResult через iterationObserver (PHASE 4)
		if e.iterationObserver != nil {
			for _, tr := range exec.toolStep.GetToolResults() {
				e.iterationObserver.EmitToolResult(ctx, tr.Name, tr.Result, time.Duration(tr.Duration)*time.Millisecond)
			}
		}

		// 3f. Check for user interruption between iterations (INTERRUPTION MECHANISM)
		if exec.chainCtx.Input.UserInputChan != nil {
			select {
			case userInput := <-exec.chainCtx.Input.UserInputChan:
				// Пользователь прервал выполнение
				interruptMsg := fmt.Sprintf(`🛑 USER INTERRUPTION

The user has interrupted the execution with the following message:

--- USER MESSAGE ---
%s
-------------------

Previous tool result is available in context. Please address the interruption and decide whether to continue or stop execution.`, userInput)

				// Добавляем interruption message как user message
				if err := exec.chainCtx.AppendMessage(llm.Message{
					Role:    llm.RoleUser,
					Content: interruptMsg,
				}); err != nil {
					return e.notifyFinishWithError(exec, fmt.Errorf("failed to append interruption message: %w", err))
				}

				// Загружаем interruption handler промпт
				promptsDir := exec.chainCtx.Input.Config.PostPromptsDir
				interruptionPath := exec.chainCtx.Input.Config.InterruptionPrompt

				interruptPrompt, promptConfig := loadInterruptionPrompt(promptsDir, interruptionPath)

				// Устанавливаем interruption handler как активный post-prompt
				// Это заменит любой предыдущий post-prompt
				exec.chainCtx.SetActivePostPrompt(interruptPrompt, promptConfig)

				// Логируем прерывание
				promptSource := "default"
				if interruptionPath != "" {
					promptSource = "yaml:" + interruptionPath
				}

				utils.Debug("User interruption received",
					"iteration", iterations+1,
					"user_input", userInput,
					"prompt_source", promptSource)

				// Отправляем событие прерывания
				if e.iterationObserver != nil {
					e.iterationObserver.EmitUserInterruption(ctx, userInput, iterations+1, promptSource)
				}

			case <-ctx.Done():
				// Context cancelled
				return e.notifyFinishWithError(exec, ctx.Err())

			default:
				// Нет пользовательского ввода — продолжаем
			}
		}

		// Notify observers: OnIterationEnd
		for _, obs := range e.observers {
			obs.OnIterationEnd(iterations + 1)
		}

		// DEBUG: логируем конец итерации
		lastMsg = exec.chainCtx.GetLastMessage()
		utils.Debug("Iteration ended",
			"iteration", iterations+1,
			"last_msg_role", lastMsg.Role,
			"has_tool_calls", len(lastMsg.ToolCalls) > 0,
			"max_iterations", exec.config.MaxIterations,
			"will_continue", iterations+1 < exec.config.MaxIterations)
	}

	// 4. Формируем результат
	lastMsg := exec.chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(exec.startTime).Milliseconds())

	// 5. Отправляем EventMessage с полным результатом для отображения в TUI
	if e.iterationObserver != nil {
		e.iterationObserver.EmitMessage(ctx, result)
	}

	// 7. Debug финализация теперь обрабатывается ChainDebugRecorder.OnFinish

	// 8. Возвращаем результат
	output := ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(exec.startTime),
		FinalState: exec.chainCtx.GetMessages(),
		DebugPath:  "", // Будет заполнен в ChainDebugObserver.OnFinish
		Signal:     exec.finalSignal,
	}

	// 9. Notify observers: OnFinish (EmitterObserver отправит EventDone, ChainDebugRecorder финализирует)
	for _, obs := range e.observers {
		obs.OnFinish(output, nil)
	}

	// 10. Fill DebugPath from ChainDebugRecorder (if available)
	for _, obs := range e.observers {
		if debugRec, ok := obs.(*ChainDebugRecorder); ok {
			output.DebugPath = debugRec.GetLogPath()
			break
		}
	}

	return output, nil
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
