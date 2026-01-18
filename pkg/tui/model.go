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

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
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
// ⚠️ REFACTORED (Phase 3A): Теперь встраивает BaseModel для использования primitives.
//
// Реализует Bubble Tea Model интерфейс. Обеспечивает:
//   - Чат-подобный интерфейс с историей сообщений (через ViewportManager)
//   - Поле ввода для запросов
//   - Отображение событий агента через events.Subscriber (через EventHandler)
//   - Базовую навигацию (скролл, Ctrl+C для выхода)
//   - Строку статусов со спиннером внизу (через StatusBarManager)
//   - Todo panel для отображения задач после plan_* tools
//
// Thread-safe.
//
// Rule 6 Compliance: Только reusable код. Бизнес-логика через callback из cmd/ слоя.
// Rule 11 Compliance: Хранит context.Context для распространения отмены.
//
// Для расширения функционала (special commands) используйте встраивание Model в internal/ui/.
//
// Структура после рефакторинга Phase 3A:
// - Встраивает BaseModel для общих TUI функций (viewport, status, events, debug)
// - Добавляет app-specific функциональность (todo panel)
// - Использует callback pattern для бизнес-логики (Rule 6 compliant)
type Model struct {
	// ===== BASEMODEL EMBEDDING (Phase 3A) =====
	// BaseModel предоставляет общую TUI функциональность через primitives:
	// - ViewportManager: умный скролл, resize обработка
	// - StatusBarManager: спиннер, DEBUG индикатор
	// - EventHandler: обработка событий агента
	// - DebugManager: Ctrl+G/S/L функции
	*BaseModel

	// ===== APP-SPECIFIC FIELDS =====
	// Todo list from CoreState (для отображения после plan_* tools)
	todos []todo.Task
	mu     sync.RWMutex

	// Deprecated: Прямые зависимости (для backward compatibility)
	// ⚠️ DEPRECATED: Используйте callback pattern вместо прямого доступа к агенту
	// Rule 6: Эти поля нарушают принцип reusable кода, но сохранены для совместимости
	agent     interface{} // agent.Agent - хранится как interface{} чтобы избежать импорта
	coreState interface{} // *state.CoreState - хранится как interface{} чтобы избежать импорта

	// Unique Model features (сохраняются после рефакторинга)
	timeout time.Duration // Таймаут для agent execution
	prompt  string          // Приглашение ввода (custom)

	// Remove: ready - теперь управляется BaseModel
	// Remove: title - теперь управляется BaseModel
	// Remove: customStatusExtra - теперь через BaseModel.SetCustomStatus()
	// Remove: showHelp - теперь управляется BaseModel
	// Remove: debugMode - теперь управляется BaseModel через DebugManager
	// Remove: keys - теперь управляется BaseModel
	// Remove: logLines - теперь управляется ViewportManager
	// Remove: ctx, eventSub - теперь управляется BaseModel
	// Remove: viewport, textarea, spinner, help - теперь управляется BaseModel
}

// NewModel создаёт новую TUI модель.
//
// ⚠️ REFACTORED (Phase 3A): Теперь использует BaseModel для primitives.
//
// Rule 11: Принимает родительский контекст для распространения отмены.
//
// Parameters:
//   - ctx: Родительский контекст для распространения отмены
//   - agent: AI агент (реализует agent.Agent интерфейс) - DEPRECATED для Rule 6
//   - coreState: Framework core состояние (явная зависимость для todo operations)
//   - eventSub: Подписчик на события агента
//
// Возвращает модель готовую к использованию с Bubble Tea.
//
// Rule 6 Note: Для новых приложений используйте callback pattern вместо прямого доступа к агенту.
func NewModel(ctx context.Context, agent agent.Agent, coreState *state.CoreState, eventSub events.Subscriber) *Model {
	// Сначала создаём BaseModel через готовый конструктор
	base := NewBaseModel(ctx, eventSub)

	// Настройка textarea из BaseModel (прямой доступ к internal полям)
	base.SetTitle("AI Agent")
	base.SetCustomStatus(func() string {
		if coreState != nil && coreState.GetTodoManager() != nil {
			// TODO: добавить todo stats
		}
		return ""
	})

	return &Model{
		BaseModel:    base,
		agent:       agent,      // DEPRECATED (Rule 6 violation)
		coreState:   coreState, // Для todo operations (app-specific feature)
		todos:       []todo.Task{},
		mu:          sync.RWMutex{},
		timeout:     5 * time.Minute,
		prompt:      "┃ ",
	}
}

