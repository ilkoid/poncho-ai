// Package tui предоставляет color schemes и стили для TUI компонентов.
//
// ColorSchemes позволяют пользователям кастомизировать внешний вид TUI
// через SimpleUIConfig без изменения кода.
package tui

import "github.com/charmbracelet/lipgloss"

// ColorScheme определяет цвета для различных элементов TUI.
//
// Используется для кастомизации внешнего вида SimpleTui через SimpleUIConfig.
// Каждое поле - это lipgloss.Color (может быть hex, ANSI, или named color).
type ColorScheme struct {
	// Status Bar
	StatusBackground lipgloss.Color // Фон статус-бара
	StatusForeground lipgloss.Color // Текст в статус-баре

	// Messages
	SystemMessage lipgloss.Color // Системные сообщения (серый)
	UserMessage  lipgloss.Color // Сообщения пользователя (желтый)
	AIMessage    lipgloss.Color // Сообщения AI (cyan)
	ErrorMessage lipgloss.Color // Ошибки (красный)
	Thinking     lipgloss.Color // Thinking content (purple)
	ThinkingDim  lipgloss.Color // Thinking content dim gray

	// Input Area
	InputPrompt     lipgloss.Color // Приглашение ввода
	InputBackground lipgloss.Color // Фон поля ввода
	InputForeground lipgloss.Color // Текст ввода

	// UI Elements
	Border lipgloss.Color // Границы и разделители
}

// ColorSchemes предоставляет предустановленные цветовые схемы.
//
// Пользователи могут использовать их напрямую или создать свои на основе.
var ColorSchemes = map[string]ColorScheme{
	"default": {
		StatusBackground: lipgloss.Color("235"),
		StatusForeground: lipgloss.Color("252"),
		SystemMessage:    lipgloss.Color("242"),
		UserMessage:      lipgloss.Color("226"),
		AIMessage:        lipgloss.Color("86"),
		ErrorMessage:     lipgloss.Color("196"),
		Thinking:         lipgloss.Color("99"),
		ThinkingDim:      lipgloss.Color("245"),
		InputPrompt:      lipgloss.Color("252"),
		InputBackground:  lipgloss.Color(""),
		InputForeground:  lipgloss.Color("252"),
		Border:           lipgloss.Color("240"),
	},
	"dark": {
		StatusBackground: lipgloss.Color("0"),
		StatusForeground: lipgloss.Color("15"),
		SystemMessage:    lipgloss.Color("8"),
		UserMessage:      lipgloss.Color("11"),
		AIMessage:        lipgloss.Color("14"),
		ErrorMessage:     lipgloss.Color("9"),
		Thinking:         lipgloss.Color("13"),
		ThinkingDim:      lipgloss.Color("7"),
		InputPrompt:      lipgloss.Color("15"),
		InputBackground:  lipgloss.Color(""),
		InputForeground:  lipgloss.Color("15"),
		Border:           lipgloss.Color("4"),
	},
	"light": {
		StatusBackground: lipgloss.Color("255"),
		StatusForeground: lipgloss.Color("0"),
		SystemMessage:    lipgloss.Color("8"),
		UserMessage:      lipgloss.Color("130"),
		AIMessage:        lipgloss.Color("31"),
		ErrorMessage:     lipgloss.Color("1"),
		Thinking:         lipgloss.Color("90"),
		ThinkingDim:      lipgloss.Color("245"),
		InputPrompt:      lipgloss.Color("0"),
		InputBackground:  lipgloss.Color(""),
		InputForeground:  lipgloss.Color("0"),
		Border:           lipgloss.Color("8"),
	},
	"dracula": {
		StatusBackground: lipgloss.Color("#282a36"),
		StatusForeground: lipgloss.Color("#f8f8f2"),
		SystemMessage:    lipgloss.Color("#6272a4"),
		UserMessage:      lipgloss.Color("#f1fa8c"),
		AIMessage:        lipgloss.Color("#8be9fd"),
		ErrorMessage:     lipgloss.Color("#ff5555"),
		Thinking:         lipgloss.Color("#bd93f9"),
		ThinkingDim:      lipgloss.Color("#44475a"),
		InputPrompt:      lipgloss.Color("#f8f8f2"),
		InputBackground:  lipgloss.Color(""),
		InputForeground:  lipgloss.Color("#f8f8f2"),
		Border:           lipgloss.Color("#44475a"),
	},
}

// DefaultColorScheme возвращает схему по умолчанию.
//
// Используется как fallback когда схема не найдена.
func DefaultColorScheme() ColorScheme {
	return ColorSchemes["default"]
}

// GetColorScheme возвращает цветовую схему по имени.
//
// Если схема не найдена, возвращает default.
func GetColorScheme(name string) ColorScheme {
	if scheme, ok := ColorSchemes[name]; ok {
		return scheme
	}
	return DefaultColorScheme()
}
