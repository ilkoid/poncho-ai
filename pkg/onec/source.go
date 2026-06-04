package onec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// HTTPSource — real API adapter (streaming JSON decode)
// ---------------------------------------------------------------------------

const httpTimeout = 10 * time.Minute // Large files (228MB PIM) need generous timeout

// HTTPSource fetches data from 1C/PIM HTTP APIs.
type HTTPSource struct {
	httpClient *http.Client
}

// NewHTTPSource creates a source with reasonable timeouts for large payloads.
func NewHTTPSource() *HTTPSource {
	return &HTTPSource{
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// FetchGoods fetches goods, SKUs, and dimension data from 1C Goods API.
// Uses streaming JSON decode — constant memory regardless of payload size.
func (s *HTTPSource) FetchGoods(ctx context.Context, goodsURL string) ([]Good, []SKU, []DimensionRow, error) {
	body, err := s.fetchBody(ctx, goodsURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetch goods: %w", err)
	}
	defer body.Close()

	br := bufio.NewReaderSize(body, 4096)
	peek, _ := br.Peek(256)
	dec := json.NewDecoder(br)

	if err := expectArrayDelim(dec, peek); err != nil {
		return nil, nil, nil, fmt.Errorf("goods API: %w", err)
	}

	var goods []Good
	var skus []SKU
	var dims []DimensionRow

	for dec.More() {
		var raw apiGood
		if err := dec.Decode(&raw); err != nil {
			return goods, skus, dims, fmt.Errorf("decode good #%d: %w", len(goods)+1, err)
		}

		goods = append(goods, convertGood(&raw))

		// Collect SKUs and extract dimensions from each
		for i := range raw.SKUs {
			s := &raw.SKUs[i]
			skus = append(skus, convertSKU(s, raw.GUID))

			// Build dimension row (source='api').
			// Width/Height swap: API.Wideness = physical height, API.Height = physical width.
			if s.Length > 0 || s.Wideness > 0 || s.Height > 0 || s.WeightSKU > 0 {
				dims = append(dims, DimensionRow{
					GoodGUID:  raw.GUID,
					SKUGUID:   s.GUID,
					GoodName:  raw.Name,
					SizeName:  s.Size,
					LengthDM:  s.Length / 10,    // cm → dm
					WidthDM:   s.Height / 10,    // SWAP: API Height = physical width
					HeightDM:  s.Wideness / 10,  // SWAP: API Wideness = physical height
					WeightKG:  s.WeightSKU / 1000, // g → kg
					VolumeCM3: s.Length * s.Wideness * s.Height,
					Source:    "api",
				})
			}
		}
	}

	return goods, skus, dims, nil
}

// FetchPrices fetches price data from 1C Prices API.
// Returns flattened rows (one per product × price type).
func (s *HTTPSource) FetchPrices(ctx context.Context, pricesURL string) ([]PriceRow, error) {
	body, err := s.fetchBody(ctx, pricesURL)
	if err != nil {
		return nil, fmt.Errorf("fetch prices: %w", err)
	}
	defer body.Close()

	br := bufio.NewReaderSize(body, 4096)
	peek, _ := br.Peek(256)
	dec := json.NewDecoder(br)
	if err := expectArrayDelim(dec, peek); err != nil {
		return nil, fmt.Errorf("prices API: %w", err)
	}

	var prices []PriceRow

	for dec.More() {
		var raw apiPriceItem
		if err := dec.Decode(&raw); err != nil {
			return prices, fmt.Errorf("decode price item #%d: %w", len(prices)+1, err)
		}

		for _, p := range raw.Prices {
			prices = append(prices, PriceRow{
				GoodGUID:  raw.GoodGUID,
				TypeGUID:  p.TypeGUID,
				TypeName:  p.TypeName,
				Price:     p.Price,
				SpecPrice: raw.SpecPrice,
			})
		}
	}

	return prices, nil
}

// FetchPIMGoods fetches product attributes from PIM Goods API.
// Extracts high-value attributes from the PIM values map into typed columns.
func (s *HTTPSource) FetchPIMGoods(ctx context.Context, pimURL string) ([]PIMGoods, error) {
	body, err := s.fetchBody(ctx, pimURL)
	if err != nil {
		return nil, fmt.Errorf("fetch pim goods: %w", err)
	}
	defer body.Close()

	br := bufio.NewReaderSize(body, 4096)
	peek, _ := br.Peek(256)
	dec := json.NewDecoder(br)
	if err := expectArrayDelim(dec, peek); err != nil {
		return nil, fmt.Errorf("pim API: %w", err)
	}

	var items []PIMGoods

	for dec.More() {
		var raw apiPIMGood
		if err := dec.Decode(&raw); err != nil {
			return items, fmt.Errorf("decode pim good #%d: %w", len(items)+1, err)
		}

		items = append(items, PIMGoods{
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
			WildberriesLength:  pimFloat(raw.Values, "wildberries_length"),
			WildberriesWidth:   pimFloat(raw.Values, "wildberries_width"),
			WildberriesHeight:  pimFloat(raw.Values, "wildberries_height"),
		})
	}

	return items, nil
}

// ---------------------------------------------------------------------------
// API → domain type converters
// ---------------------------------------------------------------------------

// convertGood maps an API response good to the storage DTO.
func convertGood(raw *apiGood) Good {
	return Good{
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
		// Dimensions & Weight
		Length:     raw.Length,
		Wideness:   raw.Wideness,
		Height:     raw.Height,
		WeightSKUG: raw.WeightSKU,
		// Certificate
		Certificate:       raw.Certificate,
		CertificateType:   raw.CertificateType,
		HasCertificate:    raw.HasCertificate,
		CertificateBegin:  raw.CertificateBegin,
		CertificateEnd:    raw.CertificateEnd,
		CertificateNumber: raw.CertificateNumber,
		// Dates
		ApprovalDate:     raw.ApprovalDate,
		DateOfProduction: raw.DateOfProduction,
		DateOfReceipt:    raw.DateOfReceipt,
		PPSDate:          raw.PPSDate,
		// Seasons & Collections
		CollectionSeason:    raw.CollectionSeason,
		CollectionYear:      raw.CollectionYear,
		LookSeason:          raw.LookSeason,
		OptCollectionSeason: raw.OptCollectionSeason,
		OptCollectionYear:   raw.OptCollectionYear,
		ProductionSeason:    raw.ProductionSeason,
		ProductionYear:      raw.ProductionYear,
		// Categories
		CategoryLevel1Name: raw.CategoryLevel1Name,
		CategoryLevel2Name: raw.CategoryLevel2Name,
		// Product attributes
		Age:             raw.Age,
		FigureFeatures:  raw.FigureFeatures,
		Licensor:        raw.Licensor,
		MainCapture:     raw.MainCapture,
		Markirovka:      raw.Markirovka,
		ModelHeight:     raw.ModelHeight,
		RatioHeat:       raw.RatioHeat,
		Recommendations: raw.Recommendations,
		SizeOnModel:     raw.SizeOnModel,
		Tag:             raw.Tag,
		QuantityBarCode: raw.QuantityBarCode,
		// Boolean flags
		IsAdult:             raw.Adult,
		IsArticleBlocked:    raw.ArticleBlocked,
		IsExcludeFromSite:   raw.ExcludeFromSite,
		IsExclusive:         raw.Exclusive,
		IsGenuineLeather:    raw.GenuineLeather,
		IsModelCancelled:    raw.ModelCancelled,
		IsNewCollection:     raw.NewCollection,
		IsNotRequireIroning: raw.NotRequireIroning,
		IsPPS:               raw.PPS,
		IsYaPriceListOpt:    raw.YaPriceListOpt,
	}
}

// convertSKU maps an API SKU to the storage DTO.
func convertSKU(raw *apiSKU, parentGUID string) SKU {
	return SKU{
		SKUGUID:    raw.GUID,
		GUID:       parentGUID,
		Barcode:    raw.Barcode,
		Size:       raw.Size,
		NDS:        raw.NDS,
		Length:     raw.Length,
		Wideness:   raw.Wideness,
		Height:     raw.Height,
		WeightSKUG: raw.WeightSKU,
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// fetchBody makes a GET request and returns the response body for streaming.
// Supports basic auth embedded in URL (user:pass@host).
func (s *HTTPSource) fetchBody(ctx context.Context, rawURL string) (io.ReadCloser, error) {
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

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	return resp.Body, nil
}

// expectArrayDelim expects a JSON array opening '['.
// When the API returns an object instead (e.g. error response),
// tries to extract an error message. Includes raw body preview for diagnostics.
func expectArrayDelim(dec *json.Decoder, peek []byte) error {
	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read opening token: %w", err)
	}
	d, ok := t.(json.Delim)
	if ok && d == '[' {
		return nil
	}
	if ok && d == '{' {
		var raw struct {
			Detail string `json:"detail"`
			Error  string `json:"error"`
			Msg    string `json:"message"`
		}
		_ = dec.Decode(&raw)
		hint := raw.Detail
		if hint == "" {
			hint = raw.Error
		}
		if hint == "" {
			hint = raw.Msg
		}
		if hint != "" {
			return fmt.Errorf("API returned error object: %s", hint)
		}
		preview := string(peek)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return fmt.Errorf("API returned JSON object instead of array (format changed?). Response preview: %s", preview)
	}
	return fmt.Errorf("expected '[', got %v", t)
}

// ---------------------------------------------------------------------------
// PIM attribute extraction helpers
// ---------------------------------------------------------------------------

// pimString extracts a scalar string from PIM values.
// PIM format: [{"locale": null, "scope": null, "data": "value"}]
func pimString(values map[string][]apiValue, key string) string {
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
func pimStringSlice(values map[string][]apiValue, key string) string {
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
func pimInt(values map[string][]apiValue, key string) int {
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

// pimFloat extracts a float64 from PIM values.
// Handles float64, int, and string representations (e.g., "32.0000" for dimensions).
func pimFloat(values map[string][]apiValue, key string) float64 {
	vs, ok := values[key]
	if !ok || len(vs) == 0 {
		return 0
	}
	switch v := vs[0].Data.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}

// toJSONStrings serializes a string slice to JSON.
func toJSONStrings(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(items)
	return string(b)
}
