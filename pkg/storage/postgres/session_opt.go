package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// BulkLoadSessionParams returns pgx RuntimeParams optimized for bulk INSERT workloads.
// These are sent as SET statements at connection time — no extra round-trip.
//
// Parameters:
//   - work_mem: 64MB (default 4MB causes disk spill for large multi-row INSERTs)
//   - synchronous_commit: off (2-5x faster COMMIT for re-downloadable data)
//   - statement_timeout: 5min safety net against stuck queries
func BulkLoadSessionParams() map[string]string {
	return map[string]string{
		"work_mem":           "64MB",
		"synchronous_commit": "off",
		"statement_timeout":  "300000", // ms
	}
}

// AfterConnectBulkLoad is an AfterConnect hook for bulk load sessions.
// Sets effective_cache_size as a planner hint (not allocatable memory).
func AfterConnectBulkLoad(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, "SET effective_cache_size = '2GB'")
	return err
}
