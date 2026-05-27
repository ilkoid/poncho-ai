package sqlite

// OneCGood — lightweight input struct for batch save.
// Maps to onec_goods table.
type OneCGood struct {
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
	TnvedCodes        string // JSON array
	BusinessLine      string // JSON array
	IsSale            bool
	IsNew             bool
	ModelStatus       string
	Date              string

	// Dimensions & Weight (cm / grams)
	Length     float64
	Wideness   float64
	Height     float64
	WeightSKUG float64 // grams! _g suffix — divide by 1000 for WB kg

	// Certificate
	Certificate      string
	CertificateType  string
	HasCertificate   bool
	CertificateBegin string
	CertificateEnd   string
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
	IsAdult            bool
	IsArticleBlocked   bool
	IsExcludeFromSite  bool
	IsExclusive        bool
	IsGenuineLeather   bool
	IsModelCancelled   bool
	IsNewCollection    bool
	IsNotRequireIroning bool
	IsPPS              bool
	IsYaPriceListOpt   bool
}

// OneCSKU — lightweight input struct for batch save.
// Maps to onec_goods_sku table.
type OneCSKU struct {
	SKUGUID  string
	GUID     string // FK → onec_goods.guid
	Barcode  string
	Size     string
	NDS      int
	// Per-SKU dimensions (cm / grams, matching API)
	Length     float64
	Wideness   float64
	Height     float64
	WeightSKUG float64
}

// OneCPriceRow — lightweight input struct for batch save.
// Maps to onec_prices table. snapshot_date set by caller.
type OneCPriceRow struct {
	GoodGUID  string
	TypeGUID  string
	TypeName  string
	Price     float64
	SpecPrice float64
}

// PIMGoodsRow — lightweight input struct for batch save.
// Maps to pim_goods table.
type PIMGoodsRow struct {
	Identifier        string
	Enabled           bool
	Family            string
	Categories        string // JSON array
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
	ValuesJSON        string // Full values dict as JSON
	WildberriesLength float64
	WildberriesWidth  float64
	WildberriesHeight float64
}

// OneCRestsRow — lightweight input struct for batch save.
// Maps to onec_rests table. snapshot_date set by caller.
type OneCRestsRow struct {
	GoodGUID    string
	SKUGUID     string
	StorageGUID string
	StorageName string
	Stock       int
	Reserv      int
	Free        int
	FirstStage  bool
}

// OneCDimensionRow — lightweight input struct for batch save.
// Maps to onec_dimensions table. Stores per-SKU weight-dimension data from 1C WMS.
type OneCDimensionRow struct {
	GoodGUID  string
	SKUGUID   string
	GoodName  string
	SizeName  string
	LengthDM  float64
	WidthDM   float64
	HeightDM  float64
	WeightKG  float64
	VolumeCM3 float64
	Source    string // "xls" or "api"
}
