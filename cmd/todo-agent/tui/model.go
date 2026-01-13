// Package tui реализует Model компонент Bubble Tea TUI для Todo Agent.
package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// agentResultMsg хранит результат работы агента для передачи через канал
type agentResultMsg struct {
	result string
	err    error
}

// agentState хранит состояние агента, требующее синхронизации.
type agentState struct {
	mu       sync.Mutex
	running  bool
	resultCh chan agentResultMsg
}

func (s *agentState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *agentState) tryStart(resultCh chan agentResultMsg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.resultCh = resultCh
	s.running = true
	return true
}

func (s *agentState) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.resultCh = nil
}

// ToolExecEntry представляет запись о выполнении инструмента.
type ToolExecEntry struct {
	ToolName string
	Args     string
	Result   string
	Duration time.Duration
	Status   string // "running", "done", "error"
}

// focusMode определяет где находится фокус ввода
type focusMode int

const (
	focusInput focusMode = iota // Фокус на поле ввода
	focusViewport               // Фокус на вьюпорте (для скролла)
)

// Model представляет TUI модель Todo Agent.
type Model struct {
	// BubbleTea компоненты
	viewport viewport.Model
	textarea textarea.Model

	// Agent компоненты
	client     *agent.Client
	currentMsg string

	// Состояние
	agent        *agentState
	todos        []todo.Task
	trace        []ToolExecEntry
	output       []string
	isProcessing bool

	// UI размеры
	width  int
	height int

	// Thread-safe ошибка
	err atomic.Value // хранит error

	// Флаг готовности
	ready bool

	// Port & Adapter: подписчик на события агента
	eventSub events.Subscriber

	// Имя текущей модели для отображения
	currentModel string

	// Текущий артикул для отображения
	currentArticle string

	// Режим фокуса (input или viewport)
	focus focusMode
}

// InitialModel создает начальное состояние UI.
func InitialModel(client *agent.Client, currentModel string, eventSub events.Subscriber) Model {
	// 1. Настройка поля ввода
	ta := textarea.New()
	ta.Placeholder = "Введите задачу (например: проверь доступность WB API)..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 500
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// 2. Настройка вьюпорта (лог чата)
	vp := viewport.New(0, 0)
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#86AAEC")).Bold(true)
	vp.SetContent(fmt.Sprintf("%s\n%s\n%s\n",
		systemStyle.Render("Todo Agent v1.0"),
		systemStyle.Render("AI ассистент для планирования и выполнения задач."),
		systemStyle.Render("Готов к работе. Введите задачу..."),
	))

	return Model{
		textarea:      ta,
		viewport:      vp,
		client:        client,
		agent:         &agentState{},
		todos:         []todo.Task{},
		trace:         []ToolExecEntry{},
		output:        []string{},
		isProcessing:  false,
		ready:         false,
		eventSub:      eventSub,
		currentModel:  currentModel,
		focus:         focusInput, // Начинаем с фокуса на вводе
	}
}

func (m Model) Init() tea.Cmd {
	// Запускаем event listener (блокирующее чтение из канала)
	return tea.Batch(
		textarea.Blink,
		m.waitForEventCmd(), // Блокирующая операция - ждёт события
	)
}

// wrappedEventMsg обёртка для событий агента, чтобы избежать конфликта с pkg/tui.EventMsg
type wrappedEventMsg struct {
	Event events.Event
}

func wrapEvent(event events.Event) tea.Msg {
	return wrappedEventMsg{Event: event}
}

