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
)

// EventType представляет тип события от агента.
type EventType string

const (
	// EventThinking отправляется когда агент начинает думать.
	EventThinking EventType = "thinking"

	// EventToolCall отправляется когда агент вызывает инструмент.
	EventToolCall EventType = "tool_call"

	// EventToolResult отправляется когда инструмент вернул результат.
	EventToolResult EventType = "tool_result"

	// EventMessage отправляется когда агент генерирует сообщение.
	EventMessage EventType = "message"

	// EventError отправляется при ошибке.
	EventError EventType = "error"

	// EventDone отправляется когда агент завершил работу.
	EventDone EventType = "done"
)

// Event представляет событие от агента.
//
// Data содержит данные события, тип зависит от EventType:
//   - EventThinking: string (запрос пользователя)
//   - EventToolCall: ToolCallData (имя инструмента, аргументы)
//   - EventToolResult: ToolResultData (результат выполнения)
//   - EventMessage: string (ответ агента)
//   - EventError: error (ошибка)
//   - EventDone: string (финальный ответ)
type Event struct {
	Type      EventType
	Data      any
	Timestamp time.Time
}

// ToolCallData содержит данные о вызове инструмента.
type ToolCallData struct {
	ToolName string
	Args     string
}

// ToolResultData содержит результат выполнения инструмента.
type ToolResultData struct {
	ToolName string
	Result   string
	Duration time.Duration
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
