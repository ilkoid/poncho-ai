// Package wb provides service layer implementations for Wildberries API.
//
// This file contains the ProductService implementation with business logic
// for product card retrieval via Content API.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
)

// Content API rate limits for card queries.
const (
	cardsServiceRateLimit = 100 // Content API: 100 req/min
	cardsServiceBurst     = 5
)

// Compile-time assertion: productService implements ProductService.
var _ ProductService = (*productService)(nil)

// productService implements ProductService using the WB Client.
type productService struct {
	client *Client
}

// GetCardsByVendorCodes retrieves full product cards by vendor codes (supplier articles).
//
// For each code, performs a server-side TextSearch query via Content API,
// then filters for exact VendorCode match on the client side.
// TextSearch may return partial matches, so client-side filtering is required.
//
// Parameters:
//   - codes: list of vendor codes (supplier articles), 1-10 items
//
// Returns full ProductCard data including photos, sizes, characteristics, and tags.
func (s *productService) GetCardsByVendorCodes(ctx context.Context, codes []string) ([]ProductCard, error) {
	// Validation
	if len(codes) == 0 {
		return nil, fmt.Errorf("vendor codes cannot be empty")
	}
	if len(codes) > 10 {
		return nil, fmt.Errorf("maximum 10 vendor codes allowed per request")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCardsByVendorCodes(codes)
	}

	var results []ProductCard

	for _, code := range codes {
		settings := CardsSettings{
			Cursor: CardsCursor{Limit: 100},
			Filter: &CardsFilter{TextSearch: code},
		}

		for {
			cards, cursor, err := s.client.GetCardsList(ctx, settings, cardsServiceRateLimit, cardsServiceBurst)
			if err != nil {
				return nil, fmt.Errorf("search cards for '%s': %w", code, err)
			}

			// Exact match filter (TextSearch may return partial matches)
			for _, card := range cards {
				if card.VendorCode == code {
					results = append(results, card)
				}
			}

			if cursor == nil {
				break
			}

			settings.Cursor = CardsCursor{
				Limit:     100,
				UpdatedAt: cursor.UpdatedAt,
				NmID:      cursor.NmID,
			}
		}
	}

	return results, nil
}

// GetCardsByNmIDs retrieves full product cards by nmIDs.
//
// Paginates through all cards via Content API with client-side filtering.
// Stops early when all requested nmIDs are found.
//
// Note: Content API does not support server-side filtering by nmID,
// so this method must paginate through the full catalog. For bulk
// operations, use download-wb-cards-v2 CLI instead.
//
// Parameters:
//   - nmIDs: list of nmIDs, 1-50 items
func (s *productService) GetCardsByNmIDs(ctx context.Context, nmIDs []int) ([]ProductCard, error) {
	// Validation
	if len(nmIDs) == 0 {
		return nil, fmt.Errorf("nmIDs cannot be empty")
	}
	if len(nmIDs) > 50 {
		return nil, fmt.Errorf("maximum 50 nmIDs allowed per request")
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockCardsByNmIDs(nmIDs)
	}

	// Build lookup set for early exit
	wanted := make(map[int]bool, len(nmIDs))
	for _, id := range nmIDs {
		wanted[id] = true
	}

	var results []ProductCard
	settings := CardsSettings{
		Cursor: CardsCursor{Limit: 100},
	}

	for {
		cards, cursor, err := s.client.GetCardsList(ctx, settings, cardsServiceRateLimit, cardsServiceBurst)
		if err != nil {
			return nil, fmt.Errorf("fetch cards: %w", err)
		}

		for _, card := range cards {
			if wanted[card.NmID] {
				results = append(results, card)
				delete(wanted, card.NmID)
			}
		}

		// Early exit: all found or no more pages
		if len(wanted) == 0 || cursor == nil {
			break
		}

		settings.Cursor = CardsCursor{
			Limit:     100,
			UpdatedAt: cursor.UpdatedAt,
			NmID:      cursor.NmID,
		}
	}

	return results, nil
}

// GetProducts retrieves products with optional filtering.
// Returns minimal ProductInfo (subset of full ProductCard).
func (s *productService) GetProducts(ctx context.Context, filter ProductFilter) ([]ProductInfo, error) {
	if s.client.IsDemoKey() {
		return s.getMockProducts(filter)
	}

	settings := CardsSettings{
		Cursor: CardsCursor{Limit: 100},
	}

	if filter.Limit > 0 && filter.Limit < 100 {
		settings.Cursor.Limit = filter.Limit
	}

	// Build filter from ProductFilter fields
	var cf *CardsFilter
	if filter.Brand != "" {
		cf = &CardsFilter{Brands: []string{filter.Brand}}
	}
	settings.Filter = cf

	var results []ProductInfo

	for {
		cards, cursor, err := s.client.GetCardsList(ctx, settings, cardsServiceRateLimit, cardsServiceBurst)
		if err != nil {
			return nil, fmt.Errorf("fetch products: %w", err)
		}

		for _, card := range cards {
			results = append(results, ProductInfo{
				NmID:    card.NmID,
				Article: card.VendorCode,
				Name:    card.Title,
			})

			if filter.Limit > 0 && len(results) >= filter.Limit {
				return results, nil
			}
		}

		if cursor == nil {
			break
		}

		settings.Cursor = CardsCursor{
			Limit:     100,
			UpdatedAt: cursor.UpdatedAt,
			NmID:      cursor.NmID,
		}
	}

	return results, nil
}

