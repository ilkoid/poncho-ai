/*
Simple Chat - простая утилита для чата с LLM моделью
Использует фреймворк Poncho AI и TUI интерфейс на Bubble Tea
*/

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ilkoid/poncho-ai/internal/ui"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// ChatState хранит состояние чата (следует паттернам фреймворка)
type ChatState struct {
	history   []llm.Message
	provider  llm.Provider
	modelDef  config.ModelDef
	modelName string
}

// teaMsg типы для коммуникации (соответствует конвенциям фреймворка)
type userMsg struct{ text string }
type aiResponseMsg struct{ text string }
type errorMsg struct{ err error }

// chatModel - TUI модель для чата (оптимизирована под фреймворк)
type chatModel struct {
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	chatState *ChatState
	loading   bool
	err       error
	ready     bool
}

// initialModel создает начальное состояние TUI (следуя паттернам фреймворка)
func initialModel(chatState *ChatState) tea.Model {
	// Настройка поля ввода (аналогично фреймворку)
	ta := textarea.New()
	ta.Placeholder = "Введите ваше сообщение..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.SetHeight(3)
	ta.CharLimit = 1000
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter отправляет, не переносит строку

	// Настройка вьюпорта (размеры обновятся при WindowSizeMsg)
	vp := viewport.New(0, 0)

	// Используем стили из фреймворка для начального контента
	initialContent := fmt.Sprintf("%s\nМодель: %s\n%s\n%s\n",
		ui.SystemMsgStyle("🤖 Simple Chat v1.0"),
		chatState.modelName,
		ui.SystemMsgStyle("Напишите сообщение и нажмите Enter"),
		ui.SystemMsgStyle("Ctrl+C или Esc для выхода"))
	vp.SetContent(initialContent)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return chatModel{
		chatState: chatState,
		textarea:  ta,
		viewport:  vp,
		spinner:   sp,
		ready:     false,
	}
}

// Init инициализирует TUI (следуя паттернам фреймворка)
func (m chatModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update обрабатывает события (оптимизировано под фреймворк)
func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerHeight := 1
		footerHeight := m.textarea.Height() + 2

		// Вычисляем высоту для области контента
		vpHeight := msg.Height - headerHeight - footerHeight
		if vpHeight < 0 {
			vpHeight = 0
		}

		// Обновляем размеры (следуя паттернам фреймворка)
		m.viewport.Width = msg.Width
		m.viewport.Height = vpHeight
		m.textarea.SetWidth(msg.Width)
		m.ready = true

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			input := m.textarea.Value()
			if input == "" {
				return m, nil
			}

			// Очищаем поле ввода
			m.textarea.Reset()

			// Добавляем сообщение пользователя в историю
			m.chatState.history = append(m.chatState.history, llm.Message{
				Role:    llm.RoleUser,
				Content: input,
			})

			// Обновляем UI с использованием стилей из фреймворка
			m.appendLog(ui.UserMsgStyle("Вы: ") + input)

			// Устанавливаем флаг загрузки и запускаем запрос
			m.loading = true
			return m, tea.Batch(
				m.spinner.Tick,
				makeAIRequestCmd(m.chatState),
			)

		case tea.KeyCtrlU:
			m.textarea.Reset()
			return m, nil
		}

	case userMsg:
		return m, nil

	case aiResponseMsg:
		m.loading = false

		// Добавляем ответ AI в историю
		m.chatState.history = append(m.chatState.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: msg.text,
		})

		// Обновляем UI с использованием стилей из фреймворка
		m.appendLog(ui.SystemMsgStyle("AI: ") + msg.text)

	case errorMsg:
		m.loading = false
		m.err = msg.err

		// Показываем ошибку с использованием стилей из фреймворка
		m.appendLog(ui.ErrorMsgStyle("❌ Ошибка: ") + msg.err.Error())
	}

	// Если идет загрузка, анимируем спиннер
	if m.loading {
		m.spinner, spCmd = m.spinner.Update(msg)
		return m, tea.Batch(tiCmd, vpCmd, spCmd)
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

// Хелпер для добавления строки в лог (следуя паттернам фреймворка)
func (m *chatModel) appendLog(str string) {
	newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), str)
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// View рендерит интерфейс (оптимизировано под фреймворк)
func (m chatModel) View() string {
	if !m.ready {
		return "Initializing UI..."
	}

	// Формируем строку статуса (Header) как в фреймворке
	status := fmt.Sprintf(" CHAT | MODEL: %s ", m.chatState.modelName)

	// Используем стили из фреймворка (создаем локальные версии)
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Bold(true)

	grayStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	header := headerStyle.
		Width(m.viewport.Width).
		Render(status)

	// Разделительная линия
	border := grayStyle.
		Width(m.viewport.Width).
		Render("──────────────────────────────────────────────────")

	// Собираем всё вместе: Header + Viewport + Border + Input
	view := fmt.Sprintf("%s\n%s\n%s\n%s",
		header,
		m.viewport.View(),
		border,
		m.textarea.View(),
	)

	if m.loading {
		view += "\n" + m.spinner.View() + " Думаю..."
	}

	return view
}

// Команда для запроса к AI
func makeAIRequestCmd(chatState *ChatState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), chatState.modelDef.Timeout)
		defer cancel()

		// Отправляем запрос с использованием метода Generate
		response, err := chatState.provider.Generate(ctx, chatState.history)
		if err != nil {
			return errorMsg{err: fmt.Errorf("ошибка API: %w", err)}
		}

		return aiResponseMsg{text: response.Content}
	}
}

func main() {
	// Используем основной конфигурационный файл фреймворка
	configPath := "../../config.yaml"
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	// Получаем модель для чата из конфига (следуя паттернам фреймворка)
	modelName := cfg.Models.DefaultChat
	if modelName == "" {
		// Fallback: берем первый ключ из определений
		for k := range cfg.Models.Definitions {
			modelName = k
			break
		}
	}

	modelDef, ok := cfg.Models.Definitions[modelName]
	if !ok {
		log.Fatalf("Модель '%s' не найдена в определениях", modelName)
	}

	// Создаем провайдер через фабрику фреймворка
	provider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		log.Fatalf("Ошибка создания провайдера: %v", err)
	}

	// Инициализируем состояние чата с системным промптом
	// TODO: вынести системный промпт в конфигурацию для соответствия паттернам фреймворка
	systemPrompt := "Ты полезный AI ассистент. Отвечай на вопросы пользователя вежливо и информативно. Отвечай на русском языке, если вопрос задан на русском. Будь кратким, но емким."

	chatState := &ChatState{
		provider:  provider,
		modelDef:  modelDef,
		modelName: modelName,
		history: []llm.Message{
			{
				Role:    llm.RoleSystem,
				Content: systemPrompt,
			},
		},
	}

	// Запускаем TUI (следуя паттернам фреймворка)
	p := tea.NewProgram(
		initialModel(chatState),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		log.Fatalf("Ошибка запуска TUI: %v", err)
	}
}
