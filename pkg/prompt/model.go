// Структуры данных - описывает формат YAML файла промпта. 
package prompt

// PromptFile описывает структуру YAML-файла с промптом
type PromptFile struct {
	Config   PromptConfig `yaml:"config"`
	Messages []Message    `yaml:"messages"`
}

// PromptConfig - настройки модели для конкретного промпта
type PromptConfig struct {
	Model       string  `yaml:"model"`       // Например "zai-vision/glm-4.5v"
	Temperature float64 `yaml:"temperature"` 
	MaxTokens   int     `yaml:"max_tokens"`
	Format      string  `yaml:"format"`      // "json_object" или text
}

// Message - одно сообщение в чате
type Message struct {
	Role    string `yaml:"role"`    // system, user, assistant
	Content string `yaml:"content"` // Шаблон с {{.Variables}}
}

// ToolPostPrompt описывает post-prompt для конкретного tool
type ToolPostPrompt struct {
	PostPrompt string `yaml:"post_prompt"` // Относительный путь к файлу промпта
	Enabled    bool   `yaml:"enabled"`     // Включён ли post-prompt
}

// ToolPostPromptConfig описывает конфигурацию связки tool → post-prompt
// Загружается из prompts/tool_postprompts.yaml
type ToolPostPromptConfig struct {
	Tools map[string]ToolPostPrompt `yaml:"tools"`
}