// Init реализует tea.Model интерфейс.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует BaseModel'у.
// Возвращает команды для:
//   - Мигания курсора
//   - Анимации спиннера
//   - Чтения событий от агента
func (m *Model) Init() tea.Cmd {
	return m.BaseModel.Init()
}

// Update реализует tea.Model интерфейс.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует BaseModel'у для базовых сообщений,
// но расширяет обработку для Model-specific сообщений (saveSuccessMsg, saveErrorMsg).
//
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала (делегируется BaseModel)
//   - tea.KeyMsg: нажатия клавиш (делегируется BaseModel)
//   - EventMsg: события от агента (делегируется BaseModel через EventHandler)
//   - spinner.TickMsg: тики спиннера (делегируется BaseModel)
//   - saveSuccessMsg/saveErrorMsg: Model-specific сообщения
//
// Для расширения (добавление новых сообщений) используйте
// встраивание Model в своей структуре.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Сначала проверяем Model-specific сообщения
	switch msg := msg.(type) {
	case saveSuccessMsg:
		m.appendLog(systemStyle(fmt.Sprintf("✓ Saved to: %s", msg.filename)))
		return m, nil

	case saveErrorMsg:
		m.appendLog(errorStyle(fmt.Sprintf("✗ Failed to save: %v", msg.err)))
		return m, nil
	}

	// Все остальные сообщения делегируем BaseModel
	baseModel, baseCmd := m.BaseModel.Update(msg)
	m.BaseModel = baseModel.(*BaseModel)
	return m, baseCmd
}

// handleAgentEvent обрабатывает события от агента.
//
// ⚠️ REFACTORED (Phase 3A): Теперь этот метод больше не нужен -
// EventHandler в BaseModel обрабатывает все события автоматически.
// Сохранен для backward compatibility, но делегирует BaseModel.
func (m *Model) handleAgentEvent(event events.Event) (tea.Model, tea.Cmd) {
	// EventHandler в BaseModel автоматически обрабатывает события
	// и обновляет ViewportManager/StatusBarManager
	m.GetEventHandler().HandleEvent(event)
	return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
		return EventMsg(e)
	})
}

// handleWindowSize обрабатывает изменение размера терминала.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует BaseModel'у.
// BaseModel.handleWindowSize уже делегирует ViewportManager.
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	// Делегируем BaseModel
	baseModel, cmd := m.BaseModel.Update(msg)
	m.BaseModel = baseModel.(*BaseModel)
	return m, cmd
}

// handleKeyPress обрабатывает нажатия клавиш.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует BaseModel'у для большинства клавиш.
// BaseModel.handleKeyPress обрабатывает: Quit, ToggleHelp, ScrollUp/Down, SaveToFile, ToggleDebug.
// Model только добавляет специфичную обработку если нужно.
func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Делегируем BaseModel - он обрабатывает все стандартные клавиши
	baseModel, cmd := m.BaseModel.Update(msg)
	m.BaseModel = baseModel.(*BaseModel)
	return m, cmd
}

// appendLog добавляет строку в лог чата.
//
// ⚠️ REFACTORED (Phase 3A): Теперь использует ViewportManager из BaseModel.
func (m *Model) appendLog(str string) {
	// ViewportManager теперь управляет logLines internally
	m.GetViewportMgr().Append(str, true)
}

// appendThinkingChunk обновляет строку с thinking content.
//
// ⚠�️ REFACTORED (Phase 3A): Теперь использует ViewportManager из BaseModel.
func (m *Model) appendThinkingChunk(chunk string) {
	// Просто добавляем новую строку с thinking content
	// ViewportManager сам управляет скроллом и форматированием
	m.GetViewportMgr().Append(
		thinkingStyle("Thinking: ")+thinkingContentStyle(chunk),
		true, // withNewline - добавляем перевод строки
	)
}

