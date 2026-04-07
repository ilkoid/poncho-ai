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
}

// OneCSKU — lightweight input struct for batch save.
// Maps to onec_goods_sku table.
type OneCSKU struct {
	SKUGUID  string
	GUID     string // FK → onec_goods.guid
	Barcode  string
	Size     string
	NDS      int
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
}
