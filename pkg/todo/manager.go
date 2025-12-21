package todo

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type TaskStatus string

const (
	StatusPending TaskStatus = "PENDING"
	StatusDone    TaskStatus = "DONE"
	StatusFailed  TaskStatus = "FAILED"
)

type Task struct {
	ID          int                    `json:"id"`
	Description string                 `json:"description"`
	Status      TaskStatus             `json:"status"`
	CreatedAt   time.Time              `json:"created_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Manager - потокобезопасное хранилище задач
type Manager struct {
	mu     sync.RWMutex
	tasks  []Task
	nextID int
}

func NewManager() *Manager {
	return &Manager{
		tasks:  make([]Task, 0),
		nextID: 1,
	}
}

// Методы для Tools
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

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = make([]Task, 0)
	m.nextID = 1
}

// Метод для Context Injection - превращает лист в строку для промпта
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

// Методы для UI
func (m *Manager) GetTasks() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]Task, len(m.tasks))
	copy(tasks, m.tasks)
	return tasks
}

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
