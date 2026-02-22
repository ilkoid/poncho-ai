// Package state предоставляет реализацию ToolsRepository.
//
// ToolsRepo инкапсулирует работу с реестром инструментов,
// используя Store как thread-safe хранилище.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// ToolsRepo реализует ToolsRepository.
//
// Использует Store для thread-safe хранения реестра инструментов.
// Rule 3: Все инструменты регистрируются через Registry.Register().
type ToolsRepo struct {
	store *Store
}

// NewToolsRepo создаёт новый репозиторий инструментов.
func NewToolsRepo(store *Store) *ToolsRepo {
	return &ToolsRepo{store: store}
}

// SetToolsRegistry устанавливает реестр инструментов.
//
// Thread-safe метод для инициализации registry после регистрации инструментов.
//
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 5: Thread-safe доступ через Store.Set.
func (r *ToolsRepo) SetToolsRegistry(registry *tools.Registry) error {
	return r.store.Set(KeyToolsRegistry, registry)
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Возвращает nil если реестр не установлен.
// Thread-safe: делегирует в Store.Get.
//
// Rule 3: Все инструменты вызываются через Registry.
// Rule 5: Thread-safe доступ через Store.Get.
func (r *ToolsRepo) GetToolsRegistry() *tools.Registry {
	val, ok := r.store.Get(KeyToolsRegistry)
	if !ok {
		return nil
	}

	registry, ok := val.(*tools.Registry)
	if !ok {
		return nil
	}
	return registry
}