// View реализует tea.Model интерфейс.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует rendering BaseModel'у.
// Возвращает строковое представление TUI для рендеринга.
func (m *Model) View() string {
	// Получаем viewport из BaseModel
	vp := m.GetViewportMgr().GetViewport()

	// Основной контент - РАСТЯГИВАЕМ на всю высоту viewport
	content := lipgloss.NewStyle().
		Height(vp.Height).
		Width(vp.Width).
		Render(vp.View())

	var sections []string
	sections = append(sections, content)

	// Help секция (показываем если включена) + пустая строка после
	if m.ShowHelp() {
		sections = append(sections, m.renderHelp())
		sections = append(sections, "") // Пустая строка после help
	}

	// Горизонтальный разделитель между выводом и вводом
	sections = append(sections, dividerStyle(vp.Width))

	// Поле ввода из BaseModel
	sections = append(sections, m.GetTextarea().View())

	// Пустая строка перед статус баром
	sections = append(sections, "")

	// Статус бар - делегируем BaseModel
	sections = append(sections, m.BaseModel.RenderStatusLine())

	return strings.Join(sections, "\n")
}

// renderStatusLine отображает строку статусов со спиннером.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует StatusBarManager через BaseModel.
func (m *Model) renderStatusLine() string {
	return m.BaseModel.RenderStatusLine()
}

// renderHelp отображает справку по горячим клавишам.
//
// ⚠️ REFACTORED (Phase 3A): Теперь делегирует BaseModel'у.
func (m *Model) renderHelp() string {
	return m.GetHelp().View(m.BaseModel.keys)
}

// contextWithTimeout создаёт контекст с таймаутом из настроек модели.
// Правило 11: принимает родительский контекст для распространения отмены.
func (m *Model) contextWithTimeout(parentCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parentCtx, m.timeout)
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
// ⚠️ REFACTORED (Phase 3B): Теперь встраивает BaseModel напрямую, без зависимости от *agent.Client.
//
// Расширяет BaseModel возможностью прерывать выполнение агента.
// Пользователь может набрать команду и нажать Enter для отправки прерывания.
//
// Thread-safe.
//
// Пример использования:
//
//	model := NewInterruptionModel(ctx, coreState, eventSub, inputChan)
//	model.SetOnInput(createAgentLauncher(...)) // MANDATORY
//	p := tea.NewProgram(model)
//	p.Run()
type InterruptionModel struct {
	// ===== BASEMODEL EMBEDDING (Phase 3B) =====
	// BaseModel предоставляет общую TUI функциональность через primitives
	*BaseModel

	// ===== INTERRUPTION-SPECIFIC FIELDS =====
	// Канал для пользовательских прерываний (передается в agent.Execute)
	inputChan chan string

	// Todo list из CoreState (для отображения после plan_* tools)
	todos []todo.Task

	// CoreState как interface{} для Rule 6 compliance
	// Используется только для todo operations
	coreState interface{} // *state.CoreState

	// Состояние модели (thread-safe)
	mu sync.RWMutex

	// FullLLMLogging — включать полную историю сообщений в debug логах
	fullLLMLogging bool

	// Путь к последнему debug-логу (для Ctrl+L)
	lastDebugPath string

	// Callback для обработки пользовательского ввода (MANDATORY).
	// Должен быть установлен через SetOnInput() перед использованием.
	onInput func(query string) tea.Cmd
}

