// Package onec implements the v2 downloader domain for 1C/PIM data.
//
// Business logic lives here (Downloader + Run); CLI in cmd/ is a thin driver.
// Storage adapters implement the Writer interface (SQLite, PostgreSQL).
// API data fetching implements the Source interface (HTTPSource, MockSource).
package onec

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Domain types — storage-facing DTOs (no JSON tags)
// ---------------------------------------------------------------------------

// Good maps to onec_goods table (69 columns).
type Good struct {
	GUID              string
	Article           string
	Name              string
	NameIM            string
	Description       string
	Brand             string
	Type              string
	Category          string
	CategoryLevel1    string
	CategoryLevel2    string
	Sex               string
	Season            string
	Composition       string
	CompositionLining string
	Color             string
	Collection        string
	CountryOfOrigin   string
	Weight            float64
	SizeRange         string
	TnvedCodes        string // JSON array serialized by caller
	BusinessLine      string // JSON array serialized by caller
	IsSale            bool
	IsNew             bool
	ModelStatus       string
	Date              string

	// Dimensions & Weight (cm / grams) — raw API values
	Length     float64
	Wideness   float64
	Height     float64
	WeightSKUG float64 // grams

	// Certificate
	Certificate       string
	CertificateType   string
	HasCertificate    bool
	CertificateBegin  string
	CertificateEnd    string
	CertificateNumber string

	// Dates
	ApprovalDate     string
	DateOfProduction string
	DateOfReceipt    string
	PPSDate          string

	// Seasons & Collections
	CollectionSeason    string
	CollectionYear      string
	LookSeason          string
	OptCollectionSeason string
	OptCollectionYear   string
	ProductionSeason    string
	ProductionYear      string

	// Categories
	CategoryLevel1Name string
	CategoryLevel2Name string

	// Product attributes
	Age             string
	FigureFeatures  string
	Licensor        string
	MainCapture     string
	Markirovka      string
	ModelHeight     string
	RatioHeat       string
	Recommendations string
	SizeOnModel     string
	Tag             string
	QuantityBarCode int

	// Boolean flags
	IsAdult             bool
	IsArticleBlocked    bool
	IsExcludeFromSite   bool
	IsExclusive         bool
	IsGenuineLeather    bool
	IsModelCancelled    bool
	IsNewCollection     bool
	IsNotRequireIroning bool
	IsPPS               bool
	IsYaPriceListOpt    bool
}

// SKU maps to onec_goods_sku table (9 columns).
type SKU struct {
	SKUGUID  string
	GUID     string // FK → onec_goods.guid
	Barcode  string
	Size     string
	NDS      int
	Length   float64 // cm
	Wideness float64 // cm
	Height   float64 // cm
	WeightSKUG float64 // grams
}

// DimensionRow maps to onec_dimensions table (10 columns).
// Stores per-SKU weight-dimension data with unit conversions applied.
type DimensionRow struct {
	GoodGUID  string
	SKUGUID   string
	GoodName  string
	SizeName  string
	LengthDM  float64
	WidthDM   float64
	HeightDM  float64
	WeightKG  float64
	VolumeCM3 float64
	Source    string // "api" or "xls"
}

// PriceRow maps to onec_prices table (6 columns).
// snapshot_date is provided by caller, not embedded.
type PriceRow struct {
	GoodGUID  string
	TypeGUID  string
	TypeName  string
	Price     float64
	SpecPrice float64
}

// PIMGoods maps to pim_goods table (27 columns).
type PIMGoods struct {
	Identifier        string
	Enabled           bool
	Family            string
	Categories        string // JSON array serialized by caller
	ProductType       string
	Sex               string
	Season            string
	Color             string
	FilterColor       string
	WbNmID            int
	YearCollection    int
	MenuProductType   string
	MenuAge           string
	AgeCategory       string
	Composition       string
	Naznacenie        string
	Minicollection    string
	BrandCountry      string
	CountryManufacture string
	SizeTable         string
	FeaturesCare      string
	Description       string
	Name              string
	Updated           string
	WildberriesLength  float64
	WildberriesWidth   float64
	WildberriesHeight  float64
}

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// Source fetches data from 1C/PIM APIs.
// Implemented by HTTPSource (real APIs) and MockSource (--mock).
type Source interface {
	// FetchGoods fetches goods, SKUs, and dimension data from 1C Goods API.
	// Returns accumulated slices (streaming decode internally).
	FetchGoods(ctx context.Context, goodsURL string) (
		goods []Good, skus []SKU, dims []DimensionRow, err error)

	// FetchPrices fetches price data from 1C Prices API.
	// Returns accumulated slice (streaming decode internally).
	FetchPrices(ctx context.Context, pricesURL string) (
		prices []PriceRow, err error)

	// FetchPIMGoods fetches product attributes from PIM Goods API.
	// Returns accumulated slice (streaming decode internally).
	FetchPIMGoods(ctx context.Context, pimURL string) (
		items []PIMGoods, err error)
}

// Writer is the persistence interface for 1C/PIM data.
// Declared here (consumer, Rule 6), implemented by storage adapters.
type Writer interface {
	// Step 1: Goods + SKUs + Dimensions
	SaveGoods(ctx context.Context, goods []Good) (int, error)
	SaveSKUs(ctx context.Context, skus []SKU) (int, error)
	SaveDimensions(ctx context.Context, dims []DimensionRow) (int, error)

	// Step 2: Prices (prefixed to avoid collision with prices.PricesWriter.SavePrices)
	SaveOneCPrices(ctx context.Context, prices []PriceRow, snapshotDate string) (int, error)

	// Step 3: PIM
	SavePIMGoods(ctx context.Context, items []PIMGoods) (int, error)

	// CleanAll wipes all 1C/PIM tables (for --clean flag).
	CleanAll(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// Options & Result
// ---------------------------------------------------------------------------

// DownloadOptions configures a Downloader run.
type DownloadOptions struct {
	// GoodsURL is the 1C Goods API endpoint (with basic auth in URL).
	GoodsURL string

	// PricesURL is the 1C Prices API endpoint (with basic auth in URL).
	PricesURL string

	// PIMURL is the PIM Goods API endpoint (with basic auth in URL).
	PIMURL string

	// SnapshotDate for price rows (YYYY-MM-DD), set by caller.
	SnapshotDate string

	// Clean wipes all 1C/PIM tables before loading.
	Clean bool

	// DryRun skips all DB writes.
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a download run.
type DownloadResult struct {
	GoodsCount     int
	SKUCount       int
	DimensionCount int
	PriceCount     int
	PIMCount       int
	Duration       time.Duration
	StepErrors     []StepError
}

// HasErrors returns true if any step failed.
func (r *DownloadResult) HasErrors() bool {
	return len(r.StepErrors) > 0
}

// StepError records a non-fatal error from one download step.
type StepError struct {
	Step string // "goods", "prices", "pim"
	Err  error
}
