// Package ui реализует Model компонент Bubble Tea TUI.
//
// Содержит структуру UI и функцию инициализации.
package ui

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/internal/app"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// MainModel представляет главную модель UI (Bubble Tea Model).
//
// Содержит все компоненты TUI:
//   - viewport: область лога чата (только для чтения)
//   - textarea: поле ввода пользователя
//   - appState: ссылка на глобальное состояние приложения
//   - err: ошибка инициализации (если была)
//   - ready: флаг первой инициализации размеров окна
type MainModel struct {
	viewport viewport.Model
	textarea textarea.Model

	appState *app.GlobalState

	// err хранит ошибку запуска, если была
	err error

	// ready флаг для первой инициализации размеров
	ready bool
}

// InitialModel создает начальное состояние UI.
//
// Инициализирует:
//   - Поле ввода с placeholder'ом
//   - Вьюпорт для лога с приветственным сообщением
//
// Принимает GlobalState для доступа к данным приложения.
func InitialModel(state *app.GlobalState) MainModel {
    // 1. Настройка поля ввода
    ta := textarea.New()
    ta.Placeholder = "Введите команду (например: load 123)..."
    ta.Focus()
    ta.Prompt = "┃ "
    ta.CharLimit = 500
    ta.SetHeight(3)
    ta.ShowLineNumbers = false

    // 2. Настройка вьюпорта (лог чата)
    // Размеры (0,0) обновятся при первом событии WindowSizeMsg
    vp := viewport.New(0, 0)
    vp.SetContent(fmt.Sprintf("%s\n%s\n", 
        systemMsgStyle("Poncho AI v0.1 Initialized."),
        systemMsgStyle("System ready. Waiting for input..."),
    ))

    return MainModel{
        textarea: ta,
        viewport: vp,
        appState: state,
        ready:    false,
    }
}

// Init запускается один раз при старте Bubble Tea программы.
//
// Возвращает команду для запуска мигания курсора в поле ввода.
func (m MainModel) Init() tea.Cmd {
	return textarea.Blink
}
