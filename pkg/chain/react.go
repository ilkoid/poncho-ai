// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// ReActCycle — реализация ReAct (Reasoning + Acting) паттерна.
//
// ReActCycle выполняет цикл:
// 1. LLM анализирует контекст и решает что делать (Reasoning)
// 2. Если нужны инструменты — выполняет их (Acting)
// 3. Повторяет пока не получен финальный ответ или не достигнут лимит
//
// Rule 1: Работает с Tool interface ("Raw In, String Out")
// Rule 2: Конфигурация через YAML
// Rule 3: Tools вызываются через Registry
// Rule 4: LLM вызывается через llm.Provider
// Rule 5: Thread-safe через sync.Mutex
// Rule 7: Все ошибки возвращаются, нет panic
// Rule 10: Godoc на всех public API
type ReActCycle struct {
	// Dependencies
	modelRegistry *models.Registry // Registry всех LLM провайдеров
	registry      *tools.Registry
	state         *state.CoreState

	// Default model name для fallback
	defaultModel string

	// Configuration
	config ReActCycleConfig

	// Runtime state (thread-safe)
	mu            sync.Mutex
	debugRecorder *ChainDebugRecorder

	// Steps (создаются один раз при конструировании)
	llmStep  *LLMInvocationStep
	toolStep *ToolExecutionStep

	// Prompts directory
	promptsDir string
}

// NewReActCycle создаёт новый ReActCycle.
//
// Rule 10: Godoc на public API.
func NewReActCycle(config ReActCycleConfig) *ReActCycle {
	// Валидируем конфигурацию
	if err := config.Validate(); err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		// Но в конструкторе не можем вернуть ошибку, поэтому логируем и используем дефолты
		config = NewReActCycleConfig()
	}

	cycle := &ReActCycle{
		config:     config,
		promptsDir: config.PromptsDir,
	}

	// Создаём шаги (без dependencies - они будут установлены через Setters)
	cycle.llmStep = &LLMInvocationStep{
		systemPrompt: config.SystemPrompt,
	}

	cycle.toolStep = &ToolExecutionStep{
		promptLoader: cycle, // ReActCycle реализует PromptLoader
	}

	return cycle
}

// SetModelRegistry устанавливает реестр моделей и дефолтную модель.
//
// Rule 3: Registry pattern для моделей.
// Rule 4: Работает через models.Registry интерфейс.
func (c *ReActCycle) SetModelRegistry(registry *models.Registry, defaultModel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelRegistry = registry
	c.defaultModel = defaultModel
	c.llmStep.modelRegistry = registry
	c.llmStep.defaultModel = defaultModel
}

// SetRegistry устанавливает реестр инструментов.
//
// Rule 3: Tools вызываются через Registry.
func (c *ReActCycle) SetRegistry(registry *tools.Registry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry = registry
	c.llmStep.registry = registry
	c.toolStep.registry = registry
}

// SetState устанавливает framework core состояние.
//
// Rule 6: Использует pkg/state.CoreState вместо internal/app.
func (c *ReActCycle) SetState(state *state.CoreState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

// AttachDebug присоединяет debug recorder.
//
// Реализует интерфейс DebuggableChain.
func (c *ReActCycle) AttachDebug(recorder *ChainDebugRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debugRecorder = recorder
	c.llmStep.debugRecorder = recorder
	c.toolStep.debugRecorder = recorder
}

// Execute выполняет ReAct цикл.
//
// ReAct цикл:
// 1. Добавляем user message в историю
// 2. Повторяем до MaxIterations:
//    a. LLMInvocationStep — вызываем LLM
//    b. Если есть tool calls → ToolExecutionStep
//    c. Если нет tool calls → BREAK (финальный ответ)
// 3. Возвращаем финальный ответ
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycle) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
	startTime := time.Now()

	// Thread-safety: блокируем на время выполнения
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Проверяем зависимости
	if err := c.validateDependencies(); err != nil {
		return ChainOutput{}, fmt.Errorf("invalid dependencies: %w", err)
	}

	// 2. Создаём контекст выполнения
	chainCtx := NewChainContext(input)

	// 3. Добавляем user message в историю
	// REFACTORED 2026-01-04: AppendMessage теперь возвращает ошибку
	if err := chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: input.UserQuery,
	}); err != nil {
		return ChainOutput{}, fmt.Errorf("failed to append user message: %w", err)
	}

	// 4. Начинаем debug запись
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.Start(input)
	}

	// 5. ReAct цикл
	iterations := 0
	for iterations = 0; iterations < c.config.MaxIterations; iterations++ {
		// 5a. LLM Invocation
		action, err := c.executeLLMStep(ctx, chainCtx, iterations)
		if err != nil {
			return c.finalizeWithError(chainCtx, startTime, err)
		}
		if action == ActionError {
			return c.finalizeWithError(chainCtx, startTime, fmt.Errorf("LLM step failed"))
		}

		// 5b. Проверяем есть ли tool calls
		lastMsg := chainCtx.GetLastMessage()
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			break
		}

		// 5c. Tool Execution
		action, err = c.executeToolStep(ctx, chainCtx)
		if err != nil {
			return c.finalizeWithError(chainCtx, startTime, err)
		}
		if action == ActionError {
			return c.finalizeWithError(chainCtx, startTime, fmt.Errorf("tool execution failed"))
		}
	}

	// 6. Формируем результат
	lastMsg := chainCtx.GetLastMessage()
	result := lastMsg.Content

	// 7. Финализируем debug
	debugPath := ""
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		debugPath, _ = c.debugRecorder.Finalize(result, time.Since(startTime))
	}

	// 8. Возвращаем результат
	return ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(startTime),
		FinalState: chainCtx.GetMessages(),
		DebugPath:  debugPath,
	}, nil
}

