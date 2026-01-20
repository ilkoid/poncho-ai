// Package tui предоставляет reusable helpers для подключения Bubble Tea TUI к агенту.
//
// base.go содержит BaseModel - универсальный TUI компонент на основе primitives.
// Это готовая основа для создания TUI моделей без дублирования кода.
//
// Rule 6: только reusable код, без app-specific логики.
// Rule 11: хранит context.Context для распространения отмены.
package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/tui/primitives"
	tea "github.com/charmbracelet/bubbletea"
)

// BaseModel представляет базовую TUI модель на основе primitives.
//
// Является reusable компонентом (Rule 6 compliant), который не содержит
// бизнес-логики и зависит только от абстракций (pkg/events).
//
// Использует primitives для всех операций:
//   - ViewportManager: управление viewport с умным скроллом
//   - StatusBarManager: статус бар со спиннером и DEBUG индикатором
//   - EventHandler: обработка событий от агента
//   - DebugManager: debug режим и сохранение экрана
//
// Thread-safe через primitives (каждый primitive использует sync.RWMutex).
//
// Rule 11: хранит родительский context.Context для распространения отмены.
type BaseModel struct {
	// Primitives (Phase 1 deliverables)
	viewportMgr *primitives.ViewportManager
	statusMgr   *primitives.StatusBarManager
	eventHdlr   *primitives.EventHandler
	debugMgr    *primitives.DebugManager

	// Dependencies (Port interface only - Rule 6 compliant)
	eventSub events.Subscriber

	// Context (Rule 11: propagate context cancellation)
	ctx context.Context

	// Configuration
	title   string
	ready   bool
	showHelp bool

	// UI Components
	textarea textarea.Model
	help     help.Model

	// Key bindings
	keys KeyMap
}

// NewBaseModel создаёт новую BaseModel с primitives.
//
// Rule 6: не зависит от pkg/agent или pkg/chain (только Port interface).
// Rule 11: принимает родительский контекст для распространения отмены.
//
// Parameters:
//   - ctx: Родительский контекст для распространения отмены
//   - eventSub: Подписчик на события агента (Port interface)
//
// Returns:
//   - BaseModel готовый к использованию с Bubble Tea
func NewBaseModel(ctx context.Context, eventSub events.Subscriber) *BaseModel {
	// Create primitives with default configuration
	vm := primitives.NewViewportManager(primitives.ViewportConfig{
		MinWidth:  20,
		MinHeight: 1,
	})

	sm := primitives.NewStatusBarManager(primitives.DefaultStatusBarConfig())

	eh := primitives.NewEventHandler(eventSub, vm, sm)

	dm := primitives.NewDebugManager(primitives.DebugConfig{
		LogsDir:  "./debug_logs",
		SaveLogs: true,
	}, vm, sm)

	// Create textarea
	ta := textarea.New()
	ta.Placeholder = "Enter your query..."
	ta.Prompt = "┃ "
	ta.CharLimit = 500
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// Create help model
	h := help.New()
	h.ShowAll = false

	// Create keymap
	keys := DefaultKeyMap()

	return &BaseModel{
		viewportMgr: vm,
		statusMgr:   sm,
		eventHdlr:   eh,
		debugMgr:    dm,
		eventSub:    eventSub,
		ctx:         ctx,
		title:       "AI Agent",
		ready:       false,
		showHelp:    false,
		textarea:    ta,
		help:        h,
		keys:        keys,
	}
}

// Init реализует tea.Model интерфейс.
//
// Возвращает команды для:
//   - Фокуса на textarea (блинк курсора)
//   - Чтения событий от агента
//   - Запуска спиннера (анимация)
func (m *BaseModel) Init() tea.Cmd {
	return tea.Batch(
		m.textarea.Focus(),
		ReceiveEventCmd(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		}),
		m.statusMgr.Tick(), // Запускаем анимацию спиннера
	)
}

// Update реализует tea.Model интерфейс.
//
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала
//   - tea.KeyMsg: нажатия клавиш
//   - EventMsg: события от агента
//   - spinner.TickMsg: тики спиннера (делегируется StatusBarManager)
func (m *BaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case EventMsg:
		event := events.Event(msg)
		m.eventHdlr.HandleEvent(event)
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case tea.MouseMsg:
		// Обработка событий мыши (включая колесико)
		return m.handleMouseMsg(msg)

	case spinner.TickMsg:
		// TickMsg для спиннера - обновляем через StatusBarManager
		cmd := m.statusMgr.Update(msg)
		return m, cmd

	default:
		// Передаём остальные сообщения в textarea
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
}

