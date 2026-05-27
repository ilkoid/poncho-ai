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
	// Dimensions & Weight (cm / grams)
	Length    float64 `json:"Length"`
	Wideness  float64 `json:"Wideness"`
	Height    float64 `json:"Height"`
	WeightSKU float64 `json:"Weight_sku"` // grams!

	// Certificate
	Certificate      string `json:"Certificate"`
	CertificateType  string `json:"CertificateType"`
	HasCertificate   bool   `json:"HasCertificate"`
	CertificateBegin  string `json:"CertificateBegin"`
	CertificateEnd    string `json:"CertificateEnd"`
	CertificateNumber string `json:"CertificateNumber"`

	// Dates
	ApprovalDate    string `json:"ApprovalDate"`
	DateOfProduction string `json:"DateOfProduction"`
	DateOfReceipt   string `json:"DateOfReceipt"`
	PPSDate         string `json:"PPSDate"`

	// Seasons & Collections
	CollectionSeason   string `json:"CollectionSeason"`
	CollectionYear     string `json:"CollectionYear"`
	LookSeason         string `json:"LookSeason"`
	OptCollectionSeason string `json:"OptCollectionSeason"`
	OptCollectionYear  string `json:"OptCollectionYear"`
	ProductionSeason   string `json:"ProductionSeason"`
	ProductionYear     string `json:"ProductionYear"`

	// Categories
	CategoryLevel1Name string `json:"CategoryLevel1Name"`
	CategoryLevel2Name string `json:"CategoryLevel2Name"`

	// Product attributes
	Age            string `json:"Age"`
	FigureFeatures string `json:"FigureFeatures"`
	Licensor       string `json:"Licensor"`
	MainCapture    string `json:"MainCapture"`
	Markirovka     string `json:"Markirovka"`
	ModelHeight    string `json:"ModelHeight"`
	RatioHeat      string `json:"RatioHeat"`
	Recommendations string `json:"Recommendations"`
	SizeOnModel    string `json:"SizeOnModel"`
	Tag            string `json:"Tag"`
	QuantityBarCode int   `json:"QuantityBarCode"`

	// Boolean flags
	Adult            bool `json:"Adult"`
	ArticleBlocked   bool `json:"ArticleBlocked"`
	ExcludeFromSite  bool `json:"ExcludeFromSite"`
	Exclusive        bool `json:"Exclusive"`
	GenuineLeather   bool `json:"GenuineLeather"`
	ModelCancelled   bool `json:"ModelCancelled"`
	NewCollection    bool `json:"NewCollection"`
	NotRequireIroning bool `json:"NotRequireIroning"`
	PPS              bool `json:"PPS"`
	YaPriceListOpt   bool `json:"YaPriceListOpt"`

	SKUs []OneCSKU `json:"sku"`
}

// OneCSKU represents a size variant (barcode) of a product.
type OneCSKU struct {
	GUID    string  `json:"guid"`
	Barcode string  `json:"barcode"`
	Size    string  `json:"size"`
	NDS     int     `json:"nds"`
	// Per-SKU dimensions from API (cm / grams)
	Length    float64 `json:"Length"`
	Wideness  float64 `json:"Wideness"`
	Height    float64 `json:"Height"`
	WeightSKU float64 `json:"Weight_sku"`
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
