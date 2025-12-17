//  Структура и Init
package ui

import (
    "fmt"

    "github.com/ilkoid/poncho-ai/internal/app" // Импортируй свой app пакет

    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
)

// MainModel - главная структура UI
type MainModel struct {
    viewport viewport.Model
    textarea textarea.Model
    
    appState *app.GlobalState
    
    // err хранит ошибку запуска, если была
    err error
    
    // ready флаг для первой инициализации размеров
    ready bool
}

// InitialModel создает начальное состояние UI
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

// Init запускается один раз при старте
func (m MainModel) Init() tea.Cmd {
    return textarea.Blink // Заставляет курсор мигать
}