// executeLLMStep выполняет LLM шаг с debug записью.
func (c *ReActCycle) executeLLMStep(ctx context.Context, chainCtx *ChainContext, iteration int) (NextAction, error) {
	// Start debug iteration
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.StartIteration(iteration + 1)
	}

	// Execute LLM step
	action, err := c.llmStep.Execute(ctx, chainCtx)

	// End debug iteration
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.EndIteration()
	}

	return action, err
}

// executeToolStep выполняет Tool шаг.
func (c *ReActCycle) executeToolStep(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	return c.toolStep.Execute(ctx, chainCtx)
}

// finalizeWithError завершает выполнение с ошибкой.
func (c *ReActCycle) finalizeWithError(chainCtx *ChainContext, startTime time.Time, err error) (ChainOutput, error) {
	// Финализируем debug с ошибкой
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.Finalize("", time.Since(startTime))
	}

	return ChainOutput{}, err
}

// validateDependencies проверяет что все зависимости установлены.
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycle) validateDependencies() error {
	if c.modelRegistry == nil {
		return fmt.Errorf("model registry is not set (call SetModelRegistry)")
	}
	if c.defaultModel == "" {
		return fmt.Errorf("default model is not set")
	}
	if c.registry == nil {
		return fmt.Errorf("tools registry is not set (call SetRegistry)")
	}
	// state опционален
	return nil
}

// LoadToolPostPrompt загружает post-prompt для инструмента.
//
// Реализует интерфейс PromptLoader для ToolExecutionStep.
func (c *ReActCycle) LoadToolPostPrompt(toolName string) (string, *prompt.PromptConfig, error) {
	// Проверяем есть ли post-prompt конфигурация
	if c.config.ToolPostPrompts == nil {
		return "", nil, fmt.Errorf("no post-prompts configured")
	}

	toolPrompt, ok := c.config.ToolPostPrompts.Tools[toolName]
	if !ok || !toolPrompt.Enabled {
		return "", nil, fmt.Errorf("no post-prompt for tool: %s", toolName)
	}

	// Загружаем prompt файл через prompt package
	promptFile, err := c.config.ToolPostPrompts.GetToolPromptFile(toolName, c.promptsDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load prompt file: %w", err)
	}

	// Post-prompt опционален — если не настроен, возвращаем nil
	if promptFile == nil {
		return "", nil, nil
	}

	// Формируем текст системного промпта
	if len(promptFile.Messages) == 0 {
		return "", nil, fmt.Errorf("prompt file has no messages: %s", toolName)
	}
	systemPrompt := promptFile.Messages[0].Content

	return systemPrompt, &promptFile.Config, nil
}

// Ensure ReActCycle implements DebuggableChain
var _ DebuggableChain = (*ReActCycle)(nil)

// Ensure ReActCycle implements Chain
var _ Chain = (*ReActCycle)(nil)

// Run выполняет ReAct цикл для запроса пользователя.
//
// Реализует Agent interface для прямого использования агента.
// Удобно для простых случаев когда не нужен полный контроль ChainInput.
//
// Thread-safe.
func (c *ReActCycle) Run(ctx context.Context, query string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Проверяем зависимости
	if err := c.validateDependencies(); err != nil {
		return "", err
	}

	// Создаем ChainInput из текущей конфигурации
	input := ChainInput{
		UserQuery: query,
		State:     c.state,
		Registry:  c.registry,
	}

	// Выполняем Chain и возвращаем результат
	output, err := c.Execute(ctx, input)
	if err != nil {
		return "", err
	}

	// Проверяем на UserChoiceRequest
	if output.Result == UserChoiceRequest {
		return UserChoiceRequest, nil
	}

	return output.Result, nil
}

// GetHistory возвращает историю диалога.
//
// Реализует Agent interface.
func (c *ReActCycle) GetHistory() []llm.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == nil {
		return []llm.Message{}
	}
	return c.state.GetHistory()
}

// Ensure ReActCycle implements PromptLoader
var _ PromptLoader = (*ReActCycle)(nil)

// Ensure ReActCycle implements Agent
var _ agent.Agent = (*ReActCycle)(nil)
