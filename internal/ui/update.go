// Package ui реализует логику обновления TUI (Bubble Tea).
//
// Обрабатывает нажатия клавиш, результаты команд и обновляет состояние UI.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// AgentFinishedMsg — сигнал что агент завершил выполнение.
type AgentFinishedMsg struct {
	Result app.CommandResultMsg
}

// AgentTickMsg — периодическое сообщение для проверки результата работы агента.
type AgentTickMsg time.Time

// Update обрабатывает сообщения Bubble Tea и обновляет состояние модели.
//
// Является частью Model-View-Update архитектуры Bubble Tea.
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала
//   - tea.KeyMsg: нажатия клавиш
//   - app.CommandResultMsg: результаты выполнения команд
//   - AgentFinishedMsg: завершение выполнения агента
//
// Возвращает обновленную модель и команду для асинхронного выполнения.
func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {

	// 0. Агент завершил работу
	case AgentFinishedMsg:
		// Обрабатываем результат
		if msg.Result.Err != nil {
			m.appendLog(errorMsgStyle("ERROR: ") + msg.Result.Err.Error())
		} else {
			m.appendLog(systemMsgStyle("SYSTEM: ") + msg.Result.Output)
		}
		m.textarea.Focus()
		return m, nil

	// 0a. Tick от агента - продолжаем опрос канала
	case AgentTickMsg:
		// Проверяем что агент запущен и получаем канал
		if m.agent != nil && m.agent.isRunning() {
			resultCh := m.agent.getChannel()
			if resultCh != nil {
				// Проверяем канал
				select {
				case agentMsg := <-resultCh:
					// Результат получен - останавливаем агент
					m.agent.stop()
					return m, func() tea.Msg {
						return AgentFinishedMsg{Result: agentMsg.result}
					}
				default:
					// Продолжаем тикать
					return m, tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
						return AgentTickMsg(t)
					})
				}
			}
		}
		return m, nil

	// 1. Изменение размера окна терминала
	case tea.WindowSizeMsg:
		// Реальная ширина todo панели = Width(40) + MarginRight(1) = 41
		const todoPanelWidth = 41 // Ширина todo панели с учетом margin
		const panelGap = 0        // Gap уже включен в MarginRight

		headerHeight := 1
		footerHeight := m.textarea.Height() + 2 // + граница

		// Вычисляем высоту для области контента
		vpHeight := msg.Height - headerHeight - footerHeight
		if vpHeight < 0 {
			vpHeight = 0
		}

		// Вычисляем ширину для основного контента (вычитаем todo панель)
		vpWidth := msg.Width - todoPanelWidth - panelGap
		if vpWidth < 20 {
			vpWidth = 20 // Минимальная ширина для очень узких окон
		}

		// Обновляем размеров существующего вьюпорта
		m.viewport.Width = vpWidth
		m.viewport.Height = vpHeight

		// Только при первом запуске (если нужно инициализировать контент)
		if !m.ready {
			m.ready = true

			// Выводим информацию о размере окна для отладки
			dimensions := fmt.Sprintf("Window: %dx%d | Viewport: %dx%d | Todo: 40",
				msg.Width, msg.Height, vpWidth, vpHeight)
			m.appendLog(systemMsgStyle("INFO: ") + dimensions)
		}

		// Textarea тоже на всю ширину основного контента
		m.textarea.SetWidth(vpWidth)

	// 2. Клавиши
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		case tea.KeyEnter:
			input := m.textarea.Value()
			if strings.TrimSpace(input) == "" {
				return m, nil
			}

			// Очищаем ввод
			m.textarea.Reset()

			// Добавляем сообщение пользователя в лог
			m.appendLog(userMsgStyle("USER > ") + input)

			// Парсим команду
			parts := strings.Fields(input)
			if len(parts) == 0 {
				return m, nil
			}
			cmd := parts[0]

			// Проверяем special cases для агента
			if cmd == "ask" && len(parts) > 1 {
				// Команда "ask" - запускаем агент
				query := strings.Join(parts[1:], " ")
				return m, startAgent(&m, query)
			}

			// Проверяем CommandRegistry
			cmdRegistry := m.appState.GetCommandRegistry()
			isKnownCommand := false
			if cmdRegistry != nil {
				cmds := cmdRegistry.GetCommands()
				for _, c := range cmds {
					if c == cmd {
						isKnownCommand = true
						break
					}
				}
			}

			if isKnownCommand {
				// Известная команда - выполняем через performCommand
				return m, performCommand(input, m.appState)
			}

			// Неизвестная команда - делегируем агенту
			if m.appState.Orchestrator != nil {
				return m, startAgent(&m, input)
			}

			// Неизвестная команда и нет агента
			return m, performCommand(input, m.appState)
		}

	// 3. Результат выполнения команды (прилетел асинхронно)
	//    NOTE: для agent-запросов используем AgentFinishedMsg для интерактивности
	case app.CommandResultMsg:
		// Если это не агентский запрос — обрабатываем как обычно
		// (агентские запросы приходят через AgentFinishedMsg)
		if msg.Err != nil {
			m.appendLog(errorMsgStyle("ERROR: ") + msg.Err.Error())
		} else {
			m.appendLog(systemMsgStyle("SYSTEM: ") + msg.Output)
		}
		// Возвращаем фокус на ввод
		m.textarea.Focus()
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

