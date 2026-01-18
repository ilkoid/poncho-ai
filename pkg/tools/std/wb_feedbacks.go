// Package std предоставляет инструменты для работы с Wildberries Feedbacks API.
//
// Реализует инструменты для получения отзывов и вопросов:
// - Список отзывов с пагинацией
// - Список вопросов с пагинацией
// - Проверка новых отзывов/вопросов
// - Количество неотвеченных отзывов
// - Количество неотвеченных вопросов
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// === WB Feedbacks API Tools ===
// Base URL: https://feedbacks-api.wildberries.ru
// Rate Limit: 60 запросов/минуту (1 запрос/сек)
// Burst: 1 (критично - при превышении 3 req/sec блокировка на 60 сек)

// FeedbacksAPIResponse — базовая обертка ответа Feedbacks API.
type FeedbacksAPIResponse[T any] struct {
	Data    T      `json:"data"`
	Error   bool   `json:"error"`
	ErrorText string `json:"errorText,omitempty"`
}

// WbFeedbacksTool — инструмент для получения отзывов о товарах.
//
// GET /api/v1/feedbacks
// Параметры: isAnswered (required), take (required), skip (required),
//            nmId (optional), order (optional), dateFrom/dateTo (optional)
type WbFeedbacksTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	path        string
	rateLimit   int
	burst       int
	description string
	defaultTake int
}

// NewWbFeedbacksTool создает инструмент для получения отзывов.
func NewWbFeedbacksTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbFeedbacksTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbFeedbacksTool{
		client:      c,
		toolID:      "get_wb_feedbacks",
		endpoint:    endpoint,
		path:        cfg.Path,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
		defaultTake: cfg.DefaultTake,
	}
}

func (t *WbFeedbacksTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_feedbacks",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID) для фильтрации",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество отзывов (макс. 5000, по умолчанию 100)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
				"order": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"dateAsc", "dateDesc"},
					"description": "Сортировка по дате (dateAsc/dateDesc)",
				},
			},
			"required": []string{"isAnswered", "take", "skip"},
		},
	}
}

