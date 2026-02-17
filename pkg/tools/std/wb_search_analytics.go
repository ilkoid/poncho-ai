// Package std предоставляет инструменты для аналитики поисковых запросов Wildberries.
//
// Реализует инструменты для анализа позиций товаров в поиске:
// - Средняя позиция в выдаче
// - Топ поисковых запросов
// - Топ-10 позиций по органике
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

// WbSearchPositionsTool — инструмент для получения позиций товаров в поиске.
//
// Использует Analytics API: POST /api/v2/search-report/report
// Возвращает среднюю позицию, видимость, переходы из поиска.
type WbSearchPositionsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSearchPositionsTool создает инструмент для получения позиций в поиске.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSearchPositionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSearchPositionsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSearchPositionsTool{
		client:      c,
		toolID:      "get_wb_search_positions",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSearchPositionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_search_positions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSearchPositionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 30 {
		return "", fmt.Errorf("days must be between 1 and 30")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	// currentPeriod: [now - days, now]
	// pastPeriod: [now - days*2, now - days - 1] - прошлый период СТРОГО до текущего
	now := time.Now()
	currentStart := now.AddDate(0, 0, -args.Days)
	pastStart := now.AddDate(0, 0, -args.Days*2)
	pastEnd := currentStart.AddDate(0, 0, -1) // прошлый период заканчивается за 1 день до текущего

	// Формируем запрос к WB API
	// API v2 search-report требует: start/end, orderBy (object), positionCluster (string), limit, offset
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"currentPeriod": map[string]string{
			"start": currentStart.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"pastPeriod": map[string]string{
			"start": pastStart.Format("2006-01-02"),
			"end":   pastEnd.Format("2006-01-02"),
		},
		"orderBy": map[string]string{
			"field": "orders", // поле сортировки: orders, openCard, avgPosition
			"mode":  "desc",   // направление: asc, desc
		},
		"positionCluster":       "all", // "all" - все, "top100" - топ-100
		"includeSubstitutedSKUs": true,
		"includeSearchTexts":    false,
		"limit":                 100,
		"offset":                0,
	}

	// API возвращает сложную структуру с positionInfo, visibilityInfo, groups и т.д.
	// Для простоты парсим весь ответ как map и передаём в LLM
	var response map[string]interface{}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/search-report/report", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get search positions: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbSearchPositionsTool) executeMock(nmIDs []int, days int) (string, error) {
	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	items := make([]map[string]interface{}, 0, len(nmIDs))
	firstHundred := 0

	for _, nmID := range nmIDs {
		avgPos := 50.0 + float64(nmID%100)
		if avgPos <= 100 {
			firstHundred++
		}

		items = append(items, map[string]interface{}{
			"nmId":        nmID,
			"avgPosition": avgPos,
			"openCard":    100 + nmID%200,
			"orders":      10 + nmID%50,
		})
	}

	result := map[string]interface{}{
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"clusters": map[string]interface{}{
			"firstHundred": firstHundred,
		},
		"items": items,
		"mock": true,
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

// WbTopSearchQueriesTool — инструмент для получения топ поисковых запросов.
//
// Использует Analytics API: POST /api/v2/search-report/product/search-texts
// Возвращает топ поисковых фраз по товару.
type WbTopSearchQueriesTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbTopSearchQueriesTool создает инструмент для получения топ поисковых запросов.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbTopSearchQueriesTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbTopSearchQueriesTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbTopSearchQueriesTool{
		client:      c,
		toolID:      "get_wb_top_search_queries",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbTopSearchQueriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_top_search_queries",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Максимальное количество запросов (по умолчанию 30, макс. 100)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbTopSearchQueriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 30 {
		return "", fmt.Errorf("days must be between 1 and 30")
	}
	if args.Limit == 0 {
		args.Limit = 30 // дефолт
	}
	if args.Limit > 100 {
		args.Limit = 100 // максимум
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days, args.Limit)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"topOrderBy": "orders", // Сортировка по заказам
		"limit":     args.Limit,
		"page":      1,
	}

	var response struct {
		Data struct {
			Items []struct {
				NMID      int    `json:"nmId"`
				Queries   []struct {
					Text    string  `json:"text"`
					Orders  int     `json:"orders"`
					Views   int     `json:"views"`
					Position float64 `json:"position"`
				} `json:"queries"`
			} `json:"items"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/search-report/product/search-texts", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get top search queries: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbTopSearchQueriesTool) executeMock(nmIDs []int, days int, limit int) (string, error) {
	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	items := make([]map[string]interface{}, 0, len(nmIDs))

	// Mock поисковые запросы
	mockQueries := []string{
		"платье женское", "вечернее платье", "платье черное",
		"платье длинное", "платье летнее", "сарафан",
		"платье коктейльное", "платье офисное", "платье красное",
		"платье с рукавами",
	}

	for _, nmID := range nmIDs {
		queries := make([]map[string]interface{}, 0, limit)

		for i, q := range mockQueries {
			if i >= limit {
				break
			}
			queries = append(queries, map[string]interface{}{
				"text":     q,
				"orders":   10 + i*5 + nmID%20,
				"views":    100 + i*50 + nmID%200,
				"position": float64(i*3 + 1 + nmID%10),
			})
		}

		items = append(items, map[string]interface{}{
			"nmId":    nmID,
			"queries": queries,
		})
	}

	result := map[string]interface{}{
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"items": items,
		"mock":  true,
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

// WbTopOrganicPositionsTool — инструмент для получения топ-10 позиций в органике.
//
// Это агрегатор, который использует данные из get_wb_top_search_queries
// и возвращает только позиции в топ-10 по каждому запросу.
type WbTopOrganicPositionsTool struct {
	topQueriesTool *WbTopSearchQueriesTool
	description    string
}

// NewWbTopOrganicPositionsTool создает инструмент для получения топ-10 позиций.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbTopOrganicPositionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbTopOrganicPositionsTool {
	return &WbTopOrganicPositionsTool{
		topQueriesTool: NewWbTopSearchQueriesTool(c, cfg, wbDefaults),
		description:    cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbTopOrganicPositionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_top_organic_positions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbTopOrganicPositionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Получаем данные из инструмента топ запросов
	rawData, err := t.topQueriesTool.Execute(ctx, argsJSON)
	if err != nil {
		return "", fmt.Errorf("failed to get top queries data: %w", err)
	}

	var response struct {
		Data struct {
			Items []struct {
				NMID    int `json:"nmId"`
				Queries []struct {
					Text    string  `json:"text"`
					Orders  int     `json:"orders"`
					Views   int     `json:"views"`
					Position float64 `json:"position"`
				} `json:"queries"`
			} `json:"items"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(rawData), &response); err != nil {
		return "", fmt.Errorf("failed to parse top queries response: %w", err)
	}

	// Фильтруем только топ-10 позиции
	result := make([]map[string]interface{}, 0, len(response.Data.Items))

	for _, item := range response.Data.Items {
		topPositions := make([]map[string]interface{}, 0)

		for _, q := range item.Queries {
			if q.Position <= 10.0 {
				topPositions = append(topPositions, map[string]interface{}{
					"query":    q.Text,
					"position": int(q.Position),
					"orders":   q.Orders,
					"views":    q.Views,
				})
			}
		}

		if len(topPositions) > 0 {
			result = append(result, map[string]interface{}{
				"nmID":         item.NMID,
				"topPositions": topPositions,
			})
		}
	}

	// Если реальных данных нет, возвращаем пустой результат с понятным сообщением
	if len(result) == 0 {
		for _, item := range response.Data.Items {
			result = append(result, map[string]interface{}{
				"nmID":         item.NMID,
				"topPositions": []map[string]interface{}{},
				"message":      "Товар не находится в топ-10 органической выдачи по анализируемым запросам",
			})
		}
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}
