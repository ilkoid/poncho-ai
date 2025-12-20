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
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register добавляет инструмент в реестр.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Definition().Name] = tool
}

// Get ищет инструмент по имени.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool '%s' not found", name)
	}
	return tool, nil
}

// GetDefinitions возвращает список всех определений для отправки в LLM.
func (r *Registry) GetDefinitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}
