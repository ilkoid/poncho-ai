// Poncho AI TUI Application
// Основная точка входа для интерактивного интерфейса
//
// REFACTORED (Phase 4): Eliminated internal/ui dependency (Rule 6 compliance)
// - Business logic moved to cmd/ layer
// - Uses tui.Model with callback pattern
// - Special commands (load, render, demo, ping) handled locally
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tui"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()

	utils.Info("Application started", "version", "1.0")

	// 1. Инициализируем конфигурацию
	cfg, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		utils.Error("Failed to load config", "error", err, "path", cfgPath)
		return err
	}

	log.Printf("Config loaded successfully from %s", cfgPath)
	utils.Info("Config loaded", "path", cfgPath, "default_model", cfg.Models.DefaultChat)

	// Логируем загруженные ключи (с маскированием для безопасности)
	logKeysInfo(cfg)

	// 2. Создаём агент
	client, err := agent.New(context.Background(), agent.Config{ConfigPath: cfgPath})
	if err != nil {
		utils.Error("Agent creation failed", "error", err)
		return fmt.Errorf("agent creation failed: %w", err)
	}

	// 3. Создаём emitter для событий агента
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := emitter.Subscribe()

	log.Printf("Agent client initialized with event emitter")

	// 4. Создаём PonchoModel с бизнес-логикой
	coreState := client.GetState()
	ponchoModel := NewPonchoModel(coreState, client, cfg.Models.DefaultChat, sub)

	// 5. Запускаем Bubble Tea программу
	log.Println("Starting TUI...")
	utils.Info("Starting TUI")

	p := tea.NewProgram(ponchoModel)

	if _, err := p.Run(); err != nil {
		utils.Error("TUI error", "error", err)
		return fmt.Errorf("TUI error: %w", err)
	}

	utils.Info("Application exited normally")
	return nil
}

// ===== PONCHO MODEL =====

// PonchoModel представляет главную модель UI для Poncho AI.
//
// REFACTORED (Phase 4): Встраивает tui.Model вместо использования internal/ui.
// Это соответствует Rule 6 - бизнес-логика теперь находится в cmd/ слое.
//
// Особенности:
//   - Поддержка специальных команд: load, render, demo, ping, ask
//   - Todo панель для отображения задач
//   - Отслеживание текущего артикула
//   - Thread-safe через embedded tui.Model
type PonchoModel struct {
	// Embed tui.Model для базовой TUI функциональности
	*tui.Model

	// App-specific state
	client           *agent.Client
	currentArticleID string
	currentModel     string
	config           *config.AppConfig
}

// NewPonchoModel создаёт новую PonchoModel.
//
// Rule 6 Compliance: Бизнес-логика (команды, todo панель) теперь в cmd/ слое,
// pkg/tui остается reusable.
func NewPonchoModel(
	coreState *state.CoreState,
	client *agent.Client,
	currentModel string,
	eventSub events.Subscriber,
) *PonchoModel {
	// Создаём базовую tui.Model
	baseModel := tui.NewModel(context.Background(), client, coreState, eventSub)

	return &PonchoModel{
		Model:            baseModel,
		client:           client,
		currentArticleID: "NONE",
		currentModel:     currentModel,
		config:           coreState.Config,
	}
}

// Init реализует tea.Model интерфейс.
func (m *PonchoModel) Init() tea.Cmd {
	return m.Model.Init()
}

// Update реализует tea.Model интерфейс.
//
// Расширяет базовую обработку:
// - Обрабатывает специальные команды (load, render, demo, ping, ask)
// - Делегирует остальные события базовой модели
func (m *PonchoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Перехватываем Enter для обработки команд
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyEnter {
		return m.handleEnter()
	}

	// Все остальные сообщения делегируем базовой модели
	baseModel, cmd := m.Model.Update(msg)
	m.Model = baseModel.(*tui.Model)
	return m, cmd
}

// View реализует tea.Model интерфейс.
//
// Добавляет todo панель справа от основного контента.
func (m *PonchoModel) View() string {
	// Получаем базовый view из tui.Model
	baseView := m.Model.View()

	// Добавляем todo панель
	coreState := m.client.GetState()
	todoPanel := renderTodoPanel(coreState.GetTodoManager(), 40)

	// Комбинируем основной контент с todo панелью
	return lipgloss.JoinHorizontal(lipgloss.Top, baseView, todoPanel)
}

