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
	"strconv"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbProductFunnelTool — инструмент для получения воронки продаж товаров.
//
// Использует Analytics API v3: POST /api/analytics/v3/sales-funnel/products
// Возвращает просмотры, корзину, заказы, выкупы, отмены, избранное, WB Club, остатки, рейтинги, финансы.
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
				"top": map[string]interface{}{
					"type":        "boolean",
					"description": "Вернуть только топовые товары (опционально)",
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

	// Формируем запрос к WB API v3
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"selectedPeriod": map[string]string{
			"start": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
	}

	var response struct {
		Data struct {
			Products []struct {
				Product struct {
					NmID          int     `json:"nmId"`
					Title         string  `json:"title"`
					VendorCode    string  `json:"vendorCode"`
					BrandName     string  `json:"brandName"`
					SubjectID     int     `json:"subjectId"`
					SubjectName   string  `json:"subjectName"`
					ProductRating float64 `json:"productRating"`
					FeedbackRating float64 `json:"feedbackRating"`
					Stocks        struct {
						WB         int `json:"wb"`
						MP         int `json:"mp"`
						BalanceSum int `json:"balanceSum"`
					} `json:"stocks"`
				} `json:"product"`
				Statistic struct {
					Selected struct {
						Period struct {
							Start string `json:"start"`
							End   string `json:"end"`
						} `json:"period"`
						OpenCount        int     `json:"openCount"`
						CartCount        int     `json:"cartCount"`
						OrderCount       int     `json:"orderCount"`
						OrderSum         int     `json:"orderSum"`
						BuyoutCount      int     `json:"buyoutCount"`
						BuyoutSum        int     `json:"buyoutSum"`
						CancelCount      int     `json:"cancelCount"`
						CancelSum        int     `json:"cancelSum"`
						AvgPrice         int     `json:"avgPrice"`
						AddToWishlist    int     `json:"addToWishlist"`
						TimeToReady      struct {
							Days  int `json:"days"`
							Hours int `json:"hours"`
							Mins  int `json:"mins"`
						} `json:"timeToReady"`
						LocalizationPercent float64 `json:"localizationPercent"`
						WBClub              struct {
							OrderCount          int     `json:"orderCount"`
							OrderSum            int     `json:"orderSum"`
							BuyoutSum           int     `json:"buyoutSum"`
							BuyoutCount         int     `json:"buyoutCount"`
							CancelSum           int     `json:"cancelSum"`
							CancelCount         int     `json:"cancelCount"`
							AvgPrice            int     `json:"avgPrice"`
							BuyoutPercent       float64 `json:"buyoutPercent"`
							AvgOrderCountPerDay float64 `json:"avgOrderCountPerDay"`
						} `json:"wbClub"`
						Conversions struct {
							AddToCartPercent   float64 `json:"addToCartPercent"`
							CartToOrderPercent float64 `json:"cartToOrderPercent"`
							BuyoutPercent      float64 `json:"buyoutPercent"`
						} `json:"conversions"`
					} `json:"selected"`
					Past *struct {
						Period struct {
							Start string `json:"start"`
							End   string `json:"end"`
						} `json:"period"`
						OpenCount     int     `json:"openCount"`
						CartCount     int     `json:"cartCount"`
						OrderCount    int     `json:"orderCount"`
						OrderSum      int     `json:"orderSum"`
						BuyoutCount   int     `json:"buyoutCount"`
						BuyoutSum     int     `json:"buyoutSum"`
						CancelCount   int     `json:"cancelCount"`
						CancelSum     int     `json:"cancelSum"`
						AvgPrice      int     `json:"avgPrice"`
						AddToWishlist int     `json:"addToWishlist"`
					} `json:"past,omitempty"`
					Comparison *struct {
						OpenCountDiff     int     `json:"openCountDiff"`
						OpenCountPercent  float64 `json:"openCountPercent"`
						CartCountDiff     int     `json:"cartCountDiff"`
						CartCountPercent  float64 `json:"cartCountPercent"`
						OrderCountDiff    int     `json:"orderCountDiff"`
						OrderCountPercent float64 `json:"orderCountPercent"`
						OrderSumDiff      int     `json:"orderSumDiff"`
						OrderSumPercent   float64 `json:"orderSumPercent"`
					} `json:"comparison,omitempty"`
				} `json:"statistic"`
			} `json:"products"`
			Currency string `json:"currency"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/analytics/v3/sales-funnel/products", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Products)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelTool) executeMock(nmIDs []int, days int) (string, error) {
	mockProducts := make([]map[string]interface{}, 0, len(nmIDs))

	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	for _, nmID := range nmIDs {
		openCount := 1000 + (nmID % 500)
		cartCount := openCount / 10
		orderCount := cartCount / 3
		buyoutCount := int(float64(orderCount) * 0.85)
		cancelCount := orderCount - buyoutCount
		avgPrice := 1500 + (nmID % 500)

		mockProducts = append(mockProducts, map[string]interface{}{
			"product": map[string]interface{}{
				"nmId":          nmID,
				"title":         "Mock Product " + strconv.Itoa(nmID),
				"vendorCode":    "ART" + strconv.Itoa(nmID),
				"brandName":     "Mock Brand",
				"subjectName":   "Mock Subject",
				"productRating": 4.5,
				"feedbackRating": 4.2,
				"stocks": map[string]interface{}{
					"wb":         50,
					"mp":         20,
					"balanceSum": 70000,
				},
			},
			"statistic": map[string]interface{}{
				"selected": map[string]interface{}{
					"period": map[string]string{
						"start": begin.Format("2006-01-02"),
						"end":   now.Format("2006-01-02"),
					},
					"openCount": openCount,
					"cartCount": cartCount,
					"orderCount": orderCount,
					"orderSum":  orderCount * avgPrice,
					"buyoutCount": buyoutCount,
					"buyoutSum":   buyoutCount * avgPrice,
					"cancelCount": cancelCount,
					"cancelSum":   cancelCount * avgPrice,
					"avgPrice":    avgPrice,
					"addToWishlist": openCount / 3,
					"localizationPercent": 45.0,
					"timeToReady": map[string]interface{}{
						"days":  1,
						"hours": 8,
						"mins":  30,
					},
					"wbClub": map[string]interface{}{
						"orderCount":           orderCount / 2,
						"orderSum":             (orderCount / 2) * avgPrice,
						"buyoutCount":          buyoutCount / 2,
						"buyoutSum":            (buyoutCount / 2) * avgPrice,
						"cancelCount":          cancelCount / 2,
						"cancelSum":            (cancelCount / 2) * avgPrice,
						"avgPrice":             avgPrice,
						"buyoutPercent":        85.0,
						"avgOrderCountPerDay":  float64(orderCount) / float64(days),
					},
					"conversions": map[string]interface{}{
						"addToCartPercent":   float64(cartCount) / float64(openCount) * 100,
						"cartToOrderPercent": float64(orderCount) / float64(cartCount) * 100,
						"buyoutPercent":      float64(buyoutCount) / float64(orderCount) * 100,
					},
				},
			},
			"currency": "RUB",
			"mock":     true,
		})
	}

	result, _ := json.Marshal(mockProducts)
	return string(result), nil
}

// WbProductFunnelHistoryTool — инструмент для получения истории воронки по дням.
//
// Использует Analytics API v3: POST /api/analytics/v3/sales-funnel/products/history
// Возвращает историю по дням (1-7 бесплатно, до 365 с подпиской Джем).
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

	// Формируем запрос к WB API v3
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"selectedPeriod": map[string]string{
			"start": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
	}

	var response struct {
		Data struct {
			Products []struct {
				Product struct {
					NmID          int     `json:"nmId"`
					Title         string  `json:"title"`
					VendorCode    string  `json:"vendorCode"`
					BrandName     string  `json:"brandName"`
					SubjectID     int     `json:"subjectId"`
					SubjectName   string  `json:"subjectName"`
					ProductRating float64 `json:"productRating"`
					FeedbackRating float64 `json:"feedbackRating"`
					Stocks        struct {
						WB         int `json:"wb"`
						MP         int `json:"mp"`
						BalanceSum int `json:"balanceSum"`
					} `json:"stocks"`
				} `json:"product"`
				Statistic struct {
					History []struct {
						Date            string  `json:"date"`
						OpenCount       int     `json:"openCount"`
						CartCount       int     `json:"cartCount"`
						OrderCount      int     `json:"orderCount"`
						OrderSum        int     `json:"orderSum"`
						BuyoutCount     int     `json:"buyoutCount"`
						BuyoutSum       int     `json:"buyoutSum"`
						CancelCount     int     `json:"cancelCount"`
						CancelSum       int     `json:"cancelSum"`
						AvgPrice        int     `json:"avgPrice"`
						AddToWishlist   int     `json:"addToWishlist"`
						TimeToReady     struct {
							Days  int `json:"days"`
							Hours int `json:"hours"`
							Mins  int `json:"mins"`
						} `json:"timeToReady"`
						LocalizationPercent float64 `json:"localizationPercent"`
						WBClub              struct {
							OrderCount          int     `json:"orderCount"`
							OrderSum            int     `json:"orderSum"`
							BuyoutSum           int     `json:"buyoutSum"`
							BuyoutCount         int     `json:"buyoutCount"`
							CancelSum           int     `json:"cancelSum"`
							CancelCount         int     `json:"cancelCount"`
							AvgPrice            int     `json:"avgPrice"`
							BuyoutPercent       float64 `json:"buyoutPercent"`
						} `json:"wbClub"`
						Conversions struct {
							AddToCartPercent   float64 `json:"addToCartPercent"`
							CartToOrderPercent float64 `json:"cartToOrderPercent"`
							BuyoutPercent      float64 `json:"buyoutPercent"`
						} `json:"conversions"`
					} `json:"history"`
				} `json:"statistic"`
			} `json:"products"`
			Currency string `json:"currency"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/analytics/v3/sales-funnel/products/history", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel history: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Products)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelHistoryTool) executeMock(nmIDs []int, days int) (string, error) {
	mockProducts := make([]map[string]interface{}, 0, len(nmIDs))
	now := time.Now()

	for _, nmID := range nmIDs {
		history := make([]map[string]interface{}, 0, days)
		avgPrice := 1500 + (nmID % 500)

		for i := days - 1; i >= 0; i-- {
			date := now.AddDate(0, 0, -i)
			openCount := 100 + (nmID % 50) + i*10
			cartCount := openCount / 10
			orderCount := cartCount / 3
			buyoutCount := int(float64(orderCount) * 0.85)
			cancelCount := orderCount - buyoutCount

			history = append(history, map[string]interface{}{
				"date":             date.Format("2006-01-02"),
				"openCount":        openCount,
				"cartCount":        cartCount,
				"orderCount":       orderCount,
				"orderSum":         orderCount * avgPrice,
				"buyoutCount":      buyoutCount,
				"buyoutSum":        buyoutCount * avgPrice,
				"cancelCount":      cancelCount,
				"cancelSum":        cancelCount * avgPrice,
				"avgPrice":         avgPrice,
				"addToWishlist":    openCount / 3,
				"timeToReady": map[string]interface{}{
					"days":  1,
					"hours": 8,
					"mins":  30,
				},
				"localizationPercent": 45.0,
				"wbClub": map[string]interface{}{
					"orderCount":    orderCount / 2,
					"orderSum":      (orderCount / 2) * avgPrice,
					"buyoutCount":   buyoutCount / 2,
					"buyoutSum":     (buyoutCount / 2) * avgPrice,
					"cancelCount":   cancelCount / 2,
					"cancelSum":     (cancelCount / 2) * avgPrice,
					"avgPrice":      avgPrice,
					"buyoutPercent": 85.0,
				},
				"conversions": map[string]interface{}{
					"addToCartPercent":   float64(cartCount) / float64(openCount) * 100,
					"cartToOrderPercent": float64(orderCount) / float64(cartCount) * 100,
					"buyoutPercent":      float64(buyoutCount) / float64(orderCount) * 100,
				},
			})
		}

		mockProducts = append(mockProducts, map[string]interface{}{
			"product": map[string]interface{}{
				"nmId":          nmID,
				"title":         "Mock Product " + strconv.Itoa(nmID),
				"vendorCode":    "ART" + strconv.Itoa(nmID),
				"brandName":     "Mock Brand",
				"subjectName":   "Mock Subject",
				"productRating": 4.5,
				"feedbackRating": 4.2,
				"stocks": map[string]interface{}{
					"wb":         50,
					"mp":         20,
					"balanceSum": 70000,
				},
			},
			"statistic": map[string]interface{}{
				"history": history,
			},
			"currency": "RUB",
			"mock":     true,
		})
	}

	result, _ := json.Marshal(mockProducts)
	return string(result), nil
}
