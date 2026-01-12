// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
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

	// Streaming configuration
	streamingEnabled bool // Streaming включен по умолчанию (из config)

	// Runtime state (thread-safe)
	mu            sync.Mutex
	debugRecorder *ChainDebugRecorder
	emitter       events.Emitter // Port & Adapter: отправка событий в UI

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

// AttachDebug присоединяет debug recorder к ReActCycle.
func (c *ReActCycle) AttachDebug(recorder *ChainDebugRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debugRecorder = recorder
	c.llmStep.debugRecorder = recorder
	c.toolStep.debugRecorder = recorder
}

// SetEmitter устанавливает emitter для отправки событий в UI.
//
// Port & Adapter pattern: ReActCycle зависит от абстракции events.Emitter.
// Thread-safe.
func (c *ReActCycle) SetEmitter(emitter events.Emitter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emitter = emitter
	// Передаём emitter в LLM шаг для streaming событий
	c.llmStep.emitter = emitter
}

// SetStreamingEnabled включает или выключает streaming режим.
//
// Thread-safe.
func (c *ReActCycle) SetStreamingEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streamingEnabled = enabled
}

// emitEvent отправляет событие если emitter установлен.
//
// Thread-safe helper метод.
func (c *ReActCycle) emitEvent(ctx context.Context, event events.Event) {
	if c.emitter == nil {
		utils.Debug("emitEvent: emitter is nil, skipping", "event_type", event.Type)
		return
	}
	utils.Debug("emitEvent: sending", "event_type", event.Type, "has_data", event.Data != nil)
	c.emitter.Emit(ctx, event)
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
		// Start debug iteration для всей итерации (LLM + Tools)
		if c.debugRecorder != nil && c.debugRecorder.Enabled() {
			c.debugRecorder.StartIteration(iterations + 1)
		}

		// 5a. LLM Invocation
		action, err := c.executeLLMStep(ctx, chainCtx)
		if err != nil {
			return c.finalizeWithError(chainCtx, startTime, err)
		}
		if action == ActionError {
			c.endDebugIteration()
			return c.finalizeWithError(chainCtx, startTime, fmt.Errorf("LLM step failed"))
		}

		// Отправляем EventThinking с контентом LLM (рассуждения)
		// НО только если НЕ был streaming (streaming отправляет EventThinkingChunk)
		lastMsg := chainCtx.GetLastMessage()

		// Проверяем: был ли streaming? Если в content есть reasoning_content markers
		// или если есть emitter (используется для streaming), то не дублируем
		shouldSendThinking := true
		if c.emitter != nil {
			// Streaming был включен, EventThinkingChunk уже отправляли
			// Проверяем по косвенным признакам: длинный content без markdown заголовков
			// В streaming режиме reasoning_content выводится по буквам
			shouldSendThinking = false
		}

		if shouldSendThinking {
			c.emitEvent(ctx, events.Event{
				Type:      events.EventThinking,
				Data:      lastMsg.Content,
				Timestamp: time.Now(),
			})
		}

		// Отправляем EventToolCall для каждого tool call
		for _, tc := range lastMsg.ToolCalls {
			c.emitEvent(ctx, events.Event{
				Type: events.EventToolCall,
				Data: events.ToolCallData{
					ToolName: tc.Name,
					Args:     tc.Args,
				},
				Timestamp: time.Now(),
			})
		}

		// 5b. Проверяем есть ли tool calls
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			c.endDebugIteration()
			break
		}

		// 5c. Tool Execution (внутри той же итерации!)
		action, err = c.executeToolStep(ctx, chainCtx)
		if err != nil {
			c.endDebugIteration()
			return c.finalizeWithError(chainCtx, startTime, err)
		}
		if action == ActionError {
			c.endDebugIteration()
			return c.finalizeWithError(chainCtx, startTime, fmt.Errorf("tool execution failed"))
		}

		// Отправляем EventToolResult для каждого выполненного tool
		for _, tr := range c.toolStep.GetToolResults() {
			c.emitEvent(ctx, events.Event{
				Type: events.EventToolResult,
				Data: events.ToolResultData{
					ToolName: tr.Name,
					Result:   tr.Result,
					Duration: time.Duration(tr.Duration) * time.Millisecond,
				},
				Timestamp: time.Now(),
			})
		}

		// End debug iteration после LLM + Tools
		c.endDebugIteration()
	}

	// 6. Формируем результат
	lastMsg := chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(startTime).Milliseconds())

	// 7. Отправляем EventMessage с текстом ответа
	utils.Debug("Sending EventMessage", "content_length", len(result))
	c.emitEvent(ctx, events.Event{
		Type:      events.EventMessage,
		Data:      result,
		Timestamp: time.Now(),
	})

	// 8. Отправляем EventDone
	utils.Debug("Sending EventDone")
	c.emitEvent(ctx, events.Event{
		Type:      events.EventDone,
		Data:      result,
		Timestamp: time.Now(),
	})

	// 9. Финализируем debug
	debugPath := ""
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		debugPath, _ = c.debugRecorder.Finalize(result, time.Since(startTime))
	}

	// 10. Возвращаем результат
	return ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(startTime),
		FinalState: chainCtx.GetMessages(),
		DebugPath:  debugPath,
	}, nil
}

// executeLLMStep выполняет LLM шаг.
//
// Debug iteration управляется в ReActCycle.Execute() (одна итерация = LLM + Tools).
func (c *ReActCycle) executeLLMStep(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	return c.llmStep.Execute(ctx, chainCtx)
}

// executeToolStep выполняет Tool шаг.
func (c *ReActCycle) executeToolStep(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	return c.toolStep.Execute(ctx, chainCtx)
}

// endDebugIteration завершает текущую debug итерацию.
//
// Helper метод для избежания дублирования кода.
func (c *ReActCycle) endDebugIteration() {
	if c.debugRecorder != nil && c.debugRecorder.Enabled() {
		c.debugRecorder.EndIteration()
	}
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

// Ensure ReActCycle implements Chain
var _ Chain = (*ReActCycle)(nil)

// Run выполняет ReAct цикл для запроса пользователя.
//
// Реализует Agent interface для прямого использования агента.
// Удобно для простых случаев когда не нужен полный контроль ChainInput.
//
// Thread-safe (делегирует блокировку в Execute).
func (c *ReActCycle) Run(ctx context.Context, query string) (string, error) {
	// Проверяем зависимости без блокировки (Execute заблокирует)
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
	// Execute выполнит блокировку мьютекса
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

// Ensure ReActCycle implements Agent (local interface in chain package)
var _ Agent = (*ReActCycle)(nil)
