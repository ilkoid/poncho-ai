// Package tui предоставляет todo операции для InterruptionModel.
package tui

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// updateTodosFromState обновляет todo list из CoreState.
//
// ⚠️ MOVED to InterruptionModel (Phase 3B): Теперь является методом InterruptionModel.
//
// ⚠️ FIXED: Используем прямое приведение типа к *state.CoreState вместо сложного
// interface type assertion, который мог отказать без сообщений об ошибке.
func (m *InterruptionModel) updateTodosFromState() {
	if m.coreState == nil {
		return
	}

	// Direct type assertion to *state.CoreState
	// Используем прямой cast вместо анонимного интерфейса для надежности
	cs, ok := m.coreState.(*state.CoreState)
	if !ok || cs == nil {
		return
	}

	todoMgr := cs.GetTodoManager()
	if todoMgr == nil {
		return
	}

	m.todos = todoMgr.GetTasks()
}

// renderTodoAsTextLines форматирует todo list как текст для отображения в TUI.
//
// ⚠️ MOVED to InterruptionModel (Phase 3B): Теперь является методом InterruptionModel.
func (m *InterruptionModel) renderTodoAsTextLines() []string {
	if len(m.todos) == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, "")

	for i, t := range m.todos {
		prefix := "  "
		switch t.Status {
		case todo.StatusDone:
			prefix = "✓"
		case todo.StatusFailed:
			prefix = "✗"
		case todo.StatusPending:
			prefix = "○"
		}
		lines = append(lines, fmt.Sprintf("  %s [%d] %s", prefix, i+1, t.Description))
	}

	lines = append(lines, "")
	return lines
}
