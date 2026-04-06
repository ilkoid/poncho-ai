package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockCardsClient implements CardsClient for testing.
// Supports cursor-based pagination with realistic card data.
type MockCardsClient struct {
	mu    sync.RWMutex
	cards []wb.ProductCard
}

// NewMockCardsClient creates a new mock cards client.
func NewMockCardsClient() *MockCardsClient {
	return &MockCardsClient{
		cards: make([]wb.ProductCard, 0),
	}
}

// GetCardsList returns a page of mock product cards.
// Simulates cursor-based pagination per WB Content API spec.
func (m *MockCardsClient) GetCardsList(_ context.Context, settings wb.CardsSettings, _, _ int) ([]wb.ProductCard, *wb.CardsCursorResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Find starting position based on cursor
	startIdx := 0
	if settings.Cursor.UpdatedAt != "" || settings.Cursor.NmID != 0 {
		// Find card matching cursor
		for i, card := range m.cards {
			if card.UpdatedAt == settings.Cursor.UpdatedAt && card.NmID == settings.Cursor.NmID {
				startIdx = i + 1 // Start AFTER the cursor position
				break
			}
		}
	}

	// Calculate end position based on limit
	endIdx := startIdx + settings.Cursor.Limit
	if endIdx > len(m.cards) {
		endIdx = len(m.cards)
	}

	// Extract page
	page := m.cards[startIdx:endIdx]

	// Build cursor response
	var cursorResp *wb.CardsCursorResponse
	if len(page) > 0 && endIdx < len(m.cards) {
		// More data available
		lastCard := page[len(page)-1]
		cursorResp = &wb.CardsCursorResponse{
			UpdatedAt: lastCard.UpdatedAt,
			NmID:      lastCard.NmID,
			Total:     len(page),
		}
	} else if len(page) > 0 {
		// Last page
		cursorResp = &wb.CardsCursorResponse{
			UpdatedAt: "",
			NmID:      0,
			Total:     len(page),
		}
	} else {
		// Empty page (end of data)
		cursorResp = nil
	}

	return page, cursorResp, nil
}