// waitForEventCmd возвращает Cmd для БЛОКИРУЮЩЕГО чтения событий.
// Это правильный паттерн для Bubble Tea - Cmd блокирует пока нет событий,
// а когда событие приходит, отправляет его в Update, где мы перезапускаем Cmd.
func (m Model) waitForEventCmd() tea.Cmd {
	return func() tea.Msg {
		// Блокирующее чтение из канала - ждём следующего события
		event, ok := <-m.eventSub.Events()
		if !ok {
			// Канал закрыт
			return tea.QuitMsg{}
		}
		// Логируем только не-chunk события (chunk слишком частые)
		if event.Type != events.EventThinkingChunk {
			utils.Debug("waitForEventCmd: received event", "event_type", event.Type)
		}
		return wrapEvent(event)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyCtrlS:
			// Сохранить вывод в файл
			filename := m.saveOutputToFile()
			m.output = append(m.output, renderSuccess(fmt.Sprintf("💾 Сохранено в %s", filename)))
			// Обновляем viewport
			if m.ready {
				content := m.buildContent()
				m.viewport.SetContent(content)
				m.viewport.GotoBottom()
			}
			return m, nil

		case tea.KeyEsc:
			// Переключаем фокус между input и viewport
			if m.focus == focusInput {
				m.focus = focusViewport
				m.textarea.Blur()
			} else {
				m.focus = focusInput
				m.textarea.Focus()
			}
			return m, nil

		case tea.KeyEnter:
			// Отправляем запрос агенту (только в режиме ввода)
			if m.focus == focusInput && m.textarea.Value() != "" && !m.agent.isRunning() {
				query := strings.TrimSpace(m.textarea.Value())
				m.textarea.Reset()

				utils.Info("TUI: Starting agent", "query", query, "query_len", len(query))

				// Сначала отображаем сообщение пользователя в окне
				userMsgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
				m.output = append(m.output, userMsgStyle.Render(fmt.Sprintf("👤 Вы: %s", query)))
				m.isProcessing = true

				// Устанавливаем флаг что агент запущен
				m.agent.mu.Lock()
				m.agent.running = true
				m.agent.mu.Unlock()

				// Обновляем viewport чтобы показать сообщение пользователя
				if m.ready {
					content := m.buildContent()
					m.viewport.SetContent(content)
					m.viewport.GotoBottom()
					utils.Debug("TUI: viewport updated after user input")
				}

				// Затем запускаем агента (waitForEventCmd уже работает постоянно из Init)
				return m, tea.Batch(m.runAgent(query))
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Обновляем размеры компонентов
		m.viewport.Width = m.width - 4
		m.viewport.Height = m.height - 8
		m.textarea.SetWidth(m.width - 4)

	case agentResultMsg:
		m.agent.stop()
		m.isProcessing = false

		if msg.err != nil {
			m.setErr(msg.err)
			m.output = append(m.output, renderError(fmt.Sprintf("Ошибка: %v", msg.err)))
		}
		// Результат не добавляем сюда - он приходит через EventMessage

		// Обновляем todo list из CoreState
		m.updateTodosFromState()

	case wrappedEventMsg:
		// Обработка событий от агента - напрямую модифицируем m.output
		// НЕ используем handleAgentEvent так как изменения теряются при копировании
		switch msg.Event.Type {
		case events.EventThinking:
			m.output = append(m.output, renderThinking(fmt.Sprintf("🤔 Думаю: %v", msg.Event.Data)))

		case events.EventThinkingChunk:
			// Streaming reasoning content - накапливаем в последней строке
			if data, ok := msg.Event.Data.(events.ThinkingChunkData); ok {
				// Добавляем chunk к последней строке вместо создания новой
				if len(m.output) > 0 {
					// Добавляем к последней строке
					lastIdx := len(m.output) - 1
					m.output[lastIdx] += data.Chunk
				} else {
					// Если output пуст, создаем первую строку
					m.output = append(m.output, renderFaint(data.Chunk))
				}
				// Обновляем viewport для отображения изменений
				m.viewport.SetContent(strings.Join(m.output, "\n"))
				m.viewport.GotoBottom()
			}

		case events.EventToolCall:
			if data, ok := msg.Event.Data.(events.ToolCallData); ok {
				utils.Debug("TUI: EventToolCall", "tool_name", data.ToolName, "args_len", len(data.Args))
				m.trace = append(m.trace, ToolExecEntry{
					ToolName: data.ToolName,
					Args:     data.Args,
					Status:   "running",
				})
				m.output = append(m.output, renderTool(fmt.Sprintf("→ Вызываю: %s", data.ToolName)))
			}

		case events.EventToolResult:
			if data, ok := msg.Event.Data.(events.ToolResultData); ok {
				utils.Debug("TUI: EventToolResult", "tool_name", data.ToolName, "result_len", len(data.Result), "duration_ms", data.Duration.Milliseconds())
				// Обновляем последнюю запись в trace
				for i := len(m.trace) - 1; i >= 0; i-- {
					if m.trace[i].ToolName == data.ToolName && m.trace[i].Status == "running" {
						m.trace[i].Result = data.Result
						m.trace[i].Duration = data.Duration
						m.trace[i].Status = "done"
						break
					}
				}

				// Выводим результат выполнения
				m.output = append(m.output, renderSuccess(fmt.Sprintf("✓ %s: %s", data.ToolName, data.Result)))

				// Для plan_* tools также обновляем и отображаем todo list
				if strings.HasPrefix(data.ToolName, "plan_") {
					m.updateTodosFromState()
					// Добавляем todo list как текст
					todoLines := m.renderTodoAsTextLines()
					m.output = append(m.output, todoLines...)
				}

				// Для classify_and_download_s3_files обновляем текущий артикул
				if data.ToolName == "classify_and_download_s3_files" {
					m.updateCurrentArticle()
				}
			}

		case events.EventMessage:
			if msgData, ok := msg.Event.Data.(events.MessageData); ok {
				utils.Debug("TUI: EventMessage", "content_len", len(msgData.Content))
				m.output = append(m.output, renderAgentMsg(msgData.Content))
			}

		case events.EventError:
			if errData, ok := msg.Event.Data.(events.ErrorData); ok {
				utils.Debug("TUI: EventError", "error", fmt.Sprintf("%v", errData.Err))
				m.output = append(m.output, renderError(fmt.Sprintf("Ошибка: %v", errData.Err)))
			}

		case events.EventDone:
			utils.Debug("TUI: EventDone", "data_type", fmt.Sprintf("%T", msg.Event.Data))
			m.output = append(m.output, renderSuccess("── Выполнено ──"))
		}

		// Обновляем todo list после каждого события
		m.updateTodosFromState()

		// Обновляем содержимое viewport сразу после изменения output
		// Это нужно чтобы новый контент отображался немедленно
		if m.ready {
			content := m.buildContent()
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
		}

		// ВСЕГДА перезапускаем waitForEventCmd для непрерывного чтения событий
		// Tick с nil return не перезапускается автоматически!
		cmds = append(cmds, m.waitForEventCmd())

	case agentErrorMsg:
		m.agent.stop()
		m.isProcessing = false
		m.setErr(msg)
		m.output = append(m.output, renderError(fmt.Sprintf("Ошибка: %v", msg)))
	}

	// Обновляем компоненты
	var cmd tea.Cmd

	// Сначала обновляем viewport - он должен обрабатывать скролл клавиши
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	// Обновляем textarea, но НЕ передаём клавиши скролла
	if m.focus == focusInput {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			// Клавиши скролла - не передаём в textarea, передаём только остальное
			switch keyMsg.Type {
			case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
				// Эти клавиши идут в viewport (уже обработан выше)
			default:
				m.textarea, cmd = m.textarea.Update(msg)
				cmds = append(cmds, cmd)
			}
		} else {
			// Не клавиатурные сообщения - передаём в textarea
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Содержимое вьюпорта обновляется в Update() при получении событий
	// View() только рендерит текущее состояние

	// Строим Layout
	header := m.renderHeader()
	middle := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderTodoList(),
		m.renderToolTrace(),
	)
	footer := m.renderFooter()

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		middle,
		m.viewport.View(),
		footer,
	)
}