// handleWindowSize обрабатывает изменение размера терминала.
//
// Вычисляет новые размеры для viewport и textarea,
// сохраняя умную прокрутку (via ViewportManager).
func (m *BaseModel) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
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
	m.textarea.SetWidth(vpWidth)

	// Делегируем resize обработку ViewportManager (с умным скроллом)
	m.viewportMgr.HandleResize(msg, headerHeight+helpHeight, footerHeight)

	if !m.ready {
		// Первый запуск - добавляем приветственное сообщение
		m.ready = true
		dimensions := fmt.Sprintf("Window: %dx%d | Viewport: %dx%d",
			msg.Width, msg.Height, vpWidth, vpHeight)
		titleWithInfo := fmt.Sprintf("%s%s",
			SystemStyle(m.title),
			SystemStyle("   INFO: "+dimensions),
		)
		m.viewportMgr.Append(titleWithInfo, false)
	}

	return m, nil
}

// handleKeyPress обрабатывает нажатия клавиш.
//
// Поддерживает все key bindings:
//   - Ctrl+C/Esc: Quit
//   - Ctrl+H: Toggle help
//   - Ctrl+U/PgUp: Scroll up
//   - Ctrl+D/PgDown: Scroll down
//   - Enter: Confirm input (должен быть переопределён в扩展)
//   - Ctrl+S: Save screen
//   - Ctrl+G: Toggle debug
//   - Ctrl+L: Show debug path
func (m *BaseModel) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.ToggleHelp):
		m.showHelp = !m.showHelp
		m.help.ShowAll = m.showHelp
		return m, nil

	case key.Matches(msg, m.keys.ScrollUp):
		// Scroll up by one full page (viewport height)
		vp := m.viewportMgr.GetViewport()
		m.viewportMgr.ScrollUp(vp.Height)
		return m, nil

	case key.Matches(msg, m.keys.ScrollDown):
		// Scroll down by one full page (viewport height)
		vp := m.viewportMgr.GetViewport()
		m.viewportMgr.ScrollDown(vp.Height)
		return m, nil

	case key.Matches(msg, m.keys.SaveToFile):
		filename, err := m.debugMgr.SaveScreen()
		if err != nil {
			m.viewportMgr.Append(ErrorStyle(fmt.Sprintf("❌ Save failed: %v", err)), true)
		} else {
			m.viewportMgr.Append(SystemStyle(fmt.Sprintf("✅ Saved: %s", filename)), true)
		}
		return m, nil

	case key.Matches(msg, m.keys.ToggleDebug):
		debugMsg := m.debugMgr.ToggleDebug()
		m.viewportMgr.Append(SystemStyle(debugMsg), true)
		return m, nil

	case key.Matches(msg, m.keys.ShowDebugPath):
		path := m.debugMgr.GetLastLogPath()
		if path != "" {
			m.viewportMgr.Append(SystemStyle(fmt.Sprintf("📁 Debug log: %s", path)), true)
		} else {
			m.viewportMgr.Append(SystemStyle("📁 No debug log available yet"), true)
		}
		return m, nil

	// ConfirmInput NOT handled here - must be handled by extended models
	// (Model, InterruptionModel, etc.) to provide their own callback logic

	default:
		// Все остальные клавиши передаем в textarea
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
}

// handleMouseMsg обрабатывает события мыши.
//
// Поддерживает прокрутку колёсиком мыши:
//   - MouseButtonWheelUp: прокрутка вверх (3 линии за тик)
//   - MouseButtonWheelDown: прокрутка вниз (3 линии за тик)
//
// Потокобезопасно через ViewportManager (sync.RWMutex).
func (m *BaseModel) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Прокрутка колёсиком вверх
	if msg.Button == tea.MouseButtonWheelUp {
		m.viewportMgr.ScrollUp(3)
		return m, nil
	}

	// Прокрутка колёсиком вниз
	if msg.Button == tea.MouseButtonWheelDown {
		m.viewportMgr.ScrollDown(3)
		return m, nil
	}

	// Другие события мыши игнорируруем (клики, движения и т.д.)
	return m, nil
}

