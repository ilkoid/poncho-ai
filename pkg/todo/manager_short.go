//go:build short

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
	// TODO: Initialize manager with empty tasks slice and nextID set to 1
	return nil
}

// Методы для Tools
func (m *Manager) Add(description string, metadata ...map[string]interface{}) int {
	// TODO: Acquire write lock
	// TODO: Create new task with provided description and metadata
	// TODO: Add task to tasks slice and increment nextID
	// TODO: Return task ID
	return 0
}

func (m *Manager) Complete(id int) error {
	// TODO: Acquire write lock
	// TODO: Find task by ID
	// TODO: Check if task is pending, otherwise return error
	// TODO: Mark task as done with completion timestamp
	// TODO: Return nil or error if task not found
	return nil
}

func (m *Manager) Fail(id int, reason string) error {
	// TODO: Acquire write lock
	// TODO: Find task by ID
	// TODO: Check if task is pending, otherwise return error
	// TODO: Mark task as failed and store error reason in metadata
	// TODO: Return nil or error if task not found
	return nil
}

func (m *Manager) Clear() {
	// TODO: Acquire write lock
	// TODO: Reset tasks slice and nextID
}

// Метод для Context Injection - превращает лист в строку для промпта
func (m *Manager) String() string {
	// TODO: Acquire read lock
	// TODO: If no tasks, return "Нет активных задач"
	// TODO: Build string representation of tasks with status indicators
	// TODO: Include statistics (done, pending, failed counts)
	// TODO: Return formatted string
	return ""
}

// Методы для UI
func (m *Manager) GetTasks() []Task {
	// TODO: Acquire read lock
	// TODO: Create copy of tasks slice
	// TODO: Return copy
	return nil
}

func (m *Manager) GetStats() (pending, done, failed int) {
	// TODO: Acquire read lock
	// TODO: Count tasks by status
	// TODO: Return statistics
	return 0, 0, 0
}