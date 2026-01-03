// Package std содержит стандартные инструменты Poncho AI.
//
// Реализует инструменты управления планом действий (planner).
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// PlannerTool — базовый тип для инструментов планировщика (не используется напрямую).
//
// Реальные инструменты реализованы как отдельные типы для каждого действия.
type PlannerTool struct {
	manager *todo.Manager
}

// NewPlannerTool создает базовый инструмент планировщика.
//
// Примечание: на практике используются конкретные инструменты (PlanAddTaskTool и т.д.).
func NewPlannerTool(manager *todo.Manager, cfg config.ToolConfig) *PlannerTool {
	return &PlannerTool{manager: manager}
}

// PlanAddTaskTool — инструмент для добавления задач в план действий.
//
// Позволяет агенту создавать новые задачи в Todo Manager.
type PlanAddTaskTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanAddTaskTool создает инструмент для добавления задач.
func NewPlanAddTaskTool(manager *todo.Manager, cfg config.ToolConfig) *PlanAddTaskTool {
	return &PlanAddTaskTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
//
// Соответствует Tool interface (Rule 1).
func (t *PlanAddTaskTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_add_task",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Описание задачи для выполнения",
				},
				"metadata": map[string]interface{}{
					"type":        "object",
					"description": "Дополнительные метаданные (опционально)",
				},
			},
			"required": []string{"description"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
//
// Принимает JSON строку с аргументами от LLM, возвращает результат выполнения.
// Соответствует Tool interface (Rule 1).
func (t *PlanAddTaskTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Description string                 `json:"description"`
		Metadata    map[string]interface{} `json:"metadata,omitempty"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if args.Description == "" {
		return "", fmt.Errorf("описание задачи не может быть пустым")
	}

	id := t.manager.Add(args.Description, args.Metadata)
	return fmt.Sprintf("✅ Задача добавлена в план (ID: %d): %s", id, args.Description), nil
}

// PlanMarkDoneTool — инструмент для отметки задач как выполненных.
//
// Позволяет агенту отмечать завершенные задачи в Todo Manager.
type PlanMarkDoneTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanMarkDoneTool создает инструмент для отметки задач как выполненных.
func NewPlanMarkDoneTool(manager *todo.Manager, cfg config.ToolConfig) *PlanMarkDoneTool {
	return &PlanMarkDoneTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanMarkDoneTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_done",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "ID задачи для отметки выполнения",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanMarkDoneTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		TaskID int `json:"task_id"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if err := t.manager.Complete(args.TaskID); err != nil {
		return "", fmt.Errorf("ошибка отметки задачи: %w", err)
	}

	return fmt.Sprintf("✅ Задача %d отмечена как выполненная", args.TaskID), nil
}

// PlanMarkFailedTool — инструмент для отметки задач как проваленных.
//
// Позволяет агенту отмечать задачи с указанием причины провала.
type PlanMarkFailedTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanMarkFailedTool создает инструмент для отметки задач как проваленных.
func NewPlanMarkFailedTool(manager *todo.Manager, cfg config.ToolConfig) *PlanMarkFailedTool {
	return &PlanMarkFailedTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanMarkFailedTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_failed",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "ID задачи для отметки провала",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Причина провала задачи",
				},
			},
			"required": []string{"task_id", "reason"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanMarkFailedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		TaskID int    `json:"task_id"`
		Reason string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if err := t.manager.Fail(args.TaskID, args.Reason); err != nil {
		return "", fmt.Errorf("ошибка отметки задачи: %w", err)
	}

	return fmt.Sprintf("❌ Задача %d отмечена как проваленная: %s", args.TaskID, args.Reason), nil
}

// PlanClearTool — инструмент для очистки всего плана действий.
//
// Позволяет агенту удалять все задачи из Todo Manager.
type PlanClearTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanClearTool создает инструмент для очистки плана.
func NewPlanClearTool(manager *todo.Manager, cfg config.ToolConfig) *PlanClearTool {
	return &PlanClearTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanClearTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_clear",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanClearTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	t.manager.Clear()
	return "🗑️ План действий очищен", nil
}

// NewPlannerTools создает карту всех инструментов планировщика.
//
// Удобная функция для массовой регистрации инструментов planner'а.
// Возвращает map[string]tools.Tool, которую можно использовать
// для непосредственной регистрации в Registry.
//
// Параметры:
//   - manager: Todo Manager для управления задачами
//   - cfg: Конфигурация tools (используется для единообразия с другими tools)
func NewPlannerTools(manager *todo.Manager, cfg config.ToolConfig) map[string]tools.Tool {
	return map[string]tools.Tool{
		"plan_add_task":    NewPlanAddTaskTool(manager, cfg),
		"plan_mark_done":   NewPlanMarkDoneTool(manager, cfg),
		"plan_mark_failed": NewPlanMarkFailedTool(manager, cfg),
		"plan_clear":       NewPlanClearTool(manager, cfg),
	}
}
