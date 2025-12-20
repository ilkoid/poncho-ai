// Реализация конкретных инструментов для WB (Subjects, Categories).
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// --- Tool: get_wb_parent_categories ---

type WbParentCategoriesTool struct {
	client *wb.Client
}

func NewWbParentCategoriesTool(c *wb.Client) *WbParentCategoriesTool {
	return &WbParentCategoriesTool{client: c}
}

func (t *WbParentCategoriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_parent_categories",
		Description: "Возвращает список родительских категорий Wildberries (например: Женщинам, Электроника). Используй это, чтобы найти ID категории.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{}, // Нет параметров
		},
	}
}

func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Аргументы не нужны, но JSON может быть "{}"
	cats, err := t.client.GetParentCategories(ctx)
	if err != nil {
		return "", fmt.Errorf("wb api error: %w", err)
	}
	
	// Сериализуем результат
	data, err := json.Marshal(cats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Tool: get_wb_subjects ---

type WbSubjectsTool struct {
	client *wb.Client
}

func NewWbSubjectsTool(c *wb.Client) *WbSubjectsTool {
	return &WbSubjectsTool{client: c}
}

func (t *WbSubjectsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects",
		Description: "Возвращает список предметов (подкатегорий) для заданной родительской категории.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"parentID": map[string]interface{}{
					"type":        "integer",
					"description": "ID родительской категории (получи его из get_wb_parent_categories)",
				},
			},
			"required": []string{"parentID"},
		},
	}
}

func (t *WbSubjectsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ParentID int `json:"parentID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments json: %w", err)
	}

	// Используем метод GetAllSubjects (с пагинацией), который мы делали ранее
	subjects, err := t.client.GetAllSubjectsLazy(ctx, args.ParentID)
	if err != nil {
		return "", fmt.Errorf("wb api error: %w", err)
	}

	data, err := json.Marshal(subjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