func (t *WbFeedbacksTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int    `json:"nmID"`
		IsAnswered bool   `json:"isAnswered"`
		Take       int    `json:"take"`
		Skip       int    `json:"skip"`
		Order      string `json:"order"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if args.Take <= 0 {
		args.Take = t.defaultTake
	}
	if args.Take > 5000 {
		return "", fmt.Errorf("take cannot exceed 5000")
	}
	if args.Skip < 0 {
		return "", fmt.Errorf("skip must be non-negative")
	}
	if args.Skip > 199990 {
		return "", fmt.Errorf("skip cannot exceed 199990")
	}
	if args.Order != "" && args.Order != "dateAsc" && args.Order != "dateDesc" {
		return "", fmt.Errorf("order must be dateAsc or dateDesc")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmID, args.IsAnswered, args.Take, args.Skip)
	}

	// Build query params
	params := url.Values{}
	params.Set("isAnswered", strconv.FormatBool(args.IsAnswered))
	params.Set("take", strconv.Itoa(args.Take))
	params.Set("skip", strconv.Itoa(args.Skip))
	if args.NmID > 0 {
		params.Set("nmId", strconv.Itoa(args.NmID))
	}
	if args.Order != "" {
		params.Set("order", args.Order)
	}

	// API response structure
	var response FeedbacksAPIResponse[struct {
		CountUnanswered int `json:"countUnanswered"`
		CountArchive    int `json:"countArchive"`
		Feedbacks       []struct {
			ID               string `json:"id"`
			Text             string `json:"text"`
			Pros             string `json:"pros"`
			Cons             string `json:"cons"`
			ProductValuation int    `json:"productValuation"`
			CreatedDate      string `json:"createdDate"`
			Answer           *struct {
				Text      string `json:"text"`
				State     string `json:"state"`
		Editable  bool   `json:"editable"`
	} `json:"answer"`
	ProductDetails struct {
		NmID            int    `json:"nmID"`
		ProductName     string `json:"productName"`
		SupplierArticle string `json:"supplierArticle"`
		BrandName       string `json:"brandName"`
	} `json:"productDetails"`
	WasViewed bool   `json:"wasViewed"`
	UserName  string `json:"userName"`
} `json:"feedbacks"`
}]

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		t.path, params, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get feedbacks: %w", err)
	}

	// Возвращаем data.feedbacks для LLM
	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

func (t *WbFeedbacksTool) executeMock(nmID int, isAnswered bool, take, skip int) (string, error) {
	mockFeedbacks := make([]map[string]interface{}, 0, take)

	for i := 0; i < take && i < 10; i++ {
		mockFeedbacks = append(mockFeedbacks, map[string]interface{}{
			"id":               fmt.Sprintf("mock_feedback_%d", i+skip),
			"text":             "Отличный товар, рекомендую!",
			"productValuation": 5,
			"createdDate":      time.Now().AddDate(0, 0, -i).Format("2006-01-02T15:04:05Z"),
			"productDetails": map[string]interface{}{
				"nmID":        nmID,
				"productName": "Товар (mock)",
			},
			"wasViewed": true,
			"mock":      true,
		})
	}

	result := map[string]interface{}{
		"feedbacks":        mockFeedbacks,
		"countUnanswered":  5,
		"countArchive":     100,
		"mock":             true,
	}
	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// WbQuestionsTool — инструмент для получения вопросов о товарах.
//
// GET /api/v1/questions
// Параметры: isAnswered (required), take (required), skip (required),
//            nmId (optional), order (optional), dateFrom/dateTo (optional)
type WbQuestionsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	path        string
	rateLimit   int
	burst       int
	description string
	defaultTake int
}

// NewWbQuestionsTool создает инструмент для получения вопросов.
func NewWbQuestionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbQuestionsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbQuestionsTool{
		client:      c,
		toolID:      "get_wb_questions",
		endpoint:    endpoint,
		path:        cfg.Path,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
		defaultTake: cfg.DefaultTake,
	}
}

func (t *WbQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_questions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID) для фильтрации",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество вопросов (макс. 10000, по умолчанию 100)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
				"order": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"dateAsc", "dateDesc"},
					"description": "Сортировка по дате (dateAsc/dateDesc)",
				},
			},
			"required": []string{"isAnswered", "take", "skip"},
		},
	}
}

func (t *WbQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int    `json:"nmID"`
		IsAnswered bool   `json:"isAnswered"`
		Take       int    `json:"take"`
		Skip       int    `json:"skip"`
		Order      string `json:"order"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if args.Take <= 0 {
		args.Take = t.defaultTake
	}
	if args.Take > 10000 {
		return "", fmt.Errorf("take cannot exceed 10000")
	}
	if args.Skip < 0 {
		return "", fmt.Errorf("skip must be non-negative")
	}
	if args.Skip > 10000 {
		return "", fmt.Errorf("skip cannot exceed 10000")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmID, args.IsAnswered, args.Take, args.Skip)
	}

	// Build query params
	params := url.Values{}
	params.Set("isAnswered", strconv.FormatBool(args.IsAnswered))
	params.Set("take", strconv.Itoa(args.Take))
	params.Set("skip", strconv.Itoa(args.Skip))
	if args.NmID > 0 {
		params.Set("nmId", strconv.Itoa(args.NmID))
	}
	if args.Order != "" {
		params.Set("order", args.Order)
	}

	// API response structure
	var response FeedbacksAPIResponse[struct {
		CountUnanswered int `json:"countUnanswered"`
		CountArchive    int `json:"countArchive"`
		Questions       []struct {
			ID             string `json:"id"`
			Text           string `json:"text"`
			CreatedDate    string `json:"createdDate"`
			State          string `json:"state"`
			Answer         *struct {
				Text  string `json:"text"`
				State string `json:"state"`
			} `json:"answer"`
			ProductDetails struct {
				NmID            int    `json:"nmID"`
				ProductName     string `json:"productName"`
				SupplierArticle string `json:"supplierArticle"`
				BrandName       string `json:"brandName"`
			} `json:"productDetails"`
			WasViewed bool `json:"wasViewed"`
		} `json:"questions"`
	}]

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		t.path, params, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get questions: %w", err)
	}

	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

