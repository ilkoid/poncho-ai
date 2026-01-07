// Package models предоставляет централизованный реестр LLM провайдеров.
//
// Реестр позволяет зарегистрировать все модели из config.yaml при старте
// и динамически переключаться между ними во время выполнения.
//
// Rule 3: Registry pattern (similar to tools.Registry)
// Rule 5: Thread-safe via sync.RWMutex
// Rule 6: Reusable library package, no imports from internal/
package models

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// Registry — потокобезопасное хранилище LLM провайдеров.
//
// Rule 5: Thread-safe через sync.RWMutex.
// Rule 3: Registry pattern.
type Registry struct {
	mu     sync.RWMutex
	models map[string]ModelEntry
}

// ModelEntry — кешированный провайдер с конфигурацией.
type ModelEntry struct {
	Provider llm.Provider
	Config   config.ModelDef
}

// NewRegistry создаёт новый пустой реестр.
//
// Rule 5: Инициализирован мьютекс, карта создана.
func NewRegistry() *Registry {
	return &Registry{
		models: make(map[string]ModelEntry),
	}
}

// Register добавляет модель в реестр.
//
// Thread-safe. Возвращает ошибку если модель с таким именем уже зарегистрирована.
//
// Rule 7: Возвращает ошибку вместо panic.
func (r *Registry) Register(name string, modelDef config.ModelDef, provider llm.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[name]; exists {
		return fmt.Errorf("model '%s' already registered", name)
	}

	r.models[name] = ModelEntry{
		Provider: provider,
		Config:   modelDef,
	}
	return nil
}

// Get извлекает провайдер по имени модели.
//
// Thread-safe. Возвращает ошибку если модель не найдена.
func (r *Registry) Get(name string) (llm.Provider, config.ModelDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.models[name]
	if !ok {
		return nil, config.ModelDef{}, fmt.Errorf("model '%s' not found in registry", name)
	}
	return entry.Provider, entry.Config, nil
}

// GetWithFallback извлекает провайдер с fallback на дефолтную модель.
//
// Thread-safe. Приоритет:
// 1. Запрошенная модель (requested)
// 2. Дефолтная модель (defaultModel)
//
// Возвращает (provider, modelDef, actualModelName, error).
func (r *Registry) GetWithFallback(requested, defaultModel string) (llm.Provider, config.ModelDef, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Пытаемся получить запрошенную модель
	if entry, ok := r.models[requested]; ok {
		return entry.Provider, entry.Config, requested, nil
	}

	// 2. Fallback на дефолтную модель
	if entry, ok := r.models[defaultModel]; ok {
		return entry.Provider, entry.Config, defaultModel, nil
	}

	// 3. Ни одна не найдена
	return nil, config.ModelDef{}, "", fmt.Errorf("neither requested model '%s' nor default '%s' found in registry", requested, defaultModel)
}

// ListNames возвращает список всех зарегистрированных имён моделей.
//
// Thread-safe. Полезно для логирования и отладки.
func (r *Registry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.models))
	for name := range r.models {
		names = append(names, name)
	}
	return names
}

// NewRegistryFromConfig создаёт и заполняет реестр из конфигурации.
//
// Итерируется через cfg.Models.Definitions и создаёт провайдеры для каждой модели.
// Возвращает ошибку если хоть одна модель не инициализируется.
//
// Rule 4: Работает через llm.Provider интерфейс.
// Rule 7: Возвращает ошибку вместо panic.
func NewRegistryFromConfig(cfg *config.AppConfig) (*Registry, error) {
	registry := NewRegistry()

	// Регистрируем все определённые модели
	for name, modelDef := range cfg.Models.Definitions {
		provider, err := factory.NewLLMProvider(modelDef)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider for model '%s': %w", name, err)
		}

		if err := registry.Register(name, modelDef, provider); err != nil {
			return nil, fmt.Errorf("failed to register model '%s': %w", name, err)
		}
	}

	return registry, nil
}