// appendLog добавляет строку в лог чата и прокручивает вьюпорт вниз.
//
// Функция автоматически переносит длинные строки, чтобы они влезали в ширину вьюпорта.
// Короткие сообщения (ввод пользователя) остаются без переносов для красоты.
func (m *MainModel) appendLog(str string) {
	// Используем полную ширину вьюпорта (уже вычтена todo панель)
	availableWidth := m.viewport.Width
	if availableWidth < 10 {
		availableWidth = 10 // Минимальная ширина
	}

	// Проверяем длину самой длинной строки в тексте
	maxLineLen := longestLineLength(str)

	// Переносим только если есть очень длинные строки
	// Короткие сообщения (ввод пользователя) оставляем как есть
	var finalStr string
	if maxLineLen > availableWidth {
		finalStr = utils.WrapText(str, availableWidth)
	} else {
		finalStr = str
	}

	newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), finalStr)
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// longestLineLength находит длину самой длинной строки в многострочном тексте.
//
// Используется для определения необходимости переноса строк при выводе в лог.
func longestLineLength(s string) int {
	maxLen := 0
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if len(line) > maxLen {
			maxLen = len(line)
		}
	}
	return maxLen
}

// performCommand обрабатывает ввод пользователя и маршрутизирует команды.
//
// Это "мозг" TUI, который:
//  1. Парсит ввод на команду и аргументы
//  2. Проверяет CommandRegistry для зарегистрированных команд
//  3. Делегирует неизвестные команды агенту (естественный интерфейс)
//  4. Выполняет команды асинхронно через tea.Cmd
//
// Поддерживаемые команды:
//   - load <article_id>: Загружает метаданные из S3 и классифицирует файлы
//   - render <prompt_file>: Рендерит промпт с данными текущего артикула
//   - ask <query>: Делегирует запрос агенту
//   - todo <subcommand>: Управление задачами (через CommandRegistry)
//   - <любой текст>: Делегируется агенту напрямую (естественный интерфейс)
//   - ping: Проверка работоспособности системы
//
// Возвращает tea.Cmd для асинхронного выполнения, чтобы UI не зависал.
func performCommand(input string, state *app.GlobalState) tea.Cmd {
	return func() tea.Msg {
		// Создаем контекст с таймаутом (увеличен для сложных запросов)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		// Разбираем ввод на команду и аргументы
		parts := strings.Fields(input)
		if len(parts) == 0 {
			return nil // Пустой ввод
		}
		cmd := parts[0]
		args := parts[1:]

		switch cmd {

		// === КОМАНДА 1: LOAD <ARTICLE_ID> ===
		// Загружает метаданные из S3 и раскладывает файлы по полочкам
		case "load":
			if len(args) < 1 {
				return app.CommandResultMsg{Err: fmt.Errorf("usage: load <article_id>")}
			}
			articleID := args[0]

			// 1. Получаем "сырой" список файлов из S3
			// (Предполагаем, что state.S3 уже инициализирован в main.go)
			if state.S3 == nil {
				return app.CommandResultMsg{Err: fmt.Errorf("s3 client is not initialized")}
			}

			rawObjects, err := state.S3.ListFiles(ctx, articleID)
			if err != nil {
				return app.CommandResultMsg{Err: fmt.Errorf("s3 error: %w", err)}
			}

			// 2. Классифицируем файлы согласно правилам из config.yaml
			classifierEngine := classifier.New(state.Config.FileRules)
			classifiedFiles, err := classifierEngine.Process(rawObjects)
			if err != nil {
				return app.CommandResultMsg{Err: fmt.Errorf("classification error: %w", err)}
			}

			// 3. Конвертируем ClassifiedFile в FileMeta
			convertedFiles := make(map[string][]*app.FileMeta)
			for tag, files := range classifiedFiles {
				var fileMetas []*app.FileMeta
				for _, file := range files {
					fileMetas = append(fileMetas, &app.FileMeta{
						ClassifiedFile:    file,
						VisionDescription: "",
						Tags:              []string{},
					})
				}
				convertedFiles[tag] = fileMetas
			}

			// 4. Обновляем глобальный State потокобезопасно
			state.SetCurrentArticle(articleID, convertedFiles)

			// 4. Формируем красивый отчет для пользователя
			var report strings.Builder
			report.WriteString(fmt.Sprintf("✅ Article %s loaded successfully.\n", articleID))
			report.WriteString("Found files:\n")

			// Проходимся по всем найденным категориям
			for tag, files := range classifiedFiles {
				report.WriteString(fmt.Sprintf("  • [%s]: %d files\n", strings.ToUpper(tag), len(files)))
			}

			// Добавим предупреждение, если важных категорий нет (опционально)
			if len(classifiedFiles["sketch"]) == 0 {
				report.WriteString("⚠️ WARNING: No sketches found!\n")
			}

			return app.CommandResultMsg{Output: report.String()}

		// === КОМАНДА 2: RENDER <PROMPT_FILE> ===
		// Тестирует промпт, подставляя данные из загруженного артикула
		case "render":
			if len(args) < 1 {
				return app.CommandResultMsg{Err: fmt.Errorf("usage: render <prompt_file.yaml>")}
			}
			filename := args[0]

			// Проверяем, загружен ли вообще артикул (потокобезопасно)
			if state.GetCurrentArticleID() == "NONE" {
				return app.CommandResultMsg{Err: fmt.Errorf("no article loaded. use 'load <id>' first")}
			}

			// 1. Загружаем сам файл промпта
			// state.Config.App.PromptsDir берется из конфига (например "./prompts")
			fullPath := fmt.Sprintf("%s/%s", state.Config.App.PromptsDir, filename)
			p, err := prompt.Load(fullPath)
			if err != nil {
				return app.CommandResultMsg{Err: fmt.Errorf("failed to load prompt '%s': %w", filename, err)}
			}

			// 2. Готовим данные для шаблона (Data Context)
			// Берем реальные данные из State потокобезопасно.
			articleID, files := state.GetCurrentArticle()
			imageURL := "NO_IMAGE_FOUND"
			if sketches, ok := files["sketch"]; ok && len(sketches) > 0 {
				// В реальном S3 URL может быть подписанным (Presigned), но пока просто ключ
				imageURL = fmt.Sprintf("s3://%s/%s", state.Config.S3.Bucket, sketches[0].OriginalKey)
			}

			templateData := map[string]interface{}{
				"ArticleID": articleID,
				"ImageURL":  imageURL,
				// Можно добавить сюда содержимое JSON из категории plm_data, если нужно
			}

			// 3. Рендерим сообщения
			messages, err := p.RenderMessages(templateData)
			if err != nil {
				return app.CommandResultMsg{Err: fmt.Errorf("render error: %w", err)}
			}

			// 4. Выводим результат (симуляция отправки)
			var output strings.Builder
			output.WriteString(fmt.Sprintf("📋 Rendered Prompt for model: %s\n", p.Config.Model))
			output.WriteString("--------------------------------------------------\n")

			for _, m := range messages {
				// Обрезаем длинный текст для красоты лога
				contentPreview := m.Content
				if len(contentPreview) > 200 {
					contentPreview = contentPreview[:200] + "...(truncated)"
				}
				output.WriteString(fmt.Sprintf("[%s]: %s\n\n", strings.ToUpper(m.Role), contentPreview))
			}

			return app.CommandResultMsg{Output: output.String()}

		// === КОМАНДА 3: DEMO ===
		// Добавляет тестовые задачи для проверки отображения todo панели
		case "demo":
			state.Todo.Add("Проверить API Wildberries")
			state.Todo.Add("Загрузить эскизы из S3")
			state.Todo.Add("Сгенерировать описание товара")
			taskID := state.Todo.Add("Провалить эту задачу для теста")
			state.Todo.Complete(2)
			state.Todo.Fail(taskID, "Тестовая ошибка")
			return app.CommandResultMsg{Output: "✅ Added 4 demo todos (1 done, 1 failed, 2 pending)"}

		// === КОМАНДА 4: PING ===
		case "ping":
			return app.CommandResultMsg{Output: "Pong! System is alive."}

		// === НЕИЗВЕСТНАЯ КОМАНДА ===
		// NOTE: "ask" и делегирование агенту обрабатываются в Update напрямую
		default:
			return app.CommandResultMsg{Err: fmt.Errorf("unknown command: '%s'. Try 'load <id>', 'demo', 'render <file>', 'ask <query>' or 'todo help'", cmd)}
		}
	}
}

