// Package std содержит стандартные инструменты для работы с Wildberries.
//
// Реализует инструменты для получения каталога категорий и предметов.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbParentCategoriesTool — инструмент для получения родительских категорий Wildberries.
//
// Позволяет агенту получить список верхнеуровневых категорий (Женщинам, Мужчинам, и т.д.).
type WbParentCategoriesTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string // Описание из YAML конфигурации
}

// NewWbParentCategoriesTool создает инструмент для получения родительских категорий.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbParentCategoriesTool(c *wb.Client, cfg config.ToolConfig) *WbParentCategoriesTool {
	// Дефолтные значения если не указаны в конфиге
	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 100 // дефолт
	}
	burst := cfg.Burst
	if burst == 0 {
		burst = 5 // дефолт
	}

	return &WbParentCategoriesTool{
		client:      c,
		toolID:      "get_wb_parent_categories",
		endpoint:    cfg.Endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbParentCategoriesTool) Definition() tools.ToolDefinition {
	desc := t.description
	if desc == "" {
		desc = "Возвращает список родительских категорий Wildberries (например: Женщинам, Электроника). Используй это, чтобы найти ID категории."
	}
	return tools.ToolDefinition{
		Name:        "get_wb_parent_categories",
		Description: desc,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Аргументы не нужны, но JSON может быть "{}"
	cats, err := t.client.GetParentCategories(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get parent categories: %w", err)
	}

	// Сериализуем результат
	data, err := json.Marshal(cats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbSubjectsTool — инструмент для получения предметов (подкатегорий) Wildberries.
//
// Позволяет агенту получить список подкатегорий для заданной родительской категории.
type WbSubjectsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSubjectsTool создает инструмент для получения предметов (подкатегорий).
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSubjectsTool(c *wb.Client, cfg config.ToolConfig) *WbSubjectsTool {
	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 100
	}
	burst := cfg.Burst
	if burst == 0 {
		burst = 5
	}

	return &WbSubjectsTool{
		client:      c,
		toolID:      "get_wb_subjects",
		endpoint:    cfg.Endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSubjectsTool) Definition() tools.ToolDefinition {
	desc := t.description
	if desc == "" {
		desc = "Возвращает список предметов (подкатегорий) для заданной родительской категории."
	}
	return tools.ToolDefinition{
		Name:        "get_wb_subjects",
		Description: desc,
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

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
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
		return "", fmt.Errorf("failed to get subjects: %w", err)
	}

	data, err := json.Marshal(subjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbPingTool — инструмент для проверки доступности Wildberries API.
//
// Позволяет агенту проверить, доступен ли WB Content API и валиден ли API ключ.
// Возвращает детальную диагностику: статус сервиса, timestamp, тип ошибки.
type WbPingTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbPingTool создает инструмент для проверки доступности WB API.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbPingTool(c *wb.Client, cfg config.ToolConfig) *WbPingTool {
	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 100
	}
	burst := cfg.Burst
	if burst == 0 {
		burst = 5
	}

	return &WbPingTool{
		client:      c,
		toolID:      "ping_wb_api",
		endpoint:    cfg.Endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbPingTool) Definition() tools.ToolDefinition {
	desc := t.description
	if desc == "" {
		desc = "Проверяет доступность Wildberries Content API. Возвращает статус сервиса, timestamp и информацию об ошибках (например, неверный API ключ, недоступность сети). Используй для диагностики перед другими операциями с WB."
	}
	return tools.ToolDefinition{
		Name:        "ping_wb_api",
		Description: desc,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbPingTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Вызываем Ping метод клиента
	resp, err := t.client.Ping(ctx)

	// Формируем развернутый ответ для LLM
	result := map[string]interface{}{
		"available": err == nil,
	}

	if err != nil {
		// Используем ClassifyError из wb.Client для определения типа ошибки
		errType := t.client.ClassifyError(err)
		result["error"] = err.Error()
		result["error_type"] = errType.String()
		result["message"] = errType.HumanMessage()
	} else {
		// Успешный ответ
		result["status"] = resp.Status
		result["timestamp"] = resp.TS
		result["message"] = "Wildberries Content API доступен и работает корректно."
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
