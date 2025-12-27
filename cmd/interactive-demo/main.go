// Interactive Demo - консольная утилита для демонстрации интерактивного агента
//
// Сценарий:
// 1. Пользователь вводит задачу
// 2. Агент создаёт план и начинает выполнение
// 3. Агент может запросить выбор пользователя через special tool
// 4. Пользователь выбирает опцию
// 5. Агент продолжает работу с выбранным значением
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/internal/agent"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// UserChoiceTool - инструмент для передачи управления пользователю
//
// Когда агенту нужен выбор пользователя, он вызывает этот tool с опциями.
type UserChoiceTool struct {
	state *app.GlobalState
}

func NewUserChoiceTool(state *app.GlobalState) *UserChoiceTool {
	return &UserChoiceTool{state: state}
}

func (t *UserChoiceTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "request_user_choice",
		Description: "Запрашивает выбор пользователя из списка опций. Используй когда нужно чтобы пользователь выбрал артикул, категорию или другой вариант из предложенных.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Вопрос пользователю (например: 'Выберите артикул из списка')",
				},
				"options": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Список опций для выбора (например: ['12345', '67890', '11111'])",
				},
			},
			"required": []string{"question", "options"},
		},
	}
}

func (t *UserChoiceTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Сохраняем опции в state для использования в главном loop
	t.state.SetUserChoice(args.Question, args.Options)

	// Возвращаем специальный маркер (должен совпадать с orchestrator.UserChoiceRequest)
	return "__USER_CHOICE_REQUIRED__", nil
}

// LoadArticleTool - инструмент для загрузки данных артикула
type LoadArticleTool struct {
	s3Client *s3storage.Client
	state    *app.GlobalState
}

func NewLoadArticleTool(s3Client *s3storage.Client, state *app.GlobalState) *LoadArticleTool {
	return &LoadArticleTool{s3Client: s3Client, state: state}
}

func (t *LoadArticleTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "load_article",
		Description: "Загружает данные артикула из S3 в память. Классифицирует файлы и сохраняет результаты.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "ID артикула для загрузки",
				},
			},
			"required": []string{"article_id"},
		},
	}
}

