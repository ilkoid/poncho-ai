// Рендер
package ui

import (
    "fmt"
    "github.com/charmbracelet/lipgloss"
)

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

    // Собираем всё вместе: Header + Viewport + Border + Input
    return fmt.Sprintf("%s\n%s\n%s\n%s",
        header,
        m.viewport.View(),
        border,
        m.textarea.View(),
    )
}