// NewInterruptionModel создаёт модель с поддержкой прерываний.
//
// ⚠️ REFACTORED (Phase 3B): Больше не принимает *agent.Client (Rule 6 compliance).
//
// Rule 11: Принимает родительский контекст для распространения отмены.
//
// ⚠️ ВАЖНО: После создания необходимо вызвать SetOnInput() для установки
// callback функции обработки пользовательского ввода. Без этого модель
// не будет работать (будет возвращена ошибка при нажатии Enter).
//
// Parameters:
//   - ctx: Родительский контекст
//   - coreState: Framework core состояние (для todo operations)
//   - eventSub: Подписчик на события агента (Port interface only)
//   - inputChan: Канал для пользовательских прерываний
//
// Возвращает модель готовую к использованию с Bubble Tea.
//
// Example:
//
//	model := tui.NewInterruptionModel(ctx, coreState, sub, inputChan)
//	model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true)) // MANDATORY
//	p := tea.NewProgram(model)
func NewInterruptionModel(
	ctx context.Context,
	coreState *state.CoreState,
	eventSub events.Subscriber,
	inputChan chan string,
) *InterruptionModel {
	// Создаём BaseModel напрямую (без agent dependency)
	base := NewBaseModel(ctx, eventSub)

	return &InterruptionModel{
		BaseModel:  base,
		inputChan:  inputChan,
		coreState:  coreState,
		todos:      []todo.Task{},
		mu:         sync.RWMutex{},
	}
}

// Init реализует tea.Model интерфейс для InterruptionModel.
//
// ⚠️ REFACTORED (Phase 3B): Делегирует BaseModel.Init().
func (m *InterruptionModel) Init() tea.Cmd {
	// Инициализируем BaseModel (блинк курсор, чтение событий)
	return m.BaseModel.Init()
}

// Update реализует tea.Model интерфейс для InterruptionModel.
//
// ⚠️ REFACTORED (Phase 3B): Теперь использует embedded BaseModel.
//
// Расширяет базовую обработку:
// - При Enter: если агент не выполняется, запускает новый
// - При Enter во время работы: отправляет прерывание в inputChan
// - EventUserInterruption: отображает прерывание в UI
func (m *InterruptionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case saveSuccessMsg:
		m.appendLog(systemStyle(fmt.Sprintf("✓ Saved to: %s", msg.filename)))
		return m, nil

	case saveErrorMsg:
		m.appendLog(errorStyle(fmt.Sprintf("✗ Failed to save: %v", msg.err)))
		return m, nil

	case EventMsg:
		// ПЕРЕХВАТЫВАЕМ события агента - не даем базовой модели их обработать
		return m.handleAgentEventWithInterruption(events.Event(msg))

	case tea.KeyMsg:
		// ПЕРВЫЕ: проверяем key bindings для глобальных действий (quit, help, scroll)
		// Эти клавиши должны работать всегда, независимо от фокуса textarea
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.ToggleHelp):
			m.SetShowHelp(!m.ShowHelp())
			return m, nil
		case key.Matches(msg, m.keys.ScrollUp):
			m.GetViewportMgr().ScrollUp(1)
			return m, nil
		case key.Matches(msg, m.keys.ScrollDown):
			m.GetViewportMgr().ScrollDown(1)
			return m, nil
		case key.Matches(msg, m.keys.ShowDebugPath):
			// Ctrl+L: показать путь к последнему debug-логу
			m.mu.RLock()
			debugPath := m.lastDebugPath
			m.mu.RUnlock()

			if debugPath != "" {
				m.appendLog(systemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
			} else {
				m.appendLog(systemStyle("📁 No debug log available yet"))
			}
			return m, nil
		case key.Matches(msg, m.keys.ConfirmInput):
			return m.handleKeyPressWithInterruption(msg)
		}
		// Все остальные клавиши передаем в базовую модель для ввода текста
		return m.BaseModel.Update(msg)

	default:
		// Все остальные сообщения передаем в базовую модель
		return m.BaseModel.Update(msg)
	}
}

// View реализует tea.Model интерфейс для InterruptionModel.
//
// ⚠️ REFACTORED (Phase 3B): Теперь использует embedded BaseModel + todo panel.
func (m *InterruptionModel) View() string {
	// Получаем viewport из BaseModel
	vp := m.GetViewportMgr().GetViewport()

	// Основной контент - РАСТЯГИВАЕМ на всю высоту viewport
	content := lipgloss.NewStyle().
		Height(vp.Height).
		Width(vp.Width).
		Render(vp.View())

	var sections []string
	sections = append(sections, content)

	// Help секция (показываем если включена) + пустая строка после
	if m.ShowHelp() {
		sections = append(sections, m.GetHelp().View(m.keys))
		sections = append(sections, "") // Пустая строка после help
	}

	// Горизонтальный разделитель между выводом и вводом
	sections = append(sections, dividerStyle(vp.Width))

	// Поле ввода из BaseModel
	sections = append(sections, m.GetTextarea().View())

	// Пустая строка перед статус баром
	sections = append(sections, "")

	// Статус бар - делегируем BaseModel
	sections = append(sections, m.RenderStatusLine())

	return strings.Join(sections, "\n")
}

