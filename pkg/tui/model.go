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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

// ===== KEY MAP =====

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
	}
}

// Model представляет базовую TUI модель для AI агента.
//
// Реализует Bubble Tea Model интерфейс. Обеспечивает:
//   - Чат-подобный интерфейс с историей сообщений
//   - Поле ввода для запросов
//   - Отображение событий агента через events.Subscriber
//   - Базовую навигацию (скролл, Ctrl+C для выхода)
//   - Строку статусов со спиннером внизу
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
	spinner  spinner.Model
	help     help.Model

	// Dependencies
	agent     agent.Agent
	coreState *state.CoreState // Явная зависимость на CoreState (Approach 2: Lego-components)
	eventSub  events.Subscriber

	// Состояние
	isProcessing bool // Флаг занятости агента
	mu           sync.RWMutex
	todos        []todo.Task // Todo list from CoreState (for display after plan_* tools)

	// Опции
	title             string // Заголовок приложения
	prompt            string // Приглашение ввода
	ready             bool   // Флаг первой инициализации
	timeout           time.Duration // Таймаут для agent execution
	customStatusExtra func() string // Опциональный callback для доп. информации (вызывается ПОСЛЕ спиннера)
	showHelp          bool   // Показывать полную помощь
	debugMode         bool   // Режим отладки (показывать DEBUG-сообщения)

	// Key bindings
	keys KeyMap

	// Храним оригинальные (не wrapped) строки для корректного reflow при resize
	logLines []string

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
//   - coreState: Framework core состояние (явная зависимость, Approach 2)
//   - eventSub: Подписчик на события агента
//
// Возвращает модель готовую к использованию с Bubble Tea.
func NewModel(ctx context.Context, agent agent.Agent, coreState *state.CoreState, eventSub events.Subscriber) *Model {
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
	// Начальный контент добавляется в handleWindowSize при первой инициализации
	vp := viewport.New(0, 0)
	vp.SetContent("")

	// Настройка спиннера
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("86")) // Cyan

	// Настройка help
	h := help.New()
	h.ShowAll = false // По умолчанию показываем только short help

	return &Model{
		viewport:     vp,
		textarea:     ta,
		spinner:      s,
		help:         h,
		agent:        agent,
		coreState:    coreState, // Approach 2: явная зависимость
		eventSub:     eventSub,
		isProcessing: false,
		title:        "AI Agent",
		prompt:       "┃ ",
		ready:        false,
		timeout:      5 * time.Minute, // дефолтный timeout
		showHelp:     false,
		keys:         DefaultKeyMap(),
		ctx:          ctx, // Rule 11: сохраняем родительский контекст
	}
}

// Init реализует tea.Model интерфейс.
//
// Возвращает команды для:
//   - Мигания курсора
//   - Анимации спиннера
//   - Чтения событий от агента
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
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
//   - spinner.TickMsg: тики спиннера для анимации
//
// Для расширения (добавление новых сообщений) используйте
// встраивание Model в своей структуре.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		sCmd  tea.Cmd
	)

	// ПРОВЕРКА: если это клавиша прокрутки, обновляем viewport напрямую
	// не передавая msg в textarea (иначе он перехватит клавиши)
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if key.Matches(keyMsg, m.keys.ScrollUp) || key.Matches(keyMsg, m.keys.ScrollDown) {
			m.viewport, vpCmd = m.viewport.Update(msg)
			m.textarea, tiCmd = m.textarea.Update(tea.KeyMsg{}) // Пустой update для фокуса
			return m, tea.Batch(tiCmd, vpCmd)
		}
	}

	// Для WindowSizeMsg обрабатываем specially, чтобы не сбросить настройки viewport
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.textarea, tiCmd = m.textarea.Update(msg)
		return m.handleWindowSize(msg)
	}

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case EventMsg:
		// События от агента
		return m.handleAgentEvent(events.Event(msg))

	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		sCmd = cmd

	case saveSuccessMsg:
		m.appendLog(systemStyle(fmt.Sprintf("✓ Saved to: %s", msg.filename)))
		return m, nil

	case saveErrorMsg:
		m.appendLog(errorStyle(fmt.Sprintf("✗ Failed to save: %v", msg.err)))
		return m, nil
	}

	return m, tea.Batch(tiCmd, vpCmd, sCmd)
}

