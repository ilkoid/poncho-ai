// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// ReActChain — реализация ReAct (Reasoning + Acting) паттерна.
//
// ReActChain выполняет цикл:
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
type ReActChain struct {
	// Dependencies
	llm        llm.Provider
	registry   *tools.Registry
	state      *state.CoreState

	// Configuration
	config     ReActChainConfig

	// Runtime state (thread-safe)
	mu            sync.Mutex
	debugRecorder *ChainDebugRecorder

	// Steps (создаются один раз при конструировании)
	llmStep    *LLMInvocationStep
	toolStep   *ToolExecutionStep

	// Prompts directory
	promptsDir string
}

// NewReActChain создаёт новый ReActChain.
//
// Rule 10: Godoc на public API.
func NewReActChain(config ReActChainConfig) *ReActChain {
	// Валидируем конфигурацию
	if err := config.Validate(); err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		// Но в конструкторе не можем вернуть ошибку, поэтому логируем и используем дефолты
		config = NewReActChainConfig()
	}

	chain := &ReActChain{
		config:     config,
		promptsDir: config.PromptsDir,
	}

	// Создаём шаги (без dependencies - они будут установлены через Setters)
	chain.llmStep = &LLMInvocationStep{
		reasoningConfig: config.ReasoningConfig,
		chatConfig:      config.ChatConfig,
		systemPrompt:    config.SystemPrompt,
	}

	chain.toolStep = &ToolExecutionStep{
		promptLoader: chain, // ReActChain реализует PromptLoader
	}

	return chain
}

// SetLLM устанавливает LLM провайдер.
//
// Rule 4: Работает через llm.Provider интерфейс.
func (c *ReActChain) SetLLM(llmProvider llm.Provider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.llm = llmProvider
	c.llmStep.llm = llmProvider
}

// SetRegistry устанавливает реестр инструментов.
//
// Rule 3: Tools вызываются через Registry.
func (c *ReActChain) SetRegistry(registry *tools.Registry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry = registry
	c.llmStep.registry = registry
	c.toolStep.registry = registry
}

// SetState устанавливает framework core состояние.
//
// Rule 6: Использует pkg/state.CoreState вместо internal/app.
func (c *ReActChain) SetState(state *state.CoreState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

// AttachDebug присоединяет debug recorder.
//
// Реализует интерфейс DebuggableChain.
func (c *ReActChain) AttachDebug(recorder *ChainDebugRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debugRecorder = recorder
	c.llmStep.debugRecorder = recorder
	c.toolStep.debugRecorder = recorder
}

// Execute выполняет ReAct цепочку.
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
func (c *ReActChain) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
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
func (c *ReActChain) executeLLMStep(ctx context.Context, chainCtx *ChainContext, iteration int) (NextAction, error) {
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
func (c *ReActChain) executeToolStep(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	return c.toolStep.Execute(ctx, chainCtx)
}

// finalizeWithError завершает выполнение с ошибкой.
func (c *ReActChain) finalizeWithError(chainCtx *ChainContext, startTime time.Time, err error) (ChainOutput, error) {
	// Финализируем debug с ошибкой
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.Finalize("", time.Since(startTime))
	}

	return ChainOutput{}, err
}

// validateDependencies проверяет что все зависимости установлены.
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActChain) validateDependencies() error {
	if c.llm == nil {
		return fmt.Errorf("LLM provider is not set (call SetLLM)")
	}
	if c.registry == nil {
		return fmt.Errorf("registry is not set (call SetRegistry)")
	}
	// state опционален
	return nil
}

// LoadToolPostPrompt загружает post-prompt для инструмента.
//
// Реализует интерфейс PromptLoader для ToolExecutionStep.
func (c *ReActChain) LoadToolPostPrompt(toolName string) (string, *prompt.PromptConfig, error) {
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

	// Формируем текст системного промпта
	systemPrompt := promptFile.Messages[0].Content

	return systemPrompt, &promptFile.Config, nil
}

// Ensure ReActChain implements DebuggableChain
var _ DebuggableChain = (*ReActChain)(nil)

// Ensure ReActChain implements Chain
var _ Chain = (*ReActChain)(nil)

// Ensure ReActChain implements PromptLoader
var _ PromptLoader = (*ReActChain)(nil)
