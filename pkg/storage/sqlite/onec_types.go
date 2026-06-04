package sqlite

import "github.com/ilkoid/poncho-ai/pkg/onec"

// Type aliases for v1 backward compatibility.
// Domain types now live in pkg/onec/; aliases ensure existing code
// (cmd/data-downloaders/download-1c-data, download-1c-rests) compiles unchanged.

type OneCGood         = onec.Good
type OneCSKU          = onec.SKU
type OneCPriceRow     = onec.PriceRow
type PIMGoodsRow      = onec.PIMGoods
type OneCDimensionRow = onec.DimensionRow

// OneCRestsRow — lightweight input struct for batch save.
// Maps to onec_rests table. snapshot_date set by caller.
// Local to sqlite (rests domain not yet in v2).
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
