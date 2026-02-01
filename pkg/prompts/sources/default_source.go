package sources

import "fmt"

// DefaultSource — загрузка встроенных (hardcoded) промптов.
//
// OCP Principle: Fallback source когда YAML файлы недоступны.
// YAML-first философия: файлы приоритетны, defaults — резерв.
type DefaultSource struct {
	// Встроенные промпты (map для простоты расширения)
	prompts map[string]*PromptData
}

// NewDefaultSource создаёт источник с Go defaults.
func NewDefaultSource() *DefaultSource {
	return &DefaultSource{
		prompts: make(map[string]*PromptData),
	}
}

// AddPrompt добавляет встроенный промпт.
func (s *DefaultSource) AddPrompt(id string, file *PromptData) {
	s.prompts[id] = file
}

// Load возвращает встроенный промпт или ErrNotFound.
func (s *DefaultSource) Load(promptID string) (*PromptData, error) {
	file, ok := s.prompts[promptID]
	if !ok {
		return nil, fmt.Errorf("default prompt '%s' not defined", promptID)
	}
	return file, nil
}

// PopulateDefaults заполняет источник стандартными промптами.
func (s *DefaultSource) PopulateDefaults() {
	s.AddPrompt("agent_system", GetDefaultAgentSystemPrompt())
	s.AddPrompt("tool_postprompts", GetDefaultToolPostprompts())
}

// GetDefaultAgentSystemPrompt возвращает дефолтный системный промпт агента.
//
// Exported функция для использования в registry factory.
func GetDefaultAgentSystemPrompt() *PromptData {
	return &PromptData{
		System: `You are a helpful AI assistant with access to tools.

Use tools when needed to answer user questions. Think step by step.

Tool usage format:
<tool_call>
{"name": "tool_name", "arguments": {"key": "value"}}
</tool_call>

After tool results, analyze and provide final answer.`,
		Metadata: map[string]any{
			"source":  "go-default",
			"version": "1.0",
		},
	}
}

// GetDefaultToolPostprompts возвращает дефолтные tool post-prompts.
func GetDefaultToolPostprompts() *PromptData {
	return &PromptData{
		Variables: map[string]string{
			"tool_postprompts": `When calling tools:
1. Use exact JSON format
2. Include all required parameters
3. Wait for tool result before continuing`,
		},
		Metadata: map[string]any{
			"source":  "go-default",
			"version": "1.0",
		},
	}
}
