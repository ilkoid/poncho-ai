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
	t.manager.Clear()
	return "🗑️ План действий очищен", nil
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
