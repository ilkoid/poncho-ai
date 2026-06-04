package onec

import (
	"context"
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// MockSource — deterministic fake data for --mock mode and tests
// ---------------------------------------------------------------------------

// MockSource implements Source with configurable fake data.
type MockSource struct {
	mu sync.Mutex

	Goods      []Good
	SKUs       []SKU
	Dimensions []DimensionRow
	Prices     []PriceRow
	PIMGoods   []PIMGoods

	// FailOn controls which step should return an error.
	// Set to "goods", "prices", or "pim" to simulate failure.
	FailOn string
}

// NewMockSource creates a MockSource populated with deterministic fake data.
func NewMockSource() *MockSource {
	src := &MockSource{}
	src.Populate(10, 3, 5) // 10 goods, 3 SKUs each, 5 price types
	return src
}

// Populate fills the MockSource with deterministic fake data.
//   - nGoods: number of goods to generate
//   - skusPerGood: number of SKUs per good
//   - priceTypes: number of price types per good
func (m *MockSource) Populate(nGoods, skusPerGood, priceTypes int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Goods = make([]Good, 0, nGoods)
	m.SKUs = make([]SKU, 0, nGoods*skusPerGood)
	m.Dimensions = make([]DimensionRow, 0, nGoods*skusPerGood)
	m.Prices = make([]PriceRow, 0, nGoods*priceTypes)
	m.PIMGoods = make([]PIMGoods, 0, nGoods)

	for i := range nGoods {
		guid := fmt.Sprintf("good-guid-%03d", i)
		article := fmt.Sprintf("ART%06d", i)

		m.Goods = append(m.Goods, Good{
			GUID:           guid,
			Article:        article,
			Name:           fmt.Sprintf("Product %d", i),
			Brand:          "TestBrand",
			Type:           "Shoes",
			Category:       "Sneakers",
			Season:         "Summer",
			Color:          "Black",
			TnvedCodes:     "[]",
			BusinessLine:   "[]",
			Length:         25.0,
			Wideness:       15.0,
			Height:         10.0,
			WeightSKUG:     300.0,
			IsSale:         i%2 == 0,
			IsNew:          i%3 == 0,
			HasCertificate: true,
			IsAdult:        false,
		})

		for j := range skusPerGood {
			skuGUID := fmt.Sprintf("sku-guid-%03d-%d", i, j)
			size := fmt.Sprintf("%d", 36+j)
			length := 25.0 + float64(j)
			wideness := 15.0 + float64(j)
			height := 10.0 + float64(j)
			weight := 300.0 + float64(j)*50

			m.SKUs = append(m.SKUs, SKU{
				SKUGUID:    skuGUID,
				GUID:       guid,
				Barcode:    fmt.Sprintf("2000%011d", i*10+j),
				Size:       size,
				NDS:        20,
				Length:     length,
				Wideness:   wideness,
				Height:     height,
				WeightSKUG: weight,
			})

			// Dimensions with unit conversion (same logic as HTTPSource)
			if length > 0 || wideness > 0 || height > 0 || weight > 0 {
				m.Dimensions = append(m.Dimensions, DimensionRow{
					GoodGUID:  guid,
					SKUGUID:   skuGUID,
					GoodName:  fmt.Sprintf("Product %d", i),
					SizeName:  size,
					LengthDM:  length / 10,
					WidthDM:   height / 10,   // SWAP
					HeightDM:  wideness / 10, // SWAP
					WeightKG:  weight / 1000,
					VolumeCM3: length * wideness * height,
					Source:    "api",
				})
			}
		}

		for k := range priceTypes {
			m.Prices = append(m.Prices, PriceRow{
				GoodGUID:  guid,
				TypeGUID:  fmt.Sprintf("type-guid-%d", k),
				TypeName:  fmt.Sprintf("Price Type %d", k),
				Price:     1000.0 + float64(i)*100 + float64(k)*50,
				SpecPrice: 900.0 + float64(i)*100,
			})
		}

		m.PIMGoods = append(m.PIMGoods, PIMGoods{
			Identifier:    article,
			Enabled:       true,
			Family:        "TestFamily",
			Categories:    "[\"Sneakers\"]",
			ProductType:   "Shoes",
			Sex:           "Unisex",
			Season:        "Summer",
			WbNmID:        10000000 + i,
			YearCollection: 2026,
			Name:          fmt.Sprintf("Product %d", i),
		})
	}
}

// FetchGoods returns mock goods, SKUs, and dimensions.
func (m *MockSource) FetchGoods(_ context.Context, _ string) ([]Good, []SKU, []DimensionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailOn == "goods" {
		return nil, nil, nil, fmt.Errorf("mock: goods step failed")
	}

	// Return copies to prevent mutation
	goods := make([]Good, len(m.Goods))
	copy(goods, m.Goods)
	skus := make([]SKU, len(m.SKUs))
	copy(skus, m.SKUs)
	dims := make([]DimensionRow, len(m.Dimensions))
	copy(dims, m.Dimensions)

	return goods, skus, dims, nil
}

// FetchPrices returns mock price rows.
func (m *MockSource) FetchPrices(_ context.Context, _ string) ([]PriceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailOn == "prices" {
		return nil, fmt.Errorf("mock: prices step failed")
	}

	prices := make([]PriceRow, len(m.Prices))
	copy(prices, m.Prices)
	return prices, nil
}

// FetchPIMGoods returns mock PIM goods.
func (m *MockSource) FetchPIMGoods(_ context.Context, _ string) ([]PIMGoods, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailOn == "pim" {
		return nil, fmt.Errorf("mock: pim step failed")
	}

	items := make([]PIMGoods, len(m.PIMGoods))
	copy(items, m.PIMGoods)
	return items, nil
}

// ---------------------------------------------------------------------------
// DiscardWriter — no-op writer for --mock mode (NEVER touches DB)
// ---------------------------------------------------------------------------

// DiscardWriter implements Writer with thread-safe counting.
// All writes are discarded — used for --mock mode and testing.
type DiscardWriter struct {
	mu sync.Mutex

	goodsCount     int
	skuCount       int
	dimensionCount int
	priceCount     int
	pimCount       int
	cleanCalled    bool
}

// NewDiscardWriter creates a DiscardWriter.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SaveGoods counts and discards.
func (w *DiscardWriter) SaveGoods(_ context.Context, goods []Good) (int, error) {
	w.mu.Lock()
	w.goodsCount += len(goods)
	w.mu.Unlock()
	return len(goods), nil
}

// SaveSKUs counts and discards.
func (w *DiscardWriter) SaveSKUs(_ context.Context, skus []SKU) (int, error) {
	w.mu.Lock()
	w.skuCount += len(skus)
	w.mu.Unlock()
	return len(skus), nil
}

// SaveDimensions counts and discards.
func (w *DiscardWriter) SaveDimensions(_ context.Context, dims []DimensionRow) (int, error) {
	w.mu.Lock()
	w.dimensionCount += len(dims)
	w.mu.Unlock()
	return len(dims), nil
}

// SaveOneCPrices counts and discards.
func (w *DiscardWriter) SaveOneCPrices(_ context.Context, prices []PriceRow, _ string) (int, error) {
	w.mu.Lock()
	w.priceCount += len(prices)
	w.mu.Unlock()
	return len(prices), nil
}

// SavePIMGoods counts and discards.
func (w *DiscardWriter) SavePIMGoods(_ context.Context, items []PIMGoods) (int, error) {
	w.mu.Lock()
	w.pimCount += len(items)
	w.mu.Unlock()
	return len(items), nil
}

// CleanAll is a no-op.
func (w *DiscardWriter) CleanAll(_ context.Context) error {
	w.mu.Lock()
	w.cleanCalled = true
	w.mu.Unlock()
	return nil
}

// Counts returns the accumulated counts (thread-safe).
func (w *DiscardWriter) Counts() (goods, skus, dims, prices, pim int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.goodsCount, w.skuCount, w.dimensionCount, w.priceCount, w.pimCount
}

// CleanWasCalled returns whether CleanAll was called.
func (w *DiscardWriter) CleanWasCalled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanCalled
}
