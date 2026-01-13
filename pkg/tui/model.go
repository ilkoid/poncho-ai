// Package tui предоставляет базовый TUI для AI агентов на Bubble Tea.
//
// Это reusable библиотечный код (Rule 6), который может быть использован
// любым приложением на базе Poncho AI.
//
// Для специфичных функций (todo-панель, special commands) используйте
// internal/ui/ который расширяет этот базовый TUI.
//
// # Basic Usage
//
//	client, _ := agent.New(...)
//	tui.Run(client) // Готовый TUI из коробки!
//
// # Advanced Usage (с кастомизацией)
//
//	client, _ := agent.New(...)
//	emitter := events.NewChanEmitter(100)
//	client.SetEmitter(emitter)
//
//	model := tui.NewModel(client, emitter.Subscribe())
//	p := tea.NewProgram(model)
//	p.Run()
package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model представляет базовую TUI модель для AI агента.
//
// Реализует Bubble Tea Model интерфейс. Обеспечивает:
//   - Чат-подобный интерфейс с историей сообщений
//   - Поле ввода для запросов
//   - Отображение событий агента через events.Subscriber
//   - Базовую навигацию (скролл, Ctrl+C для выхода)
//
// Thread-safe.
//
// Правило 11: хранит родительский context.Context для распространения отмены.
//
// Для расширения функционала (todo-панель, special commands)
// используйте встраивание (embedding) в internal/ui/.
type Model struct {
	// UI компоненты Bubble Tea
	viewport viewport.Model
	textarea textarea.Model

	// Dependencies
	agent    agent.Agent
	eventSub events.Subscriber

	// Состояние
	isProcessing bool // Флаг занятости агента
	mu           sync.RWMutex

	// Опции
	title   string // Заголовок приложения
	prompt  string // Приглашение ввода
	ready   bool   // Флаг первой инициализации
	timeout time.Duration // Таймаут для agent execution

	// Правило 11: родительский контекст для распространения отмены
	ctx context.Context
}

// NewModel создаёт новую TUI модель.
//
// Rule 11: Принимает родительский контекст для распространения отмены.
//
// Parameters:
//   - ctx: Родительский контекст для распространения отмены
//   - agent: AI агент (реализует agent.Agent интерфейс)
//   - eventSub: Подписчик на события агента
//
// Возвращает модель готовую к использованию с Bubble Tea.
func NewModel(ctx context.Context, agent agent.Agent, eventSub events.Subscriber) Model {
	// Настройка поля ввода
	ta := textarea.New()
	ta.Placeholder = "Введите запрос к AI агенту..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 500
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// Настройка вьюпорта для лога
	// Размеры (0,0) обновятся при первом WindowSizeMsg
	vp := viewport.New(0, 0)
	vp.SetContent(fmt.Sprintf("%s\n%s\n",
		systemStyle("AI Agent TUI initialized."),
		systemStyle("Type your query and press Enter..."),
	))

	return Model{
		viewport:     vp,
		textarea:     ta,
		agent:        agent,
		eventSub:     eventSub,
		isProcessing: false,
		title:        "AI Agent",
		prompt:       "┃ ",
		ready:        false,
		timeout:      5 * time.Minute, // дефолтный timeout
		ctx:          ctx, // Rule 11: сохраняем родительский контекст
	}
}

// Init реализует tea.Model интерфейс.
//
// Возвращает команды для:
//   - Мигания курсора
//   - Чтения событий от агента
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		ReceiveEventCmd(m.eventSub, func(event events.Event) tea.Msg {
			return EventMsg(event)
		}),
	)
}

// Update реализует tea.Model интерфейс.
//
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала
//   - tea.KeyMsg: нажатия клавиш
//   - EventMsg: события от агента
//
// Для расширения (добавление новых сообщений) используйте
// встраивание Model в своей структуре.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case EventMsg:
		// События от агента
		return m.handleAgentEvent(events.Event(msg))

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

// handleAgentEvent обрабатывает события от агента.
func (m Model) handleAgentEvent(event events.Event) (tea.Model, tea.Cmd) {
	switch event.Type {
	case events.EventThinking:
		m.mu.Lock()
		m.isProcessing = true
		m.mu.Unlock()
		m.appendLog(systemStyle("Thinking..."))
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventThinkingChunk:
		// Обработка порции reasoning_content при streaming
		if chunkData, ok := event.Data.(events.ThinkingChunkData); ok {
			m.appendThinkingChunk(chunkData.Chunk)
		}
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventMessage:
		if msgData, ok := event.Data.(events.MessageData); ok {
			m.appendLog(aiMessageStyle("AI: ") + msgData.Content)
		}
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventError:
		if errData, ok := event.Data.(events.ErrorData); ok {
			m.appendLog(errorStyle("ERROR: ") + errData.Err.Error())
		}
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.textarea.Focus()
		return m, nil

	case events.EventDone:
		if msgData, ok := event.Data.(events.MessageData); ok {
			m.appendLog(aiMessageStyle("AI: ") + msgData.Content)
		}
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.textarea.Focus()
		return m, nil
	}

	return m, nil
}

