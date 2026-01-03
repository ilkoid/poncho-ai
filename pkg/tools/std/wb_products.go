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
		"message": "search_wb_products tool is not implemented yet",
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
