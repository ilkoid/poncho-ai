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
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tui"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Update обрабатывает сообщения Bubble Tea и обновляет состояние модели.
//
// Является частью Model-View-Update архитектуры Bubble Tea.
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала
//   - tea.KeyMsg: нажатия клавиш
//   - commandResultMsg: результаты выполнения команд
//   - tui.EventMsg: события от агента (Port & Adapter)
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

	// 0. События от агента (Port & Adapter)
	case tui.EventMsg:
		event := events.Event(msg)
		switch event.Type {
		case events.EventThinking:
			// Агент начал думать - показываем spinner
			m.mu.Lock()
			m.isProcessing = true
			m.mu.Unlock()
			m.appendLog(systemMsgStyle("Thinking..."))
			return m, tui.WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
				return tui.EventMsg(e)
			})

		case events.EventMessage:
			// Промежуточное сообщение от агента
			if msgData, ok := event.Data.(events.MessageData); ok {
				m.appendLog(systemMsgStyle("AI: ") + msgData.Content)
			}
			return m, tui.WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
				return tui.EventMsg(e)
			})

		case events.EventError:
			// Ошибка агента
			if errData, ok := event.Data.(events.ErrorData); ok {
				m.appendLog(errorMsgStyle("ERROR: ") + errData.Err.Error())
			}
			m.mu.Lock()
			m.isProcessing = false
			m.mu.Unlock()
			m.textarea.Focus()
			return m, nil

		case events.EventDone:
			// Агент завершил работу
			if msgData, ok := event.Data.(events.MessageData); ok {
				m.appendLog(systemMsgStyle("AI: ") + msgData.Content)
			}
			m.mu.Lock()
			m.isProcessing = false
			m.mu.Unlock()
			m.textarea.Focus()
			return m, nil
		}

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

			// REFACTORED 2025-01-07: Проверяем только встроенные команды
			// Все неизвестные команды делегируются агенту
			builtInCommands := []string{"load", "render", "demo", "ping", "help"}
			isBuiltIn := false
			for _, c := range builtInCommands {
				if cmd == c {
					isBuiltIn = true
					break
				}
			}

			if isBuiltIn {
				// Встроенная команда - выполняем через performCommand
				return m, performCommand(input, m.coreState)
			}

			// Неизвестная команда - делегируем агенту
			if m.orchestrator != nil {
				return m, startAgent(&m, input)
			}

			// Неизвестная команда и нет агента
			return m, performCommand(input, m.coreState)
		}

	// 3. Результат выполнения команды (прилетел асинхронно)
	//    NOTE: для agent-запросов используем AgentFinishedMsg для интерактивности
	case commandResultMsg:
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
func performCommand(input string, state *state.CoreState) tea.Cmd {
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
				return commandResultMsg{Err: fmt.Errorf("usage: load <article_id>")}
			}
			articleID := args[0]

			// 1. Получаем "сырой" список файлов из S3
			// REFACTORED 2026-01-04: state.S3 → state.GetStorage()
			s3Client := state.GetStorage()
			if s3Client == nil {
				return commandResultMsg{Err: fmt.Errorf("s3 client is not initialized")}
			}

			rawObjects, err := s3Client.ListFiles(ctx, articleID)
			if err != nil {
				return commandResultMsg{Err: fmt.Errorf("s3 error: %w", err)}
			}

			// 2. Классифицируем файлы согласно правилам из config.yaml
			classifierEngine := classifier.New(state.Config.FileRules)
			classifiedFiles, err := classifierEngine.Process(rawObjects)
			if err != nil {
				return commandResultMsg{Err: fmt.Errorf("classification error: %w", err)}
			}

			// 3. Обновляем State и UI (thread-safe)
			state.SetCurrentArticle(articleID, classifiedFiles)

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

			return commandResultMsg{Output: report.String()}

		// === КОМАНДА 2: RENDER <PROMPT_FILE> ===
		// Тестирует промпт, подставляя данные из загруженного артикула
		case "render":
			if len(args) < 1 {
				return commandResultMsg{Err: fmt.Errorf("usage: render <prompt_file.yaml>")}
			}
			filename := args[0]

			// Проверяем, загружен ли вообще артикул (потокобезопасно)
			if state.GetCurrentArticleID() == "NONE" {
				return commandResultMsg{Err: fmt.Errorf("no article loaded. use 'load <id>' first")}
			}

			// 1. Загружаем сам файл промпта
			// state.Config.App.PromptsDir берется из конфига (например "./prompts")
			fullPath := fmt.Sprintf("%s/%s", state.Config.App.PromptsDir, filename)
			p, err := prompt.Load(fullPath)
			if err != nil {
				return commandResultMsg{Err: fmt.Errorf("failed to load prompt '%s': %w", filename, err)}
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
				return commandResultMsg{Err: fmt.Errorf("render error: %w", err)}
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

			return commandResultMsg{Output: output.String()}

		// === КОМАНДА 3: DEMO ===
		// Добавляет тестовые задачи для проверки отображения todo панели
		case "demo":
			// REFACTORED 2026-01-04: state.Todo → state.GetTodoManager()
			todoManager := state.GetTodoManager()
			if todoManager == nil {
				return commandResultMsg{Err: fmt.Errorf("todo manager not initialized")}
			}
			todoManager.Add("Проверить API Wildberries")
			todoManager.Add("Загрузить эскизы из S3")
			todoManager.Add("Сгенерировать описание товара")
			taskID := todoManager.Add("Провалить эту задачу для теста")
			todoManager.Complete(2)
			todoManager.Fail(taskID, "Тестовая ошибка")
			return commandResultMsg{Output: "✅ Added 4 demo todos (1 done, 1 failed, 2 pending)"}

		// === КОМАНДА 4: PING ===
		case "ping":
			return commandResultMsg{Output: "Pong! System is alive."}

		// === НЕИЗВЕСТНАЯ КОМАНДА ===
		// NOTE: "ask" и делегирование агенту обрабатываются в Update напрямую
		default:
			return commandResultMsg{Err: fmt.Errorf("unknown command: '%s'. Try 'load <id>', 'demo', 'render <file>', 'ask <query>' or 'todo help'", cmd)}
		}
	}
}

// startAgent запускает агента в отдельной горутине.
//
// REFACTORED 2026-01-10: Агент отправляет события через events.Emitter,
// которые обрабатываются в Update() через tui.EventMsg.
func startAgent(m *MainModel, query string) tea.Cmd {
	// Проверяем что оркестратор инициализирован
	if m.orchestrator == nil {
		utils.Error("startAgent: Orchestrator is nil!", "query", query)
		m.appendLog(errorMsgStyle("ERROR: Orchestrator not initialized"))
		return nil
	}

	// Проверяем что агент не запущен
	m.mu.RLock()
	alreadyRunning := m.isProcessing
	m.mu.RUnlock()

	if alreadyRunning {
		utils.Error("startAgent: Agent already running!", "query", query)
		m.appendLog(errorMsgStyle("ERROR: Agent already running"))
		return nil
	}

	// Запускаем агента в отдельной горутине
	go func() {
		utils.Info("startAgent: Agent goroutine started", "query", query)

		// Создаём контекст с таймаутом
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		_, err := m.orchestrator.Run(ctx, query)
		if err != nil {
			utils.Error("startAgent: Agent FAILED", "error", err)
		} else {
			utils.Info("startAgent: Agent SUCCEEDED")
		}
		// События отправляются автоматически через emitter
	}()

	return nil
}
