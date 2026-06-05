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

// OneCRestsRow — type alias for onec.RestsRow (v2 migration).
// Maps to onec_rests table. snapshot_date set by caller.
// Backward-compatible: v1 CLI code using sqlite.OneCRestsRow compiles unchanged.
type OneCRestsRow = onec.RestsRow