// handleAgentEvent обрабатывает события от агента.
func (m *Model) handleAgentEvent(event events.Event) (tea.Model, tea.Cmd) {
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
			// Добавляем перенос строки для лучшей читаемости
			content := msgData.Content
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			// DEBUG: логируем получение EventMessage
			utils.Debug("EventMessage received in TUI",
				"content_length", len(content),
				"content_preview", content[:min(200, len(content))])
			m.appendLog(aiMessageStyle("AI: ") + content)
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

	case events.EventToolResult:
		// Для plan_* tools обновляем и отображаем todo list
		if data, ok := event.Data.(events.ToolResultData); ok {
			if strings.HasPrefix(data.ToolName, "plan_") {
				m.updateTodosFromState()
				todoLines := m.renderTodoAsTextLines()
				for _, line := range todoLines {
					m.appendLog(line)
				}
			}
		}
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventDone:
		// Только обновляем состояние - контент уже отображён через EventMessage
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.textarea.Focus()
		// Добавляем пустую строку после завершения для визуального разделения
		m.appendLog("")
		return m, nil
	}

	return m, nil
}

// handleWindowSize обрабатывает изменение размера терминала.
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	headerHeight := 1
	helpHeight := 0
	if m.showHelp {
		helpHeight = 3 // Примерная высота help секции
	}
	// +1 for status line, +1 for divider line
	footerHeight := m.textarea.Height() + 2 + 1 + 1

	// Вычисляем высоту для области контента
	vpHeight := msg.Height - headerHeight - helpHeight - footerHeight
	if vpHeight < 0 {
		vpHeight = 0
	}

	// Вычисляем ширину
	vpWidth := msg.Width
	if vpWidth < 20 {
		vpWidth = 20 // Минимальная ширина
	}

	// Обновляем ширину help
	m.help.Width = vpWidth

	// Обновляем размеры viewport
	m.viewport.Height = vpHeight
	m.viewport.Width = vpWidth
	m.textarea.SetWidth(vpWidth)

	if !m.ready {
		// Первый запуск - добавляем приветственное сообщение
		m.ready = true
		dimensions := fmt.Sprintf("Window: %dx%d | Viewport: %dx%d",
			msg.Width, msg.Height, vpWidth, vpHeight)
		titleWithInfo := fmt.Sprintf("%s%s",
			systemStyle(m.title),
			systemStyle("   INFO: "+dimensions),
		)
		m.logLines = append(m.logLines, titleWithInfo)
		m.viewport.SetContent(titleWithInfo)
		m.viewport.YOffset = 0
		return m, nil
	}

	// Resize: reflow контент с новым word-wrap
	// Сохраняем текущую позицию прокрутки относительно конца контента
	totalLinesBefore := m.viewport.TotalLineCount()
	wasAtBottom := m.viewport.YOffset + m.viewport.Height >= totalLinesBefore

	var wrappedLines []string
	for _, line := range m.logLines {
		wrapped := wrap.String(line, vpWidth)
		wrappedLines = append(wrappedLines, wrapped)
	}
	fullContent := strings.Join(wrappedLines, "\n")
	m.viewport.SetContent(fullContent)

	// Восстанавливаем прокрутку: если пользователь был внизу, оставляем внизу
	// Иначе сохраняем относительную позицию
	if wasAtBottom {
		m.viewport.GotoBottom()
	} else {
		// Сохраняем позицию прокрутки (или clamp к новому размеру)
		newTotalLines := m.viewport.TotalLineCount()
		if newTotalLines > m.viewport.Height {
			// Есть что прокручивать
			if m.viewport.YOffset > newTotalLines-m.viewport.Height {
				m.viewport.YOffset = newTotalLines - m.viewport.Height
			}
		} else {
			m.viewport.YOffset = 0
		}
	}

	return m, nil
}