// GetInput возвращает текущий текст из поля ввода.
func (m *InterruptionModel) GetInput() string {
	return m.GetTextarea().Value()
}

// SetCustomStatus устанавливает callback для доп. информации в статусной строке.
// Callback вызывается при каждом рендеринге и добавляется ПОСЛЕ спиннера.
func (m *InterruptionModel) SetCustomStatus(fn func() string) {
	m.BaseModel.SetCustomStatus(fn)
}

// SetTitle устанавливает заголовок TUI.
func (m *InterruptionModel) SetTitle(title string) {
	m.BaseModel.SetTitle(title)
}

// SetFullLLMLogging включает полное логирование LLM запросов с историей сообщений.
func (m *InterruptionModel) SetFullLLMLogging(enabled bool) {
	m.fullLLMLogging = enabled
}

// SetOnInput устанавливает callback для обработки пользовательского ввода.
func (m *InterruptionModel) SetOnInput(handler func(query string) tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onInput = handler
}

// appendLog добавляет строку в лог через ViewportManager.
func (m *InterruptionModel) appendLog(str string) {
	m.GetViewportMgr().Append(str, true)
}

// handleAgentEventWithInterruption обрабатывает события агента с поддержкой прерываний.
//
// ⚠️ REFACTORED (Phase 3B): Теперь использует embedded BaseModel.
//
// Правило 6 Compliance: Этот метод является чистым UI компонентом - он только
// отображает события и обновляет UI. Бизнес-логика запуска агента находится
// в callback функции, устанавливаемой через SetOnInput().
func (m *InterruptionModel) handleAgentEventWithInterruption(event events.Event) (tea.Model, tea.Cmd) {
	// DEBUG-логирование (включается по Ctrl+G)
	if m.GetDebugManager().IsEnabled() {
		m.appendLog(systemStyle(fmt.Sprintf("[DEBUG] Event: %s", event.Type)))
	}

	switch event.Type {
	case events.EventUserInterruption:
		// Пользователь прервал выполнение - отображаем сообщение
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			m.appendLog(systemStyle(fmt.Sprintf("⏸️ Interruption (iteration %d): %s", data.Iteration, truncate(data.Message, 60))))
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventToolCall:
		// DEBUG-логирование tool calls (включается по Ctrl+G)
		if m.GetDebugManager().IsEnabled() {
			if data, ok := event.Data.(events.ToolCallData); ok {
				m.appendLog(systemStyle(fmt.Sprintf("[DEBUG] Tool call: %s", data.ToolName)))
			}
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

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
		// Продолжаем слушать события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventDone:
		// Агент завершил работу - сбрасываем isProcessing через StatusBarManager
		m.GetStatusBarMgr().SetProcessing(false)

		// Фокус на textarea
		ta := m.GetTextarea()
		ta.Focus()
		m.SetTextarea(ta)

		// Добавляем визуальный разделитель после завершения для читаемости
		m.appendLog("")

		// Продолжаем слушать события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventError:
		// Сбрасываем isProcessing через StatusBarManager
		m.GetStatusBarMgr().SetProcessing(false)

		// Фокус на textarea
		ta := m.GetTextarea()
		ta.Focus()
		m.SetTextarea(ta)

		// Продолжаем слушать события (важно!)
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	default:
		// Все остальные события передаем в базовую модель (оборачиваем в EventMsg)
		_, _ = m.BaseModel.Update(EventMsg(event))
		// ВСЕГДА возвращаем WaitForEvent чтобы не терять события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})
	}
}

// handleKeyPressWithInterruption обрабатывает нажатия клавиш с поддержкой прерываний.
//
// ⚠️ REFACTORED (Phase 3B): Теперь использует embedded BaseModel.
func (m *InterruptionModel) handleKeyPressWithInterruption(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Проверяем key bindings
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.ToggleHelp):
		m.SetShowHelp(!m.ShowHelp())
		return m, nil

	case key.Matches(msg, m.keys.ScrollUp):
		m.GetViewportMgr().ScrollUp(1)
		return m, nil

	case key.Matches(msg, m.keys.ScrollDown):
		m.GetViewportMgr().ScrollDown(1)
		return m, nil

	case key.Matches(msg, m.keys.SaveToFile):
		// Ctrl+S: сохранить экран в markdown файл
		return m, m.saveToMarkdown()

	case key.Matches(msg, m.keys.ToggleDebug):
		// Ctrl+G: переключить debug режим
		debugMsg := m.GetDebugManager().ToggleDebug()
		m.appendLog(systemStyle(debugMsg))
		return m, nil

	case key.Matches(msg, m.keys.ShowDebugPath):
		// Ctrl+L: показать путь к последнему debug-логу
		m.mu.RLock()
		debugPath := m.lastDebugPath
		m.mu.RUnlock()

		if debugPath != "" {
			m.appendLog(systemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
		} else {
			m.appendLog(systemStyle("📁 No debug log available yet"))
		}
		return m, nil

	case key.Matches(msg, m.keys.ConfirmInput):
		ta := m.GetTextarea()
		input := ta.Value()
		if input == "" {
			return m, nil
		}

		ta.Reset()
		m.SetTextarea(ta)
		m.appendLog(userMessageStyle("USER: ") + input)

		// Проверяем: установлен ли callback? (MANDATORY)
		m.mu.RLock()
		handler := m.onInput
		m.mu.RUnlock()

		if handler == nil {
			// Callback не установлен - это ошибка конфигурации
			m.appendLog(errorStyle("ERROR: No input handler set. Call SetOnInput() first."))
			return m, nil
		}

		// Устанавливаем флаг обработки для показа спиннера
		m.GetStatusBarMgr().SetProcessing(true)

		// Используем callback для обработки ввода
		return m, handler(input)
	}

	return m, nil
}

