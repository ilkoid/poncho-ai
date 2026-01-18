// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// defaultInterruptionPrompt — fallback промпт если YAML файл не найден.
//
// Используется когда InterruptionPrompt не указан в конфигурации или файл не существует.
// Гарантирует что механизм прерываний работает даже без внешнего файла.
const defaultInterruptionPrompt = `You are an INTERRUPTION HANDLER for an AI agent.

The user has interrupted execution with a message. Your task:
1. Acknowledge the interruption
2. Address the user's concern
3. Decide whether to continue or stop execution

If user mentions "todo" or "plan", use these operations:
- "todo: add <task>" or "plan: add <task>" → Call plan_add_task
- "todo: complete <N>" or "plan: done <N>" → Call plan_mark_done
- "todo: fail <N> <reason>" or "plan: fail <N> <reason>" → Call plan_mark_failed
- "todo: show" or "plan: show" → Include current todo list in response
- "todo: clear" or "plan: clear" → Call plan_clear

Always respond in plain text. Be concise. Use emojis: ✅ ❌ 🛑 ⏸️`

// loadInterruptionPrompt загружает interruption handler промпт из YAML.
//
// Приоритет:
// 1. YAML файл из InterruptionPrompt path
// 2. defaultInterruptionPrompt (fallback)
//
// Возвращает:
//   - string: системный промпт для interruption handler
//   - *prompt.PromptConfig: конфигурация (или nil для defaults)
//
// Rule 2: Загружает промпт из YAML с поддержкой fallback.
// Rule 7: Все ошибки обрабатываются, возвращает fallback вместо panic.
//
// Параметры:
//   - promptsDir: Базовая директория для промптов (из ChainConfig.PostPromptsDir)
//   - interruptionPromptPath: Путь к YAML файлу (относительный или абсолютный)
func loadInterruptionPrompt(
	promptsDir string,
	interruptionPromptPath string,
) (string, *prompt.PromptConfig) {
	// Если путь не указан — используем fallback
	if interruptionPromptPath == "" {
		return defaultInterruptionPrompt, nil
	}

	// Формируем полный путь (относительно prompts_dir или абсолютный)
	fullPath := interruptionPromptPath
	if promptsDir != "" && !filepath.IsAbs(interruptionPromptPath) {
		fullPath = filepath.Join(promptsDir, interruptionPromptPath)
	}

	// Проверяем существование файла
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// Файл не найден — используем fallback silently
		return defaultInterruptionPrompt, nil
	}

	// Загружаем YAML промпт
	promptFile, err := prompt.Load(fullPath)
	if err != nil {
		// Ошибка загрузки — используем fallback silently (Rule 7: no panic)
		return defaultInterruptionPrompt, nil
	}

	// Извлекаем системный промпт
	if len(promptFile.Messages) == 0 {
		return defaultInterruptionPrompt, nil
	}

	systemPrompt := promptFile.Messages[0].Content

	// Возвращаем конфигурацию из YAML (температура, max_tokens, etc.)
	return systemPrompt, &promptFile.Config
}
