// Package agent реализует оркестрацию AI-агента с инструментами (tools).
//
// Orchestrator координирует взаимодействие между LLM и зарегистрированными
// инструментами, реализуя ReAct (Reasoning + Acting) паттерн:
//   1. Формирует контекст из истории, системного промпта и данных состояния
//   2. Вызывает LLM с доступными tools definitions
//   3. Если LLM возвращает tool calls — выполняет их через Registry
//   4. Передаёт результаты выполнения обратно в LLM
//   5. Повторяет пока LLM не даст финальный ответ
//
// Соблюдение правил из dev_manifest.md:
//   - Работает только через llm.Provider (Правило 4)
//   - Использует tools.Registry для получения инструментов (Правило 3)
//   - Thread-safe работа с app.GlobalState (Правило 5)
//   - Никаких panic — все ошибки возвращаются (Правило 7)
//   - Инструменты вызываются по контракту "Raw In, String Out" (Правило 1)
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentInterface "github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Проверка что Orchestrator реализует интерфейс Agent
var _ agentInterface.Agent = (*Orchestrator)(nil)

// DefaultMaxIterations — максимальное число итераций ReAct цикла.
// Защита от бесконечных циклов при ошибочном поведении LLM.
const DefaultMaxIterations = 10

// UserChoiceRequest — специальный маркер для передачи управления пользователю.
// Когда tool возвращает этот маркер, orchestrator должен прервать цикл
// и вернуть управление в main loop для обработки выбора пользователя.
const UserChoiceRequest = "__USER_CHOICE_REQUIRED__"

// Orchestrator координирует взаимодействие между LLM и Tools.
//
// Является потокобезопасным — может использоваться из разных горутин.
// Все состояния хранятся в app.GlobalState (thread-safe по правилу 5).
type Orchestrator struct {
	llm          llm.Provider    // LLM провайдер (абстракция, правило 4)
	registry     *tools.Registry // Реестр инструментов (правило 3)
	state        *app.GlobalState // Глобальное состояние (thread-safe)
	maxIters     int              // Максимум итераций (защита от циклов)
	systemPrompt string           // Системный промпт агента

	// toolPostPrompts — конфигурация post-prompts для tools
	toolPostPrompts *prompt.ToolPostPromptConfig

	// activePostPrompt — текущий активный post-prompt (действует одну итерацию)
	activePostPrompt string

	// mu защищает одновременные вызовы Run
	mu sync.Mutex
}

// Config конфигурация для создания Orchestrator.
type Config struct {
	// LLM — провайдер языковой модели (обязательный)
	LLM llm.Provider

	// Registry — реестр зарегистрированных инструментов (обязательный)
	Registry *tools.Registry

	// State — глобальное состояние приложения (обязательный)
	State *app.GlobalState

	// MaxIters — максимальное число итераций ReAct цикла.
	// Если 0, используется DefaultMaxIterations.
	MaxIters int

	// SystemPrompt — системный промпт, определяющий поведение агента.
	// Если пуст, будет использован дефолтный промпт.
	SystemPrompt string

	// ToolPostPrompts — конфигурация post-prompts для tools (опционально)
	ToolPostPrompts *prompt.ToolPostPromptConfig
}

// New создает новый Orchestrator с заданной конфигурацией.
//
// Возвращает ошибку если обязательные поля конфигурации не заполнены.
func New(cfg Config) (*Orchestrator, error) {
	// Валидация обязательных полей
	if cfg.LLM == nil {
		return nil, fmt.Errorf("cfg.LLM is required")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("cfg.Registry is required")
	}
	if cfg.State == nil {
		return nil, fmt.Errorf("cfg.State is required")
	}

	// Дефолтные значения
	if cfg.MaxIters <= 0 {
		cfg.MaxIters = DefaultMaxIterations
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSystemPrompt()
	}

	return &Orchestrator{
		llm:             cfg.LLM,
		registry:        cfg.Registry,
		state:           cfg.State,
		maxIters:        cfg.MaxIters,
		systemPrompt:    cfg.SystemPrompt,
		toolPostPrompts:  cfg.ToolPostPrompts,
	}, nil
}

