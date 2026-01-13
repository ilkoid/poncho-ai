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
// PHASE 3 REFACTOR: Новая абстракция для разделения данных (ReActExecution)
// и логики исполнения (StepExecutor). Это позволяет:
//   - Добавлять новые стратегии исполнения без изменения ReActCycle
//   - Компоновать шаги в различные пайплайны (sequential, branching)
//   - Тестировать исполнители изолированно
//
// ReActExecutor — базовая реализация с классическим ReAct циклом.
// Future: ReflectionExecutor, ValidationExecutor, ParallelExecutor и т.д.
type StepExecutor interface {
	// Execute выполняет пайплайн шагов и возвращает результат.
	//
	// Принимает ReActExecution как контейнер runtime состояния,
	// но не создаёт его — этим занимается ReActCycle.Execute().
	Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error)
}

// ReActExecutor — базовая реализация StepExecutor для классического ReAct цикла.
//
// PHASE 3 REFACTOR: Выносит логику итераций из ReActExecution.Run()
// в отдельный компонент. Теперь:
//   - ReActExecution = чистый контейнер данных (runtime state)
//   - ReActExecutor = логика исполнения (iteration loop)
//
// Итерация:
//   1. LLMInvocationStep — вызов LLM
//   2. Если есть tool calls → ToolExecutionStep
//   3. Если нет tool calls или финальный сигнал → break
//
// Thread-safe: Использует ReActExecution который создаётся на каждый вызов,
// поэтому concurrent execution безопасен.
type ReActExecutor struct {
	// observers — список наблюдателей за выполнением (Phase 4)
	observers []ExecutionObserver

	// iterationObserver — наблюдатель для событий внутри итерации (PHASE 4)
	iterationObserver *EmitterIterationObserver
}

// ExecutionObserver — интерфейс для наблюдения за выполнением (Phase 4).
//
// PHASE 3 REFACTOR: Добавляем заглушку для подготовки к Phase 4.
// В Phase 4 это будет использоваться для изоляции debug и events.
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
			break
		}

		// 3d. Проверяем есть ли tool calls
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			if exec.finalSignal == SignalNone {
				exec.finalSignal = SignalFinalAnswer
			}
			break
		}

		// 3e. Tool Execution
		toolResult := exec.toolStep.Execute(ctx, exec.chainCtx)

		// Обрабатываем результат
		if toolResult.Action == ActionError || toolResult.Error != nil {
			err := toolResult.Error
			if err == nil {
				err = fmt.Errorf("tool execution failed")
			}
			return e.notifyFinishWithError(exec, err)
		}

		// Отправляем EventToolResult через iterationObserver (PHASE 4)
		if e.iterationObserver != nil {
			for _, tr := range exec.toolStep.GetToolResults() {
				e.iterationObserver.EmitToolResult(ctx, tr.Name, tr.Result, time.Duration(tr.Duration)*time.Millisecond)
			}
		}

		// Notify observers: OnIterationEnd
		for _, obs := range e.observers {
			obs.OnIterationEnd(iterations + 1)
		}
	}

	// 4. Формируем результат
	lastMsg := exec.chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(exec.startTime).Milliseconds())

	// 5. Отправляем EventMessage через iterationObserver (PHASE 4)
	if e.iterationObserver != nil {
		e.iterationObserver.EmitMessage(ctx, result)
	}

	// 6. EventDone будет отправлен через EmitterObserver.OnFinish (PHASE 4)

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
