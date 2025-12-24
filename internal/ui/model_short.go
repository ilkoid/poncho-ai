//go:build short

package ui

import (
    "fmt"

    "github.com/ilkoid/poncho-ai/internal/app"
    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
)

// MainModel - главная структура UI
type MainModel struct {
    viewport viewport.Model
    textarea textarea.Model
    appState *app.GlobalState
    err      error
    ready    bool
}

// InitialModel создает начальное состояние UI
func InitialModel(state *app.GlobalState) MainModel {
    // Создает и настраивает начальное состояние UI с textarea и viewport
    return MainModel{
        textarea: textarea.New(),
        viewport: viewport.New(0, 0),
        appState: state,
        ready:    false,
    }
}

// Init запускается один раз при старте
func (m MainModel) Init() tea.Cmd {
    // Инициализирует UI и возвращает команду для мигания курсора
    return textarea.Blink
}