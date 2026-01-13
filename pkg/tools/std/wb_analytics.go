// Package std предоставляет инструменты для аналитики Wildberries.
//
// Реализует инструменты для получения статистики товаров:
// - Воронка продаж (просмотры → корзина → заказ)
// - История по дням
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

// WbProductFunnelTool — инструмент для получения воронки продаж товаров.
//
// Использует Analytics API: POST /api/v2/nm-report/detail
// Возвращает просмотры, добавления в корзину, заказы и конверсии.
type WbProductFunnelTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbProductFunnelTool создает инструмент для получения воронки продаж.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbProductFunnelTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbProductFunnelTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbProductFunnelTool{
		client:      c,
		toolID:      "get_wb_product_funnel",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnelTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel",
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
					"description": "Количество дней для анализа (1-365)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnelTool) Execute(ctx context.Context, argsJSON string) (string, error) {
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
	if args.Days < 1 || args.Days > 365 {
		return "", fmt.Errorf("days must be between 1 and 365")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIDs": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"page": 1,
	}

	var response struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics struct {
					SelectedPeriod struct {
						OpenCardCount  int  `json:"openCardCount"`
						AddToCartCount int  `json:"addToCartCount"`
						OrdersCount    int  `json:"ordersCount"`
						Conversions    struct {
							AddToCartPercent     float64 `json:"addToCartPercent"`
							CartToOrderPercent   float64 `json:"cartToOrderPercent"`
							OpenCardToOrderPercent float64 `json:"openCardToOrderPercent"`
						} `json:"conversions"`
					} `json:"selectedPeriod"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/nm-report/detail", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Cards)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelTool) executeMock(nmIDs []int, days int) (string, error) {
	mockCards := make([]map[string]interface{}, 0, len(nmIDs))

	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	for _, nmID := range nmIDs {
		views := 1000 + (nmID % 500)
		addToCart := views / 10
		orders := addToCart / 5

		mockCards = append(mockCards, map[string]interface{}{
			"nmID": nmID,
			"period": map[string]string{
				"begin": begin.Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
			},
			"funnel": map[string]interface{}{
				"views":      views,
				"addToCart":  addToCart,
				"orders":     orders,
				"conversions": map[string]interface{}{
					"toCartPercent":   float64(addToCart) / float64(views) * 100,
					"toOrderPercent":  float64(orders) / float64(addToCart) * 100,
				},
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(mockCards)
	return string(result), nil
}

// WbProductFunnelHistoryTool — инструмент для получения истории воронки по дням.
//
// Использует Analytics API: POST /api/v2/nm-report/detail/history
// Возвращает историю по дням (до 7 дней бесплатно).
type WbProductFunnelHistoryTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbProductFunnelHistoryTool создает инструмент для получения истории по дням.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbProductFunnelHistoryTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbProductFunnelHistoryTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbProductFunnelHistoryTool{
		client:      c,
		toolID:      "get_wb_product_funnel_history",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnelHistoryTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel_history",
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
					"description": "Количество дней для истории (1-7 бесплатно, до 365 с подпиской Джем)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnelHistoryTool) Execute(ctx context.Context, argsJSON string) (string, error) {
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
	if args.Days < 1 || args.Days > 365 {
		return "", fmt.Errorf("days must be between 1 and 365")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIDs": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"page": 1,
	}

	var response struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics []struct {
					Date           string `json:"date"`
					OpenCardCount  int    `json:"openCardCount"`
					AddToCartCount int    `json:"addToCartCount"`
					OrdersCount    int    `json:"ordersCount"`
					Conversions    struct {
						AddToCartPercent     float64 `json:"addToCartPercent"`
						CartToOrderPercent   float64 `json:"cartToOrderPercent"`
						OpenCardToOrderPercent float64 `json:"openCardToOrderPercent"`
					} `json:"conversions"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/nm-report/detail/history", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel history: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Cards)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelHistoryTool) executeMock(nmIDs []int, days int) (string, error) {
	mockCards := make([]map[string]interface{}, 0, len(nmIDs))
	now := time.Now()

	for _, nmID := range nmIDs {
		history := make([]map[string]interface{}, 0, days)

		for i := days - 1; i >= 0; i-- {
			date := now.AddDate(0, 0, -i)
			views := 100 + (nmID % 50) + i*10
			addToCart := views / 10
			orders := addToCart / 5

			history = append(history, map[string]interface{}{
				"date":           date.Format("2006-01-02"),
				"openCardCount":  views,
				"addToCartCount": addToCart,
				"ordersCount":    orders,
				"conversions": map[string]interface{}{
					"addToCartPercent":     float64(addToCart) / float64(views) * 100,
					"cartToOrderPercent":   float64(orders) / float64(addToCart) * 100,
					"openCardToOrderPercent": float64(orders) / float64(views) * 100,
				},
			})
		}

		mockCards = append(mockCards, map[string]interface{}{
			"nmID":       nmID,
			"statistics": history,
			"mock":       true,
		})
	}

	result, _ := json.Marshal(mockCards)
	return string(result), nil
}
