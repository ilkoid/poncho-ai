// Package tui предоставляет reusable UI компоненты и стили.
//
// components.go содержит общие стили для всех TUI моделей.
// Это позволяет избежать дублирования кода стилей между model.go и simple.go.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ===== SHARED STYLES =====
// Эти функции используются в model.go и simple.go для консистентного рендеринга.

// SystemStyle возвращает стиль для системных сообщений.
func SystemStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")). // Серый
		Render(str)
}

// AIMessageStyle возвращает стиль для сообщений AI.
func AIMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")). // Cyan
		Bold(true).
		Render(str)
}

// ErrorStyle возвращает стиль для ошибок.
func ErrorStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")). // Red
		Bold(true).
		Render(str)
}

// UserMessageStyle возвращает стиль для сообщений пользователя.
func UserMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")). // Yellow
		Bold(true).
		Render(str)
}

// ThinkingStyle возвращает стиль для заголовка thinking.
func ThinkingStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("99")). // Purple
		Bold(true).
		Render(str)
}

// ThinkingContentStyle возвращает стиль для контента thinking.
func ThinkingContentStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")). // Dim gray
		Render(str)
}

// ThinkingDimStyle — синоним для ThinkingContentStyle для обратной совместимости.
func ThinkingDimStyle(str string) string {
	return ThinkingContentStyle(str)
}

// ToolCallStyle возвращает стиль для вызова инструмента.
func ToolCallStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("228")). // Yellow-orange
		Render(str)
}

// ToolResultStyle возвращает стиль для результата инструмента.
func ToolResultStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("154")). // Green
		Render(str)
}

// DividerStyle возвращает горизонтальную разделительную линию.
func DividerStyle(width int) string {
	line := strings.Repeat("─", width)
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")). // Тёмно-серый
		Render(line)
}

// dividerStyle — внутренняя функция для использования в pkg/tui (неэкспортируемая).
// Оставлена для обратной совместимости с существующим кодом.
func dividerStyle(width int) string {
	return DividerStyle(width)
}

// ===== COMPONENT BUILDERS =====

// RenderStatusBar рендерит статус-бар с заданными параметрами.
//
// Parameters:
//   - title: Заголовок приложения
//   - model: Имя модели
//   - streaming: Статус streaming ("ON", "OFF", "THINKING")
//   - colors: Цветовая схема
//
// Возвращает отрендеренную строку статус-бара.
func RenderStatusBar(title, model, streaming string, colors ColorScheme) string {
	if model == "" {
		model = "N/A"
	}
	if streaming == "" {
		streaming = "OFF"
	}

	content := " " + title + " | Model: " + model + " | Streaming: " + streaming + " "

	style := lipgloss.NewStyle().
		Foreground(colors.StatusForeground).
		Background(colors.StatusBackground).
		Bold(true)

	return style.Render(content)
}
