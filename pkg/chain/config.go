// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// ReActCycleConfig — конфигурация ReAct цикла.
//
// Используется при создании ReActCycle через NewReActCycle.
// Конфигурация может быть загружена из YAML или создана программно.
type ReActCycleConfig struct {
	// SystemPrompt — базовый системный промпт для ReAct агента.
	SystemPrompt string

	// ToolPostPrompts — конфигурация post-prompts для инструментов.
	// Опционально: может быть nil.
	ToolPostPrompts *prompt.ToolPostPromptConfig

	// PromptsDir — директория с post-prompts файлами.
	// Rule 11: Поиск рядом с бинарником или в текущей директории.
	PromptsDir string

	// MaxIterations — максимальное количество итераций ReAct цикла.
	// По умолчанию: 10.
	MaxIterations int

	// Timeout — таймаут выполнения всей цепочки.
	// По умолчанию: 5 минут.
	Timeout time.Duration

	// DefaultToolTimeout — защитный timeout для выполнения инструментов.
	// Если tool не завершится за это время, он будет отменён.
	// По умолчанию: 30 секунд.
	DefaultToolTimeout time.Duration

	// DefaultEmitter — emitter по умолчанию для созданных executions.
	// Может быть переопределён через SetEmitter.
	// Thread-safe: Emitter не должен модифицироваться после установки.
	DefaultEmitter events.Emitter

	// DefaultDebugRecorder — debug recorder по умолчанию для созданных executions.
	// Может быть переопределён через AttachDebug.
	// Thread-safe: Recorder не должен модифицироваться после установки.
	DefaultDebugRecorder *ChainDebugRecorder

	// StreamingEnabled — включён ли streaming по умолчанию.
	// Может быть переопределён через SetStreamingEnabled.
	StreamingEnabled bool
}

// NewReActCycleConfig создаёт конфигурацию ReAct цикла с дефолтными значениями.
//
// Rule 10: Godoc на public API.
func NewReActCycleConfig() ReActCycleConfig {
	return ReActCycleConfig{
		SystemPrompt:       DefaultSystemPrompt,
		MaxIterations:      10,
		Timeout:            5 * time.Minute,
		DefaultToolTimeout: 30 * time.Second, // Защита от зависания инструментов
	}
}

// Validate проверяет конфигурацию на валидность.
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycleConfig) Validate() error {
	if c.SystemPrompt == "" {
		return fmt.Errorf("system_prompt is required")
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("max_iterations must be positive, got %d", c.MaxIterations)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %v", c.Timeout)
	}
	return nil
}

// UserChoiceRequest — маркер для передачи управления UI.
// Используется для прерывания ReAct цикла и запроса пользовательского ввода.
const UserChoiceRequest = "__USER_CHOICE_REQUIRED__"

// DefaultSystemPrompt — базовый системный промпт по умолчанию.
const DefaultSystemPrompt = `You are a helpful AI assistant with access to tools.

When you need to use a tool, respond with a function call in the following format:
{
  "name": "tool_name",
  "arguments": {"param1": "value1"}
}

After receiving tool results, analyze them and determine if you need more information or can provide a final answer.
Be concise and helpful in your responses.`
