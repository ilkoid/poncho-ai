// Интерфейс Tool и структуры определений.

package tools

import "context"

// JSONSchema представляет JSON Schema для параметров инструмента.
//
// Используется вместо interface{} для типобезопасности.
// Формат соответствует JSON Schema specification для Function Calling API.
type JSONSchema map[string]any

// ToolDefinition описывает инструмент для LLM (Function Calling API format).
type ToolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  JSONSchema `json:"parameters"` // JSON Schema объекта аргументов
}

// Tool — контракт, который должен реализовать любой инструмент.
type Tool interface {
	// Definition возвращает описание инструмента для LLM.
	Definition() ToolDefinition

	// Execute выполняет логику инструмента.
	// argsJSON — это сырой JSON с аргументами, который прислала LLM.
	// Возвращает результат (обычно JSON) или ошибку.
	Execute(ctx context.Context, argsJSON string) (string, error)
}

