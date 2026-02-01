package prompts

import (
	"fmt"
)

// SourceRegistry — реестр источников промптов с fallback chain.
//
// OCP Principle: Добавление новых источников через AddSource()
// без изменения существующего кода.
//
// Fallback Chain: Источники пробуются по порядку добавления.
// Первый успешный Load() возвращается, все ошибки игнорируются.
// Если все источники失败了, возвращается последняя ошибка.
type SourceRegistry struct {
	sources []PromptSource
}

// NewSourceRegistry создаёт новый реестр источников.
func NewSourceRegistry() *SourceRegistry {
	return &SourceRegistry{
		sources: make([]PromptSource, 0),
	}
}

// AddSource добавляет источник в fallback chain.
// Источники пробуются в порядке добавления.
func (r *SourceRegistry) AddSource(source PromptSource) {
	r.sources = append(r.sources, source)
}

// Load загружает промпт из первого доступного источника.
//
// Fallback Chain:
// 1. Пробует каждый источник по порядку
// 2. Возвращает первый успешный результат
// 3. Если все источники失败了 — возвращает последнюю ошибку
func (r *SourceRegistry) Load(promptID string) (*PromptFile, error) {
	var lastErr error

	for i, source := range r.sources {
		file, err := source.Load(promptID)
		if err == nil {
			// Success! Return from this source
			return file, nil
		}
		// Store last error for fallback reporting
		lastErr = fmt.Errorf("source %d: %w", i, err)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all sources failed for '%s': %w", promptID, lastErr)
	}

	// No sources configured
	return nil, fmt.Errorf("no sources configured for prompt '%s'", promptID)
}

// HasSources проверяет, есть ли хотя бы один источник.
func (r *SourceRegistry) HasSources() bool {
	return len(r.sources) > 0
}
