// Красота

package ui

import "github.com/charmbracelet/lipgloss"

var (
    // Цвета (можно настроить под бренд)
    primaryColor   = lipgloss.Color("62")  // Фиолетовый
    secondaryColor = lipgloss.Color("205") // Розовый
    grayColor      = lipgloss.Color("240")

    // Стили хедера
    headerStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#FFFFFF")).
        Background(primaryColor).
        Padding(0, 1).
        Bold(true)

    // Стили для сообщений в логе
    userMsgStyle = lipgloss.NewStyle().
        Foreground(secondaryColor).
        Bold(true).
        Render

    systemMsgStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#04B575")). // Зеленый
        Render
    
    errorMsgStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#FF0000")).
        Bold(true).
        Render
)