func (t *WbQuestionsTool) executeMock(nmID int, isAnswered bool, take, skip int) (string, error) {
	mockQuestions := make([]map[string]interface{}, 0, take)

	for i := 0; i < take && i < 10; i++ {
		mockQuestions = append(mockQuestions, map[string]interface{}{
			"id":          fmt.Sprintf("mock_question_%d", i+skip),
			"text":        "Здравствуйте, есть ли этот товар в наличии?",
			"createdDate": time.Now().AddDate(0, 0, -i).Format("2006-01-02T15:04:05Z"),
			"productDetails": map[string]interface{}{
				"nmID":        nmID,
				"productName": "Товар (mock)",
			},
			"wasViewed": false,
			"mock":      true,
		})
	}

	result := map[string]interface{}{
		"questions":        mockQuestions,
		"countUnanswered":  10,
		"countArchive":     200,
		"mock":             true,
	}
	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// WbNewFeedbacksQuestionsTool — инструмент для проверки новых отзывов и вопросов.
//
// GET /api/v1/new-feedbacks-questions
// Без параметров
type WbNewFeedbacksQuestionsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	path        string
	rateLimit   int
	burst       int
	description string
}

// NewWbNewFeedbacksQuestionsTool создает инструмент для проверки новых отзывов/вопросов.
func NewWbNewFeedbacksQuestionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbNewFeedbacksQuestionsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbNewFeedbacksQuestionsTool{
		client:      c,
		toolID:      "get_wb_new_feedbacks_questions",
		endpoint:    endpoint,
		path:        cfg.Path,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbNewFeedbacksQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_new_feedbacks_questions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbNewFeedbacksQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		mockResult := map[string]interface{}{
			"hasNewQuestions": true,
			"hasNewFeedbacks": false,
			"mock":           true,
		}
		result, _ := json.Marshal(mockResult)
		return string(result), nil
	}

	var response FeedbacksAPIResponse[struct {
		HasNewQuestions  bool `json:"hasNewQuestions"`
		HasNewFeedbacks  bool `json:"hasNewFeedbacks"`
	}]

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		t.path, nil, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get new feedbacks/questions: %w", err)
	}

	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

// WbUnansweredFeedbacksCountsTool — инструмент для получения количества неотвеченных отзывов.
//
// GET /api/v1/feedbacks/count-unanswered
// Без параметров
type WbUnansweredFeedbacksCountsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	path        string
	rateLimit   int
	burst       int
	description string
}

// NewWbUnansweredFeedbacksCountsTool создает инструмент для получения неотвеченных отзывов.
func NewWbUnansweredFeedbacksCountsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbUnansweredFeedbacksCountsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbUnansweredFeedbacksCountsTool{
		client:      c,
		toolID:      "get_wb_unanswered_feedbacks_counts",
		endpoint:    endpoint,
		path:        cfg.Path,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbUnansweredFeedbacksCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_feedbacks_counts",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredFeedbacksCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		mockResult := map[string]interface{}{
			"countUnanswered":     3,
			"countUnansweredToday": 0,
			"valuation":           "4.7",
			"mock":                true,
		}
		result, _ := json.Marshal(mockResult)
		return string(result), nil
	}

	var response FeedbacksAPIResponse[struct {
		CountUnanswered     int    `json:"countUnanswered"`
		CountUnansweredToday int    `json:"countUnansweredToday"`
		Valuation           string `json:"valuation"`
	}]

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		t.path, nil, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get unanswered feedbacks counts: %w", err)
	}

	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

// WbUnansweredQuestionsCountsTool — инструмент для получения количества неотвеченных вопросов.
//
// GET /api/v1/questions/count-unanswered
// Без параметров
type WbUnansweredQuestionsCountsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	path        string
	rateLimit   int
	burst       int
	description string
}

// NewWbUnansweredQuestionsCountsTool создает инструмент для получения неотвеченных вопросов.
func NewWbUnansweredQuestionsCountsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbUnansweredQuestionsCountsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbUnansweredQuestionsCountsTool{
		client:      c,
		toolID:      "get_wb_unanswered_questions_counts",
		endpoint:    endpoint,
		path:        cfg.Path,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbUnansweredQuestionsCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_questions_counts",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredQuestionsCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		mockResult := map[string]interface{}{
			"countUnanswered":     15,
			"countUnansweredToday": 2,
			"mock":                true,
		}
		result, _ := json.Marshal(mockResult)
		return string(result), nil
	}

	var response FeedbacksAPIResponse[struct {
		CountUnanswered     int `json:"countUnanswered"`
		CountUnansweredToday int `json:"countUnansweredToday"`
	}]

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		t.path, nil, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get unanswered questions counts: %w", err)
	}

	result, _ := json.Marshal(response.Data)
	return string(result), nil
}
