// Package ui реализует Model компонент Bubble Tea TUI.
//
// Содержит структуру UI и функцию инициализации.
package ui

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tui"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// agentResultMsg хранит результат работы агента для передачи через канал
type agentResultMsg struct {
	result commandResultMsg // Результат выполнения агента
}

// commandResultMsg - сообщение с результатом выполнения команды
type commandResultMsg struct {
	Output string
	Err    error
}

// agentState хранит состояние агента, требующее синхронизации.
//
// Вынесено в отдельную структуру чтобы избежать копирования мьютекса
// при value receiver в Bubble Tea Update(). MainModel хранит указатель
// на эту структуру, поэтому мьютекс не копируется.
type agentState struct {
	mu       sync.Mutex
	running  bool
	resultCh chan agentResultMsg
}

// isRunning возвращает флаг running под защитой мьютекса.
func (s *agentState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// start устанавливает канал и флаг running.
func (s *agentState) start(resultCh chan agentResultMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resultCh = resultCh
	s.running = true
}

// stop сбрасывает состояние агента.
func (s *agentState) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.resultCh = nil
}

// getChannel возвращает канал результата (под мьютексом).
func (s *agentState) getChannel() chan agentResultMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resultCh
}

// tryStart пытается запустить агент. Возвращает false если уже запущен.
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

// MainModel представляет главную модель UI (Bubble Tea Model).
//
// Содержит все компоненты TUI:
//   - viewport: область лога чата (только для чтения)
//   - textarea: поле ввода пользователя
//   - coreState: ссылка на framework core состояние
//   - orchestrator: ссылка на AI агента
//   - err: ошибка инициализации (если была)
//   - ready: флаг первой инициализации размеров окна
//   - agent: указатель на agentState (не копируется при Update!)
//
// REFACTORED 2025-01-07: Хранит CoreState + UI-specific поля отдельно.
// REFACTORED 2026-01-10: Использует events.Subscriber вместо agentState (Port & Adapter).
type MainModel struct {
	viewport viewport.Model
	textarea textarea.Model

	// Framework core (library code)
	coreState *state.CoreState

	// UI-specific state (TUI only)
	orchestrator     agent.Agent // AI Agent implementation
	currentArticleID string      // Текущий артикул для отображения в UI
	currentModel     string      // Текущая модель для отображения в UI
	isProcessing     bool        // Флаг занятости для spinner
	mu               sync.RWMutex // Защита UI-specific полей

	// err хранит ошибку запуска, если была.
	// Используем atomic.Value для thread-safe доступа.
	err atomic.Value // хранит error

	// ready флаг для первой инициализации размеров
	ready bool

	// Port & Adapter: подписчик на события агента
	// Заменяет старый механизм agentState
	eventSub events.Subscriber
}

// getErr возвращает ошибку thread-safe.
func (m *MainModel) getErr() error {
	if v := m.err.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// setErr устанавливает ошибку thread-safe.
func (m *MainModel) setErr(err error) {
	if err != nil {
		m.err.Store(err)
	} else {
		m.err.Store((*error)(nil))
	}
}

// InitialModel создает начальное состояние UI.
//
// Инициализирует:
//   - Поле ввода с placeholder'ом
//   - Вьюпорт для лога с приветственным сообщением
//
// Принимает CoreState, Orchestrator и events.Subscriber для работы с агентом.
// REFACTORED 2025-01-07: Не зависит от internal/app.
// REFACTORED 2026-01-10: Использует events.Subscriber (Port & Adapter).
func InitialModel(coreState *state.CoreState, orchestrator agent.Agent, currentModel string, eventSub events.Subscriber) MainModel {
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
		textarea:         ta,
		viewport:         vp,
		coreState:        coreState,
		orchestrator:     orchestrator,
		currentArticleID: "NONE",
		currentModel:     currentModel,
		isProcessing:     false,
		ready:            false,
		eventSub:         eventSub,
	}
}

// Init запускается один раз при старте Bubble Tea программы.
//
// Возвращает команду для:
//   - Запуска мигания курсора в поле ввода
//   - Чтения событий из агента (Port & Adapter)
func (m MainModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tui.ReceiveEventCmd(m.eventSub, func(event events.Event) tea.Msg {
			return tui.EventMsg(event)
		}),
	)
}
