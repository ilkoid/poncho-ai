// Package events предоставляет интерфейсы для реализации Port & Adapter паттерна.
//
// Это Port (интерфейс) для подписки на события от AI агента.
// Позволяет подключать любые UI (TUI, Web, CLI) без изменения библиотечной логики.
//
// # Port & Adapter Pattern
//
//	Port — это интерфейс (Emitter, Subscriber), определённый в библиотеке.
//	Adapter — это реализация интерфейса для конкретного UI (TUI, Web, etc).
//
// # Basic Usage
//
//	// В библиотеке (pkg/agent/):
//	client.SetEmitter(&events.ChanEmitter{Events: make(chan events.Event)})
//
//	// В UI (internal/ui/):
//	sub := client.Subscribe()
//	for event := range sub.Events() {
//	    switch event.Type {
//	    case events.EventThinking:
//	        ui.showSpinner()
//	    case events.EventMessage:
//	        ui.showMessage(event.Data)
//	    }
//	}
//
// # Thread Safety
//
// Все реализации интерфейсов должны быть thread-safe.
//
// # Rule 11: Context Propagation
//
// Emitter.Emit() принимает context.Context для отмены операции.
package events

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/questions"
)

// EventType представляет тип события от агента.
type EventType string

const (
	// EventThinking отправляется когда агент начинает думать.
	EventThinking EventType = "thinking"

	// EventThinkingChunk отправляется для каждой порции reasoning_content.
	// Используется только в streaming mode с thinking enabled (Zai GLM).
	EventThinkingChunk EventType = "thinking_chunk"

	// EventToolCall отправляется когда агент вызывает инструмент.
	EventToolCall EventType = "tool_call"

	// EventToolResult отправляется когда инструмент вернул результат.
	EventToolResult EventType = "tool_result"

	// EventUserInterruption отправляется когда пользователь прерывает выполнение агента.
	// Содержит сообщение пользователя и текущую итерацию.
	EventUserInterruption EventType = "user_interruption"

	// EventUserQuestion отправляется когда tool (ask_user_question) задает вопрос пользователю.
	// Содержит вопрос и варианты ответов. TUI переключается в режим вопрос-ответ.
	EventUserQuestion EventType = "user_question"

	// EventMessage отправляется когда агент генерирует сообщение.
	EventMessage EventType = "message"

	// EventError отправляется при ошибке.
	EventError EventType = "error"

	// EventDone отправляется когда агент завершил работу.
	EventDone EventType = "done"
)

// EventData — sealed interface для данных события.
//
// Только типы из пакета events могут реализовать этот интерфейс,
// что обеспечивает compile-time type safety.
type EventData interface {
	eventData()
}

// ThinkingData содержит данные для EventThinking.
type ThinkingData struct {
	Query string
}

func (ThinkingData) eventData() {}

// ThinkingChunkData содержит данные для EventThinkingChunk.
//
// Используется для потоковой передачи reasoning_content из thinking mode (Zai GLM).
type ThinkingChunkData struct {
	// Chunk — инкрементальные данные (delta)
	Chunk string

	// Accumulated — накопленные данные (полный reasoning_content на данный момент)
	Accumulated string
}

func (ThinkingChunkData) eventData() {}

// ToolCallData содержит данные о вызове инструмента.
type ToolCallData struct {
	ToolName string
	Args     string
}

func (ToolCallData) eventData() {}

// ToolResultData содержит результат выполнения инструмента.
type ToolResultData struct {
	ToolName string
	Result   string
	Duration time.Duration
}

func (ToolResultData) eventData() {}

// UserInterruptionData содержит данные о прерывании пользователем.
type UserInterruptionData struct {
	// Message — сообщение пользователя от которого произошло прерывание
	Message string

	// Iteration — номер текущей итерации ReAct цикла
	Iteration int

	// PromptSource — источник промпта interruption handler ("yaml:path" или "default")
	PromptSource string
}

func (UserInterruptionData) eventData() {}

// QuestionData содержит данные для EventUserQuestion.
//
// Используется tool ask_user_question для задания вопросов пользователю
// с вариантами ответов.
type QuestionData struct {
	// ID — уникальный идентификатор вопроса
	ID string

	// Question — текст вопроса
	Question string

	// Options — варианты ответов (1-5)
	Options []questions.QuestionOption
}

func (QuestionData) eventData() {}

// MessageData содержит данные для EventMessage и EventDone.
type MessageData struct {
	Content string
}

func (MessageData) eventData() {}

// ErrorData содержит данные для EventError.
type ErrorData struct {
	Err error
}

func (ErrorData) eventData() {}

// Event представляет событие от агента.
//
// Data содержит типизированные данные события (EventData).
// Для каждого EventType существует соответствующий тип данных:
//   - EventThinking: ThinkingData (запрос пользователя)
//   - EventThinkingChunk: ThinkingChunkData (порция reasoning_content)
//   - EventToolCall: ToolCallData (имя инструмента, аргументы)
//   - EventToolResult: ToolResultData (результат выполнения)
//   - EventUserInterruption: UserInterruptionData (прерывание пользователем)
//   - EventUserQuestion: QuestionData (вопрос пользователю с вариантами)
//   - EventMessage: MessageData (ответ агента)
//   - EventError: ErrorData (ошибка)
//   - EventDone: MessageData (финальный ответ)
type Event struct {
	Type      EventType
	Data      EventData
	Timestamp time.Time
}

// Emitter — это Port для отправки событий.
//
// Emitter инвертирует зависимость: библиотека (pkg/agent) зависит
// от этого интерфейса, а не от конкретного UI.
//
// Rule 11: все операции должны уважать context.Context.
type Emitter interface {
	// Emit отправляет событие.
	//
	// Если context отменён, операция должна прерваться.
	// Блокирующая реализация должна возвращать ошибку context.Canceled.
	Emit(ctx context.Context, event Event)
}

// Subscriber позволяет читать события из канала.
//
// Rule 5: thread-safe операции.
type Subscriber interface {
	// Events возвращает read-only канал событий.
	//
	// Канал закрывается при вызове Close().
	Events() <-chan Event

	// Close закрывает канал событий и освобождает ресурсы.
	Close()
}
