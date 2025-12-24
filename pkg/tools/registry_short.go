//go:build short

// Реестр для хранения и поиска инструментов.
package tools

import (
	"fmt"
	"sync"
)

// Registry — потокобезопасное хранилище инструментов.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry создает новый пустой реестр.
func NewRegistry() *Registry {
	// TODO: Initialize registry with empty tools map
	return nil
}

// Register добавляет инструмент в реестр.
func (r *Registry) Register(tool Tool) {
	// TODO: Acquire write lock
	// TODO: Store tool by its definition name in the map
}

// Get ищет инструмент по имени.
func (r *Registry) Get(name string) (Tool, error) {
	// TODO: Acquire read lock
	// TODO: Look up tool by name
	// TODO: Return tool or error if not found
	return nil, nil
}

// GetDefinitions возвращает список всех определений для отправки в LLM.
func (r *Registry) GetDefinitions() []ToolDefinition {
	// TODO: Acquire read lock
	// TODO: Create slice with capacity for all tools
	// TODO: Collect definitions from all tools
	// TODO: Return definitions slice
	return nil
}