// Run выполняет полный цикл агента для обработки пользовательского запроса.
//
// Метод является потокобезопасным — может вызываться одновременно из разных горутин.
//
// Алгоритм:
//  1. Добавляет запрос пользователя в историю
//  2. Запускает ReAct цикл:
//     a. Формирует контекст (системный промпт + история + файлы + todos)
//     b. Вызывает LLM с definitions всех зарегистрированных tools
//     c. Если LLM вернул tool calls — выполняет их через Registry
//     d. Добавляет результаты выполнения в историю
//     e. Повторяет пока есть tool calls или не достигнут лимит итераций
//  3. Возвращает финальный ответ LLM
//
// Возвращает ошибку если:
//   - LLM провайдер вернул ошибку
//   - Превышен лимит итераций (возможный цикл)
//   - Контекст отменён (ctx.Done())
//
// Правило 7: Никаких panic — все ошибки возвращаются.
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
	startTime := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()

	utils.Info("=== Orchestrator.Run STARTED ===", "query", userQuery, "maxIters", o.maxIters)

	// 1. Добавляем запрос пользователя в историю (thread-safe)
	o.state.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: userQuery,
	})

	// 2. Получаем определения всех инструментов для Function Calling
	toolDefs := o.registry.GetDefinitions()

	// 3. ReAct цикл
	var iterCount int
	for iterCount < o.maxIters {
		iterCount++
		utils.Debug("Agent iteration", "num", iterCount, "max", o.maxIters)

		// Проверка отмены контекста
		select {
		case <-ctx.Done():
			utils.Error("Context canceled", "error", ctx.Err(), "iterations", iterCount)
			return "", fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		// 3.1. Формируем контекст для LLM
		// BuildAgentContext thread-safe (правило 5)
		// Если есть активный post-prompt — используем его вместо базового system prompt
		systemPrompt := o.systemPrompt
		if o.activePostPrompt != "" {
			systemPrompt = o.activePostPrompt
		}
		messages := o.state.BuildAgentContext(systemPrompt)

		// 3.2. Вызываем LLM с tools definitions
		// Generate работает через интерфейс Provider (правило 4)
		response, err := o.llm.Generate(ctx, messages, toolDefs)
		if err != nil {
			utils.Error("LLM generation failed", "error", err, "iteration", iterCount)
			return "", fmt.Errorf("llm generation failed (iter %d): %w", iterCount, err)
		}

		// 3.3. Санитизируем ответ LLM (удаляем markdown обёртку)
		response.Content = utils.SanitizeLLMOutput(response.Content)

		// 3.4. Добавляем ответ ассистента в историю
		o.state.AppendMessage(response)

		// 3.5. Если есть tool calls — выполняем их
		if len(response.ToolCalls) > 0 {
			utils.Info("Tool calls received", "count", len(response.ToolCalls), "iteration", iterCount)
			hasUserChoiceRequest := false
			for _, tc := range response.ToolCalls {
				// Выполняем инструмент и получаем результат
				// executeTool обрабатывает ошибки согласно правилу 7
				result := o.executeTool(ctx, tc)

				// Проверяем на специальный маркер UserChoiceRequest
				if result == UserChoiceRequest {
					hasUserChoiceRequest = true
				}

				// Добавляем результат в историю как RoleTool сообщение
				o.state.AppendMessage(llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					Content:    result,
				})
			}

			// Если был запрос выбора пользователя — прерываем цикл
			if hasUserChoiceRequest {
				return UserChoiceRequest, nil
			}

			// Продолжаем цикл — передаём результаты в LLM для анализа
			continue
		}

		// 3.6. Нет tool calls — это финальный ответ
		// Сбрасываем active post-prompt после завершения
		o.activePostPrompt = ""

		// Выводим финальный todo list для визуализации
		todoString := o.state.Todo.String()
		if todoString != "" && todoString != "Нет активных задач" {
			utils.Info("Final todo list", "plan", todoString)
		}

		utils.Info("Agent completed", "iterations", iterCount, "duration_ms", time.Since(startTime).Milliseconds())
		return response.Content, nil
	}

	// Превышен лимит итераций — возможный бесконечный цикл
	utils.Error("Max iterations exceeded", "max", o.maxIters, "duration_ms", time.Since(startTime).Milliseconds())
	return "", fmt.Errorf("max iterations (%d) exceeded - possible LLM loop", o.maxIters)
}

// executeTool выполняет отдельный инструмент по запросу от LLM.
//
// Соблюдает правило 3: работает только через Registry.
// Соблюдает правило 1: Raw In (JSON строка) → String Out.
// Соблюдает правило 7: ошибки не паничат, а возвращаются как строка.
//
// Возвращает результат выполнения или описание ошибки в виде строки.
func (o *Orchestrator) executeTool(ctx context.Context, tc llm.ToolCall) string {
	startTime := time.Now()

	// 1. Находим инструмент по имени через Registry (правило 3)
	tool, err := o.registry.Get(tc.Name)
	if err != nil {
		// Ошибка поиска инструмента — возвращаем её в LLM для анализа
		utils.Error("Tool not found", "tool", tc.Name, "error", err)
		return fmt.Sprintf("Tool not found error: %v", err)
	}

	utils.Info("Executing tool", "name", tc.Name, "args_length", len(tc.Args))

	// 2. Санитизируем JSON аргументы от markdown обёртки
	// LLM может вернуть ```json {...}``` вместо чистого JSON
	cleanArgs := utils.CleanJsonBlock(tc.Args)

	// 3. Выполняем инструмент (Raw In, String Out — правило 1)
	result, err := tool.Execute(ctx, cleanArgs)
	if err != nil {
		// Ошибка выполнения — оборачиваем с контекстом для дебаггинга
		duration := time.Since(startTime).Milliseconds()
		wrappedErr := fmt.Errorf("tool %s: %w", tc.Name, err)

		utils.Error("Tool execution failed",
			"tool", tc.Name,
			"error", err,
			"wrapped_error", wrappedErr,
			"duration_ms", duration)

		// Возвращаем форматированную ошибку для LLM
		return fmt.Sprintf("Tool execution error: %v", wrappedErr)
	}

	// 4. Возвращаем результат (String Out — правило 1)
	duration := time.Since(startTime).Milliseconds()
	utils.Info("Tool executed successfully",
		"tool", tc.Name,
		"result_length", len(result),
		"duration_ms", duration)

	// 4.5. Для planner tools выводим результат отдельно (для визуализации todo list)
	if isPlannerTool(tc.Name) {
		utils.Info("Plan updated", "action", tc.Name, "result", result)
	}

	// 5. Проверяем есть ли post-prompt для этого tool
	if o.toolPostPrompts != nil {
		postPrompt, err := o.toolPostPrompts.GetToolPostPrompt(tc.Name, o.state.Config.App.PromptsDir)
		if err != nil {
			// Логируем ошибку, но не прерываем выполнение
			utils.Error("Failed to get post-prompt", "tool", tc.Name, "error", err)
		} else if postPrompt != "" {
			// Устанавливаем активный post-prompt для следующей итерации
			o.activePostPrompt = postPrompt
			utils.Info("Post-prompt activated", "tool", tc.Name, "next_iteration")
		}
	}

	return result
}

