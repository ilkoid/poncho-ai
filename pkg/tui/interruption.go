// Package tui предоставляет InterruptionModel - TUI модель с поддержкой прерываний.
//
// InterruptionModel расширяет BaseModel возможностью прерывать выполнение агента.
// Пользователь может набрать команду и нажать Enter для отправки прерывания.
//
// Thread-safe.
//
// Пример использования:
//
//	model := tui.NewInterruptionModel(ctx, coreState, eventSub, inputChan)
//	model.SetOnInput(createAgentLauncher(...)) // MANDATORY
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
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// InterruptionModel - модель TUI с поддержкой прерываний.
//
// ⚠️ REFACTORED (Phase 3B): Теперь встраивает BaseModel напрямую, без зависимости от *agent.Client.
//
// Расширяет BaseModel возможностью прерывать выполнение агента.
// Пользователь может набрать команду и нажать Enter для отправки прерывания.
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

	// ===== QUESTION MODE (ask_user_question tool) =====
	// questionMode — активен когда LLM задает вопрос пользователю
	questionMode bool
	// currentQuestionID — ID текущего вопроса
	currentQuestionID string
	// questionManager — менеджер вопросов для polling
	questionManager interface{} // *questions.QuestionManager

	// ===== QUIT CONFIRMATION MODE =====
	// quitting — true когда пользователь нажал Esc первый раз (требуется подтверждение)
	quitting bool
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
func NewInterruptionModel(
	ctx context.Context,
	coreState *state.CoreState,
	eventSub events.Subscriber,
	inputChan chan string,
) *InterruptionModel {
	// NOT calling initDebugLog anymore - logs are created lazily

	// Создаём BaseModel напрямую (без agent dependency)
	base := NewBaseModel(ctx, eventSub)

	model := &InterruptionModel{
		BaseModel:  base,
		inputChan:  inputChan,
		coreState:  coreState,
		todos:      []todo.Task{},
		mu:         sync.RWMutex{},
	}

	// Log creation only if debug mode is already enabled (edge case)
	if model.GetDebugManager().IsEnabled() {
		model.debugLogIfEnabled("NewInterruptionModel: Creating model")
		model.debugLogIfEnabled("NewInterruptionModel: BaseModel created")
		model.debugLogIfEnabled("NewInterruptionModel: InterruptionModel created")
	}

	return model
}

