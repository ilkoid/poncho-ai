// Package tui предоставляет reusable KeyMap для TUI компонентов.
package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap определяет клавиатурные сокращения для TUI.
type KeyMap struct {
	Quit          key.Binding
	ScrollUp      key.Binding
	ScrollDown    key.Binding
	ToggleHelp    key.Binding
	ConfirmInput  key.Binding
	SaveToFile    key.Binding
	ToggleDebug   key.Binding
	ShowDebugPath key.Binding // Shows path to last debug log file
	ClearLogs     key.Binding // Clears all log files (Ctrl+K)
}

// ShortHelp реализует help.KeyMap интерфейс.
func (km KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		km.ScrollUp,
		km.ScrollDown,
		km.ToggleHelp,
		km.SaveToFile,
		km.ToggleDebug,
		km.ShowDebugPath,
		km.ClearLogs,
		km.ConfirmInput,
	}
}

// FullHelp реализует help.KeyMap интерфейс.
func (km KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{
			km.ScrollUp,
			km.ScrollDown,
			km.ToggleHelp,
		},
		{
			km.ConfirmInput,
			km.SaveToFile,
			km.ToggleDebug,
			km.ShowDebugPath,
			km.ClearLogs,
		},
		{
			km.Quit,
		},
	}
}

// DefaultKeyMap возвращает дефолтный KeyMap.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c", "esc"),
			key.WithHelp("Ctrl+C", "quit"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("ctrl+u", "pgup"),
			key.WithHelp("Ctrl+U", "scroll up"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("ctrl+d", "pgdown"),
			key.WithHelp("Ctrl+D", "scroll down"),
		),
		ToggleHelp: key.NewBinding(
			key.WithKeys("ctrl+h"),
			key.WithHelp("Ctrl+H", "toggle help"),
		),
		ConfirmInput: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("Enter", "send query"),
		),
		SaveToFile: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("Ctrl+S", "save to file"),
		),
		ToggleDebug: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("Ctrl+G", "toggle debug"),
		),
		ShowDebugPath: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("Ctrl+L", "show debug log path"),
		),
		ClearLogs: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("Ctrl+K", "clear all logs"),
		),
	}
}
