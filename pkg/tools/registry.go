// Реестр для хранения и поиска инструментов.
package tools

import (
	"encoding/json"
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

// validateToolDefinition проверяет что ToolDefinition соответствует JSON Schema.
//
// Валидирует:
//   - Name не пустой
//   - Parameters является JSON объектом
//   - Parameters.type == "object"
//   - Parameters.required является массивом строк
func validateToolDefinition(def ToolDefinition) error {
	// 1. Проверяем имя
	if def.Name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	// 2. Проверяем что Parameters не nil
	if def.Parameters == nil {
		return fmt.Errorf("tool '%s': parameters cannot be nil", def.Name)
	}

	// 3. Сериализуем Parameters в JSON для проверки структуры
	paramsJSON, err := json.Marshal(def.Parameters)
	if err != nil {
		return fmt.Errorf("tool '%s': failed to marshal parameters: %w", def.Name, err)
	}

	// 4. Парсим как map[string]interface{}
	var params map[string]interface{}
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return fmt.Errorf("tool '%s': parameters must be a JSON object, got: %s", def.Name, string(paramsJSON))
	}

	// 5. Проверяем что type == "object"
	typeVal, ok := params["type"]
	if !ok {
		return fmt.Errorf("tool '%s': parameters must have 'type' field", def.Name)
	}

	typeStr, ok := typeVal.(string)
	if !ok {
		return fmt.Errorf("tool '%s': parameters.type must be a string, got: %T", def.Name, typeVal)
	}

	if typeStr != "object" {
		return fmt.Errorf("tool '%s': parameters.type must be 'object', got: '%s'", def.Name, typeStr)
	}

	// 6. Проверяем что 'required' (если есть) является массивом строк
	if requiredVal, exists := params["required"]; exists {
		required, ok := requiredVal.([]interface{})
		if !ok {
			// Попробуем распарсить как []interface{} из JSON массива
			return fmt.Errorf("tool '%s': parameters.required must be an array", def.Name)
		}

		// Проверяем что все элементы - строки
		for i, item := range required {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("tool '%s': parameters.required[%d] must be a string, got: %T", def.Name, i, item)
			}
		}
	}

	return nil
}

// Register добавляет инструмент в реестр с валидацией схемы.
//
// Возвращает ошибку если определение инструмента не валидно.
func (r *Registry) Register(tool Tool) error {
	def := tool.Definition()

	// Валидируем определение перед регистрацией
	if err := validateToolDefinition(def); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = tool
	return nil
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
