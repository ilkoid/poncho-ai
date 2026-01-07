// Рендер
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// Стили для Todo панели
var (
	todoBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1).
			MarginRight(1)

	todoTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)

	taskPendingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("251"))

	taskDoneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Strikethrough(true)

	taskFailedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Strikethrough(true)

	statsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true).
			MarginTop(1)
)

// RenderTodoPanel рендерит панель с задачами для TUI.
//
// Переиспользуемая функция для отображения todo списка в любом приложении
// этого репозитория. Использует lipgloss для красивого форматирования с
// рамкой, цветами и иконками статуса.
//
// Параметры:
//   - manager: Todo Manager с задачами для отображения
//   - width: Ширина панели в символах (рекомендуется 40)
//
// Возвращает отформатированную строку готовую для вывода в TUI.
func RenderTodoPanel(manager *todo.Manager, width int) string {
	tasks := manager.GetTasks()
	pending, done, failed := manager.GetStats()

	if len(tasks) == 0 {
		return todoBorderStyle.Width(width).Render(
			todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ") + "\n" +
				taskPendingStyle.Render("Нет активных задач"),
		)
	}

	var content strings.Builder
	content.WriteString(todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ"))
	content.WriteString("\n\n")

	for _, task := range tasks {
		var statusIcon string
		var taskStyle lipgloss.Style

		switch task.Status {
		case todo.StatusDone:
			statusIcon = "✓"
			taskStyle = taskDoneStyle
		case todo.StatusFailed:
			statusIcon = "✗"
			taskStyle = taskFailedStyle
		default:
			statusIcon = "○"
			taskStyle = taskPendingStyle
		}

		content.WriteString(fmt.Sprintf("%s %d. %s\n",
			statusIcon, task.ID,
			taskStyle.Render(task.Description)))

		if task.Status == todo.StatusFailed && task.Metadata != nil {
			if err, ok := task.Metadata["error"].(string); ok {
				content.WriteString(fmt.Sprintf("   %s\n",
					taskFailedStyle.Render("Ошибка: "+err)))
			}
		}
	}

	content.WriteString("\n")
	content.WriteString(statsStyle.Render(
		fmt.Sprintf("Выполнено: %d | В работе: %d | Провалено: %d",
			done, pending, failed)))

	return todoBorderStyle.Width(width).Render(content.String())
}

func (m MainModel) View() string {
	if !m.ready {
		return "Initializing UI..."
	}

	// Формируем строку статуса (Header)
	m.mu.RLock()
	status := fmt.Sprintf(" ACT: %s | MODEL: %s ",
		m.currentArticleID,
		m.currentModel,
	)
	m.mu.RUnlock()

	// Хедер на ширину вьюпорта (уже вычтена todo панель)
	header := headerStyle.
		Width(m.viewport.Width).
		Render(status)

	// Разделительная линия на ширину вьюпорта
	border := lipgloss.NewStyle().
		Foreground(grayColor).
		Width(m.viewport.Width).
		Render("──────────────────────────────────────────────────")

	// Создаем основной контент
	mainContent := fmt.Sprintf("%s\n%s\n%s\n%s",
		header,
		m.viewport.View(),
		border,
		m.textarea.View(),
	)

	// Добавляем Todo панель справа
	// REFACTORED 2026-01-04: m.coreState.Todo → m.coreState.GetTodoManager()
	todoPanel := RenderTodoPanel(m.coreState.GetTodoManager(), 40)

	// Комбинируем основной контент с Todo панелью
	return lipgloss.JoinHorizontal(lipgloss.Top,
		mainContent,
		todoPanel,
	)
}
