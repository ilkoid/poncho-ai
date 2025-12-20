// Логика - Обрабатывает нажатия клавиш и результаты команд.

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
)

// CommandResultMsg - сообщение, которое возвращает worker после работы
type CommandResultMsg struct {
	Output string
	Err    error
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {

	// 1. Изменение размера окна терминала
	case tea.WindowSizeMsg:
		headerHeight := 1
		footerHeight := m.textarea.Height() + 2 // + граница

		// Вычисляем высоту для области контента
		vpHeight := msg.Height - headerHeight - footerHeight
		if vpHeight < 0 {
			vpHeight = 0
		}

		// Обновляем размеры существующего вьюпорта
		m.viewport.Width = msg.Width
		m.viewport.Height = vpHeight

		// Только при первом запуске (если нужно инициализировать контент)
		if !m.ready {
			m.ready = true
			// Опционально: можно принудительно обновить контент, если он зависит от ширины
		}

		m.textarea.SetWidth(msg.Width)

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

			// Запускаем асинхронную команду
			return m, performCommand(input, m.appState)
		}

	// 3. Результат выполнения команды (прилетел асинхронно)
	case CommandResultMsg:
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

// Хелпер для добавления строки в лог и прокрутки вниз
func (m *MainModel) appendLog(str string) {
	newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), str)
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// performCommand - симуляция работы (позже подключим реальный контроллер)
// performCommand — это "мозг", обрабатывающий ввод пользователя.
// Она возвращает tea.Cmd, который выполнится асинхронно, чтобы не завис UI.
func performCommand(input string, state *app.GlobalState) tea.Cmd {
	return func() tea.Msg {
		// Создаем контекст с таймаутом (чтобы не висеть вечно)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
				return CommandResultMsg{Err: fmt.Errorf("usage: load <article_id>")}
			}
			articleID := args[0]

			// 1. Получаем "сырой" список файлов из S3
			// (Предполагаем, что state.S3 уже инициализирован в main.go)
			if state.S3 == nil {
				return CommandResultMsg{Err: fmt.Errorf("s3 client is not initialized")}
			}

			rawObjects, err := state.S3.ListFiles(ctx, articleID)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("s3 error: %w", err)}
			}

			// 2. Классифицируем файлы согласно правилам из config.yaml
			classifierEngine := classifier.New(state.Config.FileRules)
			classifiedFiles, err := classifierEngine.Process(rawObjects)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("classification error: %w", err)}
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

			// 4. Обновляем глобальный State (потокобезопасно, т.к. мы в одной горутине tea.Cmd)
			state.CurrentArticleID = articleID
			state.Files = convertedFiles

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

			return CommandResultMsg{Output: report.String()}

		// === КОМАНДА 2: RENDER <PROMPT_FILE> ===
		// Тестирует промпт, подставляя данные из загруженного артикула
		case "render":
			if len(args) < 1 {
				return CommandResultMsg{Err: fmt.Errorf("usage: render <prompt_file.yaml>")}
			}
			filename := args[0]

			// Проверяем, загружен ли вообще артикул
			if state.CurrentArticleID == "NONE" {
				return CommandResultMsg{Err: fmt.Errorf("no article loaded. use 'load <id>' first")}
			}

			// 1. Загружаем сам файл промпта
			// state.Config.App.PromptsDir берется из конфига (например "./prompts")
			fullPath := fmt.Sprintf("%s/%s", state.Config.App.PromptsDir, filename)
			p, err := prompt.Load(fullPath)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("failed to load prompt '%s': %w", filename, err)}
			}

			// 2. Готовим данные для шаблона (Data Context)
			// Берем реальные данные из State.
			// Например, берем первый попавшийся эскиз для демонстрации.
			imageURL := "NO_IMAGE_FOUND"
			if sketches, ok := state.Files["sketch"]; ok && len(sketches) > 0 {
				// В реальном S3 URL может быть подписанным (Presigned), но пока просто ключ
				imageURL = fmt.Sprintf("s3://%s/%s", state.Config.S3.Bucket, sketches[0].OriginalKey)
			}

			templateData := map[string]interface{}{
				"ArticleID": state.CurrentArticleID,
				"ImageURL":  imageURL,
				// Можно добавить сюда содержимое JSON из категории plm_data, если нужно
			}

			// 3. Рендерим сообщения
			messages, err := p.RenderMessages(templateData)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("render error: %w", err)}
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

			return CommandResultMsg{Output: output.String()}

		// === КОМАНДА 3: PING ===
		case "ping":
			return CommandResultMsg{Output: "Pong! System is alive."}

		// Неизвестная команда
		default:
			return CommandResultMsg{Err: fmt.Errorf("unknown command: '%s'. Try 'load <id>' or 'render <file>'", cmd)}
		}
	}
}