// runAgent запускает агента в отдельной горутине.
func (m Model) runAgent(query string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		result, err := m.client.Run(context.Background(), query)
		return agentResultMsg{result: result, err: err}
	})
}

// handleAgentEvent обрабатывает события от агента.
func (m *Model) handleAgentEvent(event events.Event) {
	switch event.Type {
	case events.EventThinking:
		m.appendOutput(renderThinking(fmt.Sprintf("Думаю: %v", event.Data)))

	case events.EventToolCall:
		if data, ok := event.Data.(events.ToolCallData); ok {
			m.trace = append(m.trace, ToolExecEntry{
				ToolName: data.ToolName,
				Args:     data.Args,
				Status:   "running",
			})
			m.appendOutput(renderTool(fmt.Sprintf("→ Вызываю: %s", data.ToolName)))
		}

	case events.EventToolResult:
		if data, ok := event.Data.(events.ToolResultData); ok {
			// Обновляем последнюю запись в trace
			for i := len(m.trace) - 1; i >= 0; i-- {
				if m.trace[i].ToolName == data.ToolName && m.trace[i].Status == "running" {
					m.trace[i].Result = data.Result
					m.trace[i].Duration = data.Duration
					m.trace[i].Status = "done"
					break
				}
			}

			// Выводим результат выполнения
			m.appendOutput(renderSuccess(fmt.Sprintf("✓ %s: %s", data.ToolName, data.Result)))

			// Для plan_* tools также обновляем и отображаем todo list
			if strings.HasPrefix(data.ToolName, "plan_") {
				m.updateTodosFromState()
				m.renderTodoAsText()
			}
		}

	case events.EventMessage:
		if msgData, ok := event.Data.(events.MessageData); ok {
			m.appendOutput(renderAgentMsg(msgData.Content))
		}

	case events.EventError:
		if errData, ok := event.Data.(events.ErrorData); ok {
			m.appendOutput(renderError(fmt.Sprintf("Ошибка: %v", errData.Err)))
		}

	case events.EventDone:
		m.appendOutput(renderSuccess("── Выполнено ──"))
	}
}

