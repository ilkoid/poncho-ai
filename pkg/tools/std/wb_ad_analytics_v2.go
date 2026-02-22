// Package std provides V2 Wildberries advertising analytics tools using the service layer.
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

// WbCampaignFullstats2Tool — V2 инструмент для детальной статистики кампаний.
//
// Использует AdvertisingService вместо прямого вызова Client.
// Преимущества V2:
//   - Валидация в service layer
//   - Унифицированный mock режим
//   - Подготовка для кэширования
type WbCampaignFullstats2Tool struct {
	service     wb.AdvertisingService
	toolID      string
	description string
}

// NewWbCampaignFullstats2Tool создает V2 инструмент для статистики кампаний.
//
// Параметры:
//   - service: AdvertisingService для бизнес-логики
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbCampaignFullstats2Tool(service wb.AdvertisingService, cfg config.ToolConfig) *WbCampaignFullstats2Tool {
	return &WbCampaignFullstats2Tool{
		service:     service,
		toolID:      "get_wb_campaign_fullstats2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbCampaignFullstats2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_campaign_fullstats2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний (максимум 50)",
				},
				"beginDate": map[string]interface{}{
					"type":        "string",
					"description": "Дата начала интервала (YYYY-MM-DD)",
				},
				"endDate": map[string]interface{}{
					"type":        "string",
					"description": "Дата окончания интервала (YYYY-MM-DD)",
				},
			},
			"required": []string{"ids", "beginDate", "endDate"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbCampaignFullstats2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		IDs       []int  `json:"ids"`
		BeginDate string `json:"beginDate"`
		EndDate   string `json:"endDate"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer (валидация внутри сервиса)
	response, err := t.service.GetCampaignFullstats(ctx, wb.CampaignFullstatsRequest{
		IDs:       args.IDs,
		BeginDate: args.BeginDate,
		EndDate:   args.EndDate,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get campaign fullstats: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbAttributionSummary2Tool — V2 инструмент для атрибуции органика vs реклама.
//
// Использует AttributionService для агрегации данных из funnel и campaigns.
type WbAttributionSummary2Tool struct {
	service     wb.AttributionService
	toolID      string
	description string
}

// NewWbAttributionSummary2Tool создает V2 инструмент для атрибуции.
func NewWbAttributionSummary2Tool(service wb.AttributionService, cfg config.ToolConfig) *WbAttributionSummary2Tool {
	return &WbAttributionSummary2Tool{
		service:     service,
		toolID:      "get_wb_attribution_summary2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbAttributionSummary2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_attribution_summary2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"advertIds": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний для атрибуции",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"nmIDs", "advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbAttributionSummary2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs     []int `json:"nmIDs"`
		AdvertIds []int `json:"advertIds"`
		Days      int   `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer
	summary, err := t.service.GetAttributionSummary(ctx, wb.AttributionRequest{
		NmIDs:     args.NmIDs,
		AdvertIDs: args.AdvertIds,
		Period:    args.Days,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get attribution summary: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbCampaignStats2Tool — V2 инструмент для статистики кампаний (v2 API).
type WbCampaignStats2Tool struct {
	service     wb.AdvertisingService
	toolID      string
	description string
}

// NewWbCampaignStats2Tool создает V2 инструмент для статистики кампаний.
func NewWbCampaignStats2Tool(service wb.AdvertisingService, cfg config.ToolConfig) *WbCampaignStats2Tool {
	return &WbCampaignStats2Tool{
		service:     service,
		toolID:      "get_wb_campaign_stats2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbCampaignStats2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_campaign_stats2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"advertIds": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbCampaignStats2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AdvertIds []int `json:"advertIds"`
		Days      int   `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Calculate dates
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Вызов service layer
	stats, err := t.service.GetCampaignStats(ctx, args.AdvertIds, begin.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		return "", fmt.Errorf("failed to get campaign stats: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(stats)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbFeedbacks2Tool — V2 инструмент для получения отзывов.
type WbFeedbacks2Tool struct {
	service     wb.FeedbackService
	toolID      string
	description string
}

// NewWbFeedbacks2Tool создает V2 инструмент для отзывов.
func NewWbFeedbacks2Tool(service wb.FeedbackService, cfg config.ToolConfig) *WbFeedbacks2Tool {
	return &WbFeedbacks2Tool{
		service:     service,
		toolID:      "get_wb_feedbacks2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbFeedbacks2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_feedbacks2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество отзывов (макс. 100)",
				},
				"noffset": map[string]interface{}{
					"type":        "integer",
					"description": "Смещение для пагинации",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (опционально)",
				},
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Фильтр по товару (опционально)",
				},
			},
			"required": []string{},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbFeedbacks2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Take       int   `json:"take"`
		Noffset    int   `json:"noffset"`
		IsAnswered *bool `json:"isAnswered"`
		NmID       int   `json:"nmID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer
	response, err := t.service.GetFeedbacks(ctx, wb.FeedbacksRequest{
		Take:       args.Take,
		Noffset:    args.Noffset,
		IsAnswered: args.IsAnswered,
		NmID:       args.NmID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get feedbacks: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// WbQuestions2Tool — V2 инструмент для получения вопросов.
type WbQuestions2Tool struct {
	service     wb.FeedbackService
	toolID      string
	description string
}

// NewWbQuestions2Tool создает V2 инструмент для вопросов.
func NewWbQuestions2Tool(service wb.FeedbackService, cfg config.ToolConfig) *WbQuestions2Tool {
	return &WbQuestions2Tool{
		service:     service,
		toolID:      "get_wb_questions2",
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbQuestions2Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_questions2",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество вопросов (макс. 100)",
				},
				"noffset": map[string]interface{}{
					"type":        "integer",
					"description": "Смещение для пагинации",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (опционально)",
				},
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Фильтр по товару (опционально)",
				},
			},
			"required": []string{},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbQuestions2Tool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Take       int   `json:"take"`
		Noffset    int   `json:"noffset"`
		IsAnswered *bool `json:"isAnswered"`
		NmID       int   `json:"nmID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Вызов service layer
	response, err := t.service.GetQuestions(ctx, wb.QuestionsRequest{
		Take:       args.Take,
		Noffset:    args.Noffset,
		IsAnswered: args.IsAnswered,
		NmID:       args.NmID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get questions: %w", err)
	}

	// Форматируем ответ для LLM
	result, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}
