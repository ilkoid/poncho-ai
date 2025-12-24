//go:build short

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// CommandResultMsg - сообщение, которое возвращает worker после работы
type CommandResultMsg struct {
	Output string
	Err    error
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Обрабатывает сообщения: изменение размера окна, нажатия клавиш, результаты команд
	return m, tea.Batch(m.textarea.Update(msg), m.viewport.Update(msg))
}

// Хелпер для добавления строки в лог и прокрутки вниз
func (m *MainModel) appendLog(str string) {
	// Добавляет строку в лог и прокручивает viewport вниз
}

// performCommand - симуляция работы (позже подключим реальный контроллер)
func performCommand(input string, state *app.GlobalState) tea.Cmd {
	return func() tea.Msg {
		// Создает контекст с таймаутом и разбирает ввод на команду и аргументы
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		parts := strings.Fields(input)
		if len(parts) == 0 {
			return nil
		}
		cmd := parts[0]
		args := parts[1:]

		switch cmd {
		case "load":
			// Загружает метаданные из S3 и классифицирует файлы
			return CommandResultMsg{Output: "Load command executed"}

		case "render":
			// Тестирует промпт, подставляя данные из загруженного артикула
			return CommandResultMsg{Output: "Render command executed"}

		case "ping":
			return CommandResultMsg{Output: "Pong! System is alive."}

		default:
			// Пробует передать в CommandRegistry если он существует
			if cmdRegistry := state.GetCommandRegistry(); cmdRegistry != nil {
				return cmdRegistry.Execute(input, state)
			}
			return CommandResultMsg{Err: fmt.Errorf("unknown command: '%s'", cmd)}
		}
	}
}