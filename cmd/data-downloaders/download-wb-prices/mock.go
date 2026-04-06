package main

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MockPricesClient implements PricesClient for testing.
type MockPricesClient struct {
	prices []wb.ProductPrice
}

// NewMockPricesClient creates a new mock client.
func NewMockPricesClient() *MockPricesClient {
	return &MockPricesClient{}
}

// GetPrices returns a paginated slice of mock prices.
func (m *MockPricesClient) GetPrices(ctx context.Context, limit, offset, _, _ int) ([]wb.ProductPrice, int, error) {
	if offset >= len(m.prices) {
		return nil, 0, nil
	}

	end := offset + limit
	if end > len(m.prices) {
		end = len(m.prices)
	}

	return m.prices[offset:end], end - offset, nil
}

// PopulateMockPrices fills the mock client with test data.
func PopulateMockPrices(mock *MockPricesClient, count int) {
	vendorCodes := []string{"SA-001", "SA-002", "SA-003", "SB-010", "SB-011"}

	for i := 0; i < count; i++ {
		basePrice := 500 + (i * 73 % 2000)
		discount := 5 + (i * 7 % 30)
		discountedPrice := float64(basePrice) * (1 - float64(discount)/100)
		clubDiscount := discount + 5
		clubDiscountedPrice := float64(basePrice) * (1 - float64(clubDiscount)/100)

		mock.prices = append(mock.prices, wb.ProductPrice{
			NmID:                1000000 + i,
			VendorCode:          fmt.Sprintf("%s-%d", vendorCodes[i%len(vendorCodes)], i),
			Price:               basePrice,
			DiscountedPrice:     discountedPrice,
			ClubDiscountedPrice: clubDiscountedPrice,
			Discount:            discount,
			ClubDiscount:        clubDiscount,
			Currency:            "RUB",
			EditableSizePrice:   false,
		})
	}
}
