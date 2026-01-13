// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ReActExecution — runtime состояние выполнения ReAct цикла.
//
// Создаётся на каждый вызов Execute(), не разделяется между goroutines.
// ReActCycle (template) → создаёт → ReActExecution (runtime)
//
// Thread-safe: Не нуждается в синхронизации так как создаётся на каждый вызов
// и никогда не разделяется между goroutines.
type ReActExecution struct {
	// Context
	ctx      context.Context
	chainCtx *ChainContext

	// Steps (локальные экземпляры для этого выполнения)
	llmStep  *LLMInvocationStep
	toolStep *ToolExecutionStep

	// Cross-cutting concerns (локальные)
	emitter        events.Emitter
	debugRecorder  *ChainDebugRecorder

	// Configuration
	streamingEnabled bool
	startTime       time.Time

	// Configuration reference (не создаём копию, читаем только)
	config *ReActCycleConfig
}

// NewReActExecution создаёт execution для одного вызова Execute().
//
// Клонирует шаги из шаблона для изоляции состояния между выполнениями.
func NewReActExecution(
	ctx context.Context,
	input ChainInput,
	llmStepTemplate *LLMInvocationStep,
	toolStepTemplate *ToolExecutionStep,
	emitter events.Emitter,
	debugRecorder *ChainDebugRecorder,
	streamingEnabled bool,
	config *ReActCycleConfig,
) *ReActExecution {
	// Создаём контекст выполнения
	chainCtx := NewChainContext(input)

	// Клонируем LLM шаг для этого выполнения (изолируем emitter, debugRecorder)
	llmStep := &LLMInvocationStep{
		modelRegistry:  llmStepTemplate.modelRegistry,
		defaultModel:   llmStepTemplate.defaultModel,
		registry:       llmStepTemplate.registry,
		systemPrompt:   llmStepTemplate.systemPrompt,
		emitter:        emitter,
		debugRecorder:  debugRecorder,
	}

	// Клонируем Tool шаг для этого выполнения
	toolStep := &ToolExecutionStep{
		registry:       toolStepTemplate.registry,
		promptLoader:   toolStepTemplate.promptLoader,
		debugRecorder:  debugRecorder,
	}

	return &ReActExecution{
		ctx:              ctx,
		chainCtx:         chainCtx,
		llmStep:          llmStep,
		toolStep:         toolStep,
		emitter:          emitter,
		debugRecorder:    debugRecorder,
		streamingEnabled: streamingEnabled,
		startTime:        time.Now(),
		config:           config,
	}
}

// Run выполняет ReAct цикл.
//
// Основная логика из ReActCycle.Execute(), но БЕЗ sync.Mutex
// так как execution не шарится между goroutines.
//
// Использует локальные экземпляры llmStep, toolStep, emitter.
func (e *ReActExecution) Run() (ChainOutput, error) {
	// 1. Добавляем user message в историю
	if err := e.chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: e.chainCtx.Input.UserQuery,
	}); err != nil {
		return ChainOutput{}, fmt.Errorf("failed to append user message: %w", err)
	}

	// 2. Начинаем debug запись
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		e.debugRecorder.Start(*e.chainCtx.Input)
	}

	// 3. ReAct цикл
	iterations := 0
	for iterations = 0; iterations < e.config.MaxIterations; iterations++ {
		// Start debug iteration для всей итерации (LLM + Tools)
		if e.debugRecorder != nil && e.debugRecorder.Enabled() {
			e.debugRecorder.StartIteration(iterations + 1)
		}

		// 3a. LLM Invocation
		action, err := e.llmStep.Execute(e.ctx, e.chainCtx)
		if err != nil {
			return e.finalizeWithError(err)
		}
		if action == ActionError {
			e.endDebugIteration()
			return e.finalizeWithError(fmt.Errorf("LLM step failed"))
		}

		// Отправляем EventThinking с контентом LLM (рассуждения)
		// НО только если НЕ был streaming (streaming отправляет EventThinkingChunk)
		lastMsg := e.chainCtx.GetLastMessage()

		// Проверяем: был ли streaming? Если в content есть reasoning_content markers
		// или если есть emitter (используется для streaming), то не дублируем
		shouldSendThinking := true
		if e.emitter != nil && e.streamingEnabled {
			// Streaming был включен, EventThinkingChunk уже отправляли
			shouldSendThinking = false
		}

		if shouldSendThinking {
			e.emitEvent(events.Event{
				Type:      events.EventThinking,
				Data:      events.ThinkingData{Query: lastMsg.Content},
				Timestamp: time.Now(),
			})
		}

		// Отправляем EventToolCall для каждого tool call
		for _, tc := range lastMsg.ToolCalls {
			e.emitEvent(events.Event{
				Type: events.EventToolCall,
				Data: events.ToolCallData{
					ToolName: tc.Name,
					Args:     tc.Args,
				},
				Timestamp: time.Now(),
			})
		}

		// 3b. Проверяем есть ли tool calls
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			e.endDebugIteration()
			break
		}

		// 3c. Tool Execution (внутри той же итерации!)
		action, err = e.toolStep.Execute(e.ctx, e.chainCtx)
		if err != nil {
			e.endDebugIteration()
			return e.finalizeWithError(err)
		}
		if action == ActionError {
			e.endDebugIteration()
			return e.finalizeWithError(fmt.Errorf("tool execution failed"))
		}

		// Отправляем EventToolResult для каждого выполненного tool
		for _, tr := range e.toolStep.GetToolResults() {
			e.emitEvent(events.Event{
				Type: events.EventToolResult,
				Data: events.ToolResultData{
					ToolName: tr.Name,
					Result:   tr.Result,
					Duration: time.Duration(tr.Duration) * time.Millisecond,
				},
				Timestamp: time.Now(),
			})
		}

		// End debug iteration после LLM + Tools
		e.endDebugIteration()
	}

	// 4. Формируем результат
	lastMsg := e.chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(e.startTime).Milliseconds())

	// 5. Отправляем EventMessage с текстом ответа
	utils.Debug("Sending EventMessage", "content_length", len(result))
	e.emitEvent(events.Event{
		Type:      events.EventMessage,
		Data:      events.MessageData{Content: result},
		Timestamp: time.Now(),
	})

	// 6. Отправляем EventDone
	utils.Debug("Sending EventDone")
	e.emitEvent(events.Event{
		Type:      events.EventDone,
		Data:      events.MessageData{Content: result},
		Timestamp: time.Now(),
	})

	// 7. Финализируем debug
	debugPath := ""
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		debugPath, _ = e.debugRecorder.Finalize(result, time.Since(e.startTime))
	}

	// 8. Возвращаем результат
	return ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(e.startTime),
		FinalState: e.chainCtx.GetMessages(),
		DebugPath:  debugPath,
	}, nil
}

// emitEvent отправляет событие если emitter установлен.
func (e *ReActExecution) emitEvent(event events.Event) {
	if e.emitter == nil {
		utils.Debug("emitEvent: emitter is nil, skipping", "event_type", event.Type)
		return
	}
	utils.Debug("emitEvent: sending", "event_type", event.Type, "has_data", event.Data != nil)
	e.emitter.Emit(e.ctx, event)
}

// endDebugIteration завершает текущую debug итерацию.
func (e *ReActExecution) endDebugIteration() {
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		e.debugRecorder.EndIteration()
	}
}

// finalizeWithError завершает выполнение с ошибкой.
func (e *ReActExecution) finalizeWithError(err error) (ChainOutput, error) {
	// Финализируем debug с ошибкой
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		e.debugRecorder.Finalize("", time.Since(e.startTime))
	}

	return ChainOutput{}, err
}
