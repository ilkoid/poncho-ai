// Package tui предоставляет reusable helpers для подключения Bubble Tea TUI к агенту.
//
// Это НЕ готовый TUI (он остаётся в internal/ui/), а reusable адаптеры
// и конвертеры для удобной работы с событиями агента.
//
// Port & Adapter паттерн:
//   - pkg/events.* — Port (интерфейсы)
//   - pkg/tui.* — Adapter helpers (переиспользуемые утилиты)
//   - internal/ui.* — Конкретная реализация TUI (app-specific)
//
// # Basic Usage
//
//	client, _ := agent.New(...)
//	sub := client.Subscribe()
//
//	// Конвертируем события агента в Bubble Tea сообщения
//	cmd := tui.ReceiveEvents(sub, func(event events.Event) tea.Msg {
//	    return EventMsg(event)
//	})
//
// Rule 6: только reusable код, без app-specific логики.
package tui

import (
	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
)

// EventMsg конвертирует events.Event в Bubble Tea сообщение.
//
// Используется в Bubble Tea Update() для обработки событий агента.
type EventMsg events.Event

// ReceiveEventCmd возвращает Bubble Tea Cmd для чтения событий из Subscriber.
//
// Функция-конвертер вызывается для каждого полученного события и должна
// возвращать Bubble Tea сообщение.
//
// Пример использования в Bubble Tea Model:
//
//	func (m model) Init() tea.Cmd {
//	    return tui.ReceiveEventCmd(subscriber, func(evt events.Event) tea.Msg {
//	        return EventMsg(evt)
//	    })
//	}
//
// Rule 11: cmd можно отменить через context.
func ReceiveEventCmd(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-sub.Events()
		if !ok {
			return tea.QuitMsg{}
		}
		return converter(event)
	}
}

// WaitForEvent возвращает Cmd который ждёт следующего события.
//
// Используется в Update() для продолжения чтения событий:
//
//	case EventMsg(event):
//	    // ... обработка события
//	    return m, tui.WaitForEvent(sub, converter)
func WaitForEvent(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd {
	return ReceiveEventCmd(sub, converter)
}
