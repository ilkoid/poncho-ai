//go:build short

package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

type PlannerTool struct {
	manager *todo.Manager
}

func NewPlannerTool(manager *todo.Manager) *PlannerTool {
	return &PlannerTool{manager: manager}
}

// --- Tool: plan_add_task ---
// Позволяет агенту добавлять новые задачи в план действий

type PlanAddTaskTool struct {
	manager *todo.Manager
}

func NewPlanAddTaskTool(manager *todo.Manager) *PlanAddTaskTool {
	return &PlanAddTaskTool{manager: manager}
}

func (t *PlanAddTaskTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_add_task",
		Description: "Добавляет новую задачу в план действий",
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

func (t *PlanAddTaskTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Parse JSON arguments for description and metadata
	// TODO: Validate that description is not empty
	// TODO: Add task to manager with provided description and metadata
	// TODO: Return success message with task ID
	return "", nil
}

// --- Tool: plan_mark_done ---
// Позволяет агенту отмечать задачи как выполненные

type PlanMarkDoneTool struct {
	manager *todo.Manager
}

func NewPlanMarkDoneTool(manager *todo.Manager) *PlanMarkDoneTool {
	return &PlanMarkDoneTool{manager: manager}
}

func (t *PlanMarkDoneTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_done",
		Description: "Отмечает задачу как выполненную",
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

func (t *PlanMarkDoneTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Parse JSON arguments for task_id
	// TODO: Mark task as completed in manager
	// TODO: Return success message with task ID
	return "", nil
}

// --- Tool: plan_mark_failed ---
// Позволяет агенту отмечать задачи как проваленные

type PlanMarkFailedTool struct {
	manager *todo.Manager
}

func NewPlanMarkFailedTool(manager *todo.Manager) *PlanMarkFailedTool {
	return &PlanMarkFailedTool{manager: manager}
}

func (t *PlanMarkFailedTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_failed",
		Description: "Отмечает задачу как проваленную с указанием причины",
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

func (t *PlanMarkFailedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Parse JSON arguments for task_id and reason
	// TODO: Mark task as failed in manager with reason
	// TODO: Return failure message with task ID and reason
	return "", nil
}

// --- Tool: plan_clear ---
// Позволяет агенту очищать весь план действий

type PlanClearTool struct {
	manager *todo.Manager
}

func NewPlanClearTool(manager *todo.Manager) *PlanClearTool {
	return &PlanClearTool{manager: manager}
}

func (t *PlanClearTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_clear",
		Description: "Очищает весь план действий",
		Parameters:  map[string]interface{}{"type": "object"},
	}
}

func (t *PlanClearTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Clear all tasks in manager
	// TODO: Return success message
	return "", nil
}

// Вспомогательные функции для создания всех инструментов планировщика
func NewPlannerTools(manager *todo.Manager) map[string]tools.Tool {
	return map[string]tools.Tool{
		"plan_add_task":    NewPlanAddTaskTool(manager),
		"plan_mark_done":   NewPlanMarkDoneTool(manager),
		"plan_mark_failed": NewPlanMarkFailedTool(manager),
		"plan_clear":       NewPlanClearTool(manager),
	}
}