// updateTodosFromState обновляет todo list из CoreState.
func (m *Model) updateTodosFromState() {
	if m.client == nil {
		return
	}

	coreState := m.client.GetState()
	if coreState == nil {
		return
	}

	todoMgr := coreState.GetTodoManager()
	if todoMgr == nil {
		return
	}

	m.todos = todoMgr.GetTasks()
}

// updateCurrentArticle обновляет текущий артикул из CoreState.
func (m *Model) updateCurrentArticle() {
	if m.client == nil {
		return
	}

	coreState := m.client.GetState()
	if coreState == nil {
		return
	}

	m.currentArticle = coreState.GetCurrentArticleID()
}

// renderTodoAsTextLines форматирует todo list как текст и возвращает строки.
// Используется в Update() для прямого добавления в output.
func (m Model) renderTodoAsTextLines() []string {
	if len(m.todos) == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, "")
	lines = append(lines, renderInfo("📋 План задач:"))

	for i, t := range m.todos {
		prefix := "  "
		statusText := ""
		switch t.Status {
		case "DONE":
			prefix = renderSuccess("✓")
			statusText = renderFaint("[выполнено]")
		case "FAILED":
			prefix = renderError("✗")
			statusText = renderError("[ошибка]")
		case "PENDING":
			prefix = renderPending("○")
			statusText = renderPending("[в работе]")
		}
		lines = append(lines, fmt.Sprintf("  %s [%d] %s %s", prefix, i+1, t.Description, statusText))
	}

	lines = append(lines, "")
	return lines
}

// renderTodoAsText форматирует и выводит todo list как текст.
// УСТАРЕЛО: Используйте renderTodoAsTextLines() в Update().
func (m *Model) renderTodoAsText() {
	if len(m.todos) == 0 {
		return
	}

	var lines []string
	lines = append(lines, "")
	lines = append(lines, renderInfo("📋 План задач:"))

	for i, t := range m.todos {
		prefix := "  "
		statusText := ""
		switch t.Status {
		case "DONE":
			prefix = renderSuccess("✓")
			statusText = renderFaint("[выполнено]")
		case "FAILED":
			prefix = renderError("✗")
			statusText = renderError("[ошибка]")
		case "PENDING":
			prefix = renderPending("○")
			statusText = renderPending("[в работе]")
		}
		lines = append(lines, fmt.Sprintf("  %s [%d] %s %s", prefix, i+1, t.Description, statusText))
	}

	lines = append(lines, "")
	m.appendOutput(strings.Join(lines, "\n"))
}