// handleKeyPress обрабатывает нажатия клавиш.
func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Проверяем key bindings
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.ToggleHelp):
		m.showHelp = !m.showHelp
		m.help.ShowAll = m.showHelp
		return m, nil

	case key.Matches(msg, m.keys.ScrollUp):
		m.viewport.ScrollUp(1)
		return m, nil

	case key.Matches(msg, m.keys.ScrollDown):
		m.viewport.ScrollDown(1)
		return m, nil

	case key.Matches(msg, m.keys.SaveToFile):
		return m, m.saveToMarkdown()

	case key.Matches(msg, m.keys.ToggleDebug):
		m.debugMode = !m.debugMode
		status := "OFF"
		if m.debugMode {
			status = "ON"
		}
		m.appendLog(systemStyle(fmt.Sprintf("Debug mode: %s", status)))
		return m, nil

	case key.Matches(msg, m.keys.ConfirmInput):
		input := m.textarea.Value()
		if input == "" {
			return m, nil
		}

		// Очищаем ввод
		m.textarea.Reset()

		// Добавляем пустую строку перед запросом для визуального разделения
		m.appendLog("")

		// Добавляем сообщение пользователя в лог
		m.appendLog(userMessageStyle("USER: ") + input)

		// Устанавливаем флаг обработки немедленно для показа спиннера
		m.mu.Lock()
		m.isProcessing = true
		m.mu.Unlock()

		// Запускаем агента
		return m, m.startAgent(input)
	}

	// Все остальные клавиши передаем в textarea для ввода текста
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
	// Сохраняем оригинальную строку без word-wrap для корректного reflow при resize
	m.logLines = append(m.logLines, str)

	// Word-wrap для длинных строк по ширине viewport
	width := m.viewport.Width
	if width < 20 {
		width = 20
	}
	wrapped := wrap.String(str, width)

	// Используем текущий контент viewport (он хранит полный контент внутри)
	currentContent := m.viewport.View()
	newContent := fmt.Sprintf("%s\n%s", currentContent, wrapped)

	// Применяем умную прокрутку (сохраняет позицию пользователя)
	AppendToViewport(&m.viewport, newContent)
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

	// Объединяем обратно и применяем умную прокрутку
	newContent := strings.Join(linesList, "\n")
	AppendToViewport(&m.viewport, newContent)
}

// View реализует tea.Model интерфейс.
//
// Возвращает строковое представление TUI для рендеринга.
func (m Model) View() string {
	// Основной контент - РАСТЯГИВАЕМ на всю высоту viewport
	// Это гарантирует что status bar будет внизу экрана
	content := lipgloss.NewStyle().
		Height(m.viewport.Height).
		Width(m.viewport.Width).
		Render(m.viewport.View())

	var sections []string
	sections = append(sections, content)

	// Help секция (показываем если включена) + пустая строка после
	if m.showHelp {
		sections = append(sections, m.renderHelp())
		sections = append(sections, "") // Пустая строка после help
	}

	// Горизонтальный разделитель между выводом и вводом
	sections = append(sections, dividerStyle(m.viewport.Width))

	// Поле ввода
	sections = append(sections, m.textarea.View())

	// Пустая строка перед статус баром
	sections = append(sections, "")

	// Статус бар
	sections = append(sections, m.renderStatusLine())

	return strings.Join(sections, "\n")
}

// renderStatusLine отображает строку статусов со спиннером.
func (m Model) renderStatusLine() string {
	m.mu.RLock()
	isProcessing := m.isProcessing
	m.mu.RUnlock()

	// Спиннер с цветом (с фоном как у extra info)
	var spinnerText string
	if isProcessing {
		spinnerText = m.spinner.View()
	} else {
		spinnerText = "✓ Ready"
	}

	// Рендерим спиннер с единым фоном
	spinnerPart := lipgloss.NewStyle().
		Background(lipgloss.Color("235")). // Темно-серый фон
		Padding(0, 1).                    // Отступы слева и справа
		Foreground(func() lipgloss.Color {
			if isProcessing {
				return lipgloss.Color("86") // Cyan
			}
			return lipgloss.Color("242") // Gray
		}()).
		Render(spinnerText)

	// Собираем полный текст
	var statusText string
	if m.debugMode {
		statusText = " | DEBUG"
	}
	if m.customStatusExtra != nil {
		extraInfo := m.customStatusExtra()
		if extraInfo != "" {
			statusText += " | " + extraInfo
		}
	}

	// Рендерим дополнительный текст с фоном (если есть)
	var extraPart string
	if statusText != "" {
		// DEBUG индикатор с красным фоном, остальное - серый
		extraStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("235")). // Темно-серый фон
			Padding(0, 1)                      // Отступы слева и справа

		// Если DEBUG включен - красный фон для индикатора
		if m.debugMode {
			extraPart = lipgloss.NewStyle().
				Background(lipgloss.Color("196")). // Красный фон для DEBUG
				Foreground(lipgloss.Color("15")).  // Белый текст
				Bold(true).
				Padding(0, 1).
				Render(" DEBUG ") + extraStyle.Render(statusText[7:]) // Пропускаем " | DEBUG"
		} else {
			extraPart = extraStyle.Render(statusText)
		}
	}

	// Комбинируем: спиннер + доп. информация с фоном
	return spinnerPart + extraPart
}