// GetProductByID retrieves a single product by nmID.
// Returns minimal ProductInfo (not full ProductCard).
func (s *productService) GetProductByID(ctx context.Context, nmID int) (*ProductInfo, error) {
	cards, err := s.GetCardsByNmIDs(ctx, []int{nmID})
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("product with nmID %d not found", nmID)
	}
	card := cards[0]
	return &ProductInfo{
		NmID:    card.NmID,
		Article: card.VendorCode,
		Name:    card.Title,
	}, nil
}

// SyncProducts is not implemented in the service layer.
// Use download-wb-cards-v2 CLI for bulk sync operations.
func (s *productService) SyncProducts(ctx context.Context) (int, error) {
	return 0, fmt.Errorf("not implemented: use download-wb-cards-v2 for bulk sync")
}

// ============================================================================
// Mock Data
// ============================================================================

// getMockCardsByVendorCodes returns deterministic mock cards for demo mode.
func (s *productService) getMockCardsByVendorCodes(codes []string) ([]ProductCard, error) {
	results := make([]ProductCard, 0, len(codes))
	for i, code := range codes {
		results = append(results, makeMockCard(i+1, code))
	}
	return results, nil
}

// getMockCardsByNmIDs returns deterministic mock cards for demo mode.
func (s *productService) getMockCardsByNmIDs(nmIDs []int) ([]ProductCard, error) {
	results := make([]ProductCard, 0, len(nmIDs))
	for _, nmID := range nmIDs {
		results = append(results, makeMockCard(nmID, fmt.Sprintf("ART%06d", nmID%10000)))
	}
	return results, nil
}

// getMockProducts returns minimal mock products for demo mode.
func (s *productService) getMockProducts(filter ProductFilter) ([]ProductInfo, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	results := make([]ProductInfo, 0, limit)
	for i := 1; i <= limit; i++ {
		results = append(results, ProductInfo{
			NmID:    100000000 + i,
			Article: fmt.Sprintf("ART%06d", i),
			Name:    fmt.Sprintf("Mock Product %d", i),
		})
	}
	return results, nil
}

// makeMockCard creates a deterministic mock ProductCard for demo mode.
// Uses nmID-based values for variety (modulo arithmetic).
func makeMockCard(nmID int, vendorCode string) ProductCard {
	return ProductCard{
		NmID:        nmID,
		ImtID:       nmID * 10,
		NmUUID:      fmt.Sprintf("uuid-%d-mock", nmID),
		SubjectID:   1 + (nmID % 50),
		SubjectName: "Платье женское",
		VendorCode:  vendorCode,
		Brand:       "MockBrand",
		Title:       fmt.Sprintf("Mock Product %s", vendorCode),
		Description: fmt.Sprintf("Описание для артикула %s (демо-режим)", vendorCode),
		Photos: []ProductPhoto{
			{
				Big:      fmt.Sprintf("https://mock.wb.ru/photo/%d/big.jpg", nmID),
				C246x328: fmt.Sprintf("https://mock.wb.ru/photo/%d/246x328.jpg", nmID),
				C516x688: fmt.Sprintf("https://mock.wb.ru/photo/%d/516x688.jpg", nmID),
				Square:   fmt.Sprintf("https://mock.wb.ru/photo/%d/square.jpg", nmID),
				Tm:       fmt.Sprintf("https://mock.wb.ru/photo/%d/tm.jpg", nmID),
			},
		},
		Sizes: []CardSize{
			{ChrtID: nmID*100 + 1, TechSize: "M", WBSize: "44-46", Skus: []string{"sku-mock-001"}},
			{ChrtID: nmID*100 + 2, TechSize: "L", WBSize: "46-48", Skus: []string{"sku-mock-002"}},
		},
		Characteristics: []CardCharacteristic{
			{ID: 100 + (nmID % 5), Name: "Цвет", ValueRaw: json.RawMessage(`["синий"]`)},
			{ID: 200 + (nmID % 3), Name: "Состав", ValueRaw: json.RawMessage(`["хлопок 100%"]`)},
			{ID: 300, Name: "Пол", ValueRaw: json.RawMessage(`["Женский"]`)},
		},
		Tags: []CardTag{
			{ID: 1, Name: "Новинка", Color: "green"},
		},
		Dimensions: &CardDimensions{
			Length:       25.0 + float64(nmID%10),
			Width:        15.0 + float64(nmID%5),
			Height:       5.0 + float64(nmID%3),
			WeightBrutto: 0.3 + float64(nmID%7)*0.1,
			IsValid:      true,
		},
		CreatedAt: "2026-01-15T10:00:00Z",
		UpdatedAt: "2026-05-20T14:30:00Z",
	}
}
