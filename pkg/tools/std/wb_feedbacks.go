// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// === WB Feedbacks API Tools ===
// TODO: Реализовать инструменты для работы с Feedbacks API
// https://feedbacks-api.wildberries.ru

// WbFeedbacksTool — заглушка для получения отзывов о товарах.
type WbFeedbacksTool struct {
	client *wb.Client
}

// NewWbFeedbacksTool создает заглушку для инструмента получения отзывов.
func NewWbFeedbacksTool(client *wb.Client, cfg config.ToolConfig) *WbFeedbacksTool {
	return &WbFeedbacksTool{client: client}
}

func (t *WbFeedbacksTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_feedbacks",
		Description: "Возвращает отзывы на товары Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID). [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID)",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество отзывов (пагинация)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *WbFeedbacksTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int  `json:"nmID"`
		IsAnswered *bool `json:"isAnswered"`
		Take       int  `json:"take"`
		Skip       int  `json:"skip"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_feedbacks tool is not implemented yet",
		"args":    args,
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbQuestionsTool — заглушка для получения вопросов о товарах.
type WbQuestionsTool struct {
	client *wb.Client
}

// NewWbQuestionsTool создает заглушку для инструмента получения вопросов.
func NewWbQuestionsTool(client *wb.Client, cfg config.ToolConfig) *WbQuestionsTool {
	return &WbQuestionsTool{client: client}
}

func (t *WbQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_questions",
		Description: "Возвращает вопросы о товарах Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID). [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID)",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество вопросов (пагинация)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *WbQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int  `json:"nmID"`
		IsAnswered *bool `json:"isAnswered"`
		Take       int  `json:"take"`
		Skip       int  `json:"skip"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_questions tool is not implemented yet",
		"args":    args,
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbNewFeedbacksQuestionsTool — заглушка для проверки новых отзывов/вопросов.
type WbNewFeedbacksQuestionsTool struct {
	client *wb.Client
}

// NewWbNewFeedbacksQuestionsTool создает заглушку для инструмента проверки новых отзывов/вопросов.
func NewWbNewFeedbacksQuestionsTool(client *wb.Client, cfg config.ToolConfig) *WbNewFeedbacksQuestionsTool {
	return &WbNewFeedbacksQuestionsTool{client: client}
}

func (t *WbNewFeedbacksQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_new_feedbacks_questions",
		Description: "Проверяет наличие новых отзывов и вопросов на Wildberries. Возвращает количество непрочитанных отзывов и вопросов. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbNewFeedbacksQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_new_feedbacks_questions tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbUnansweredFeedbacksCountsTool — заглушка для получения количества неотвеченных отзывов.
type WbUnansweredFeedbacksCountsTool struct {
	client *wb.Client
}

// NewWbUnansweredFeedbacksCountsTool создает заглушку для инструмента получения неотвеченных отзывов.
func NewWbUnansweredFeedbacksCountsTool(client *wb.Client, cfg config.ToolConfig) *WbUnansweredFeedbacksCountsTool {
	return &WbUnansweredFeedbacksCountsTool{client: client}
}

func (t *WbUnansweredFeedbacksCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_feedbacks_counts",
		Description: "Возвращает количество неотвеченных отзывов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredFeedbacksCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_unanswered_feedbacks_counts tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbUnansweredQuestionsCountsTool — заглушка для получения количества неотвеченных вопросов.
type WbUnansweredQuestionsCountsTool struct {
	client *wb.Client
}

// NewWbUnansweredQuestionsCountsTool создает заглушку для инструмента получения неотвеченных вопросов.
func NewWbUnansweredQuestionsCountsTool(client *wb.Client, cfg config.ToolConfig) *WbUnansweredQuestionsCountsTool {
	return &WbUnansweredQuestionsCountsTool{client: client}
}

func (t *WbUnansweredQuestionsCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_questions_counts",
		Description: "Возвращает количество неотвеченных вопросов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredQuestionsCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_unanswered_questions_counts tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}
