package main

// JSON structs for 1C/PIM API responses.
// Local to cmd/ per Rule 6 (pkg/ has no cmd/ imports).

// --- 1C Goods API (/feeds/ones/goods/) ---

// OneCGood represents a product from 1C accounting system.
type OneCGood struct {
	GUID              string     `json:"guid"`
	Article           string     `json:"article"`
	Name              string     `json:"name"`
	NameIM            string     `json:"NameIM"`
	Description       string     `json:"Description"`
	Brand             string     `json:"Brand"`
	Type              string     `json:"Type"`
	Category          string     `json:"Сategory"` // Note: Cyrillic 'С' in API key
	CategoryLevel1    string     `json:"CategoryLevel1"`
	CategoryLevel2    string     `json:"CategoryLevel2"`
	Sex               string     `json:"Sex"`
	Season            string     `json:"Season"`
	Composition       string     `json:"Composition"`
	CompositionLining string     `json:"CompositionLining"`
	Color             string     `json:"Color"`
	Collection        string     `json:"Collection"`
	CountryOfOrigin   string     `json:"CountryOfOrigin"`
	Weight            float64    `json:"Weight"`
	SizeRange         string     `json:"SizeRange"`
	TnvedCodes        []string   `json:"TnvedCodes"`
	BusinessLine      []string   `json:"BusinessLine"`
	Sale              bool       `json:"Sale"`
	New               bool       `json:"New"`
	ModelStatus       string     `json:"ModelStatus"`
	Date              string     `json:"date"`
	SKUs              []OneCSKU  `json:"sku"`
}

// OneCSKU represents a size variant (barcode) of a product.
type OneCSKU struct {
	GUID    string  `json:"guid"`
	Barcode string  `json:"barcode"`
	Size    string  `json:"size"`
	NDS     int     `json:"nds"`
}

// --- 1C Prices API (/feeds/ones/prices/) ---

// OneCPriceItem represents price data for one product.
type OneCPriceItem struct {
	GoodGUID string         `json:"good_guid"`
	Prices   []OneCPrice    `json:"prices"`
	SpecPrice float64       `json:"SpecPrice"`
}

// OneCPrice represents one price type for a product.
type OneCPrice struct {
	TypeGUID string  `json:"type_guid"`
	TypeName string  `json:"type_name"`
	Price    float64 `json:"price"`
}

// --- PIM Goods API (/feeds/pim/goods/) ---

// PIMGood represents a validated product from the PIM system.
type PIMGood struct {
	Identifier string            `json:"identifier"`
	Enabled    bool              `json:"enabled"`
	Family     string            `json:"family"`
	Categories []string          `json:"categories"`
	Values     map[string][]PIMValue `json:"values"`
	Created    string            `json:"created"`
	Updated    string            `json:"updated"`
}

// PIMValue wraps a single attribute value from PIM.
// Format: [{"locale": null, "scope": null, "data": <value>}]
type PIMValue struct {
	Locale *string     `json:"locale"`
	Scope  *string     `json:"scope"`
	Data   any `json:"data"`
}
