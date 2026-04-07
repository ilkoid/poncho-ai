package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// OneCClient fetches data from 1C/PIM APIs.
type OneCClient struct {
	httpClient *http.Client
}

// NewOneCClient creates a client with reasonable timeouts for large payloads.
func NewOneCClient() *OneCClient {
	return &OneCClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // Large files (228MB PIM) need generous timeout
		},
	}
}

// FetchGoods fetches and saves 1C goods (products + SKUs) using streaming decode.
// Returns (goodsCount, skuCount, error).
//
// Optimization: json.Decoder streams the response body — constant memory usage
// regardless of payload size. Each good is decoded and saved immediately,
// avoiding a single 26K-element slice in memory.
func (c *OneCClient) FetchGoods(ctx context.Context, apiURL string, repo *sqlite.SQLiteSalesRepository) (int, int, error) {
	body, err := c.fetchBody(ctx, apiURL)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch goods: %w", err)
	}
	defer body.Close()

	dec := json.NewDecoder(body)

	// Read opening '['
	if err := expectDelim(dec, '['); err != nil {
		return 0, 0, err
	}

	goodsBatch := make([]sqlite.OneCGood, 0, 500)
	var skuBatch []sqlite.OneCSKU
	totalGoods := 0
	totalSKUs := 0

	for dec.More() {
		var raw OneCGood
		if err := dec.Decode(&raw); err != nil {
			return totalGoods, totalSKUs, fmt.Errorf("decode good #%d: %w", totalGoods+1, err)
		}

		// Convert to storage type
		goodsBatch = append(goodsBatch, sqlite.OneCGood{
			GUID:              raw.GUID,
			Article:           raw.Article,
			Name:              raw.Name,
			NameIM:            raw.NameIM,
			Description:       raw.Description,
			Brand:             raw.Brand,
			Type:              raw.Type,
			Category:          raw.Category,
			CategoryLevel1:    raw.CategoryLevel1,
			CategoryLevel2:    raw.CategoryLevel2,
			Sex:               raw.Sex,
			Season:            raw.Season,
			Composition:       raw.Composition,
			CompositionLining: raw.CompositionLining,
			Color:             raw.Color,
			Collection:        raw.Collection,
			CountryOfOrigin:   raw.CountryOfOrigin,
			Weight:            raw.Weight,
			SizeRange:         raw.SizeRange,
			TnvedCodes:        toJSONStrings(raw.TnvedCodes),
			BusinessLine:      toJSONStrings(raw.BusinessLine),
			IsSale:            raw.Sale,
			IsNew:             raw.New,
			ModelStatus:       raw.ModelStatus,
			Date:              raw.Date,
		})

		// Collect SKUs from this good
		for _, s := range raw.SKUs {
			skuBatch = append(skuBatch, sqlite.OneCSKU{
				SKUGUID: s.GUID,
				GUID:    raw.GUID,
				Barcode: s.Barcode,
				Size:    s.Size,
				NDS:     s.NDS,
			})
		}

		// Flush batches when goods reach capacity
		if len(goodsBatch) >= 500 {
			n, err := repo.SaveOneCGoods(ctx, goodsBatch)
			if err != nil {
				return totalGoods, totalSKUs, err
			}
			totalGoods += n

			if len(skuBatch) > 0 {
				sn, err := repo.SaveOneCSKUs(ctx, skuBatch)
				if err != nil {
					return totalGoods, totalSKUs, err
				}
				totalSKUs += sn
			}

			goodsBatch = goodsBatch[:0]
			skuBatch = skuBatch[:0]
		}
	}

	// Flush remaining
	if len(goodsBatch) > 0 {
		n, err := repo.SaveOneCGoods(ctx, goodsBatch)
		if err != nil {
			return totalGoods, totalSKUs, err
		}
		totalGoods += n

		if len(skuBatch) > 0 {
			sn, err := repo.SaveOneCSKUs(ctx, skuBatch)
			if err != nil {
				return totalGoods, totalSKUs, err
			}
			totalSKUs += sn
		}
	}

	return totalGoods, totalSKUs, nil
}

// FetchPrices fetches and saves 1C prices using streaming decode.
// Returns (priceRows, productsCount, error).
//
// Optimization: prices API returns 26K products × 25 price types = ~660K rows.
// json.Decoder processes one product at a time, extracts price rows,
// and saves in batches of 500 rows. Memory stays constant at ~500 rows.
func (c *OneCClient) FetchPrices(ctx context.Context, apiURL string, snapshotDate string, repo *sqlite.SQLiteSalesRepository) (int, int, error) {
	body, err := c.fetchBody(ctx, apiURL)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch prices: %w", err)
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	if err := expectDelim(dec, '['); err != nil {
		return 0, 0, err
	}

	priceBatch := make([]sqlite.OneCPriceRow, 0, 500)
	totalRows := 0
	totalProducts := 0

	for dec.More() {
		var raw OneCPriceItem
		if err := dec.Decode(&raw); err != nil {
			return totalRows, totalProducts, fmt.Errorf("decode price item #%d: %w", totalProducts+1, err)
		}
		totalProducts++

		for _, p := range raw.Prices {
			priceBatch = append(priceBatch, sqlite.OneCPriceRow{
				GoodGUID:  raw.GoodGUID,
				TypeGUID:  p.TypeGUID,
				TypeName:  p.TypeName,
				Price:     p.Price,
				SpecPrice: raw.SpecPrice,
			})
		}

		// Flush when batch is full
		if len(priceBatch) >= 500 {
			n, err := repo.SaveOneCPrices(ctx, priceBatch, snapshotDate)
			if err != nil {
				return totalRows, totalProducts, err
			}
			totalRows += n
			priceBatch = priceBatch[:0]
		}
	}

	// Flush remaining
	if len(priceBatch) > 0 {
		n, err := repo.SaveOneCPrices(ctx, priceBatch, snapshotDate)
		if err != nil {
			return totalRows, totalProducts, err
		}
		totalRows += n
	}

	return totalRows, totalProducts, nil
}

