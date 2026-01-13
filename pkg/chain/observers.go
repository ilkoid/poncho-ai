// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// PHASE 4 REFACTOR: Observer implementations for isolating cross-cutting concerns.
//
// This file contains observer implementations that handle:
// - Event emission to UI (EmitterObserver, EmitterIterationObserver)
//
// Observers remove cross-cutting logic from core orchestration (executor.Execute())
// and enable clean separation of concerns.

// EmitterObserver — наблюдатель который отправляет события в Emitter.
//
// # Purpose (PHASE 4 REFACTOR)
//
// EmitterObserver implements ExecutionObserver to send final lifecycle events
// to the UI through the events.Emitter interface (Port & Adapter pattern).
//
// # Event Emission
//
// - OnStart: (no event)
// - OnIterationStart: (no event)
// - OnIterationEnd: (no event)
// - OnFinish: Sends EventDone (success) or EventError (failure)
//
// # Thread Safety
//
// Thread-safe when used with thread-safe events.Emitter implementations.
// Each execution should use its own observer instance.
type EmitterObserver struct {
	emitter events.Emitter
}

// NewEmitterObserver создаёт новый EmitterObserver.
func NewEmitterObserver(emitter events.Emitter) *EmitterObserver {
	return &EmitterObserver{
		emitter: emitter,
	}
}

// OnStart вызывается в начале выполнения Execute().
func (o *EmitterObserver) OnStart(ctx context.Context, exec *ReActExecution) {
	// Нет события для начала выполнения - Execution не отправляет события старта
}

// OnIterationStart вызывается в начале каждой итерации.
func (o *EmitterObserver) OnIterationStart(iteration int) {
	// Нет события для начала итерации
}

// OnIterationEnd вызывается в конце каждой итерации.
func (o *EmitterObserver) OnIterationEnd(iteration int) {
	// События отправляются в течение итерации, не в конце
}

// OnFinish вызывается в конце выполнения Execute().
//
// Отправляет финальные события: EventDone (success) or EventError (failure).
func (o *EmitterObserver) OnFinish(result ChainOutput, err error) {
	if o.emitter == nil {
		return
	}

	ctx := context.Background()

	if err != nil {
		// Отправляем EventError
		o.emitter.Emit(ctx, events.Event{
			Type:      events.EventError,
			Data:      events.ErrorData{Err: err},
			Timestamp: time.Now(),
		})
		return
	}

	// Отправляем EventDone
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventDone,
		Data:      events.MessageData{Content: result.Result},
		Timestamp: time.Now(),
	})
}

// Ensure EmitterObserver implements ExecutionObserver
var _ ExecutionObserver = (*EmitterObserver)(nil)

// EmitterIterationObserver — наблюдатель для событий внутри итерации.
//
// # Purpose (PHASE 4 REFACTOR)
//
// EmitterIterationObserver sends events during each iteration of the ReAct loop.
// Unlike ExecutionObserver (which is notified of lifecycle events), this observer
// is called manually from executor.Execute() for specific events.
//
// # Event Emission
//
// - EmitThinking: Sends EventThinking after LLM response
// - EmitToolCall: Sends EventToolCall for each tool call from LLM
// - EmitToolResult: Sends EventToolResult after each tool execution
// - EmitMessage: Sends EventMessage with final result
//
// # Why Separate Observer?
//
// This is separate from EmitterObserver because:
//   - These events occur DURING iterations (not at lifecycle boundaries)
//   - They require iteration-specific data (query, tool calls, results)
//   - They're called manually from executor, not via observer notifications
//
// # Thread Safety
//
// Thread-safe when used with thread-safe events.Emitter implementations.
type EmitterIterationObserver struct {
	emitter events.Emitter
}

// NewEmitterIterationObserver создаёт новый EmitterIterationObserver.
func NewEmitterIterationObserver(emitter events.Emitter) *EmitterIterationObserver {
	return &EmitterIterationObserver{
		emitter: emitter,
	}
}

// EmitThinking отправляет EventThinking с контентом LLM.
func (o *EmitterIterationObserver) EmitThinking(ctx context.Context, query string) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: query},
		Timestamp: time.Now(),
	})
}

// EmitToolCall отправляет EventToolCall для каждого tool call.
func (o *EmitterIterationObserver) EmitToolCall(ctx context.Context, toolCall llm.ToolCall) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type: events.EventToolCall,
		Data: events.ToolCallData{
			ToolName: toolCall.Name,
			Args:     toolCall.Args,
		},
		Timestamp: time.Now(),
	})
}

// EmitToolResult отправляет EventToolResult для каждого выполненного tool.
func (o *EmitterIterationObserver) EmitToolResult(ctx context.Context, toolName, result string, duration time.Duration) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type: events.EventToolResult,
		Data: events.ToolResultData{
			ToolName: toolName,
			Result:   result,
			Duration: duration,
		},
		Timestamp: time.Now(),
	})
}

// EmitMessage отправляет EventMessage с текстом ответа.
func (o *EmitterIterationObserver) EmitMessage(ctx context.Context, content string) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventMessage,
		Data:      events.MessageData{Content: content},
		Timestamp: time.Now(),
	})
}
