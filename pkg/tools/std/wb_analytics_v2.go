// Package std provides V2 Wildberries analytics tools using the service layer.
//
// V2 tools use WbService instead of direct client calls, providing:
// - Better separation of concerns
// - Centralized validation
// - Mock support through service layer
// - Future caching capabilities
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbProductFunnel2Tool — V2 инструмент для получения воронки продаж.
//
// Использует SalesService вместо прямого вызова Client.
// Преимущества V2:
//   - Валидация в service layer
//   - Унифицированный mock режим
//   - Подготовка для кэширования
type WbProductFunnel2Tool struct {
	service     wb.SalesService
	toolID      string
	description string
}

// NewWbProductFunnel2Tool создает V2 инструмент для получения воронки продаж.
//
// Параметры:
//   - service: SalesService для бизнес-логики
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbProductFunnel2Tool(service wb.SalesService, cfg config.ToolConfig) *WbProductFunnel2Tool {
	return &WbProductFunnel2Tool{
		service:     service,
		toolID:      "get_wb_product_funnel2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnel2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-365)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnel2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int   `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer (валидация внутри сервиса)
	metrics, err := t.service.GetFunnelMetrics(ctx, wb.FunnelRequest{
		NmIDs:  args.NmIDs,
		Period: args.Days,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get funnel metrics: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(metrics)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbProductFunnelHistory2Tool — V2 инструмент для получения истории воронки по дням.
//
// Использует SalesService вместо прямого вызова Client.
type WbProductFunnelHistory2Tool struct {
	service     wb.SalesService
	toolID      string
	description string
}

// NewWbProductFunnelHistory2Tool создает V2 инструмент для получения истории воронки.
func NewWbProductFunnelHistory2Tool(service wb.SalesService, cfg config.ToolConfig) *WbProductFunnelHistory2Tool {
	return &WbProductFunnelHistory2Tool{
		service:     service,
		toolID:      "get_wb_product_funnel_history2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnelHistory2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel_history2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для истории (1-7 бесплатно, до 365 с подпиской Джем)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnelHistory2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int   `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Calculate dates
	now := time.Now()

	// Вызов service layer
	history, err := t.service.GetFunnelHistory(ctx, wb.FunnelHistoryRequest{
		NmIDs:    args.NmIDs,
		DateFrom: now.AddDate(0, 0, -args.Days),
		DateTo:   now,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get funnel history: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(history)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbSearchPositions2Tool — V2 инструмент для получения позиций в поиске.
//
// Использует SalesService вместо прямого вызова Client.
type WbSearchPositions2Tool struct {
	service     wb.SalesService
	toolID      string
	description string
}

// NewWbSearchPositions2Tool создает V2 инструмент для получения позиций в поиске.
func NewWbSearchPositions2Tool(service wb.SalesService, cfg config.ToolConfig) *WbSearchPositions2Tool {
	return &WbSearchPositions2Tool{
		service:     service,
		toolID:      "get_wb_search_positions2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSearchPositions2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_search_positions2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-365)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSearchPositions2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int   `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer
	result, err := t.service.GetSearchPositions(ctx, args.NmIDs, args.Days)
	if err != nil {
		return "", fmt.Errorf("failed to get search positions: %w", err)
	}

	return result, nil
}

// WbTopSearchQueries2Tool — V2 инструмент для получения топ поисковых запросов.
type WbTopSearchQueries2Tool struct {
	service     wb.SalesService
	toolID      string
	description string
}

// NewWbTopSearchQueries2Tool создает V2 инструмент для получения топ запросов.
func NewWbTopSearchQueries2Tool(service wb.SalesService, cfg config.ToolConfig) *WbTopSearchQueries2Tool {
	return &WbTopSearchQueries2Tool{
		service:     service,
		toolID:      "get_wb_top_search_queries2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbTopSearchQueries2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_top_search_queries2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "nmID товара",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-365)",
				},
			},
			"required": []string{"nmID", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbTopSearchQueries2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID int `json:"nmID"`
		Days int `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer
	result, err := t.service.GetTopSearchQueries(ctx, args.NmID, args.Days)
	if err != nil {
		return "", fmt.Errorf("failed to get top search queries: %w", err)
	}

	return result, nil
}
