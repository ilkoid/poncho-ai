package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// runStage rebuilds the staging table from confirmed penalties + current card dims.
// Returns (pending, skipped) counts. Cards whose dims already match the WB
// measurement are staged as 'skipped' (idempotent — they will not be rewritten).
func runStage(ctx context.Context, pool *pgxpool.Pool, cfg *Config, audit *Auditor) (pending, skipped int, err error) {
	if err := initStagingSchema(ctx, pool); err != nil {
		return 0, 0, err
	}

	totalPenalized, err := countPenalizedNmIDs(ctx, pool, cfg.Filter)
	if err != nil {
		return 0, 0, err
	}

	rows, err := fetchStageCandidates(ctx, pool, cfg.Filter)
	if err != nil {
		return 0, 0, err
	}

	pending, skipped, err = upsertStaging(ctx, pool, rows, nowUTC())
	if err != nil {
		return 0, 0, err
	}

	// Audit the skipped (already-matching) cards so the manager sees them too.
	if audit != nil {
		for _, r := range rows {
			if dimsMatch(r.OldLength, r.OldWidth, r.OldHeight, r.NewLength, r.NewWidth, r.NewHeight) {
				audit.Skip(r)
			}
		}
	}

	withoutCard := totalPenalized - len(rows)
	fmt.Printf("stage: %d penalized nm_ids, %d staged (%d pending, %d skipped), %d without matching card\n",
		totalPenalized, len(rows), pending, skipped, withoutCard)
	if audit != nil {
		audit.Run(fmt.Sprintf("stage: penalized=%d staged=%d pending=%d skipped=%d without_card=%d",
			totalPenalized, len(rows), pending, skipped, withoutCard))
	}
	return pending, skipped, nil
}

// showDiff prints every staged row's before→after dimensions for review.
func showDiff(ctx context.Context, pool *pgxpool.Pool) error {
	all, err := selectByStatus(ctx, pool, "pending")
	if err != nil {
		return err
	}
	applied, err := selectByStatus(ctx, pool, "applied")
	if err != nil {
		return err
	}
	skipped, err := selectByStatus(ctx, pool, "skipped")
	if err != nil {
		return err
	}
	errored, err := selectByStatus(ctx, pool, "error")
	if err != nil {
		return err
	}

	rows := append(append(append(all, applied...), skipped...), errored...)
	if len(rows) == 0 {
		fmt.Println("no staged cards found")
		return nil
	}

	fmt.Printf("=== fix-penalties-dims: diff (%d cards) ===\n\n", len(rows))
	fmt.Printf("%-12s %-18s %-16s | %-16s | %s\n", "nm_id", "vendor_code", "old (L×W×H)", "new (L×W×H)", "status")
	for _, r := range rows {
		old := fmt.Sprintf("%g×%g×%g", r.OldLength, r.OldWidth, r.OldHeight)
		new := fmt.Sprintf("%g×%g×%g", r.NewLength, r.NewWidth, r.NewHeight)
		extra := ""
		if r.Status == "error" && r.ErrorMsg != "" {
			extra = " [" + r.ErrorMsg + "]"
		}
		fmt.Printf("%-12d %-18s %-16s | %-16s | %s%s\n", r.NmID, r.VendorCode, old, new, r.Status, extra)
	}
	fmt.Printf("\nTotal: %d cards (pending=%d, applied=%d, skipped=%d, error=%d)\n",
		len(rows), len(all), len(applied), len(skipped), len(errored))
	return nil
}
