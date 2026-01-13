// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ReActExecution — runtime состояние выполнения ReAct цикла.
//
// PHASE 3 REFACTOR: Теперь это чистый контейнер данных (data container).
// Логика исполнения вынесена в StepExecutor (ReActExecutor).
//
// Создаётся на каждый вызов Execute(), не разделяется между goroutines.
// ReActCycle (template) → создаёт → ReActExecution (runtime data)
// ReActExecution → исполняется → StepExecutor (e.g., ReActExecutor)
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
	emitter       events.Emitter
	debugRecorder *ChainDebugRecorder

	// Configuration
	streamingEnabled bool
	startTime       time.Time

	// Configuration reference (не создаём копию, читаем только)
	config *ReActCycleConfig

	// PHASE 2 REFACTOR: Трекаем финальный сигнал от выполнения
	finalSignal ExecutionSignal
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