// ClearHistory очищает историю диалога в GlobalState.
//
// Метод thread-safe.
func (o *Orchestrator) ClearHistory() {
	o.state.ClearHistory()
}

// GetHistory возвращает копию истории диалога.
//
// Метод thread-safe — возвращает копию, чтобы избежать race conditions.
func (o *Orchestrator) GetHistory() []llm.Message {
	return o.state.GetHistory()
}

// SetSystemPrompt обновляет системный промпт агента.
//
// Метод thread-safe.
func (o *Orchestrator) SetSystemPrompt(prompt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.systemPrompt = prompt
}

// defaultSystemPrompt возвращает дефолтный системный промпт агента.
func defaultSystemPrompt() string {
	return `Ты AI-ассистент Poncho для работы с Wildberries и анализа данных.

## Твои возможности

У тебя есть доступ к инструментам (tools) для получения актуальных данных:
- Работа с категориями Wildberries (get_wb_parent_categories, get_wb_subjects)
- Работа с S3 хранилищем
- **Управление планом действий** (plan_add_task, plan_mark_done, plan_mark_failed, plan_clear)

## Правила работы

1. Используй tools когда нужно получить актуальные данные — не выдумывай
2. Анализируй запрос пользователя перед вызовом инструмента
3. Формируй понятные структурированные ответы
4. Если инструмент вернул ошибку — сообщи о ней пользователю
5. Если данных недостаточно — спроси уточняющий вопрос

## Управление планом действий (ВАЖНО!)

Для сложных запросов, требующих несколько шагов, СОСТАВЬ ПЛАН:

### Когда создавать план:
- Запрос требует 2+ шагов для выполнения
- Нужна последовательность действий
- Запрос сложный или многосоставный

### Как работать с планом:

1. **plan_add_task** — добавь задачу в план
   - Используй для каждого шага отдельную задачу
   - Описание должно быть чётким и кратким
   - Пример: "Получить ID категории Женщинам"

2. **plan_mark_done** — отметь задачу как выполненную
   - Вызывай после успешного завершения каждого шага

3. **plan_mark_failed** — отметь задачу как проваленную
   - Используй если шаг не удался
   - Укажи причину провала

4. **plan_clear** — очисти весь план
   - Используй после завершения всей работы

## Примеры

### Простой запрос (без плана):
Запрос: "покажи родительские категории товаров"
Действие: Вызвать get_wb_parent_categories и оформить ответ

### Сложный запрос (с планом):
Запрос: "найди все товары в категории Верхняя одежда и покажи их количество"

Действия:
1. plan_add_task "Найти ID категории Верхняя одежда через родительские категории"
2. get_wb_parent_categories → найти ID
3. plan_mark_done 1
4. plan_add_task "Получить список подкатегорий для Верхняя одежда"
5. get_wb_subjects с найденным ID
6. plan_mark_done 2
7. plan_add_task "Посчитать общее количество товаров"
8. Анализировать результаты и подсчитать
9. plan_mark_done 3
10. Оформить ответ пользователю
11. plan_clear (опционально)

### Ещё пример:
Запрос: "какие товары в категории Женщинам?"
Действие:
  1. plan_add_task "Найти ID категории Женщинам"
  2. get_wb_parent_categories → найти ID
  3. plan_mark_done 1
  4. plan_add_task "Получить подкатегории Женщинам"
  5. get_wb_subjects с этим ID
  6. plan_mark_done 2
  7. Оформить ответ пользователю

## Запомни:

- Для ПРОСТЫХ запросов (1 действие) → отвечай сразу, план не нужен
- Для СЛОЖНЫХ запросов (2+ действий) → сначала создай план через plan_add_task
- Пользователь видит план в правой панели — это помогает понимать прогресс
`
}

// isPlannerTool проверяет, является ли tool инструментом планировщика.
func isPlannerTool(toolName string) bool {
	switch toolName {
	case "plan_add_task", "plan_mark_done", "plan_mark_failed", "plan_clear":
		return true
	default:
		return false
	}
}