// startAgent запускает агента в отдельной горутине и возвращает tea.Tick для опроса результата.
//
// Канал сохраняется в модели для последующей проверки в Update().
// Возвращаемая команда только отправляет AgentTickMsg - чтение канала происходит в Update().
func startAgent(m *MainModel, query string) tea.Cmd {
	state := m.appState

	// Проверяем что оркестратор инициализирован
	if state.Orchestrator == nil {
		utils.Error("startAgent: Orchestrator is nil!", "query", query)
		return func() tea.Msg {
			return AgentFinishedMsg{Result: app.CommandResultMsg{Err: fmt.Errorf("orchestrator not initialized")}}
		}
	}

	// Создаем канал для результата
	resultCh := make(chan agentResultMsg, 1)

	// Пытаемся запустить агент (thread-safe проверка)
	if !m.agent.tryStart(resultCh) {
		utils.Error("startAgent: Agent already running!", "query", query)
		return func() tea.Msg {
			return AgentFinishedMsg{Result: app.CommandResultMsg{Err: fmt.Errorf("agent already running")}}
		}
	}

	// Запускаем агента в отдельной горутине
	go func() {
		utils.Info("startAgent: Agent goroutine started", "query", query)

		// Создаём контекст с таймаутом
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		answer, err := state.Orchestrator.Run(ctx, query)
		result := app.CommandResultMsg{}
		if err != nil {
			utils.Error("startAgent: Agent FAILED", "error", err)
			result.Err = fmt.Errorf("agent error: %w", err)
		} else {
			utils.Info("startAgent: Agent SUCCEEDED", "response_length", len(answer))
			result.Output = answer
		}

		// Отправляем результат в канал (блокируется пока Update не прочитает)
		resultCh <- agentResultMsg{result: result}
		utils.Info("startAgent: Result sent to channel")
	}()

	// Возвращаем команду которая просто тикает - чтение канала в Update()
	// ИЗМЕНЕНО: убран select из этого места чтобы избежать двойного чтения
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return AgentTickMsg(t)
	})
}
