// Package todo реализует потокобезопасный менеджер задач для AI-агента.
//
// Предоставляет инструменты для создания, отслеживания и управления задачами
// в рамках агentic workflow (ReAct pattern).
package todo

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TaskStatus представляет статус задачи в плане.
type TaskStatus string

const (
	StatusPending TaskStatus = "PENDING"
	StatusDone    TaskStatus = "DONE"
	StatusFailed  TaskStatus = "FAILED"
)

// Task представляет задачу в плане действий агента.
type Task struct {
	ID          int                    `json:"id"`
	Description string                 `json:"description"`
	Status      TaskStatus             `json:"status"`
	CreatedAt   time.Time              `json:"created_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Manager — потокобезопасное хранилище задач для агента.
//
// Используется для управления планом действий в ReAct цикле.
// Все методы thread-safe.
type Manager struct {
	mu     sync.RWMutex
	tasks  []Task
	nextID int
}

// NewManager создает новый пустой менеджер задач.
func NewManager() *Manager {
	return &Manager{
		tasks:  make([]Task, 0),
		nextID: 1,
	}
}

// Add добавляет новую задачу в план и возвращает её ID.
//
// Принимает опциональные метаданные для хранения дополнительной информации.
// Thread-safe метод.
func (m *Manager) Add(description string, metadata ...map[string]interface{}) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	var meta map[string]interface{}
	if len(metadata) > 0 {
		meta = metadata[0]
	}

	task := Task{
		ID:          m.nextID,
		Description: description,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		Metadata:    meta,
	}

	m.tasks = append(m.tasks, task)
	m.nextID++
	return task.ID
}

// Complete отмечает задачу как выполненную.
//
// Возвращает ошибку если задача не найдена или уже имеет статус DONE/FAILED.
// Thread-safe метод.
func (m *Manager) Complete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.tasks {
		if m.tasks[i].ID == id {
			if m.tasks[i].Status != StatusPending {
				return fmt.Errorf("задача %d уже выполнена или провалена", id)
			}
			m.tasks[i].Status = StatusDone
			now := time.Now()
			m.tasks[i].CompletedAt = &now
			return nil
		}
	}
	return fmt.Errorf("задача %d не найдена", id)
}

// Fail отмечает задачу как проваленную с указанием причины.
//
// Возвращает ошибку если задача не найдена или уже имеет статус DONE/FAILED.
// Причина провала сохраняется в Metadata["error"].
// Thread-safe метод.
func (m *Manager) Fail(id int, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.tasks {
		if m.tasks[i].ID == id {
			if m.tasks[i].Status != StatusPending {
				return fmt.Errorf("задача %d уже выполнена или провалена", id)
			}
			m.tasks[i].Status = StatusFailed
			if m.tasks[i].Metadata == nil {
				m.tasks[i].Metadata = make(map[string]interface{})
			}
			m.tasks[i].Metadata["error"] = reason
			return nil
		}
	}
	return fmt.Errorf("задача %d не найдена", id)
}

// Clear удаляет все задачи из плана и сбрасывает счётчик ID.
//
// Thread-safe метод.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = make([]Task, 0)
	m.nextID = 1
}

// String форматирует план задач для вставки в промпт (Context Injection).
//
// Возвращает текстовое представление плана с визуальными индикаторами статуса:
//   - [ ] для PENDING
//   - [✓] для DONE
//   - [✗] для FAILED
//
// Используется автоматически в BuildAgentContext для передачи контекста в LLM.
// Thread-safe метод.
func (m *Manager) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.tasks) == 0 {
		return "Нет активных задач"
	}

	var result strings.Builder
	result.WriteString("ТЕКУЩИЙ ПЛАН:\n")

	pending := 0
	done := 0
	failed := 0

	for _, task := range m.tasks {
		status := "[ ]"
		switch task.Status {
		case StatusDone:
			status = "[✓]"
			done++
		case StatusFailed:
			status = "[✗]"
			failed++
		default:
			pending++
		}

		result.WriteString(fmt.Sprintf("%s %d. %s\n", status, task.ID, task.Description))

		if task.Status == StatusFailed && task.Metadata != nil {
			if err, ok := task.Metadata["error"].(string); ok {
				result.WriteString(fmt.Sprintf("    Ошибка: %s\n", err))
			}
		}
	}

	result.WriteString(fmt.Sprintf("\nСтатистика: %d выполнено, %d в работе, %d провалено",
		done, pending, failed))

	return result.String()
}

// GetTasks возвращает копию списка всех задач для использования в UI.
//
// Возвращает копию слайса, чтобы избежать race conditions при итерации.
// Thread-safe метод.
func (m *Manager) GetTasks() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]Task, len(m.tasks))
	copy(tasks, m.tasks)
	return tasks
}

// GetStats возвращает статистику по задачам.
//
// Возвращает кортеж (pending, done, failed) с количеством задач в каждом статусе.
// Thread-safe метод.
func (m *Manager) GetStats() (pending, done, failed int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, task := range m.tasks {
		switch task.Status {
		case StatusDone:
			done++
		case StatusFailed:
			failed++
		default:
			pending++
		}
	}
	return
}
