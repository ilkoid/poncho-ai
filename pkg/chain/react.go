// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/events"
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
// Rule 5: Thread-safe через immutability (шаблон + execution)
// Rule 7: Все ошибки возвращаются, нет panic
// Rule 10: Godoc на всех public API
//
// PHASE 1 REFACTOR: ReActCycle теперь immutable template.
// Runtime состояние вынесено в ReActExecution (execution.go).
// Concurrent execution безопасен - каждый Execute() создаёт свой execution.
type ReActCycle struct {
	// Dependencies (immutable)
	modelRegistry *models.Registry // Registry всех LLM провайдеров
	registry      *tools.Registry
	state         *state.CoreState

	// Default model name для fallback
	defaultModel string

	// Configuration (immutable после создания, кроме runtimeDefaults)
	config ReActCycleConfig

	// Runtime defaults protection - только для mutable полей config
	// (DefaultEmitter, DefaultDebugRecorder, StreamingEnabled)
	mu sync.RWMutex

	// Steps (immutable template - клонируются в execution)
	llmStep  *LLMInvocationStep
	toolStep *ToolExecutionStep

	// Prompts directory (immutable)
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

	// Создаём шаги (immutable template - будут клонироваться в execution)
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
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetModelRegistry(registry *models.Registry, defaultModel string) {
	c.modelRegistry = registry
	c.defaultModel = defaultModel
	c.llmStep.modelRegistry = registry
	c.llmStep.defaultModel = defaultModel
}

// SetRegistry устанавливает реестр инструментов.
//
// Rule 3: Tools вызываются через Registry.
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetRegistry(registry *tools.Registry) {
	c.registry = registry
	c.llmStep.registry = registry
	c.toolStep.registry = registry
}

// SetState устанавливает framework core состояние.
//
// Rule 6: Использует pkg/state.CoreState вместо internal/app.
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetState(state *state.CoreState) {
	c.state = state
}

// AttachDebug присоединяет debug recorder к ReActCycle.
//
// PHASE 1 REFACTOR: Теперь сохраняет recorder в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.DefaultDebugRecorder.
func (c *ReActCycle) AttachDebug(recorder *ChainDebugRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.DefaultDebugRecorder = recorder
}

// SetEmitter устанавливает emitter для отправки событий в UI.
//
// Port & Adapter pattern: ReActCycle зависит от абстракции events.Emitter.
//
// PHASE 1 REFACTOR: Теперь сохраняет emitter в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.DefaultEmitter.
func (c *ReActCycle) SetEmitter(emitter events.Emitter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.DefaultEmitter = emitter
}

// SetStreamingEnabled включает или выключает streaming режим.
//
// PHASE 1 REFACTOR: Теперь сохраняет флаг в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.StreamingEnabled.
func (c *ReActCycle) SetStreamingEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.StreamingEnabled = enabled
}

// Execute выполняет ReAct цикл.
//
// PHASE 1 REFACTOR: Теперь создаёт ReActExecution на каждый вызов.
// Thread-safe: читает runtime defaults с RWMutex, concurrent execution безопасен.
// Concurrent execution безопасен - несколько Execute() могут работать параллельно.
//
// ReAct цикл:
// 1. Валидация зависимостей (read-only, без блокировки)
// 2. Читаем runtime defaults с RWMutex
// 3. Создаём ReActExecution (runtime state)
// 4. Запускаем execution.Run()
// 5. Возвращаем результат
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycle) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
	// 1. Валидация (read-only, без блокировки)
	if err := c.validateDependencies(); err != nil {
		return ChainOutput{}, fmt.Errorf("invalid dependencies: %w", err)
	}

	// 2. Читаем runtime defaults с RLock
	c.mu.RLock()
	defaultEmitter := c.config.DefaultEmitter
	defaultDebugRecorder := c.config.DefaultDebugRecorder
	streamingEnabled := c.config.StreamingEnabled
	c.mu.RUnlock()

	// 3. Создаём execution (runtime state)
	execution := NewReActExecution(
		ctx,
		input,
		c.llmStep,               // Шаблон шага (будет склонирован)
		c.toolStep,              // Шаблон шага (будет склонирован)
		defaultEmitter,          // Emitter из config (thread-safe copy)
		defaultDebugRecorder,    // Debug recorder из config (thread-safe copy)
		streamingEnabled,        // Streaming флаг из config (thread-safe copy)
		&c.config,               // Reference на config (immutable part)
	)

	// 4. Запускаем execution (без mutex!)
	return execution.Run()
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
// PHASE 1 REFACTOR: Thread-safe через immutability - без mutex.
func (c *ReActCycle) Run(ctx context.Context, query string) (string, error) {
	// Проверяем зависимости (read-only)
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
// PHASE 1 REFACTOR: Блокировка не нужна - CoreState thread-safe (RWMutex).
func (c *ReActCycle) GetHistory() []llm.Message {
	if c.state == nil {
		return []llm.Message{}
	}
	return c.state.GetHistory()
}

// Ensure ReActCycle implements PromptLoader
var _ PromptLoader = (*ReActCycle)(nil)

// Ensure ReActCycle implements Agent (local interface in chain package)
var _ Agent = (*ReActCycle)(nil)
