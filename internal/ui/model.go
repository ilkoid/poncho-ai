// Package ui реализует Model компонент Bubble Tea TUI.
//
// Содержит структуру UI и функцию инициализации.
package ui

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ilkoid/poncho-ai/internal/app"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// agentResultMsg хранит результат работы агента для передачи через канал
type agentResultMsg struct {
	result app.CommandResultMsg
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
//   - appState: ссылка на глобальное состояние приложения
//   - err: ошибка инициализации (если была)
//   - ready: флаг первой инициализации размеров окна
//   - agent: указатель на agentState (не копируется при Update!)
type MainModel struct {
	viewport viewport.Model
	textarea textarea.Model

	appState *app.AppState

	// err хранит ошибку запуска, если была.
	// Используем atomic.Value для thread-safe доступа.
	err atomic.Value // хранит error

	// ready флаг для первой инициализации размеров
	ready bool

	// agent хранит состояние агента с мьютексом.
	// УКАЗАТЕЛЬ - не копируется при Bubble Tea Update() value receiver!
	agent *agentState
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
// Принимает AppState для доступа к данным приложения.
// Rule 6: AppState вместо GlobalState после рефакторинга.
func InitialModel(state *app.AppState) MainModel {
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
		textarea: ta,
		viewport: vp,
		appState: state,
		ready:    false,
		agent:    &agentState{}, // Инициализация пустого состояния
	}
}

// Init запускается один раз при старте Bubble Tea программы.
//
// Возвращает команду для запуска мигания курсора в поле ввода.
func (m MainModel) Init() tea.Cmd {
	return textarea.Blink
}