// View реализует tea.Model интерфейс.
//
// Возвращает строковое представление TUI для рендеринга:
//   - Title (если есть)
//   - Viewport (основной контент)
//   - Help (если включен)
//   - Divider
//   - Textarea (поле ввода)
//   - Status bar (с спиннером и DEBUG индикатором)
func (m *BaseModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Title
	title := m.title

	// Viewport
	viewport := m.viewportMgr.GetViewport().View()

	// Help
	var help string
	if m.showHelp {
		help = "\n" + m.help.View(m.keys)
	}

	// Divider
	divider := "\n" + dividerStyle(m.viewportMgr.GetViewport().Width)

	// Status bar
	status := m.statusMgr.Render()

	// Textarea
	input := m.textarea.View()

	return fmt.Sprintf("%s\n%s%s%s\n%s\n%s",
		title, viewport, help, divider, input, status)
}

// ===== PUBLIC API FOR EXTENSIONS =====

// Append добавляет контент в viewport с умным скроллом.
//
// Сохраняет позицию пользователя если он прокрутил вверх.
// Автоскролл вниз только если пользователь был в нижней позиции.
//
// Parameters:
//   - content: Контент для добавления (может содержать ANSI коды)
//   - preservePosition: Сохранять ли позицию прокрутки
//
// Example:
//
//	m.Append(SystemStyle("System message"), true)
func (m *BaseModel) Append(content string, preservePosition bool) {
	m.viewportMgr.Append(content, preservePosition)
}

// SetProcessing устанавливает статус обработки (показывает спиннер).
//
// Parameters:
//   - processing: true для показа спиннера, false для "✓ Ready"
func (m *BaseModel) SetProcessing(processing bool) {
	m.statusMgr.SetProcessing(processing)
}

// IsProcessing возвращает текущий статус обработки.
func (m *BaseModel) IsProcessing() bool {
	return m.statusMgr.IsProcessing()
}

// SetCustomStatus устанавливает callback для доп. информации в статусной строке.
//
// Callback вызывается при каждом рендеринге и добавляется ПОСЛЕ спиннера.
// Формат: "Todo: 3/12" или любая другая информация.
//
// Parameters:
//   - fn: Функция которая возвращает строку для отображения
//
// Example:
//
//	m.SetCustomStatus(func() string {
//	    return fmt.Sprintf("Queries: %d", m.queryCount)
//	})
func (m *BaseModel) SetCustomStatus(fn func() string) {
	m.statusMgr.SetCustomExtra(fn)
}

// SetTitle устанавливает заголовок TUI.
func (m *BaseModel) SetTitle(title string) {
	m.title = title
}

// GetViewportMgr возвращает ViewportManager для прямого доступа.
//
// Используется в расширенных моделях для специфичных операций.
func (m *BaseModel) GetViewportMgr() *primitives.ViewportManager {
	return m.viewportMgr
}

// GetStatusBarMgr возвращает StatusBarManager для прямого доступа.
func (m *BaseModel) GetStatusBarMgr() *primitives.StatusBarManager {
	return m.statusMgr
}

// GetEventHandler возвращает EventHandler для прямого доступа.
func (m *BaseModel) GetEventHandler() *primitives.EventHandler {
	return m.eventHdlr
}

// GetDebugManager возвращает DebugManager для прямого доступа.
func (m *BaseModel) GetDebugManager() *primitives.DebugManager {
	return m.debugMgr
}

// GetContext возвращает родительский контекст (Rule 11).
func (m *BaseModel) GetContext() context.Context {
	return m.ctx
}

// GetSubscriber возвращает подписчик на события.
func (m *BaseModel) GetSubscriber() events.Subscriber {
	return m.eventSub
}

// GetTextarea возвращает textarea.Model для прямого доступа.
//
// Используется в расширенных моделях для переопределения поведения.
func (m *BaseModel) GetTextarea() textarea.Model {
	return m.textarea
}

// SetTextarea устанавливает textarea.Model.
//
// Используется в расширенных моделях для обновления textarea после Update().
func (m *BaseModel) SetTextarea(ta textarea.Model) {
	m.textarea = ta
}

// ShowHelp возвращает статус отображения help.
func (m *BaseModel) ShowHelp() bool {
	return m.showHelp
}

// SetShowHelp устанавливает статус отображения help.
func (m *BaseModel) SetShowHelp(show bool) {
	m.showHelp = show
	m.help.ShowAll = show
}

// GetHelp возвращает help.Model для прямого доступа.
func (m *BaseModel) GetHelp() help.Model {
	return m.help
}

// RenderStatusLine возвращает отрендеренную строку статуса.
//
// Делегирует StatusBarManager.Render().
func (m *BaseModel) RenderStatusLine() string {
	return m.statusMgr.Render()
}

// Ensure BaseModel implements tea.Model
var _ tea.Model = (*BaseModel)(nil)
