package sqlite

import (
	"github.com/ilkoid/poncho-ai/pkg/funnelagg"
)

// Compile-time assertion: SQLiteSalesRepository implements funnelagg.Writer.
// Methods already exist on SQLiteSalesRepository with matching signatures:
//   - SaveFunnelAggregatedBatch (funnel_agg.go:174)
//   - GetFunnelAggregatedCount  (funnel_agg.go:347)
//   - GetDistinctNmIDCount       (promotion_repo.go — queries sales table)
var _ funnelagg.Writer = (*SQLiteSalesRepository)(nil)
