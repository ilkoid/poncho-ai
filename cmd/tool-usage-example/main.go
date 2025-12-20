/*
Что делает этот код?
Интеграция: Использует pkg/wb, pkg/llm, pkg/tools (все наши наработки).

Prompt Engineering: В функции RunAgentStep мы динамически формируем системный промпт, добавляя туда описания инструментов из реестра (registry.GetDefinitions()).

Логика выбора: Если LLM отвечает JSON-ом {"tool": ...}, мы парсим это, находим инструмент в реестре и выполняем его.

TUI: Показывает красивый чат с историей и спиннером загрузки.

Как запустить
Создай файл cmd/tool-usage-example/main.go.

Вставь код.

go mod tidy.

go run cmd/tool-usage-example/main.go.

Введи: Покажи категории WB (или что-то похожее).

Наблюдай магию: агент должен вызвать get_wb_parent_categories и показать JSON с категориями.

P.S. В примере я использовал упрощенный парсинг JSON из ответа LLM. В реальном "умном" агенте (следующий этап) мы научим адаптер openai нативно поддерживать tool_calls API, чтобы не парсить текст руками. Но для демонстрации этого достаточно.

*/


package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	_"github.com/charmbracelet/lipgloss"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// --- 1. Бизнес-логика Агента ---

// AgentState хранит контекст нашего разговора
type AgentState struct {
	History  []llm.Message
	Provider llm.Provider
	Registry *tools.Registry
}

// RunAgentStep - один шаг мысли агента (LLM Call -> Tool Call -> Result)
func RunAgentStep(ctx context.Context, state *AgentState, userInput string) (string, error) {
	// 1. Добавляем сообщение пользователя в историю
	state.History = append(state.History, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{{Type: llm.TypeText, Text: userInput}},
	})

	// 2. Формируем запрос к LLM, включая список доступных инструментов
	defs := state.Registry.GetDefinitions()
	
	// ВАЖНО: В текущем простом адаптере (pkg/llm/openai) мы пока не реализовали
	// нативную поддержку поля "tools" в API запросе.
	// Поэтому мы используем "Prompt Engineering" подход:
	// Мы опишем инструменты в системном промпте.
	// (Для продакшена лучше допилить адаптер на поле `tools`, но для примера сойдет и так).
	
	systemPrompt := "Ты помощник по Wildberries. У тебя есть доступ к следующим инструментам:\n"
	for _, d := range defs {
		systemPrompt += fmt.Sprintf("- %s: %s (args: %v)\n", d.Name, d.Description, d.Parameters)
	}
	systemPrompt += "\nЕсли тебе нужно вызвать инструмент, верни ответ строго в формате JSON: {\"tool\": \"name\", \"args\": \"...\"}.\nЕсли инструмент не нужен, просто ответь текстом."

	// Вставляем системный промпт в начало (или обновляем существующий)
	messagesToSend := append([]llm.Message{{
		Role:    llm.RoleSystem,
		Content: []llm.ContentPart{{Type: llm.TypeText, Text: systemPrompt}},
	}}, state.History...)

	req := llm.ChatRequest{
		Model:       "glm-4.6v-flash", // или модель из конфига
		Temperature: 0.1,              // Низкая температура для точности вызова функций
		MaxTokens:   1000,
		Messages:    messagesToSend,
		// Format: "json_object", // Можно включить, если хотим форсировать JSON
	}

	// 3. Вызов LLM
	response, err := state.Provider.Chat(ctx, req)
	if err != nil {
		return "", err
	}

	// 4. Проверяем, хочет ли модель вызвать инструмент (парсим ответ)
	// (Упрощенная логика: ищем JSON блок)
	var toolCall struct {
		Tool string `json:"tool"`
		Args string `json:"args"` // Аргументы могут быть строкой или объектом, упрощаем
	}
	
	// Пытаемся распарсить ответ как вызов инструмента
	isToolCall := false
	if strings.Contains(response, "{") {
		// Очищаем markdown (``````)
		cleanJson := cleanJsonBlock(response)
		if err := json.Unmarshal([]byte(cleanJson), &toolCall); err == nil && toolCall.Tool != "" {
			isToolCall = true
		}
	}

	if isToolCall {
		// 5. Выполняем инструмент
		t, err := state.Registry.Get(toolCall.Tool)
		if err != nil {
			return fmt.Sprintf("Error finding tool: %v", err), nil
		}
		
		toolResult, err := t.Execute(ctx, toolCall.Args)
		if err != nil {
			return fmt.Sprintf("Tool Execution Error: %v", err), nil
		}
		
		// 6. Возвращаем результат инструмента в контекст и делаем еще один шаг (рекурсия или просто возврат)
		// Для примера просто вернем результат пользователю
		return fmt.Sprintf("🔧 Tool Called: %s\n📦 Result: %s", toolCall.Tool, toolResult), nil
	}

	// Если это не вызов инструмента, возвращаем текст ответа
	state.History = append(state.History, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{{Type: llm.TypeText, Text: response}},
	})
	
	return response, nil
}


