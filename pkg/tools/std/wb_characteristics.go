// Package std содержит инструменты для работы с характеристиками и справочниками Wildberries.
//
// Реализует инструменты для получения характеристик предметов, кодов ТНВЭД и брендов.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbSubjectsByNameTool — инструмент для поиска предметов по подстроке.
//
// Позволяет агенту найти предмет по названию (без учета регистра).
type WbSubjectsByNameTool struct {
	client *wb.Client
}

// NewWbSubjectsByNameTool создает инструмент для поиска предметов по имени.
func NewWbSubjectsByNameTool(c *wb.Client) *WbSubjectsByNameTool {
	return &WbSubjectsByNameTool{client: c}
}

func (t *WbSubjectsByNameTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects_by_name",
		Description: "Ищет предметы (подкатегории) Wildberries по подстроке в названии. Возвращает список подходящих предметов с их ID и родительскими категориями.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Подстрока для поиска (например, 'платье', 'кроссовки'). Поиск работает по подстроке и без учета регистра.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Максимальное количество результатов (по умолчанию 50, максимум 1000).",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *WbSubjectsByNameTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Дефолтный лимит
	if args.Limit <= 0 {
		args.Limit = 50
	}
	if args.Limit > 1000 {
		args.Limit = 1000
	}

	// Используем существующий метод с пагинацией
	// GetAllSubjectsLazy возвращает все предметы, поэтому фильтруем в Go
	allSubjects, err := t.client.GetAllSubjectsLazy(ctx, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get subjects: %w", err)
	}

	// Фильтруем по имени (case-insensitive substring)
	var matches []wb.Subject

	for _, s := range allSubjects {
		// Простая проверка на вхождение (можно улучшить fuzzy search)
		if containsIgnoreCase(s.SubjectName, args.Name) {
			matches = append(matches, s)
			if len(matches) >= args.Limit {
				break
			}
		}
	}

	data, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbCharacteristicsTool — инструмент для получения характеристик предмета.
//
// Возвращает обязательные и опциональные характеристики для указанного предмета.
type WbCharacteristicsTool struct {
	client *wb.Client
}

// NewWbCharacteristicsTool создает инструмент для получения характеристик.
func NewWbCharacteristicsTool(c *wb.Client) *WbCharacteristicsTool {
	return &WbCharacteristicsTool{client: c}
}

func (t *WbCharacteristicsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_characteristics",
		Description: "Возвращает список характеристик для указанного предмета (subjectID). Включает информацию о том, является ли характеристика обязательной, тип данных, единицы измерения.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbCharacteristicsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int `json:"subjectID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	charcs, err := t.client.GetCharacteristics(ctx, args.SubjectID)
	if err != nil {
		return "", fmt.Errorf("failed to get characteristics: %w", err)
	}

	data, err := json.Marshal(charcs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbTnvedTool — инструмент для получения кодов ТНВЭД.
//
// Возвращает список кодов ТНВЭД для указанного предмета с информацией о маркировке.
type WbTnvedTool struct {
	client *wb.Client
}

// NewWbTnvedTool создает инструмент для получения кодов ТНВЭД.
func NewWbTnvedTool(c *wb.Client) *WbTnvedTool {
	return &WbTnvedTool{client: c}
}

func (t *WbTnvedTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_tnved",
		Description: "Возвращает список кодов ТНВЭД для указанного предмета. Коды ТНВЭД нужны для создания карточки товара и маркировки Честный ЗНАК (isKiz=true).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
				"search": map[string]interface{}{
					"type":        "string",
					"description": "Опциональный поиск по коду ТНВЭД (например, '6106903000').",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbTnvedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int    `json:"subjectID"`
		Search    string `json:"search"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	tnveds, err := t.client.GetTnved(ctx, args.SubjectID, args.Search)
	if err != nil {
		return "", fmt.Errorf("failed to get tnved: %w", err)
	}

	data, err := json.Marshal(tnveds)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbBrandsTool — инструмент для получения списка брендов.
//
// Возвращает бренды для указанного предмета (с авто-пагинацией и лимитом из конфига).
type WbBrandsTool struct {
	client *wb.Client
	limit  int // Лимит из конфига
}

// NewWbBrandsTool создает инструмент для получения брендов.
func NewWbBrandsTool(c *wb.Client, brandsLimit int) *WbBrandsTool {
	return &WbBrandsTool{client: c, limit: brandsLimit}
}

func (t *WbBrandsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_brands",
		Description: "Возвращает список брендов для указанного предмета. Бренды отсортированы по популярности. Используй это для выбора бренда при создании карточки товара.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbBrandsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int `json:"subjectID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	brands, err := t.client.GetBrands(ctx, args.SubjectID, t.limit)
	if err != nil {
		return "", fmt.Errorf("failed to get brands: %w", err)
	}

	// Для экономии токенов возвращаем только ID и название
	type brandPreview struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	previews := make([]brandPreview, len(brands))
	for i, b := range brands {
		previews[i] = brandPreview{ID: b.ID, Name: b.Name}
	}

	data, err := json.Marshal(previews)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper функция для case-insensitive поиска (работает с Unicode)
func containsIgnoreCase(s, substr string) bool {
	// Используем strings.ToLower для корректной работы с кириллицей
	return len(s) >= len(substr) &&
		(s == substr ||
		 len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	// Case-insensitive поиск с поддержкой Unicode
	sLower := strings.ToLower(s)
	substrLower := strings.ToLower(substr)
	return strings.Contains(sLower, substrLower)
}
