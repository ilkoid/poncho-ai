// Package tui предоставляет SimpleTui — примитивный "lego brick" TUI компонент.
//
// SimpleTui это максимально простой, переиспользуемый TUI для AI агентов.
// Он НЕ содержит бизнес-логики агента, только UI компоненты.
//
// # Layout
//
//	┌─────────────────────────────────────────────────┐
//	│ 🤖 Poncho AI | Model: glm-4.6 | Streaming: ON │ ← Status Bar
//	├─────────────────────────────────────────────────┤
//	│  [14:32:15] User: Show me categories           │
//	│  [14:32:16] Agent: Thinking...                  │
//	│  [14:32:18] Agent: Here are categories...      │
//	│  [14:32:20] Tool Call: get_wb_categories()    │
//	│                                                 │
//	│  Main Area (auto-scroll, streaming messages)   │
//	├─────────────────────────────────────────────────┤
//	│ > user input here                              │ ← Input Area
//	└─────────────────────────────────────────────────┘
//
// # Basic Usage
//
//	client, _ := agent.New(ctx, agent.Config{ConfigPath: "config.yaml"})
//	sub := client.Subscribe()
//
//	tui := NewSimpleTui(sub, SimpleUIConfig{
//	    Colors:        ColorSchemes["dark"],
//	    InputPrompt:   "AI> ",
//	    ShowTimestamp: true,
//	})
//
//	tui.OnInput(func(input string) {
//	    client.Run(ctx, input)
//	})
//
//	tui.Run()
//
// # Via Preset (future)
//
//	app.RunPreset(ctx, "ecommerce-analyzer")
//	// Automatically uses SimpleTui with preset config
//
// Rule 6: Reusable library code, no app-specific logic.
package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
)

// SimpleUIConfig конфигурирует SimpleTui.
//
// Все поля опциональны, используются дефолтные значения если не заданы.
type SimpleUIConfig struct {
	// Colors определяет цветовую схему
	Colors ColorScheme

	// StatusHeight — высота статус-бара (1 для однострочного, 2 для двухстрочного)
	StatusHeight int

	// InputHeight — высота поля ввода
	InputHeight int

	// InputPrompt — текст приглашения ввода
	InputPrompt string

	// ShowTimestamp — показывать timestamp в сообщениях
	ShowTimestamp bool

	// MaxMessages — максимальное количество сообщений в логе (0 = безлимит)
	MaxMessages int

	// WrapText — включить перенос длинных строк
	WrapText bool

	// Title — заголовок приложения (отображается в статус-баре)
	Title string

	// ModelName — имя модели для отображения в статус-баре
	ModelName string

	// StreamingStatus — статус streaming для статус-бара
	StreamingStatus string // "ON", "OFF", или "THINKING"
}

// SimpleTui примитивный "lego brick" TUI компонент.
//
// Thread-safe.
//
// Не содержит бизнес-логики агента, только UI.
// Работает с events.Subscriber для получения событий агента.
type SimpleTui struct {
	// config — конфигурация TUI
	config SimpleUIConfig

	// subscriber — подписчик на события агента (Port & Adapter)
	subscriber events.Subscriber

	// onInput — callback для обработки пользовательского ввода
	onInput func(input string)

	// quitChan — канал для graceful shutdown
	quitChan chan struct{}

	// Bubble Tea компоненты
	viewport viewport.Model
	textarea textarea.Model

	// Состояние
	mu           sync.RWMutex
	messages     []string // История сообщений
	ready        bool     // Флаг первой инициализации размеров
	isProcessing bool     // Флаг занятости агента
}

