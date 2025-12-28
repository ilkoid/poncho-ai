// Package prompt предоставляет функции для загрузки и рендеринга промптов.
//
// Этот файл реализует систему post-prompts — промптов, которые активируются
// после выполнения конкретного tool и используются для следующей LLM итерации.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// LoadToolPostPrompts загружает конфигурацию связки tool → post-prompt из основного config.yaml.
//
// Читает из секции cfg.Tools, которая содержит поле post_prompt для каждого инструмента.
// Валидирует что все referenced файлы существуют (fail-fast).
func LoadToolPostPrompts(cfg *config.AppConfig) (*ToolPostPromptConfig, error) {
	ppConfig := &ToolPostPromptConfig{
		Tools: make(map[string]ToolPostPrompt),
	}

	// Конвертируем cfg.Tools в ToolPostPromptConfig
	for toolName, toolCfg := range cfg.Tools {
		ppConfig.Tools[toolName] = ToolPostPrompt{
			PostPrompt: toolCfg.PostPrompt,
			Enabled:    toolCfg.Enabled,
		}

		// Валидация: проверяем что referenced файлы существуют
		if toolCfg.Enabled && toolCfg.PostPrompt != "" {
			promptPath := filepath.Join(cfg.App.PromptsDir, toolCfg.PostPrompt)
			if _, err := os.Stat(promptPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("post-prompt file not found for tool '%s': %s (tool: %s, path: %s)",
					toolName, promptPath, toolName, toolCfg.PostPrompt)
			}
		}
	}

	return ppConfig, nil
}

// GetToolPostPrompt возвращает текст post-prompt для заданного tool.
//
// Возвращает:
//   - string: текст промпта (пустой если не настроен или отключён)
//   - error: ошибка если не удалось загрузить файл промпта
func (cfg *ToolPostPromptConfig) GetToolPostPrompt(toolName string, promptsDir string) (string, error) {
	// Проверяем есть ли конфиг для этого tool
	toolCfg, exists := cfg.Tools[toolName]
	if !exists {
		return "", nil // Не настроен — это нормально
	}

	// Проверяем включён ли
	if !toolCfg.Enabled || toolCfg.PostPrompt == "" {
		return "", nil // Отключён — это нормально
	}

	// Загружаем файл промпта
	promptPath := filepath.Join(promptsDir, toolCfg.PostPrompt)
	pf, err := Load(promptPath)
	if err != nil {
		return "", fmt.Errorf("load post-prompt file for tool '%s': %w", toolName, err)
	}

	// Если в файле есть messages — берём первое как system prompt
	if len(pf.Messages) > 0 && pf.Messages[0].Role == "system" {
		return pf.Messages[0].Content, nil
	}

	// Иначе читаем как plain text
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read post-prompt file %s: %w", promptPath, err)
	}

	return string(data), nil
}

// GetToolPromptFile возвращает весь PromptFile включая Config для заданного tool.
//
// Возвращает:
//   - *PromptFile: весь файл промпта с Config и Messages (nil если не настроен)
//   - error: ошибка если не удалось загрузить файл промпта
//
// Используется для runtime переопределения параметров модели (model, temperature, max_tokens).
func (cfg *ToolPostPromptConfig) GetToolPromptFile(toolName string, promptsDir string) (*PromptFile, error) {
	// Проверяем есть ли конфиг для этого tool
	toolCfg, exists := cfg.Tools[toolName]
	if !exists {
		return nil, nil // Не настроен — это нормально
	}

	// Проверяем включён ли
	if !toolCfg.Enabled || toolCfg.PostPrompt == "" {
		return nil, nil // Отключён — это нормально
	}

	// Загружаем файл промпта
	promptPath := filepath.Join(promptsDir, toolCfg.PostPrompt)
	pf, err := Load(promptPath)
	if err != nil {
		return nil, fmt.Errorf("load post-prompt file for tool '%s': %w", toolName, err)
	}

	return pf, nil
}