// handleWindowSize обрабатывает изменение размера терминала.
func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	headerHeight := 1
	footerHeight := m.textarea.Height() + 2

	// Вычисляем высоту для области контента
	vpHeight := msg.Height - headerHeight - footerHeight
	if vpHeight < 0 {
		vpHeight = 0
	}

	// Вычисляем ширину
	vpWidth := msg.Width
	if vpWidth < 20 {
		vpWidth = 20 // Минимальная ширина
	}

	m.viewport.Width = vpWidth
	m.viewport.Height = vpHeight

	// Только при первом запуске
	if !m.ready {
		m.ready = true
		dimensions := fmt.Sprintf("Window: %dx%d | Viewport: %dx%d",
			msg.Width, msg.Height, vpWidth, vpHeight)
		m.appendLog(systemStyle("INFO: ") + dimensions)
	}

	m.textarea.SetWidth(vpWidth)

	return m, nil
}

// handleKeyPress обрабатывает нажатия клавиш.
func (m Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit

	case tea.KeyEnter:
		input := m.textarea.Value()
		if input == "" {
			return m, nil
		}

		// Очищаем ввод
		m.textarea.Reset()

		// Добавляем сообщение пользователя в лог
		m.appendLog(userMessageStyle("USER: ") + input)

		// Запускаем агента
		return m, m.startAgent(input)
	}

	return m, nil
}

// startAgent запускает агента с заданным запросом.
// Правило 11: использует сохранённый родительский контекст.
func (m Model) startAgent(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.contextWithTimeout(m.ctx)
		defer cancel()

		_, err := m.agent.Run(ctx, query)
		if err != nil {
			return EventMsg{
				Type: events.EventError,
				Data: events.ErrorData{Err: err},
			}
		}
		// События придут через emitter автоматически
		return nil
	}
}

// appendLog добавляет строку в лог чата.
func (m *Model) appendLog(str string) {
	newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), str)
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// appendThinkingChunk обновляет строку с thinking content.
//
// В отличие от appendLog, этот метод обновляет последнюю строку
// вместо добавления новой (для эффекта печатающегося текста).
func (m *Model) appendThinkingChunk(chunk string) {
	currentContent := m.viewport.View()
	lines := fmt.Sprintf("%s", currentContent)

	// Разбиваем на строки
	linesList := strings.Split(lines, "\n")

	// Если последняя строка начинается с "Thinking: ", обновляем её
	if len(linesList) > 0 {
		lastLine := linesList[len(linesList)-1]
		if strings.Contains(lastLine, "Thinking") {
			// Заменяем последнюю строку с новым chunk
			linesList[len(linesList)-1] = thinkingStyle("Thinking: ") + thinkingContentStyle(chunk)
		} else {
			// Добавляем новую строку
			linesList = append(linesList, thinkingStyle("Thinking: ")+thinkingContentStyle(chunk))
		}
	} else {
		// Добавляем новую строку
		linesList = []string{thinkingStyle("Thinking: ") + thinkingContentStyle(chunk)}
	}

	// Объединяем обратно
	newContent := strings.Join(linesList, "\n")
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// View реализует tea.Model интерфейс.
//
// Возвращает строковое представление TUI для рендеринга.
func (m Model) View() string {
	return fmt.Sprintf("%s\n%s",
		m.viewport.View(),
		m.textarea.View(),
	)
}

// contextWithTimeout создаёт контекст с таймаутом из настроек модели.
// Правило 11: принимает родительский контекст для распространения отмены.
func (m Model) contextWithTimeout(parentCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parentCtx, m.timeout)
}

// ===== STYLES =====

// systemStyle возвращает стиль для системных сообщений.
func systemStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")). // Серый
		Render(str)
}

// aiMessageStyle возвращает стиль для сообщений AI.
func aiMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")). // Cyan
		Bold(true).
		Render(str)
}

// errorStyle возвращает стиль для ошибок.
func errorStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")). // Red
		Bold(true).
		Render(str)
}

// userMessageStyle возвращает стиль для сообщений пользователя.
func userMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")). // Yellow
		Bold(true).
		Render(str)
}

// thinkingStyle возвращает стиль для заголовка thinking.
func thinkingStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("99")). // Purple
		Bold(true).
		Render(str)
}

// thinkingContentStyle возвращает стиль для контента thinking.
func thinkingContentStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")). // Dim gray
		Render(str)
}

// Ensure Model implements tea.Model
var _ tea.Model = (*Model)(nil)