// NewSimpleTui создаёт новый SimpleTui.
//
// Parameters:
//   - subscriber: Подписчик на события агента (events.Subscriber)
//   - config: Конфигурация TUI (используются дефолтные значения если пустые)
//
// Возвращает инициализированный SimpleTui готовый к использованию.
func NewSimpleTui(subscriber events.Subscriber, config SimpleUIConfig) *SimpleTui {
	// Применяем дефолтные значения
	if config.StatusHeight == 0 {
		config.StatusHeight = 1
	}
	if config.InputHeight == 0 {
		config.InputHeight = 3
	}
	if config.InputPrompt == "" {
		config.InputPrompt = "> "
	}
	if config.Colors.StatusForeground == "" {
		config.Colors = DefaultColorScheme()
	}
	if config.Title == "" {
		config.Title = "AI Agent"
	}

	// Настройка textarea
	ta := textarea.New()
	ta.Placeholder = "Введите запрос..."
	ta.Focus()
	ta.Prompt = config.InputPrompt
	ta.CharLimit = 500
	ta.SetHeight(config.InputHeight)
	ta.ShowLineNumbers = false

	// Настройка viewport
	vp := viewport.New(0, 0)
	vp.SetContent(fmt.Sprintf("%s\n",
		systemStyle("AI Agent initialized. Type your query..."),
	))

	return &SimpleTui{
		config:     config,
		subscriber: subscriber,
		onInput:    nil, // Устанавливается через OnInput()
		quitChan:   make(chan struct{}),
		viewport:   vp,
		textarea:   ta,
		messages:   []string{},
		ready:      false,
	}
}

// OnInput устанавливает callback для обработки пользовательского ввода.
//
// Вызывается каждый раз когда пользователь нажимает Enter.
// Callback получает текст ввода (без переносов строк).
//
// Пример:
//
//	tui.OnInput(func(input string) {
//	    client.Run(ctx, input)
//	})
func (t *SimpleTui) OnInput(handler func(input string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onInput = handler
}

// Run запускает TUI (блокирующий вызов).
//
// Возвращает ошибку если TUI завершился с ошибкой.
// nil при нормальном завершении (Ctrl+C или Quit()).
func (t *SimpleTui) Run() error {
	p := tea.NewProgram(t)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

// Quit завершает работу TUI извне.
//
// Можно вызвать из другой горутины для graceful shutdown.
// Thread-safe.
func (t *SimpleTui) Quit() {
	close(t.quitChan)
}

// ===== BUBBLE TEA MODEL INTERFACE =====

// Init реализует tea.Model интерфейс.
func (t *SimpleTui) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		ReceiveEventCmd(t.subscriber, func(event events.Event) tea.Msg {
			return EventMsg(event)
		}),
	)
}

// Update реализует tea.Model интерфейс.
func (t *SimpleTui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	// Используем указатель для избежания копирования мьютекса
	t.textarea, tiCmd = t.textarea.Update(msg)
	t.viewport, vpCmd = t.viewport.Update(msg)

	switch msg := msg.(type) {
	case EventMsg:
		return t.handleAgentEvent(events.Event(msg))

	case tea.WindowSizeMsg:
		return t.handleWindowSize(msg)

	case tea.KeyMsg:
		return t.handleKeyPress(msg)
	}

	return t, tea.Batch(tiCmd, vpCmd)
}

// handleAgentEvent обрабатывает события от агента.
func (t *SimpleTui) handleAgentEvent(event events.Event) (tea.Model, tea.Cmd) {
	switch event.Type {
	case events.EventThinking:
		t.mu.Lock()
		t.isProcessing = true
		t.mu.Unlock()
		t.appendMessage(SystemStyle("Thinking..."), false)

	case events.EventThinkingChunk:
		// Обработка streaming reasoning content
		if chunkData, ok := event.Data.(events.ThinkingChunkData); ok {
			t.updateLastThinking(chunkData.Chunk)
		}

	case events.EventMessage:
		if msgData, ok := event.Data.(events.MessageData); ok {
			t.appendMessage(AIMessageStyle("AI: ")+msgData.Content, true)
		}

	case events.EventToolCall:
		if toolData, ok := event.Data.(events.ToolCallData); ok {
			t.appendMessage(ToolCallStyle(fmt.Sprintf("Tool: %s(%s)", toolData.ToolName, toolData.Args)), false)
		}

	case events.EventToolResult:
		if resultData, ok := event.Data.(events.ToolResultData); ok {
			duration := resultData.Duration.Milliseconds()
			t.appendMessage(ToolResultStyle(fmt.Sprintf("Result: %s (%dms)", resultData.ToolName, duration)), false)
		}

	case events.EventError:
		if errData, ok := event.Data.(events.ErrorData); ok {
			t.appendMessage(ErrorStyle("ERROR: " + errData.Err.Error()), true)
		}
		t.mu.Lock()
		t.isProcessing = false
		t.mu.Unlock()
		t.textarea.Focus()

	case events.EventDone:
		if msgData, ok := event.Data.(events.MessageData); ok {
			t.appendMessage(AIMessageStyle("AI: ")+msgData.Content, true)
		}
		t.mu.Lock()
		t.isProcessing = false
		t.mu.Unlock()
		t.textarea.Focus()
	}

	return t, WaitForEvent(t.subscriber, func(e events.Event) tea.Msg {
		return EventMsg(e)
	})
}