// handleEnter обрабатывает нажатие Enter.
//
// Парсит команду и либо выполняет специальную команду, либо делегирует агенту.
func (m *PonchoModel) handleEnter() (tea.Model, tea.Cmd) {
	// Получаем ввод из textarea
	textarea := m.GetTextarea()
	input := textarea.Value()
	if strings.TrimSpace(input) == "" {
		return m, nil
	}

	// Очищаем ввод
	textarea.Reset()
	m.SetTextarea(textarea)

	// Добавляем сообщение пользователя в лог
	m.Append(userMsgStyle("USER > ") + input, true)

	// Парсим команду
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return m, nil
	}
	cmd := parts[0]

	// Специальные команды
	switch cmd {
	case "ask":
		// Делегируем агенту
		if len(parts) > 1 {
			query := strings.Join(parts[1:], " ")
			return m, m.startAgent(query)
		}
		return m, m.startAgent(input)

	case "load", "render", "demo", "ping":
		// Выполняем встроенную команду
		return m, m.performCommand(input)

	default:
		// Неизвестная команда - делегируем агенту
		return m, m.startAgent(input)
	}
}

// startAgent запускает агент с обработкой событий.
func (m *PonchoModel) startAgent(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, err := m.client.Run(ctx, query)
		if err != nil {
			return tui.EventMsg(events.Event{
				Type:      events.EventError,
				Data:      events.ErrorData{Err: err},
				Timestamp: time.Now(),
			})
		}

		return tui.EventMsg(events.Event{
			Type:      events.EventDone,
			Data:      events.MessageData{Content: result},
			Timestamp: time.Now(),
		})
	}
}

// performCommand выполняет встроенные команды.
func (m *PonchoModel) performCommand(input string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		parts := strings.Fields(input)
		if len(parts) == 0 {
			return nil
		}
		cmd := parts[0]
		args := parts[1:]

		coreState := m.client.GetState()

		switch cmd {
		case "load":
			if len(args) < 1 {
				m.Append(errorMsgStyle("ERROR: usage: load <article_id>"), true)
				return nil
			}
			articleID := args[0]

			s3Client := coreState.GetStorage()
			if s3Client == nil {
				m.Append(errorMsgStyle("ERROR: s3 client is not initialized"), true)
				return nil
			}

			rawObjects, err := s3Client.ListFiles(ctx, articleID)
			if err != nil {
				m.Append(errorMsgStyle("ERROR: "+err.Error()), true)
				return nil
			}

			classifierEngine := classifier.New(coreState.Config.FileRules)
			classifiedFiles, err := classifierEngine.Process(rawObjects)
			if err != nil {
				m.Append(errorMsgStyle("ERROR: "+err.Error()), true)
				return nil
			}

			coreState.SetCurrentArticle(articleID, classifiedFiles)
			m.currentArticleID = articleID

			var report strings.Builder
			report.WriteString(fmt.Sprintf("✅ Article %s loaded successfully.\n", articleID))
			report.WriteString("Found files:\n")
			for tag, files := range classifiedFiles {
				report.WriteString(fmt.Sprintf("  • [%s]: %d files\n", strings.ToUpper(tag), len(files)))
			}
			if len(classifiedFiles["sketch"]) == 0 {
				report.WriteString("⚠️ WARNING: No sketches found!\n")
			}

			m.Append(systemMsgStyle(report.String()), true)

		case "render":
			if len(args) < 1 {
				m.Append(errorMsgStyle("ERROR: usage: render <prompt_file.yaml>"), true)
				return nil
			}
			filename := args[0]

			if coreState.GetCurrentArticleID() == "NONE" {
				m.Append(errorMsgStyle("ERROR: no article loaded. use 'load <id>' first"), true)
				return nil
			}

			fullPath := fmt.Sprintf("%s/%s", coreState.Config.App.PromptsDir, filename)
			p, err := prompt.Load(fullPath)
			if err != nil {
				m.Append(errorMsgStyle("ERROR: "+err.Error()), true)
				return nil
			}

			articleID, files := coreState.GetCurrentArticle()
			imageURL := "NO_IMAGE_FOUND"
			if sketches, ok := files["sketch"]; ok && len(sketches) > 0 {
				imageURL = fmt.Sprintf("s3://%s/%s", coreState.Config.S3.Bucket, sketches[0].OriginalKey)
			}

			templateData := map[string]interface{}{
				"ArticleID": articleID,
				"ImageURL":  imageURL,
			}

			messages, err := p.RenderMessages(templateData)
			if err != nil {
				m.Append(errorMsgStyle("ERROR: "+err.Error()), true)
				return nil
			}

			var output strings.Builder
			output.WriteString(fmt.Sprintf("📋 Rendered Prompt for model: %s\n", p.Config.Model))
			output.WriteString("--------------------------------------------------\n")

			for _, msg := range messages {
				contentPreview := msg.Content
				if len(contentPreview) > 200 {
					contentPreview = contentPreview[:200] + "...(truncated)"
				}
				output.WriteString(fmt.Sprintf("[%s]: %s\n\n", strings.ToUpper(msg.Role), contentPreview))
			}

			m.Append(systemMsgStyle(output.String()), true)

		case "demo":
			todoManager := coreState.GetTodoManager()
			if todoManager == nil {
				m.Append(errorMsgStyle("ERROR: todo manager not initialized"), true)
				return nil
			}
			todoManager.Add("Проверить API Wildberries")
			todoManager.Add("Загрузить эскизы из S3")
			todoManager.Add("Сгенерировать описание товара")
			taskID := todoManager.Add("Провалить эту задачу для теста")
			todoManager.Complete(2)
			todoManager.Fail(taskID, "Тестовая ошибка")
			m.Append(systemMsgStyle("✅ Added 4 demo todos (1 done, 1 failed, 2 pending)"), true)

		case "ping":
			m.Append(systemMsgStyle("Pong! System is alive."), true)
		}

		return nil
	}
}