// --- 2. UI Компоненты (TUI) ---

type model struct {
	state    *AgentState
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	
	loading  bool
	err      error
}

func initialModel(agentState *AgentState) model {
	ta := textarea.New()
	ta.Placeholder = "Спроси что-нибудь (например: 'Покажи категории WB')..."
	ta.Focus()
	ta.SetHeight(2)

	vp := viewport.New(80, 20)
	vp.SetContent("🤖 Agent Ready. Tools: get_wb_parent_categories\n")

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		state:    agentState,
		textarea: ta,
		viewport: vp,
		spinner:  sp,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			input := m.textarea.Value()
			if input == "" {
				return m, nil
			}
			m.textarea.Reset()
			
			// Добавляем в лог
			content := m.viewport.View() + fmt.Sprintf("\n\n👤 You: %s", input)
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
			
			m.loading = true
			return m, tea.Batch(
				m.spinner.Tick,
				makeRequestCmd(m.state, input),
			)
		}
	
	case agentResponseMsg:
		m.loading = false
		content := m.viewport.View() + fmt.Sprintf("\n🤖 Agent:\n%s", msg.text)
		m.viewport.SetContent(content)
		m.viewport.GotoBottom()
	
	case errMsg:
		m.loading = false
		m.err = msg.err
	}

	if m.loading {
		m.spinner, spCmd = m.spinner.Update(msg)
		return m, tea.Batch(tiCmd, vpCmd, spCmd)
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m model) View() string {
	view := fmt.Sprintf(
		"%s\n\n%s",
		m.viewport.View(),
		m.textarea.View(),
	)
	if m.loading {
		view += fmt.Sprintf("\n%s Thinking...", m.spinner.View())
	}
	return view
}

// --- 3. Асинхронные команды ---

type agentResponseMsg struct{ text string }
type errMsg struct{ err error }

func makeRequestCmd(state *AgentState, input string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		
		resp, err := RunAgentStep(ctx, state, input)
		if err != nil {
			return errMsg{err}
		}
		return agentResponseMsg{resp}
	}
}

// --- 4. Main Setup ---

func main() {
	// A. Загрузка конфига
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// B. Инициализация клиентов
	// 1. WB Client
	wbClient := wb.New(cfg.WB.APIKey)
	
	// 2. LLM Provider
	// Берем дефолтную модель из конфига
	modelDef := cfg.Models.Definitions[cfg.Models.DefaultVision]
	provider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		log.Fatal(err)
	}

	// C. Регистрация инструментов
	reg := tools.NewRegistry()
	reg.Register(std.NewWbParentCategoriesTool(wbClient))

	// D. Сборка состояния агента
	agentState := &AgentState{
		Provider: provider,
		Registry: reg,
		History:  make([]llm.Message, 0),
	}

	// E. Запуск TUI
	p := tea.NewProgram(initialModel(agentState))
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// Хелпер для очистки JSON от markdown - тут возможны ошибки в Trim!!!
func cleanJsonBlock(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