// buildContent строит содержимое для вьюпорта.
func (m Model) buildContent() string {
	if len(m.output) == 0 {
		return ""
	}

	// Ширина для word wrap
	width := m.width - 4 // учитываем отступы
	if width < 40 {
		width = 40 // минимальная ширина
	}

	// Объединяем строки и делаем word wrap
	fullText := strings.Join(m.output, "\n")
	return wrapText(fullText, width)
}

// wrapText разбивает текст на строки с заданной шириной, сохраняя слова.
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	var currentLine strings.Builder
	var currentWord strings.Builder

	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsSpace(r) {
			// Конец слова - добавляем в текущую строку
			word := currentWord.String()
			currentWord.Reset()

			// Проверяем помещается ли слово
			testLine := currentLine.String() + word + string(r)
			if currentLine.Len() == 0 {
				// Первое слово в строке
				currentLine.WriteString(word)
				currentLine.WriteRune(r)
			} else if len([]rune(testLine)) <= width {
				// Слово помещается
				currentLine.WriteString(word)
				currentLine.WriteRune(r)
			} else {
				// Слово не помещается - переносим строку
				result.WriteString(currentLine.String())
				result.WriteByte('\n')
				currentLine.Reset()
				currentLine.WriteString(word)
				currentLine.WriteRune(r)
			}
		} else if r == '\n' {
			// Явный перенос строки
			if currentWord.Len() > 0 {
				word := currentWord.String()
				if len([]rune(currentLine.String()+word)) > width && currentLine.Len() > 0 {
					result.WriteString(currentLine.String())
					result.WriteByte('\n')
					currentLine.Reset()
				}
				currentLine.WriteString(word)
				currentWord.Reset()
			}
			result.WriteString(currentLine.String())
			result.WriteByte('\n')
			currentLine.Reset()
		} else {
			// Накапливаем символы слова
			currentWord.WriteRune(r)
		}

		// Последний символ - завершаем
		if i == len(runes)-1 {
			if currentWord.Len() > 0 {
				word := currentWord.String()
				if len([]rune(currentLine.String()+word)) > width && currentLine.Len() > 0 {
					result.WriteString(currentLine.String())
					result.WriteByte('\n')
					currentLine.Reset()
				}
				currentLine.WriteString(word)
			}
			result.WriteString(currentLine.String())
		}
	}

	return result.String()
}

// appendOutput добавляет строку в вывод.
func (m *Model) appendOutput(s string) {
	m.output = append(m.output, s)

	// Ограничиваем размер вывода
	if len(m.output) > 1000 {
		m.output = m.output[len(m.output)-1000:]
	}
}

// saveOutputToFile сохраняет текущий вывод в текстовый файл.
// Возвращает имя созданного файла.
func (m *Model) saveOutputToFile() string {
	// Генерируем имя файла с timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("todo_output_%s.txt", timestamp)

	// Удаляем ANSI-коды для чистого текста
	content := m.stripAnsiCodes(strings.Join(m.output, "\n"))

	// Добавляем заголовок с информацией о сессии
	header := fmt.Sprintf("Todo Agent Output\nSaved: %s\nArticle: %s\nModel: %s\n%s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		m.currentArticle,
		m.currentModel,
		strings.Repeat("=", 60))

	fullContent := header + content

	// Записываем в файл
	if err := os.WriteFile(filename, []byte(fullContent), 0644); err != nil {
		return fmt.Sprintf("ошибка: %v", err)
	}

	return filename
}

// stripAnsiCodes удаляет ANSI escape коды из строки.
func (m Model) stripAnsiCodes(s string) string {
	// Регулярка для ANSI escape последовательностей
	ansiRegex := `\x1b\[[0-9;]*[mGKH]`
	re := regexp.MustCompile(ansiRegex)
	return re.ReplaceAllString(s, "")
}

// setErr устанавливает ошибку thread-safe.
func (m *Model) setErr(err error) {
	if err != nil {
		m.err.Store(err)
	} else {
		m.err.Store((*error)(nil))
	}
}

