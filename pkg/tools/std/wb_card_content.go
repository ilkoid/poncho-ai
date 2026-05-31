// Package std provides the WB Card Content tool for AI agents.
//
// This tool retrieves full product card data (photos, sizes, characteristics, tags)
// from WB Content API via the ProductService service layer.
//
// V2 architecture: uses ProductService instead of direct Client calls.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbCardContentTool retrieves full product card data by vendor codes or nmIDs.
//
// Uses ProductService for business logic, validation, and mock support.
// Returns complete ProductCard including photos, sizes, characteristics, and tags.
//
// The tool accepts either vendor_codes (supplier articles) or nm_ids (product IDs),
// or both. At least one must be provided.
type WbCardContentTool struct {
	service     wb.ProductService
	toolID      string
	description string
}

// NewWbCardContentTool creates a tool for retrieving product card content.
//
// Parameters:
//   - service: ProductService for business logic
//   - cfg: tool configuration from YAML
func NewWbCardContentTool(service wb.ProductService, cfg config.ToolConfig) *WbCardContentTool {
	return &WbCardContentTool{
		service:     service,
		toolID:      "get_card_content",
		description: cfg.Description,
	}
}

// Definition returns the tool definition for LLM function calling.
func (t *WbCardContentTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_card_content",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"vendor_codes": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Артикулы продавца (supplier articles), макс. 10",
				},
				"nm_ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "nmID товаров (product IDs), макс. 50",
				},
			},
			"required": []string{"vendor_codes"},
		},
	}
}

// Execute implements the "Raw In, String Out" tool contract.
//
// Parses vendor_codes and/or nm_ids from JSON args, calls ProductService,
// and returns full ProductCard data as JSON string.
func (t *WbCardContentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		VendorCodes []string `json:"vendor_codes"`
		NmIDs       []int    `json:"nm_ids"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// At least one identifier type required
	if len(args.VendorCodes) == 0 && len(args.NmIDs) == 0 {
		return "", fmt.Errorf("at least one of vendor_codes or nm_ids must be provided")
	}

	// Collect results from both search methods
	var allCards []wb.ProductCard

	if len(args.VendorCodes) > 0 {
		cards, err := t.service.GetCardsByVendorCodes(ctx, args.VendorCodes)
		if err != nil {
			return "", fmt.Errorf("search by vendor codes: %w", err)
		}
		allCards = append(allCards, cards...)
	}

	if len(args.NmIDs) > 0 {
		cards, err := t.service.GetCardsByNmIDs(ctx, args.NmIDs)
		if err != nil {
			return "", fmt.Errorf("search by nmIDs: %w", err)
		}
		allCards = append(allCards, cards...)
	}

	// Deduplicate by nmID (in case both methods return the same card)
	seen := make(map[int]bool, len(allCards))
	deduped := make([]wb.ProductCard, 0, len(allCards))
	for _, card := range allCards {
		if !seen[card.NmID] {
			seen[card.NmID] = true
			deduped = append(deduped, card)
		}
	}

	result, err := json.Marshal(deduped)
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}

	return string(result), nil
}
