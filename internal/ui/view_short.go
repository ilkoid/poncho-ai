//go:build short

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

// renderTodoPanel рендерит панель с задачами
func renderTodoPanel(manager *todo.Manager, width int) string {
	// Рендерит панель Todo со списком задач и статистикой
	return todoBorderStyle.Width(width).Render("Todo Panel")
}

func (m MainModel) View() string {
	if !m.ready {
		return "Initializing UI..."
	}

	// Формируем строку статуса (Header)
	status := fmt.Sprintf(" ACT: %s | MODEL: %s ",
		m.appState.CurrentArticleID,
		m.appState.CurrentModel,
	)

	// Растягиваем хедер на всю ширину
	header := headerStyle.
		Width(m.viewport.Width).
		Render(status)

	// Разделительная линия
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
	todoPanel := renderTodoPanel(m.appState.Todo, 40)

	// Комбинируем основной контент с Todo панелью
	return lipgloss.JoinHorizontal(lipgloss.Top,
		mainContent,
		todoPanel,
	)
}