// renderHelp отображает справку по горячим клавишам.
func (m Model) renderHelp() string {
	// Используем bubbles/help для рендеринга
	return m.help.View(m.keys)
}

// contextWithTimeout создаёт контекст с таймаутом из настроек модели.
// Правило 11: принимает родительский контекст для распространения отмены.
func (m Model) contextWithTimeout(parentCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parentCtx, m.timeout)
}

// saveToMarkdown сохраняет содержимое лога в markdown файл.
// Файл сохраняется в текущую директорию с именем формата: poncho_log_YYYYMMDD_HHMMSS.md
func (m *Model) saveToMarkdown() tea.Cmd {
	return func() tea.Msg {
		// Генерируем имя файла на основе текущего времени
		timestamp := time.Now().Format("20060102_150405")
		filename := fmt.Sprintf("poncho_log_%s.md", timestamp)

		// Собираем содержимое лога
		// Убираем ANSI коды цветов для чистого markdown
		var content strings.Builder
		content.WriteString("# Poncho AI Session Log\n\n")
		content.WriteString(fmt.Sprintf("**Generated:** %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		content.WriteString("---\n\n")

		// Добавляем все строки лога
		for _, line := range m.logLines {
			// Удаляем ANSI коды (форматирование lipgloss)
			cleanLine := stripANSICodes(line)
			content.WriteString(cleanLine)
			content.WriteString("\n")
		}

		// Записываем в файл
		err := os.WriteFile(filename, []byte(content.String()), 0644)
		if err != nil {
			return saveErrorMsg{err: err}
		}

		return saveSuccessMsg{filename: filename}
	}
}

// stripANSICodes удаляет ANSI escape коды из строки.
func stripANSICodes(s string) string {
	// Простая реализация - убираем ESC последовательности
	// Более сложная версия может использовать регулярные выражения
	result := strings.Builder{}
	i := 0
	for i < len(s) {
		if s[i] == 0x1B { // ESC символ
			// Пропускаем до конца последовательности (до буквы/цифры)
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++ // пропускаем последний символ последовательности
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// saveSuccessMsg — сообщение об успешном сохранении.
type saveSuccessMsg struct {
	filename string
}

// saveErrorMsg — сообщение об ошибке сохранения.
type saveErrorMsg struct {
	err error
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

// dividerStyle возвращает горизонтальную разделительную линию.
func dividerStyle(width int) string {
	line := strings.Repeat("─", width)
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")). // Тёмно-серый
		Render(line)
}

// ===== INTERRUPTION MODEL =====

// InterruptionModel - модель TUI с поддержкой прерываний.
//
// Расширяет базовую Model возможностью прерывать выполнение агента.
// Пользователь может набрать команду и нажать Enter для отправки прерывания.
//
// Thread-safe.
//
// Пример использования:
//
//	client, _ := agent.New(...)
//	model := NewInterruptionModel(ctx, client, sub, inputChan, chainCfg)
//	p := tea.NewProgram(model)
//	p.Run()
type InterruptionModel struct {
	// Указатель на базовую модель (композиция через указатель)
	base *Model

	// Дополнительные поля для прерываний
	inputChan chan string       // Канал для пользовательских прерываний
	chainCfg  chain.ChainConfig // Конфигурация ReAct цикла

	// Состояние модели (thread-safe)
	mu sync.RWMutex

	// FullLLMLogging — включать полную историю сообщений в debug логах
	fullLLMLogging bool

	// Путь к последнему debug-логу (для Ctrl+L)
	lastDebugPath string

	// Callback для обработки пользовательского ввода (MANDATORY).
	// Должен быть установлен через SetOnInput() перед использованием.
	// Если не установлен - будет возвращена ошибка при нажатии Enter.
	onInput func(query string) tea.Cmd
}

// NewInterruptionModel создаёт модель с поддержкой прерываний.
//
// Rule 11: Принимает родительский контекст для распространения отмены.
//
// ⚠️ ВАЖНО: После создания необходимо вызвать SetOnInput() для установки
// callback функции обработки пользовательского ввода. Без этого модель
// не будет работать (будет возвращать ошибку при нажатии Enter).
//
// Parameters:
//   - ctx: Родительский контекст
//   - client: AI клиент (*agent.Client) - используется только для создания базовой Model
//   - coreState: Framework core состояние (явная зависимость, Approach 2)
//   - eventSub: Подписчик на события агента
//   - inputChan: Канал для пользовательских прерываний
//   - chainCfg: Конфигурация ReAct цикла
//
// Возвращает модель готовую к использованию с Bubble Tea.
//
// Example:
//
//	baseModel := tui.NewInterruptionModel(ctx, client, coreState, sub, inputChan, chainCfg)
//	baseModel.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true)) // MANDATORY
//	p := tea.NewProgram(baseModel)
func NewInterruptionModel(
	ctx context.Context,
	client *agent.Client,
	coreState *state.CoreState,
	eventSub events.Subscriber,
	inputChan chan string,
	chainCfg chain.ChainConfig,
) *InterruptionModel {
	// Создаём базовую модель
	base := NewModel(ctx, client, coreState, eventSub)

	return &InterruptionModel{
		base:       base,
		inputChan:  inputChan,
		chainCfg:   chainCfg,
		mu:         sync.RWMutex{},
	}
}

// Init реализует tea.Model интерфейс для InterruptionModel.
//
// В отличие от базовой модели, запускает агента сразу при инициализации.
func (m *InterruptionModel) Init() tea.Cmd {
	// Сначала инициализируем базовую модель (блинк курсор, чтение событий)
	baseInitCmd := m.base.Init()

	// Затем запускаем агента с первым запросом (если есть)
	startAgentCmd := func() tea.Msg {
		// Ждем первого ввода от пользователя - агент не запускаем
		return nil
	}

	return tea.Batch(baseInitCmd, startAgentCmd)
}

// Update реализует tea.Model интерфейс для InterruptionModel.
//
// Расширяет базовую обработку:
// - При Enter: если агент не выполняется, запускает новый
// - При Enter во время работы: отправляет прерывание в inputChan
// - EventUserInterruption: отображает прерывание в UI
func (m *InterruptionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EventMsg:
		// ПЕРЕХВАТЫВАЕМ события агента - не даем базовой модели их обработать
		return m.handleAgentEventWithInterruption(events.Event(msg))

	case tea.KeyMsg:
		// ПЕРВЫЕ: проверяем key bindings для глобальных действий (quit, help, scroll)
		// Эти клавиши должны работать всегда, независимо от фокуса textarea
		switch {
		case key.Matches(msg, m.base.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.base.keys.ToggleHelp):
			m.base.showHelp = !m.base.showHelp
			m.base.help.ShowAll = m.base.showHelp
			return m, nil
		case key.Matches(msg, m.base.keys.ScrollUp):
			m.base.viewport.ScrollUp(1)
			return m, nil
		case key.Matches(msg, m.base.keys.ScrollDown):
			m.base.viewport.ScrollDown(1)
			return m, nil
		case key.Matches(msg, m.base.keys.ShowDebugPath):
			// Ctrl+L: показать путь к последнему debug-логу
			m.mu.RLock()
			debugPath := m.lastDebugPath
			m.mu.RUnlock()

			if debugPath != "" {
				m.base.appendLog(systemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
			} else {
				m.base.appendLog(systemStyle("📁 No debug log available yet"))
			}
			return m, nil
		case key.Matches(msg, m.base.keys.ConfirmInput):
			return m.handleKeyPressWithInterruption(msg)
		}
		// Все остальные клавиши передаем в базовую модель для ввода текста
		newBase, baseCmd := m.base.Update(msg)
		m.base = newBase.(*Model)
		return m, baseCmd

	default:
		// Все остальные сообщения передаем в базовую модель
		newBase, baseCmd := m.base.Update(msg)
		m.base = newBase.(*Model)
		return m, baseCmd
	}
}

// View реализует tea.Model интерфейс для InterruptionModel.
//
// Делегирует отображение базовой модели.
func (m *InterruptionModel) View() string {
	return m.base.View()
}

// GetInput возвращает текущий текст из поля ввода.
func (m *InterruptionModel) GetInput() string {
	return m.base.textarea.Value()
}

// SetCustomStatus устанавливает callback для доп. информации в статусной строке.
// Callback вызывается при каждом рендеринге и добавляется ПОСЛЕ спиннера.
// Формат: "◐ | Interruptions: 0 | Queries: 1 | Duration: 21s | Status: Running..."
func (m *InterruptionModel) SetCustomStatus(fn func() string) {
	m.base.customStatusExtra = fn
}

// SetTitle устанавливает заголовок TUI.
// Заголовок отображается в приветственном сообщении при старте.
func (m *InterruptionModel) SetTitle(title string) {
	m.base.title = title
}

// SetFullLLMLogging включает полное логирование LLM запросов с историей сообщений.
//
// Используется для отладки потери контекста в диалогах.
func (m *InterruptionModel) SetFullLLMLogging(enabled bool) {
	m.fullLLMLogging = enabled
}

// SetOnInput устанавливает callback для обработки пользовательского ввода.
//
// Callback вызывается когда пользователь нажимает Enter с непустым вводом.
// Это позволяет вынести бизнес-логику запуска агента из TUI в cmd/ слой
// (Rule 6 compliance: pkg/ должен быть reusable).
//
// Parameters:
//   - handler: Функция которая получает пользовательский ввод и возвращает tea.Cmd
//
// Example:
//
//	baseModel.SetOnInput(func(query string) tea.Cmd {
//	    return func() tea.Msg {
//	        // Запускаем агента здесь
//	        output, err := client.Execute(ctx, chainInput)
//	        return tui.EventMsg(events.Event{...})
//	    }
//	})
func (m *InterruptionModel) SetOnInput(handler func(query string) tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onInput = handler
}

// handleAgentEventWithInterruption обрабатывает события агента с поддержкой прерываний.
//
// Правило 6 Compliance: Этот метод является чистым UI компонентом - он только
// отображает события и обновляет UI. Бизнес-логика запуска агента находится
// в callback функции, устанавливаемой через SetOnInput().
func (m *InterruptionModel) handleAgentEventWithInterruption(event events.Event) (tea.Model, tea.Cmd) {
	// DEBUG-логирование (включается по Ctrl+G)
	if m.base.debugMode {
		m.base.appendLog(systemStyle(fmt.Sprintf("[DEBUG] Event: %s", event.Type)))
	}

	switch event.Type {
	case events.EventUserInterruption:
		// Пользователь прервал выполнение - отображаем сообщение
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			m.base.appendLog(systemStyle(fmt.Sprintf("⏸️ Interruption (iteration %d): %s", data.Iteration, truncate(data.Message, 60))))
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventToolCall:
		// DEBUG-логирование tool calls (включается по Ctrl+G)
		if m.base.debugMode {
			if data, ok := event.Data.(events.ToolCallData); ok {
				m.base.appendLog(systemStyle(fmt.Sprintf("[DEBUG] Tool call: %s", data.ToolName)))
			}
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventToolResult:
		// Для plan_* tools обновляем и отображаем todo list
		if data, ok := event.Data.(events.ToolResultData); ok {
			if strings.HasPrefix(data.ToolName, "plan_") {
				m.base.updateTodosFromState()
				todoLines := m.base.renderTodoAsTextLines()
				for _, line := range todoLines {
					m.base.appendLog(line)
				}
			}
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventDone:
		// Агент завершил работу - сбрасываем isProcessing в базовой модели для остановки спиннера
		m.base.mu.Lock()
		m.base.isProcessing = false
		m.base.mu.Unlock()

		m.base.textarea.Focus()

		// Добавляем визуальный разделитель после завершения для читаемости
		m.base.appendLog("")

		// Продолжаем слушать события
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventError:
		// Сбрасываем isProcessing в базовой модели для остановки спиннера
		m.base.mu.Lock()
		m.base.isProcessing = false
		m.base.mu.Unlock()

		m.base.textarea.Focus()
		// Продолжаем слушать события (важно!)
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	default:
		// Все остальные события передаем в базовую модель (оборачиваем в EventMsg)
		newBase, _ := m.base.Update(EventMsg(event))
		m.base = newBase.(*Model)
		// ВСЕГДА возвращаем WaitForEvent чтобы не терять события
		return m, WaitForEvent(m.base.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})
	}
}

// handleKeyPressWithInterruption обрабатывает нажатия клавиш с поддержкой прерываний.
func (m *InterruptionModel) handleKeyPressWithInterruption(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Проверяем key bindings
	switch {
	case key.Matches(msg, m.base.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.base.keys.ToggleHelp):
		m.base.showHelp = !m.base.showHelp
		m.base.help.ShowAll = m.base.showHelp
		return m, nil

	case key.Matches(msg, m.base.keys.ScrollUp):
		m.base.viewport.ScrollUp(1)
		return m, nil

	case key.Matches(msg, m.base.keys.ScrollDown):
		m.base.viewport.ScrollDown(1)
		return m, nil

	case key.Matches(msg, m.base.keys.SaveToFile):
		return m, m.base.saveToMarkdown()

	case key.Matches(msg, m.base.keys.ToggleDebug):
		m.base.debugMode = !m.base.debugMode
		status := "OFF"
		if m.base.debugMode {
			status = "ON"
		}
		m.base.appendLog(systemStyle(fmt.Sprintf("Debug mode: %s", status)))
		return m, nil

	case key.Matches(msg, m.base.keys.ShowDebugPath):
		// Ctrl+L: показать путь к последнему debug-логу
		m.mu.RLock()
		debugPath := m.lastDebugPath
		m.mu.RUnlock()

		if debugPath != "" {
			m.base.appendLog(systemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
		} else {
			m.base.appendLog(systemStyle("📁 No debug log available yet"))
		}
		return m, nil

	case key.Matches(msg, m.base.keys.ConfirmInput):
		input := m.base.textarea.Value()
		if input == "" {
			return m, nil
		}

		m.base.textarea.Reset()
		m.base.appendLog(userMessageStyle("USER: ") + input)

		// Проверяем: установлен ли callback? (MANDATORY)
		m.mu.RLock()
		handler := m.onInput
		m.mu.RUnlock()

		if handler == nil {
			// Callback не установлен - это ошибка конфигурации
			m.base.appendLog(errorStyle("ERROR: No input handler set. Call SetOnInput() first."))
			return m, nil
		}

		// Устанавливаем флаг обработки для показа спиннера
		m.base.mu.Lock()
		m.base.isProcessing = true
		m.base.mu.Unlock()

		// Используем callback для обработки ввода
		return m, handler(input)
	}

	return m, nil
}

// truncate укорачивает строку до указанной длины (по символам, не байтам).
// Корректно обрабатывает Unicode (включая русский текст).
func truncate(s string, maxLen int) string {
	// Конвертируем в руны для корректной работы с Unicode
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// updateTodosFromState обновляет todo list из CoreState.
//
// Используется после выполнения plan_* tools для отображения
// сформированного плана задач в TUI.
// Approach 2: прямой доступ к CoreState без type assertion.
func (m *Model) updateTodosFromState() {
	if m.coreState == nil {
		return
	}

	todoMgr := m.coreState.GetTodoManager()
	if todoMgr == nil {
		return
	}

	m.todos = todoMgr.GetTasks()
}

// renderTodoAsTextLines форматирует todo list как текст для отображения в TUI.
//
// Возвращает строки с отформатированным списком задач или nil если список пуст.
func (m Model) renderTodoAsTextLines() []string {
	if len(m.todos) == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, "")
	lines = append(lines, "📋 План задач:")

	for i, t := range m.todos {
		prefix := "  "
		switch t.Status {
		case todo.StatusDone:
			prefix = "✓"
		case todo.StatusFailed:
			prefix = "✗"
		case todo.StatusPending:
			prefix = "○"
		}
		lines = append(lines, fmt.Sprintf("  %s [%d] %s", prefix, i+1, t.Description))
	}

	lines = append(lines, "")
	return lines
}

// Ensure InterruptionModel implements tea.Model
var _ tea.Model = (*InterruptionModel)(nil)

// Ensure Model implements tea.Model
var _ tea.Model = (*Model)(nil)

// min возвращает минимум из двух int
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