// FetchPIMGoods fetches and saves PIM goods using streaming decode.
// Returns (savedCount, error).
//
// Optimization: PIM API returns 228MB JSON with 25K products × 109 attributes.
// json.Decoder processes one product at a time. High-value attributes are
// extracted into typed columns; the full values dict is preserved as values_json.
func (c *OneCClient) FetchPIMGoods(ctx context.Context, pimURL string, repo *sqlite.SQLiteSalesRepository) (int, error) {
	body, err := c.fetchBody(ctx, pimURL)
	if err != nil {
		return 0, fmt.Errorf("fetch pim goods: %w", err)
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	if err := expectDelim(dec, '['); err != nil {
		return 0, err
	}

	batch := make([]sqlite.PIMGoodsRow, 0, 500)
	totalSaved := 0

	for dec.More() {
		var raw PIMGood
		if err := dec.Decode(&raw); err != nil {
			return totalSaved, fmt.Errorf("decode pim good #%d: %w", totalSaved+1, err)
		}

		row := sqlite.PIMGoodsRow{
			Identifier:         raw.Identifier,
			Enabled:            raw.Enabled,
			Family:             raw.Family,
			Categories:         toJSONStrings(raw.Categories),
			ProductType:        pimString(raw.Values, "Product_type"),
			Sex:                pimStringSlice(raw.Values, "sex"),
			Season:             pimStringSlice(raw.Values, "Season"),
			Color:              pimStringSlice(raw.Values, "color"),
			FilterColor:        pimStringSlice(raw.Values, "filter_color"),
			WbNmID:             pimInt(raw.Values, "wildberries"),
			YearCollection:     pimInt(raw.Values, "Year_of_collection"),
			MenuProductType:    pimStringSlice(raw.Values, "menu_product_type"),
			MenuAge:            pimStringSlice(raw.Values, "menu_age"),
			AgeCategory:        pimStringSlice(raw.Values, "Age_category"),
			Composition:        pimString(raw.Values, "Composition"),
			Naznacenie:         pimStringSlice(raw.Values, "naznacenie"),
			Minicollection:     pimStringSlice(raw.Values, "Minicollection"),
			BrandCountry:       pimStringSlice(raw.Values, "Brand_country"),
			CountryManufacture: pimStringSlice(raw.Values, "Country_of_manufacture"),
			SizeTable:          pimStringSlice(raw.Values, "Size_table"),
			FeaturesCare:       pimString(raw.Values, "Features_of_care"),
			Description:        pimString(raw.Values, "description"),
			Name:               pimString(raw.Values, "name"),
			Updated:            raw.Updated,
			ValuesJSON:         valuesToJSON(raw.Values),
		}

		batch = append(batch, row)

		if len(batch) >= 500 {
			n, err := repo.SavePIMGoods(ctx, batch)
			if err != nil {
				return totalSaved, err
			}
			totalSaved += n
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		n, err := repo.SavePIMGoods(ctx, batch)
		if err != nil {
			return totalSaved, err
		}
		totalSaved += n
	}

	return totalSaved, nil
}

// fetchBody makes a GET request and returns the response body for streaming.
// Supports basic auth embedded in URL (user:pass@host).
func (c *OneCClient) fetchBody(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Extract basic auth from URL if present
	if u.User != nil {
		password, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	return resp.Body, nil
}

// --- PIM value extraction helpers ---

// pimString extracts a scalar string from PIM values.
// PIM format: [{"locale": null, "scope": null, "data": "value"}]
func pimString(values map[string][]PIMValue, key string) string {
	vs, ok := values[key]
	if !ok || len(vs) == 0 {
		return ""
	}
	switch v := vs[0].Data.(type) {
	case string:
		return v
	default:
		return ""
	}
}

// pimStringSlice extracts a string (or string array) from PIM values.
// If data is a string array, joins with comma. If scalar string, returns as-is.
func pimStringSlice(values map[string][]PIMValue, key string) string {
	vs, ok := values[key]
	if !ok || len(vs) == 0 {
		return ""
	}
	switch v := vs[0].Data.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

// pimInt extracts an integer from PIM values.
// Handles both numeric and string representations (e.g., "9242327" for nmID).
func pimInt(values map[string][]PIMValue, key string) int {
	vs, ok := values[key]
	if !ok || len(vs) == 0 {
		return 0
	}
	switch v := vs[0].Data.(type) {
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	default:
		return 0
	}
}

// valuesToJSON serializes the full values map for storage.
// Preserves all 109 attributes for future use.
func valuesToJSON(values map[string][]PIMValue) string {
	if len(values) == 0 {
		return "{}"
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// toJSONStrings serializes a string slice to JSON.
func toJSONStrings(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(items)
	return string(b)
}

// expectDelim reads and validates the opening JSON delimiter.
func expectDelim(dec *json.Decoder, expected json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read opening token: %w", err)
	}
	d, ok := t.(json.Delim)
	if !ok || d != expected {
		return fmt.Errorf("expected '%c', got %v", expected, t)
	}
	return nil
}