// renderHeader рендерит заголовок.
func (m Model) renderHeader() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#86AAEC")).
		Render("Todo Agent v1.0")

	modelInfo := lipgloss.NewStyle().
		Faint(true).
		Render(fmt.Sprintf("Model: %s", m.currentModel))

	// Добавляем информацию о текущем артикуле
	articleInfo := ""
	if m.currentArticle != "" {
		articleInfo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#50FA7B")).
			Render(fmt.Sprintf(" | Article: %s", m.currentArticle))
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, title, "   ", modelInfo, articleInfo)
}

// renderTodoList рендерит список задач.
func (m Model) renderTodoList() string {
	width := m.width/2 - 4

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#86AAEC")).
		Padding(1).
		Width(width)

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#86AAEC")).
		Render("📋 Todo List")

	if len(m.todos) == 0 {
		return style.Width(width).Render(lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			renderFaint("Нет задач"),
		))
	}

	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")

	for i, t := range m.todos {
		prefix := "  "
		switch t.Status {
		case todo.StatusDone:
			prefix = renderSuccess("✓")
		case todo.StatusFailed:
			prefix = renderError("✗")
		case todo.StatusPending:
			prefix = renderPending("○")
		}

		lines = append(lines, fmt.Sprintf("%s [%d] %s", prefix, i+1, t.Description))
	}

	return style.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderToolTrace рендерит трейс выполнения инструментов.
func (m Model) renderToolTrace() string {
	width := m.width/2 - 4

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#F79AC8")).
		Padding(1).
		Width(width)

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F79AC8")).
		Render("🔧 Tool Trace")

	if len(m.trace) == 0 {
		return style.Width(width).Render(lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			renderFaint("Нет выполненных инструментов"),
		))
	}

	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")

	// Показываем последние 10 записей
	start := 0
	if len(m.trace) > 10 {
		start = len(m.trace) - 10
	}

	for i := start; i < len(m.trace); i++ {
		t := m.trace[i]
		prefix := "  "
		switch t.Status {
		case "done":
			prefix = renderSuccess("✓")
		case "error":
			prefix = renderError("✗")
		case "running":
			prefix = renderThinking("→")
		}

		line := fmt.Sprintf("%s %s", prefix, t.ToolName)
		if t.Duration > 0 {
			line += fmt.Sprintf(" (%v)", t.Duration.Round(time.Millisecond))
		}
		lines = append(lines, line)
	}

	return style.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderFooter рендерит футер с полем ввода.
func (m Model) renderFooter() string {
	inputStyle := lipgloss.NewStyle().
		Padding(1).
		Width(m.width - 4)

	var hint string
	if m.agent.isRunning() || m.isProcessing {
		hint = renderFaint("(обработка...) ")
	} else {
		scrollHint := renderFaint("↑/↓/PageUp/PageDown - скролл, ")
		if m.focus == focusViewport {
			scrollHint = renderSuccess("↑/↓/PageUp/PageDown - скролл, ")
		}
		hint = lipgloss.JoinHorizontal(lipgloss.Left,
			scrollHint,
			renderFaint("Enter - отправить, "),
			renderFaint("Esc - фокус, "),
			renderFaint("Ctrl+S - сохранить, "),
			renderFaint("Ctrl+C - выход"),
		)
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		inputStyle.Render(m.textarea.View()),
		hint,
	)
}

// Стили для оформления (функции, не переменные)
func renderSystemMsg(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#86AAEC")).Bold(true).Render(s)
}

func renderUserMsg(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")).Bold(true).Render(s)
}

func renderInfo(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA")).Render(s)
}

func renderSuccess(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Render(s)
}

func renderError(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Render(s)
}

func renderWarning(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Render(s)
}

func renderTool(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#8BE9FD")).Render(s)
}

func renderThinking(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#BD93F9")).Render(s)
}

func renderPending(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).Render(s)
}

func renderAgentMsg(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2")).Render(s)
}

func renderFaint(s string) string {
	return lipgloss.NewStyle().Faint(true).Render(s)
}

// agentErrorMsg представляет ошибку агента.
type agentErrorMsg error
