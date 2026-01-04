// Package agent реализует оркестрацию AI-агента с инструментами (tools).
//
// Orchestrator является тонкой обёрткой над Chain Pattern:
//   - Создаёт и конфигурирует ReActChain
//   - Делегирует выполнение ReAct цикла Chain
//   - Обрабатывает agent-specific логику (UserChoiceRequest, planner logging)
//
// Соблюдение правил из dev_manifest.md:
//   - Работает только через llm.Provider (Правило 4)
//   - Использует tools.Registry для инструментов (Правило 3)
//   - Thread-safe работа с app.AppState (Правило 5)
//   - Никаких panic — все ошибки возвращаются (Правило 7)
//   - Делегирует бизнес-логику Chain Pattern (Правило 0)
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentInterface "github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Проверка что Orchestrator реализует интерфейс Agent
var _ agentInterface.Agent = (*Orchestrator)(nil)

// DefaultMaxIterations — максимальное число итераций ReAct цикла.
const DefaultMaxIterations = 10

// UserChoiceRequest — специальный маркер для передачи управления пользователю.
const UserChoiceRequest = "__USER_CHOICE_REQUIRED__"

// Orchestrator — тонкая обёртка над ReActChain.
//
// После рефакторинга с Chain Pattern:
//   - Создаёт и конфигурирует ReActChain
//   - Делегирует выполнение ReAct цикла Chain
//   - Обрабатывает только agent-specific логику
//
// Thread-safe через sync.Mutex.
type Orchestrator struct {
	// Chain — главная цепочка выполнения (ReAct)
	chain *chain.ReActChain

	// Dependencies
	llm          llm.Provider
	state        *app.AppState
	registry     *tools.Registry

	// Agent-specific логика
	maxIters int

	// mu защищает одновременные вызовы Run
	mu sync.Mutex
}

// Config конфигурация для создания Orchestrator.
type Config struct {
	// LLM — провайдер языковой модели (обязательный)
	LLM llm.Provider

	// Registry — реестр зарегистрированных инструментов (обязательный)
	Registry *tools.Registry

	// State — состояние приложения (обязательный)
	// Rule 6: AppState вместо GlobalState после рефакторинга
	State *app.AppState

	// MaxIters — максимальное число итераций ReAct цикла
	MaxIters int

	// SystemPrompt — системный промпт агента
	SystemPrompt string

	// ToolPostPrompts — конфигурация post-prompts для tools
	ToolPostPrompts *prompt.ToolPostPromptConfig

	// ReasoningConfig — параметры для reasoning модели
	ReasoningConfig llm.GenerateOptions

	// ChatConfig — параметры для chat модели
	ChatConfig llm.GenerateOptions

	// DebugConfig — конфигурация debug логирования
	DebugConfig chain.DebugConfig

	// PromptsDir — директория с post-prompts
	PromptsDir string
}

// New создаёт новый Orchestrator с заданной конфигурацией.
//
// Rule 10: Godoc на public API.
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

	// Создаём ReActChain конфигурацию
	chainConfig := chain.ReActChainConfig{
		SystemPrompt:     cfg.SystemPrompt,
		ReasoningConfig:  cfg.ReasoningConfig,
		ChatConfig:       cfg.ChatConfig,
		ToolPostPrompts:  cfg.ToolPostPrompts,
		PromptsDir:       cfg.PromptsDir,
		MaxIterations:    cfg.MaxIters,
		Timeout:          5 * time.Minute,
	}

	// Создаём ReActChain
	reactChain := chain.NewReActChain(chainConfig)
	reactChain.SetLLM(cfg.LLM)
	reactChain.SetRegistry(cfg.Registry)
	// Rule 6: Передаем CoreState в chain (framework logic)
	reactChain.SetState(cfg.State.CoreState)

	// Создаём debug recorder если включён
	debugRecorder, err := chain.NewChainDebugRecorder(cfg.DebugConfig)
	if err != nil {
		utils.Error("Failed to create debug recorder", "error", err)
	} else if debugRecorder.Enabled() {
		reactChain.AttachDebug(debugRecorder)
	}

	return &Orchestrator{
		chain:    reactChain,
		llm:      cfg.LLM,
		state:    cfg.State,
		registry: cfg.Registry,
		maxIters: cfg.MaxIters,
	}, nil
}

// Run выполняет полный цикл агента для обработки пользовательского запроса.
//
// Делегирует выполнение ReAct цикла Chain, обрабатывая только agent-specific логику:
//   - UserChoiceRequest для прерывания цикла
//   - Planner tool логирование
//
// Thread-safe — может вызываться одновременно из разных горутин.
//
// Rule 7: Все ошибки возвращаются, нет panic.
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	utils.Info("=== Orchestrator.Run STARTED ===", "query", userQuery)

	// 1. Добавляем user query в историю (thread-safe)
	// REFACTORED 2026-01-04: AppendMessage → Append, теперь возвращает ошибку
	if err := o.state.Append(llm.Message{
		Role:    llm.RoleUser,
		Content: userQuery,
	}); err != nil {
		return "", fmt.Errorf("failed to append user message: %w", err)
	}

	// 2. Формируем ChainInput
	// Rule 6: Передаем CoreState в chain (framework logic)
	input := chain.ChainInput{
		UserQuery: userQuery,
		State:     o.state.CoreState,
		LLM:       o.llm,
		Registry:  o.registry,
		Config:    chain.ChainConfig{},
	}

	// 3. Выполняем Chain (ReAct цикл)
	output, err := o.chain.Execute(ctx, input)
	if err != nil {
		utils.Error("Chain execution failed", "error", err)
		return "", err
	}

	// 4. Проверяем на UserChoiceRequest
	if output.Result == UserChoiceRequest {
		utils.Info("User choice requested", "iterations", output.Iterations)
		return UserChoiceRequest, nil
	}

	// 5. Логируем planner tools (agent-specific логика)
	// Planner detection пока не реализовано - tools выполняются без дополнительной обработки

	utils.Info("=== Orchestrator.Run COMPLETED ===",
		"iterations", output.Iterations,
		"duration_ms", output.Duration.Milliseconds(),
	)

	if output.DebugPath != "" {
		utils.Info("Debug log saved", "file", output.DebugPath)
	}

	return output.Result, nil
}

// GetHistory возвращает копию истории диалога.
//
// Thread-safe.
func (o *Orchestrator) GetHistory() []llm.Message {
	return o.state.GetHistory()
}

// SetSystemPrompt обновляет системный промпт агента.
//
// Thread-safe.
func (o *Orchestrator) SetSystemPrompt(prompt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// TODO: обновить system prompt в chain
	_ = prompt // Временно
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
- Запрос сложный или многосоставной

### Как работать с планом:

1. **plan_add_task** — добавь задачу в план
2. **plan_mark_done** — отметь задачу как выполненную
3. **plan_mark_failed** — отметь задачу как проваленную
4. **plan_clear** — очисти весь план

## Запомни:
- Для ПРОСТЫХ запросов (1 действие) → отвечай сразу, план не нужен
- Для СЛОЖНЫХ запросов (2+ действий) → сначала создай план через plan_add_task
`
}