// PopulateMockCards fills mock client with realistic test data.
// Generates cardCount cards with nested data (photos, sizes, characteristics, tags).
func PopulateMockCards(m *MockCardsClient, cardCount int) {
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}
	subjects := []string{"Кроссовки", "Футболка", "Джинсы", "Куртка", "Шорты"}
	colors := []string{"Черный", "Белый", "Красный", "Синий", "Зеленый"}

	cards := make([]wb.ProductCard, cardCount)
	baseTime := time.Now().Add(-30 * 24 * time.Hour) // 30 days ago

	for i := 0; i < cardCount; i++ {
		brand := brands[i%len(brands)]
		subject := subjects[i%len(subjects)]
		color := colors[i%len(colors)]

		cards[i] = wb.ProductCard{
			NmID:        100000 + i,
			ImtID:       1000 + i%10,
			NmUUID:      fmt.Sprintf("uuid-%d", i),
			SubjectID:   100 + i%5,
			SubjectName: subject,
			VendorCode:  fmt.Sprintf("%s-%s-%d", brand, subject, i),
			Brand:       brand,
			Title:       fmt.Sprintf("%s %s %s", brand, subject, color),
			Description: fmt.Sprintf("Описание товара %d. Качественный материал.", i),
			NeedKiz:     i%3 == 0, // Some cards need KIZ marking

			// Photos (2 per card)
			Photos: []wb.ProductPhoto{
				{
					Big:      fmt.Sprintf("https://photo.wb.net/big/%d.jpg", i),
					C246x328: fmt.Sprintf("https://photo.wb.net/c246x328/%d.jpg", i),
					C516x688: fmt.Sprintf("https://photo.wb.net/c516x688/%d.jpg", i),
					Square:   fmt.Sprintf("https://photo.wb.net/square/%d.jpg", i),
					Tm:       fmt.Sprintf("https://photo.wb.net/tm/%d.jpg", i),
				},
				{
					Big:      fmt.Sprintf("https://photo.wb.net/big/%d-2.jpg", i),
					C246x328: fmt.Sprintf("https://photo.wb.net/c246x328/%d-2.jpg", i),
					C516x688: fmt.Sprintf("https://photo.wb.net/c516x688/%d-2.jpg", i),
					Square:   fmt.Sprintf("https://photo.wb.net/square/%d-2.jpg", i),
					Tm:       fmt.Sprintf("https://photo.wb.net/tm/%d-2.jpg", i),
				},
			},

			// Video (some cards have video)
			Video: func() string {
				if i%5 == 0 {
					return fmt.Sprintf("https://video.wb.net/%d.mp4", i)
				}
				return ""
			}(),

			// Wholesale (some cards enabled)
			Wholesale: func() *wb.CardWholesale {
				if i%4 == 0 {
					return &wb.CardWholesale{
						Enabled: true,
						Quantum: 10 + i%5,
					}
				}
				return nil
			}(),

			// Dimensions (all cards)
			Dimensions: &wb.CardDimensions{
				Length:       20.0 + float64(i%10)*2,
				Width:        10.0 + float64(i%5)*2,
				Height:       5.0 + float64(i%3)*2,
				WeightBrutto: 0.5 + float64(i%10)*0.1,
				IsValid:      i%10 != 0, // Some invalid dimensions
			},

			// Characteristics (2-3 per card)
			Characteristics: []wb.CardCharacteristic{
				{ID: 1, Name: "Цвет", ValueRaw: rawJSON("[\""+color+"\"]")},
				{ID: 2, Name: "Материал", ValueRaw: rawJSON("[\""+[]string{"Хлопок 100%", "Полиэстер"}[i%2]+"\"]")},
				{ID: 3, Name: "Страна", ValueRaw: rawJSON("[\""+[]string{"Китай", "Вьетнам", "Россия"}[i%3]+"\"]")},
			},

			// Sizes (1-2 per card)
			Sizes: makeSizesForCard(i),

			// Tags (0-2 per card)
			Tags: makeTagsForCard(i),

			// Timestamps (ascending for pagination)
			CreatedAt: baseTime.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
			UpdatedAt: baseTime.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
	}

	m.mu.Lock()
	m.cards = cards
	m.mu.Unlock()
}

// makeSizesForCard generates 1-2 sizes for a card.
func makeSizesForCard(cardIdx int) []wb.CardSize {
	baseChrtID := 50000 + cardIdx*10

	if cardIdx%3 == 0 {
		// Single size
		return []wb.CardSize{
			{
				ChrtID:   baseChrtID,
				TechSize: "42",
				WBSize:   "42",
				Skus:     []string{fmt.Sprintf("SKU-%d", baseChrtID)},
			},
		}
	}

	// Multiple sizes
	return []wb.CardSize{
		{
			ChrtID:   baseChrtID,
			TechSize: "42",
			WBSize:   "42",
			Skus:     []string{fmt.Sprintf("SKU-%d", baseChrtID), fmt.Sprintf("SKU-%d-2", baseChrtID)},
		},
		{
			ChrtID:   baseChrtID + 1,
			TechSize: "44",
			WBSize:   "44",
			Skus:     []string{fmt.Sprintf("SKU-%d", baseChrtID+1)},
		},
	}
}

// rawJSON is a helper to create json.RawMessage from a string.
func rawJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}

// makeTagsForCard generates 0-2 tags for a card.
func makeTagsForCard(cardIdx int) []wb.CardTag {
	if cardIdx%4 == 0 {
		return nil // No tags for some cards
	}

	tags := []wb.CardTag{
		{ID: 1, Name: "Новинка", Color: "#FF0000"},
	}
	if cardIdx%3 == 0 {
		tags = append(tags, wb.CardTag{ID: 2, Name: "Хит продаж", Color: "#00FF00"})
	}

	return tags
}