// saveToMarkdown сохраняет содержимое лога в markdown файл.
func (m *InterruptionModel) saveToMarkdown() tea.Cmd {
	return func() tea.Msg {
		// Генерируем имя файла на основе текущего времени
		timestamp := time.Now().Format("20060102_150405")
		filename := fmt.Sprintf("poncho_log_%s.md", timestamp)

		// Собираем содержимое лога
		var content strings.Builder
		content.WriteString("# Poncho AI Session Log\n\n")
		content.WriteString(fmt.Sprintf("**Generated:** %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		content.WriteString("---\n\n")

		// Получаем контент из ViewportManager
		for _, line := range m.GetViewportMgr().Content() {
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

// updateTodosFromState обновляет todo list из CoreState.
//
// ⚠️ MOVED to InterruptionModel (Phase 3B): Теперь является методом InterruptionModel.
func (m *InterruptionModel) updateTodosFromState() {
	if m.coreState == nil {
		return
	}

	// Type assertion для interface{} (Rule 6 compliance)
	cs, ok := m.coreState.(interface {
		GetTodoManager() interface {
			GetTasks() []todo.Task
		}
	})
	if !ok || cs == nil {
		return
	}

	todoMgr := cs.GetTodoManager()
	if todoMgr == nil {
		return
	}

	m.todos = todoMgr.GetTasks()
}

// renderTodoAsTextLines форматирует todo list как текст для отображения в TUI.
//
// ⚠️ MOVED to InterruptionModel (Phase 3B): Теперь является методом InterruptionModel.
func (m *InterruptionModel) renderTodoAsTextLines() []string {
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

// Ensure InterruptionModel implements tea.Model
var _ tea.Model = (*InterruptionModel)(nil)

// Ensure Model implements tea.Model
var _ tea.Model = (*Model)(nil)
