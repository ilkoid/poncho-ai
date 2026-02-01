package prompts

import "fmt"

// PromptFile — содержимое загруженного промпта.
//
// Используется всеми реализациями PromptSource интерфейса.
type PromptFile struct {
	// System — системный промпт (например, agent_system)
	System string `yaml:"system"`

	// Template — шаблон промпта (опционально)
	Template string `yaml:"template"`

	// Variables — переменные для подстановки (например, tool_postprompts)
	Variables map[string]string `yaml:"variables"`

	// Metadata — метаданные промпта
	Metadata map[string]any `yaml:"metadata"`
}

// ErrNotFound возвращается когда источник не содержит промпт.
var ErrNotFound = fmt.Errorf("prompt not found in source")