// handleWindowSize обрабатывает изменение размера терминала.
func (t *SimpleTui) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	headerHeight := t.config.StatusHeight
	footerHeight := t.textarea.Height() + 1

	// Вычисляем высоту для области контента
	vpHeight := msg.Height - headerHeight - footerHeight
	if vpHeight < 1 {
		vpHeight = 1
	}

	// Вычисляем ширину
	vpWidth := msg.Width
	if vpWidth < 20 {
		vpWidth = 20
	}

	t.viewport.Width = vpWidth
	t.viewport.Height = vpHeight
	t.textarea.SetWidth(vpWidth)

	if !t.ready {
		t.ready = true
		dimensions := fmt.Sprintf("Window: %dx%d", msg.Width, msg.Height)
		t.appendMessage(systemStyle(dimensions), false)
	}

	return t, nil
}

// handleKeyPress обрабатывает нажатия клавиш.
func (t *SimpleTui) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return t, tea.Quit

	case tea.KeyEnter:
		input := t.textarea.Value()
		if input == "" {
			return t, nil
		}

		// Очищаем ввод
		t.textarea.Reset()

		// Добавляем сообщение пользователя
		t.appendMessage(UserMessageStyle("User: ") + input, true)

		// Вызываем callback если установлен
		t.mu.RLock()
		handler := t.onInput
		t.mu.RUnlock()

		if handler != nil {
			// Запускаем handler в отдельной горутине
			go handler(input)
		}
	}

	return t, nil
}

// View реализует tea.Model интерфейс.
func (t *SimpleTui) View() string {
	return fmt.Sprintf("%s\n%s\n%s",
		t.renderStatusBar(),
		t.viewport.View(),
		t.textarea.View(),
	)
}

// ===== INTERNAL METHODS =====

// renderStatusBar рендерит статус-бар.
func (t *SimpleTui) renderStatusBar() string {
	return RenderStatusBar(t.config.Title, t.config.ModelName, t.config.StreamingStatus, t.config.Colors)
}

// appendMessage добавляет сообщение в лог.
func (t *SimpleTui) appendMessage(msg string, showTimestamp bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var line string
	if showTimestamp && t.config.ShowTimestamp {
		line = fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	} else {
		line = msg
	}

	t.messages = append(t.messages, line)

	// Trim если превышен лимит
	if t.config.MaxMessages > 0 && len(t.messages) > t.config.MaxMessages {
		t.messages = t.messages[len(t.messages)-t.config.MaxMessages:]
	}

	// Обновляем viewport с умной прокруткой (сохраняет позицию пользователя)
	content := strings.Join(t.messages, "\n")
	AppendToViewport(&t.viewport, content)
}

// updateLastThinking обновляет последнюю строку с thinking content.
func (t *SimpleTui) updateLastThinking(chunk string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.messages) == 0 {
		t.appendMessage(ThinkingStyle("Thinking: ") + ThinkingDimStyle(chunk), false)
		return
	}

	// Проверяем последнюю строку
	lastLine := t.messages[len(t.messages)-1]
	if strings.Contains(lastLine, "Thinking:") {
		// Обновляем последнюю строку
		t.messages[len(t.messages)-1] = ThinkingStyle("Thinking: ") + ThinkingDimStyle(chunk)
	} else {
		// Добавляем новую строку
		t.messages = append(t.messages, ThinkingStyle("Thinking: ") + ThinkingDimStyle(chunk))
	}

	// Trim если превышен лимит
	if t.config.MaxMessages > 0 && len(t.messages) > t.config.MaxMessages {
		t.messages = t.messages[len(t.messages)-t.config.MaxMessages:]
	}

	// Обновляем viewport с умной прокруткой (сохраняет позицию пользователя)
	content := strings.Join(t.messages, "\n")
	AppendToViewport(&t.viewport, content)
}

// Ensure SimpleTui implements tea.Model
var _ tea.Model = (*SimpleTui)(nil)
