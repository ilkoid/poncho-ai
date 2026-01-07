// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"gopkg.in/yaml.v3"
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
}

// NewReActCycleConfig создаёт конфигурацию ReAct цикла с дефолтными значениями.
//
// Rule 10: Godoc на public API.
func NewReActCycleConfig() ReActCycleConfig {
	return ReActCycleConfig{
		SystemPrompt:  DefaultSystemPrompt,
		MaxIterations: 10,
		Timeout:       5 * time.Minute,
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

// DefaultMaxIterations — стандартный лимит итераций ReAct цикла.
const DefaultMaxIterations = 10

// DefaultChainTimeout — стандартный таймаут для выполнения chain.
const DefaultChainTimeout = 5 * time.Minute

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

// ChainYAMLConfig — структура для загрузки chain конфигурации из YAML.
//
// Используется в config.yaml для определения цепочек.
type ChainYAMLConfig struct {
	// Type — тип цепочки ("react", "sequential")
	Type string `yaml:"type"`

	// Description — описание цепочки (для документации)
	Description string `yaml:"description,omitempty"`

	// SystemPrompt — системный промпт (для ReAct)
	SystemPrompt string `yaml:"system_prompt,omitempty"`

	// Model — модель по умолчанию
	Model string `yaml:"model,omitempty"`

	// Temperature — температура по умолчанию
	Temperature float64 `yaml:"temperature,omitempty"`

	// MaxTokens — максимальное количество токенов
	MaxTokens int `yaml:"max_tokens,omitempty"`

	// MaxIterations — максимальное количество итераций (для ReAct)
	MaxIterations int `yaml:"max_iterations,omitempty"`

	// Timeout — таймаут выполнения (например: "30s", "5m")
	Timeout string `yaml:"timeout,omitempty"`

	// PromptsDir — директория с post-prompts
	PromptsDir string `yaml:"prompts_dir,omitempty"`

	// Debug — конфигурация debug логирования
	Debug DebugConfig `yaml:"debug,omitempty"`

	// Steps — шаги цепочки (для SequentialChain)
	Steps []StepConfig `yaml:"steps,omitempty"`

	// ToolPostPrompts — маппинг tool → post-prompt файл
	ToolPostPrompts map[string]string `yaml:"tool_post_prompts,omitempty"`
}

// ToReActConfig конвертирует YAML конфигурацию в ReActCycleConfig.
//
// Rule 2: Конфигурация через YAML с дефолтными значениями.
func (y *ChainYAMLConfig) ToReActConfig() (ReActCycleConfig, error) {
	cfg := NewReActCycleConfig()

	// Override с YAML значений
	if y.SystemPrompt != "" {
		cfg.SystemPrompt = y.SystemPrompt
	}
	if y.MaxIterations > 0 {
		cfg.MaxIterations = y.MaxIterations
	}
	if y.PromptsDir != "" {
		cfg.PromptsDir = y.PromptsDir
	}
	if y.Timeout != "" {
		timeout, err := time.ParseDuration(y.Timeout)
		if err != nil {
			return ReActCycleConfig{}, fmt.Errorf("invalid timeout format: %w", err)
		}
		cfg.Timeout = timeout
	}

	// Tool post-prompts
	if len(y.ToolPostPrompts) > 0 {
		tools := make(map[string]prompt.ToolPostPrompt)
		for toolName, promptPath := range y.ToolPostPrompts {
			tools[toolName] = prompt.ToolPostPrompt{
				PostPrompt: promptPath,
				Enabled:    true,
			}
		}
		cfg.ToolPostPrompts = &prompt.ToolPostPromptConfig{
			Tools: tools,
		}
	}

	return cfg, cfg.Validate()
}

// LoadChainFromYAML загружает конфигурацию цепочки из YAML bytes.
//
// Rule 2: Конфигурация через YAML.
// Rule 7: Возвращает ошибку вместо panic (никаких Must* функций).
func LoadChainFromYAML(data []byte) (ChainYAMLConfig, error) {
	var cfg ChainYAMLConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ChainYAMLConfig{}, fmt.Errorf("failed to parse chain config: %w", err)
	}
	return cfg, nil
}

// MergeYAMLConfigs мёржит несколько YAML конфигураций.
//
// Приоритет у поздних конфигураций (правые перезаписывают левые).
func MergeYAMLConfigs(configs ...ChainYAMLConfig) ChainYAMLConfig {
	result := ChainYAMLConfig{}
	for _, cfg := range configs {
		if cfg.Type != "" {
			result.Type = cfg.Type
		}
		if cfg.Description != "" {
			result.Description = cfg.Description
		}
		if cfg.SystemPrompt != "" {
			result.SystemPrompt = cfg.SystemPrompt
		}
		if cfg.Model != "" {
			result.Model = cfg.Model
		}
		if cfg.Temperature > 0 {
			result.Temperature = cfg.Temperature
		}
		if cfg.MaxTokens > 0 {
			result.MaxTokens = cfg.MaxTokens
		}
		if cfg.MaxIterations > 0 {
			result.MaxIterations = cfg.MaxIterations
		}
		if cfg.Timeout != "" {
			result.Timeout = cfg.Timeout
		}
		if cfg.PromptsDir != "" {
			result.PromptsDir = cfg.PromptsDir
		}
		if cfg.Debug.Enabled {
			result.Debug = cfg.Debug
		}
		if len(cfg.Steps) > 0 {
			result.Steps = cfg.Steps
		}
		if len(cfg.ToolPostPrompts) > 0 {
			if result.ToolPostPrompts == nil {
				result.ToolPostPrompts = make(map[string]string)
			}
			for k, v := range cfg.ToolPostPrompts {
				result.ToolPostPrompts[k] = v
			}
		}
	}
	return result
}
