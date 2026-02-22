// Package state предоставляет реализацию TodoRepository.
//
// TodoRepo инкапсулирует работу с задачами,
// используя Store как thread-safe хранилище и todo.Manager для логики.
package state

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// TodoRepo реализует TodoRepository.
//
// Использует Store для thread-safe хранения todo.Manager.
// Делегирует основную логику в todo.Manager.
type TodoRepo struct {
	store *Store
}

// NewTodoRepo создаёт новый репозиторий задач.
func NewTodoRepo(store *Store) *TodoRepo {
	return &TodoRepo{store: store}
}

// AddTask добавляет новую задачу в план.
//
// Параметры:
//   - description: описание задачи
//   - metadata: опциональные метаданные (ключ-значение)
//
// Возвращает ID созданной задачи.
// Автоматически создаёт todo.Manager если не существует.
//
// Thread-safe: делегирует в Store для получения/создания менеджера.
func (r *TodoRepo) AddTask(description string, metadata ...map[string]any) (int, error) {
	manager := r.getManager()
	if manager == nil {
		// Создаем новый менеджер если не существует
		manager = todo.NewManager()
		if err := r.store.Set(KeyTodo, manager); err != nil {
			return 0, fmt.Errorf("failed to create todo manager: %w", err)
		}
	}

	id := manager.Add(description, metadata...)
	return id, nil
}

// CompleteTask отмечает задачу как выполненную.
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует в todo.Manager (thread-safe).
func (r *TodoRepo) CompleteTask(id int) error {
	manager := r.getManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Complete(id)
}

// FailTask отмечает задачу как проваленную.
//
// Параметры:
//   - id: ID задачи
//   - reason: причина неудачи
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует в todo.Manager (thread-safe).
func (r *TodoRepo) FailTask(id int, reason string) error {
	manager := r.getManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Fail(id, reason)
}

// GetTodoString возвращает строковое представление плана.
//
// Thread-safe: делегирует в todo.Manager (thread-safe).
func (r *TodoRepo) GetTodoString() string {
	manager := r.getManager()
	if manager == nil {
		return ""
	}
	return manager.String()
}

// GetTodoStats возвращает статистику по задачам.
//
// Возвращает количество pending, done, failed задач.
// Thread-safe: делегирует в todo.Manager (thread-safe).
func (r *TodoRepo) GetTodoStats() (pending, done, failed int) {
	manager := r.getManager()
	if manager == nil {
		return 0, 0, 0
	}
	return manager.GetStats()
}

// GetTodoManager возвращает todo.Manager для использования в tools.
//
// ВНИМАНИЕ: Это метод для backward compatibility с tools которые
// требуют *todo.Manager. Предпочтительно использовать TodoRepository методы.
//
// Thread-safe: делегирует в Store.Get.
func (r *TodoRepo) GetTodoManager() *todo.Manager {
	return r.getManager()
}

// SetTodoManager устанавливает todo manager.
//
// Thread-safe: делегирует в Store.Set.
func (r *TodoRepo) SetTodoManager(manager *todo.Manager) error {
	return r.store.Set(KeyTodo, manager)
}

// getManager — вспомогательный метод для получения todo.Manager.
//
// Возвращает nil если менеджер не инициализирован.
func (r *TodoRepo) getManager() *todo.Manager {
	val, ok := r.store.Get(KeyTodo)
	if !ok {
		return nil
	}

	manager, ok := val.(*todo.Manager)
	if !ok {
		return nil
	}
	return manager
}