func (t *LoadArticleTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ArticleID string `json:"article_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// 1. Получаем файлы из S3
	rawObjects, err := t.s3Client.ListFiles(ctx, args.ArticleID)
	if err != nil {
		return "", fmt.Errorf("s3 list error: %w", err)
	}

	// 2. Классифицируем файлы
	classifierEngine := classifier.New(t.state.Config.FileRules)
	classifiedFiles, err := classifierEngine.Process(rawObjects)
	if err != nil {
		return "", fmt.Errorf("classification error: %w", err)
	}

	// 3. Конвертируем в FileMeta
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

	// 4. Сохраняем в state
	t.state.SetCurrentArticle(args.ArticleID, convertedFiles)

	// 5. Формируем отчёт
	var report strings.Builder
	report.WriteString(fmt.Sprintf("Артикул %s загружен:\n", args.ArticleID))
	for tag, files := range classifiedFiles {
		report.WriteString(fmt.Sprintf("  [%s]: %d файлов\n", strings.ToUpper(tag), len(files)))
		for _, f := range files {
			report.WriteString(fmt.Sprintf("    - %s (%s)\n", f.Filename, formatSize(f.Size)))
		}
	}

	return report.String(), nil
}

// ShowJsonTool - инструмент для показа JSON содержимого файла
type ShowJsonTool struct {
	s3Client *s3storage.Client
}

func NewShowJsonTool(s3Client *s3storage.Client) *ShowJsonTool {
	return &ShowJsonTool{s3Client: s3Client}
}

func (t *ShowJsonTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "show_json",
		Description: "Показывает содержимое JSON файла из S3 в читаемом формате",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Ключ файла в S3 (например: '12345/plm_data.json')",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *ShowJsonTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Скачиваем файл
	content, err := t.s3Client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", fmt.Errorf("download error: %w", err)
	}

	// Проверяем что это JSON
	var jsn interface{}
	if err := json.Unmarshal(content, &jsn); err != nil {
		return "", fmt.Errorf("not a valid JSON file: %w", err)
	}

	// Форматируем для вывода
	formatted, _ := json.MarshalIndent(jsn, "", "  ")
	return fmt.Sprintf("Содержимое %s:\n%s", args.Key, string(formatted)), nil
}

// ============================================
// Главная программа
// ============================================

type InteractiveState struct {
	choiceQuestion string
	choiceOptions  []string
	choiceMade     bool
	choiceValue    string
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()
	utils.Info("interactive-demo started")

	// 1. Загружаем конфиг
	cfg, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Конфиг загружен из: %s", cfgPath)

	// 2. Инициализируем компоненты
	components, err := appcomponents.Initialize(cfg, 10, "", appcomponents.ToolsAll)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Компоненты инициализированы")

	// 3. Регистрируем дополнительные инструменты
	registry := components.State.GetToolsRegistry()

	// S3 инструменты (из pkg/tools/std/s3_tools.go)
	registry.Register(std.NewS3ListTool(components.State.S3))
	registry.Register(std.NewS3ReadTool(components.State.S3))

	// Интерактивные инструменты
	registry.Register(NewUserChoiceTool(components.State))
	registry.Register(NewLoadArticleTool(components.State.S3, components.State))
	registry.Register(NewShowJsonTool(components.State.S3))

	// Recreat orchestrator с новыми tools
	orchestrator, err := agent.New(agent.Config{
		LLM:          components.LLM,
		Registry:     registry,
		State:        components.State,
		MaxIters:     10,
		SystemPrompt: getSystemPrompt(),
	})
	if err != nil {
		log.Fatal(err)
	}
	components.State.Orchestrator = orchestrator

	// 4. Интерактивный loop
	reader := bufio.NewReader(os.Stdin)
	interactiveState := &InteractiveState{}

	printHeader()

	for {
		fmt.Print("\n> ")

		// Читаем ввод (поддерживает pipe и интерактивный режим)
		input, err := reader.ReadString('\n')
		if err != nil {
			// EOF или ошибка — завершаем работу
			if err != io.EOF {
				fmt.Printf("\nОшибка чтения: %v\n", err)
			}
			break
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if input == "quit" || input == "exit" {
			fmt.Println("До свидания!")
			break
		}

		// Если есть ожидающий выбор - обрабатываем как выбор
		if interactiveState.choiceQuestion != "" {
			handleUserChoice(components, interactiveState, input)
			continue
		}

		// Иначе - выполняем через агента
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		response, err := orchestrator.Run(ctx, input)
		cancel()

		if err != nil {
			fmt.Printf("❌ Ошибка: %v\n", err)
			continue
		}

		// Проверяем на запрос выбора
		if response == "__USER_CHOICE_REQUIRED__" {
			showUserChoicePrompt(interactiveState, components.State)
			continue
		}

		// Показываем ответ
		fmt.Printf("\n%s\n", response)

		// Показываем Todo статус
		showTodoStatus(components.State)
	}
}

// handleUserChoice обрабатывает выбор пользователя
func handleUserChoice(components *appcomponents.Components, state *InteractiveState, choice string) {
	// Валидация выбора
	valid := false
	for _, opt := range state.choiceOptions {
		if choice == opt {
			valid = true
			break
		}
	}

	if !valid {
		fmt.Printf("❌ Неверный выбор. Доступные опции: %v\n", state.choiceOptions)
		return
	}

	// Сохраняем выбор
	state.choiceValue = choice
	state.choiceMade = true

	fmt.Printf("✅ Выбрано: %s\n", choice)

	// Добавляем выбор в историю как пользовательское сообщение
	components.State.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: fmt.Sprintf("Я выбрал: %s", choice),
	})

	// Продолжаем выполнение агента
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	response, err := components.Orchestrator.Run(ctx, "Продолжай с выбранным артикулом: "+choice)
	cancel()

	if err != nil {
		fmt.Printf("❌ Ошибка: %v\n", err)
	} else if response != "__USER_CHOICE_REQUIRED__" {
		fmt.Printf("\n%s\n", response)
		showTodoStatus(components.State)
	}

	// Сбрасываем состояние выбора
	state.choiceQuestion = ""
	state.choiceOptions = nil
	state.choiceMade = false
}

// showUserChoicePrompt показывает приглашение для выбора
func showUserChoicePrompt(state *InteractiveState, gs *app.GlobalState) {
	question, options := gs.GetUserChoice()
	state.choiceQuestion = question
	state.choiceOptions = options

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("👉 %s\n", question)
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	for i, opt := range options {
		fmt.Printf("  [%d] %s\n", i+1, opt)
	}
	fmt.Println()
	fmt.Printf("Введите номер или значение (или 'cancel' для отмены): ")
}

// showTodoStatus показывает статус задач
func showTodoStatus(state *app.GlobalState) {
	tasks := state.Todo.GetTasks()
	if len(tasks) == 0 {
		return
	}

	fmt.Println("\n─────────────────────────────────────────────────────────")
	fmt.Println("📋 ПЛАН ДЕЙСТВИЙ:")
	for _, task := range tasks {
		status := "○"
		color := "\033[0m" // reset
		if task.Status == "DONE" {
			status = "✓"
			color = "\033[32m" // green
		} else if task.Status == "FAILED" {
			status = "✗"
			color = "\033[31m" // red
		}
		fmt.Printf("  %s%s\033[0m %d. %s\n", color, status, task.ID, task.Description)
	}
	fmt.Println("─────────────────────────────────────────────────────────")
}

func printHeader() {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════╗")
	fmt.Println("║     🤖 Poncho AI - Interactive Demo Console               ║")
	fmt.Println("║                                                           ║")
	fmt.Println("║  Агент с возможностью интерактивного выбора пользователя  ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Команды:")
	fmt.Println("  Любой текст    - отправить запрос агенту")
	fmt.Println("  quit / exit    - выйти")
	fmt.Println()
}

func getSystemPrompt() string {
	return `Ты AI-ассистент Poncho для работы с артикулами и S3 хранилищем.

## Твои возможности

У тебя есть инструменты:
- list_s3_files - показать список файлов в S3 по префиксу
- request_user_choice - запросить выбор пользователя из списка опций
- load_article - загрузить данные артикула в память
- show_json - показать содержимое JSON файла
- plan_add_task, plan_mark_done, plan_mark_failed, plan_clear - управление планом

## Сценарий работы

Когда пользователь просит "показать данные по артикулам" или похожую задачу:

1. СОЗДАЙ ПЛАН через plan_add_task:
   - "Получить список артикулов из S3"
   - "Показать список пользователю"
   - "Загрузить выбранный артикул"
   - "Показать JSON данные"

2. Вызови list_s3_files с пустым префиксом - получишь список папок (артикулов)

3. Из списка файлов ИЗВЛЕКИ уникальные articulate (первые части ключей до '/')
   - Например: из ["12345/sketch.jpg", "67890/plm.json"] → ["12345", "67890"]

4. Вызови request_user_choice с:
   - question: "Выберите артикул из списка:"
   - options: ["12345", "67890", ...]

5. После выбора пользователя вызови load_article с выбранным article_id

6. Найди JSON файл в загруженных данных (обычно *plm_data*)
   Вызови show_json с ключом этого файла

## Пример диалога

Пользователь: "покажи данные по артикулам и загрузи контент по артикулу"

Агент:
1. plan_add_task "Получить список артикулов из S3"
2. list_s3_files(prefix="") → получаешь файлы
3. Извлекаешь артикулы: ["12345", "67890", "11111"]
4. request_user_choice(question="Выберите артикул", options=["12345", "67890", "11111"])
5. [ОЖИДАНИЕ ВЫБОРА ПОЛЬЗОВАТЕЛЯ]
6. plan_mark_done 1
7. plan_add_task "Загрузить выбранный артикул"
8. load_article(article_id="ВЫБРАННОЕ")
9. plan_mark_done 2
10. plan_add_task "Показать JSON данные"
11. show_json(key="ВЫБРАННОЕ/plm_data.json")
12. plan_mark_done 3
13. Оформить ответ пользователю

## Важно

- ВСЕГДА создавай план для многошаговых задач
- Используй request_user_choice когда нужно выбрать из опций
- Показывай прогресс через plan_mark_done
- При ошибках используй plan_mark_failed с причиной
`
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