// Init реализует tea.Model интерфейс для InterruptionModel.
//
// ⚠️ REFACTORED (Phase 3B): Делегирует BaseModel.Init().
func (m *InterruptionModel) Init() tea.Cmd {
	m.debugLogIfEnabled("InterruptionModel.Init: called")
	defer m.debugLogIfEnabled("InterruptionModel.Init: finished")

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
//
// ⚠️ PANIC RECOVERY: Wrap with defer/recover to prevent WSL2 crash from nil pointer or race conditions
func (m *InterruptionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Panic recovery to prevent WSL2 crashes
	defer func() {
		if r := recover(); r != nil {
			m.appendLog(ErrorStyle(fmt.Sprintf("🔥 PANIC RECOVERED in Update: %v", r)))
			m.debugLogIfEnabled("PANIC in Update: %v", r)
			// Try to continue despite panic
		}
	}()

	m.debugLogIfEnabled("InterruptionModel.Update: called, msg type=%T", msg)

	switch msg := msg.(type) {
	case saveSuccessMsg:
		m.appendLog(SystemStyle(fmt.Sprintf("✓ Saved to: %s", msg.filename)))
		return m, nil

	case saveErrorMsg:
		m.appendLog(ErrorStyle(fmt.Sprintf("✗ Failed to save: %v", msg.err)))
		return m, nil

	case EventMsg:
		m.debugLogIfEnabled("InterruptionModel.Update: EventMsg received, type=%s", events.Event(msg).Type)
		// ПЕРЕХВАТЫВАЕМ события агента - не даем базовой модели их обработать
		return m.handleAgentEventWithInterruption(events.Event(msg))

	case tea.KeyMsg:
		m.debugLogIfEnabled("InterruptionModel.Update: KeyMsg received, key=%s", msg.String())
		// ПЕРВЫЕ: question mode обрабатывает цифры 1-5
		if m.questionMode {
			return m.handleQuestionKey(msg)
		}

		// Проверяем key bindings для глобальных действий (quit, help, scroll)
		// Эти клавиши должны работать всегда, независимо от фокуса textarea
		matchesConfirm := key.Matches(msg, m.keys.ConfirmInput)
		matchesQuit := key.Matches(msg, m.keys.Quit)
		m.debugLogIfEnabled("InterruptionModel.Update: matchesConfirm=%v matchesQuit=%v quitting=%v", matchesConfirm, matchesQuit, m.quitting)

		switch {
		case matchesQuit:
			// ===== QUIT CONFIRMATION MODE =====
			// Первый Esc: показать предупреждение, второй - выйти
			if m.quitting {
				// Второй Esc или Ctrl+C - подтверждение выхода
				return m, tea.Quit
			}
			// Первый Esc - активируем режим подтверждения
			m.quitting = true
			return m, nil
		case key.Matches(msg, m.keys.ToggleHelp):
			// Отмена режима quit при любой другой клавише
			m.quitting = false
			// Делегируем BaseModel для обновления help
			baseModel, baseCmd := m.BaseModel.Update(msg)
			m.BaseModel = baseModel.(*BaseModel)
			return m, baseCmd
		case key.Matches(msg, m.keys.ScrollUp):
			m.quitting = false
			m.GetViewportMgr().ScrollUp(1)
			return m, nil
		case key.Matches(msg, m.keys.ScrollDown):
			m.quitting = false
			m.GetViewportMgr().ScrollDown(1)
			return m, nil
		case key.Matches(msg, m.keys.SaveToFile):
			m.quitting = false
			// Делегируем BaseModel для сохранения
			baseModel, baseCmd := m.BaseModel.Update(msg)
			m.BaseModel = baseModel.(*BaseModel)
			return m, baseCmd
		case key.Matches(msg, m.keys.ToggleDebug):
			m.quitting = false
			// Делегируем BaseModel для toggle debug
			baseModel, baseCmd := m.BaseModel.Update(msg)
			m.BaseModel = baseModel.(*BaseModel)
			return m, baseCmd
		case key.Matches(msg, m.keys.ShowDebugPath):
			m.quitting = false
			// Ctrl+L: показать путь к последнему debug-логу
			m.mu.RLock()
			debugPath := m.lastDebugPath
			m.mu.RUnlock()

			if debugPath != "" {
				m.appendLog(SystemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
			} else {
				m.appendLog(SystemStyle("📁 No debug log available yet"))
			}
			return m, nil
		case key.Matches(msg, m.keys.ClearLogs):
			m.quitting = false
			// Ctrl+K: удалить все лог-файлы
			count, err := clearLogs()
			if err != nil {
				m.appendLog(ErrorStyle(fmt.Sprintf("✗ Failed to delete logs: %v", err)))
			} else if count > 0 {
				m.appendLog(SystemStyle(fmt.Sprintf("🗑️ Deleted %d log file(s)", count)))
			} else {
				m.appendLog(SystemStyle("🗑️ No log files found"))
			}
			return m, nil
		case matchesConfirm:
			m.quitting = false
			return m.handleKeyPressWithInterruption(msg)
		}

		// Все остальные клавиши - обрабатываем ввод текста в textarea
		// НЕ передаём в BaseModel.Update() чтобы избежать двойной обработки Enter
		m.quitting = false // Отмена режима quit при текстовом вводе
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	default:
		// Все остальные сообщения передаем в базовую модель, но ВСЕГДА возвращаем InterruptionModel
		// Это критично! Если вернуть BaseModel, BubbleTea перестанет вызывать InterruptionModel.Update()
		baseModel, baseCmd := m.BaseModel.Update(msg)
		m.BaseModel = baseModel.(*BaseModel)
		return m, baseCmd
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

	// ===== QUIT CONFIRMATION BANNER =====
	// Показываем warning когда пользователь нажал Esc первый раз
	if m.quitting {
		warningText := "⚠️ Press Esc again to quit (or any other key to cancel)"
		warningBanner := lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")). // White text
			Background(lipgloss.Color("196")). // Red background
			Bold(true).
			Padding(0, 1).
			Width(vp.Width).
			Render(warningText)
		sections = append(sections, warningBanner)
	}

	// ===== QUESTION MODE BANNER =====
	// Показываем когда активен режим вопросов от ask_user_question tool
	if m.questionMode {
		questionText := "🤔 QUESTION MODE - Press 1-5 to answer, Esc to cancel"
		questionBanner := lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).   // Black text for better contrast on yellow
			Background(lipgloss.Color("226")). // Yellow background
			Bold(true).
			Padding(0, 1).
			Width(vp.Width).
			Render(questionText)
		sections = append(sections, questionBanner)
	}

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

// debugLogIfEnabled пишет сообщение в tui_debug.log только если debug mode включён.
// Лог-файл создаётся лениво при первой записи в debug mode.
func (m *InterruptionModel) debugLogIfEnabled(format string, args ...interface{}) {
	// Проверяем, включён ли debug mode
	if !m.GetDebugManager().IsEnabled() {
		return
	}

	// Lazy init: создаём файл только при первой записи в debug mode
	if debugLogFile == nil {
		f, err := os.OpenFile("tui_debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_SYNC, 0644)
		if err != nil {
			return
		}
		debugLogFile = f
		fmt.Fprintf(debugLogFile, "[%s] === TUI Debug Log Started (Debug Mode: ON) ===\n", time.Now().Format("15:04:05.000"))
	}

	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(debugLogFile, "[%s] %s\n", timestamp, fmt.Sprintf(format, args...))
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
		m.appendLog(SystemStyle(fmt.Sprintf("[DEBUG] Event: %s", event.Type)))
	}

	switch event.Type {
	case events.EventUserInterruption:
		// Пользователь прервал выполнение - отображаем сообщение
		if data, ok := event.Data.(events.UserInterruptionData); ok {
			m.appendLog(SystemStyle(fmt.Sprintf("⏸️ Interruption (iteration %d): %s", data.Iteration, truncate(data.Message, 60))))
		}
		// Продолжаем слушать события
		return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventToolCall:
		// DEBUG-логирование tool calls (включается по Ctrl+G)
		if m.GetDebugManager().IsEnabled() {
			if data, ok := event.Data.(events.ToolCallData); ok {
				m.appendLog(SystemStyle(fmt.Sprintf("[DEBUG] Tool call: %s", data.ToolName)))
			}
		}
		// ПРОВЕРКА QUESTIONS: Polling после EventToolCall
		// ask_user_question tool создаёт вопрос БЛОКИРУЯСЬ на WaitForAnswer()
		// TUI должен опросить QuestionManager ПРЕЖДЕ чем tool вернёт результат
		if m.checkForPendingQuestions() {
			m.debugLogIfEnabled("[QUESTION] ✓ Question detected after ToolCall, entering question mode")
			return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
				return EventMsg(e)
			})
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

		m.debugLogIfEnabled("[QUESTION] After event %s, checking for questions...", event.Type)

		// ПРОВЕРКА QUESTIONS: Polling QuestionManager после каждого события
		if m.checkForPendingQuestions() {
			m.debugLogIfEnabled("[QUESTION] ✓ Entered question mode, waiting for user input")
			// Переключились в question mode - продолжаем слушать события
			return m, WaitForEvent(m.GetSubscriber(), func(e events.Event) tea.Msg {
				return EventMsg(e)
			})
		}

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
		m.appendLog(SystemStyle(debugMsg))
		return m, nil

	case key.Matches(msg, m.keys.ShowDebugPath):
		// Ctrl+L: показать путь к последнему debug-логу
		m.mu.RLock()
		debugPath := m.lastDebugPath
		m.mu.RUnlock()

		if debugPath != "" {
			m.appendLog(SystemStyle(fmt.Sprintf("📁 Debug log: %s", debugPath)))
		} else {
			m.appendLog(SystemStyle("📁 No debug log available yet"))
		}
		return m, nil

	case key.Matches(msg, m.keys.ConfirmInput):
		m.debugLogIfEnabled("handleKeyPressWithInterruption: ConfirmInput matched")
		ta := m.GetTextarea()
		input := ta.Value()
		m.debugLogIfEnabled("handleKeyPressWithInterruption: input=%q len=%d", input, len(input))

		if input == "" {
			m.debugLogIfEnabled("handleKeyPressWithInterruption: input is empty, returning")
			return m, nil
		}

		ta.Reset()
		m.SetTextarea(ta)
		m.appendLog(UserMessageStyle("USER: ") + input)
		m.debugLogIfEnabled("handleKeyPressWithInterruption: USER message logged")

		// Проверяем: установлен ли callback? (MANDATORY)
		m.mu.RLock()
		handler := m.onInput
		m.mu.RUnlock()
		m.debugLogIfEnabled("handleKeyPressWithInterruption: handler is nil: %v", handler == nil)

		if handler == nil {
			// Callback не установлен - это ошибка конфигурации
			m.appendLog(ErrorStyle("ERROR: No input handler set. Call SetOnInput() first."))
			m.debugLogIfEnabled("handleKeyPressWithInterruption: ERROR - no handler set")
			return m, nil
		}

		// Устанавливаем флаг обработки для показа спиннера
		m.GetStatusBarMgr().SetProcessing(true)
		m.debugLogIfEnabled("handleKeyPressWithInterruption: calling handler")

		// Используем callback для обработки ввода
		cmd := handler(input)
		m.debugLogIfEnabled("handleKeyPressWithInterruption: handler returned, cmd is nil: %v", cmd == nil)
		return m, cmd
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

// Ensure InterruptionModel implements tea.Model
var _ tea.Model = (*InterruptionModel)(nil)