// ===== STYLES =====

func userMsgStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")). // Розовый
		Bold(true).
		Render(str)
}

func systemMsgStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("04B575")). // Зеленый
		Render(str)
}

func errorMsgStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")). // Красный
		Bold(true).
		Render(str)
}

// ===== TODO PANEL RENDERING =====

// renderTodoPanel рендерит панель с задачами.
//
// Локальная функция для cmd/poncho (не для переиспользования).
func renderTodoPanel(manager *todo.Manager, width int) string {
	tasks := manager.GetTasks()
	pending, done, failed := manager.GetStats()

	if len(tasks) == 0 {
		return todoBorderStyle.Width(width).Render(
			todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ") + "\n" +
				taskPendingStyle.Render("Нет активных задач"),
		)
	}

	var content strings.Builder
	content.WriteString(todoTitleStyle.Render("📋 ПЛАН ДЕЙСТВИЙ"))
	content.WriteString("\n\n")

	for _, task := range tasks {
		var statusIcon string
		var taskStyle lipgloss.Style

		switch task.Status {
		case todo.StatusDone:
			statusIcon = "✓"
			taskStyle = taskDoneStyle
		case todo.StatusFailed:
			statusIcon = "✗"
			taskStyle = taskFailedStyle
		default:
			statusIcon = "○"
			taskStyle = taskPendingStyle
		}

		content.WriteString(fmt.Sprintf("%s %d. %s\n",
			statusIcon, task.ID,
			taskStyle.Render(task.Description)))

		if task.Status == todo.StatusFailed && task.Metadata != nil {
			if err, ok := task.Metadata["error"].(string); ok {
				content.WriteString(fmt.Sprintf("   %s\n",
					taskFailedStyle.Render("Ошибка: "+err)))
			}
		}
	}

	content.WriteString("\n")
	content.WriteString(statsStyle.Render(
		fmt.Sprintf("Выполнено: %d | В работе: %d | Провалено: %d",
			done, pending, failed)))

	return todoBorderStyle.Width(width).Render(content.String())
}

// Стили для Todo панели
var (
	todoBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1).
			MarginRight(1)

	todoTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)

	taskPendingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("251"))

	taskDoneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Strikethrough(true)

	taskFailedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Strikethrough(true)

	statsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true).
			MarginTop(1)
)

// ===== UTILITIES =====

// maskKey показывает первые 8 символов ключа для идентификации.
func maskKey(key string) string {
	if key == "" {
		return "NOT SET"
	}
	if len(key) <= 8 {
		return key + "..."
	}
	return key[:8] + "..."
}

// logKeysInfo логирует информацию о загруженных API ключах.
func logKeysInfo(cfg *config.AppConfig) {
	log.Println("=== API Keys Status ===")

	// ZAI API Key (берём из первой модели определения)
	if len(cfg.Models.Definitions) > 0 {
		for _, modelDef := range cfg.Models.Definitions {
			log.Printf("  ZAI_API_KEY (model: %s): %s", modelDef.ModelName, maskKey(modelDef.APIKey))
			break // Показываем только первый
		}
	}

	// S3 Keys
	log.Printf("  S3_ACCESS_KEY: %s", maskKey(cfg.S3.AccessKey))
	log.Printf("  S3_SECRET_KEY: %s", maskKey(cfg.S3.SecretKey))

	// WB API Key
	log.Printf("  WB_API_CONTENT_KEY: %s", maskKey(cfg.WB.APIKey))

	log.Println("======================")
}

// Ensure PonchoModel implements tea.Model
var _ tea.Model = (*PonchoModel)(nil)
