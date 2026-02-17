// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// === WB Seller Products List ===
// Инструменты для получения списка товаров продавца

// WbSellerProductsTool — инструмент для получения списка товаров продавца.
//
// Использует Prices & Discounts API (endpoint: https://discounts-prices-api.wildberries.ru)
// Метод: GET /api/v2/list/goods/filter
//
// Возвращает товары продавца с информацией о ценах, скидках и остатках.
// Поддерживает пагинацию (limit до 1000 товаров за запрос).
//
// ВАЖНО: видит только товары продавца, которому принадлежит API токен.
// Для поиска товаров других продавцов используйте get_wb_feedbacks или get_wb_questions.
type WbSellerProductsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSellerProductsTool создает инструмент для получения списка товаров продавца.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSellerProductsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSellerProductsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSellerProductsTool{
		client:      c,
		toolID:      "list_wb_seller_products",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSellerProductsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "list_wb_seller_products",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Количество товаров для возврата (максимум 1000, по умолчанию 100)",
					"minimum":     1,
					"maximum":     1000,
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Количество товаров для пропуска (для пагинации, по умолчанию 0)",
					"minimum":     0,
				},
				"filter_subject_id": map[string]interface{}{
					"type":        "integer",
					"description": "Опционально: ID предмета для фильтрации (получи из get_wb_subjects)",
				},
				"filter_brand": map[string]interface{}{
					"type":        "string",
					"description": "Опционально: название бренда для фильтрации",
				},
			},
			"required": []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSellerProductsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Limit            int     `json:"limit"`
		Offset           int     `json:"offset"`
		FilterSubjectID  *int    `json:"filter_subject_id"`
		FilterBrand      *string `json:"filter_brand"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments json: %w", err)
	}

	// Дефолтные значения
	limit := args.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(limit, offset, args.FilterSubjectID, args.FilterBrand)
	}

	// Формируем query параметры для GET запроса
	queryParams := url.Values{}
	queryParams.Set("limit", fmt.Sprintf("%d", limit))
	queryParams.Set("offset", fmt.Sprintf("%d", offset))

	// Фильтры опциональны
	if args.FilterSubjectID != nil {
		queryParams.Set("filter.subjectID", fmt.Sprintf("%d", *args.FilterSubjectID))
	}
	if args.FilterBrand != nil && *args.FilterBrand != "" {
		queryParams.Set("filter.brand", *args.FilterBrand)
	}

	// Выполняем GET запрос к Prices & Discounts API
	var resp struct {
		Data struct {
			Total   int `json:"total"`
			Offset  int `json:"offset"`
			Limit   int `json:"limit"`
			Products []struct {
				NmID       int    `json:"nmID"`
				VendorCode string `json:"vendorCode"`
				Brand      string `json:"brand"`
				Title      string `json:"title"`
				SubjectID  int    `json:"subjectID"`
				Subject    string `json:"subject"`
			} `json:"listGoods"`
		} `json:"data"`
		Error bool `json:"error"`
	}

	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst, "/api/v2/list/goods/filter", queryParams, &resp)
	if err != nil {
		return "", fmt.Errorf("failed to get seller products: %w", err)
	}

	// Формируем ответ для LLM
	result := map[string]interface{}{
		"total":  resp.Data.Total,
		"offset": resp.Data.Offset,
		"limit":  resp.Data.Limit,
		"count":  len(resp.Data.Products),
		"products": resp.Data.Products,
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные для списка товаров продавца.
func (t *WbSellerProductsTool) executeMock(limit, offset int, filterSubjectID *int, filterBrand *string) (string, error) {
	// Базовые mock товары
	mockProducts := []map[string]interface{}{
		{
			"nmID":       123456789,
			"vendorCode": "ART001",
			"brand":      "BabyBrand",
			"title":      "Детский комбинезон зимний",
			"subjectID":  1481,
			"subject":    "Комбинезоны",
			"prices": map[string]interface{}{
				"basic":      2500.0,
				"discounted": 2000.0,
				"discount":   20,
			},
		},
		{
			"nmID":       234567890,
			"vendorCode": "ART002",
			"brand":      "KidsWear",
			"title":      "Комбинезон демисезонный",
			"subjectID":  1481,
			"subject":    "Комбинезоны",
			"prices": map[string]interface{}{
				"basic":      1800.0,
				"discounted": 1500.0,
				"discount":   17,
			},
		},
		{
			"nmID":       345678901,
			"vendorCode": "ART003",
			"brand":      "FashionStyle",
			"title":      "Платье летнее женское",
			"subjectID":  685,
			"subject":    "Платья",
			"prices": map[string]interface{}{
				"basic":      1200.0,
				"discounted": 960.0,
				"discount":   20,
			},
		},
	}

	// Применяем фильтры
	filtered := mockProducts

	if filterSubjectID != nil {
		var result []map[string]interface{}
		for _, p := range filtered {
			if sid, ok := p["subjectID"].(int); ok && sid == *filterSubjectID {
				result = append(result, p)
			}
		}
		filtered = result
	}

	if filterBrand != nil && *filterBrand != "" {
		var result []map[string]interface{}
		for _, p := range filtered {
			if brand, ok := p["brand"].(string); ok {
				if containsFold(brand, *filterBrand) {
					result = append(result, p)
				}
			}
		}
		filtered = result
	}

	// Применяем пагинацию
	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	var paginated []map[string]interface{}
	if start < end {
		paginated = filtered[start:end]
	}

	result := map[string]interface{}{
		"total":  total,
		"offset": offset,
		"limit":  limit,
		"count":  len(paginated),
		"products": paginated,
		"mock":    true,
		"message": "Demo режим: используйте настоящий API ключ для получения реальных данных",
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// containsFold проверяет что строка содержит подстроку без учета регистра.
func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (containsFoldHelper(s, substr)))
}

func containsFoldHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc := s[i+j]
			subc := substr[j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 32
			}
			if subc >= 'A' && subc <= 'Z' {
				subc += 32
			}
			if sc != subc {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// === WB Products Search ===
// TODO: Реализовать инструмент для поиска товаров Wildberries

// WbProductSearchTool — заглушка для поиска товаров по артикулам поставщика.
type WbProductSearchTool struct {
	client *wb.Client
	wbCfg  config.WBConfig
	toolCfg config.ToolConfig
}

// NewWbProductSearchTool создает заглушку для инструмента поиска товаров.
func NewWbProductSearchTool(client *wb.Client, toolCfg config.ToolConfig, wbCfg config.WBConfig) *WbProductSearchTool {
	return &WbProductSearchTool{
		client:  client,
		wbCfg:   wbCfg,
		toolCfg: toolCfg,
	}
}

func (t *WbProductSearchTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "search_wb_products",
		Description: "Ищет товары Wildberries по артикулам поставщика (vendor code/supplier article) и возвращает их nmID. Использует Content API (категория Promotion). ВАЖНО: видит только товары продавца, которому принадлежит API токен (до 100 карточек). Для поиска товаров других продавцов используйте get_wb_feedbacks или get_wb_questions. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"vendor_codes": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Список артикулов поставщика для поиска",
				},
			},
			"required": []string{"vendor_codes"},
		},
	}
}

func (t *WbProductSearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		VendorCodes []string `json:"vendor_codes"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if len(args.VendorCodes) == 0 {
		return "", fmt.Errorf("vendor_codes cannot be empty")
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "search_wb_products tool is not implemented yet. Use list_wb_seller_products instead.",
		"vendor_codes": args.VendorCodes,
		"results": []map[string]interface{}{
			{
				"vendor_code": args.VendorCodes[0],
				"nmID":        0,
				"price":       0,
				"status":      "STUB",
			},
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}
