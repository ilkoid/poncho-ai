package main

import (
	"context"
	"database/sql"
	"fmt"
)

// runStage rebuilds the staging table from confirmed penalties + current card dims.
// Returns (pending, skipped) counts. Status is set by classifyFix: 'pending' for real
// under-declarations (will be rewritten), 'skipped' for exact matches and over-declared
// cards (no risk). Skipped cards are NOT written to the audit CSV — the CSV is a
// change log (what was rewritten), and skipped means "nothing changed". Their count
// still appears in the RUN summary line; the staging table keeps the per-row status.
func runStage(ctx context.Context, db *sql.DB, cfg *Config, audit *Auditor) (pending, skipped int, err error) {
	if err := initStagingSchema(ctx, db); err != nil {
		return 0, 0, err
	}

	totalPenalized, err := countPenalizedNmIDs(ctx, db, cfg.Filter)
	if err != nil {
		return 0, 0, err
	}
	if totalPenalized == 0 {
		// No confirmed penalties. WIPE staging so a subsequent --apply/--auto can't
		// rewrite cards from STALE 'pending' rows — a prior run interrupted by a WB
		// validation error (runApply `break`) leaves some rows 'pending', and without
		// this clear they'd be applied on the next cron tick for penalties that no
		// longer exist. Staging is working state, not history (audit CSV keeps the
		// chronicle); wiping is the correct "nothing to do" semantics.
		if _, err := db.ExecContext(ctx, "DELETE FROM fix_penalties_dims_staging"); err != nil {
			return 0, 0, fmt.Errorf("clear staging (0 penalties): %w", err)
		}
		fmt.Println("stage: 0 confirmed penalties — staging cleared, nothing to apply")
		fmt.Println("       (if unexpected: run download-wb-cards + download-wb-penalties-v2 to populate fixer.db)")
		return 0, 0, nil
	}

	rows, err := fetchStageCandidates(ctx, db, cfg.Filter)
	if err != nil {
		return 0, 0, err
	}

	pending, skipped, err = upsertStaging(ctx, db, rows, nowUTC())
	if err != nil {
		return 0, 0, err
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

// showDiff prints staged rows that are actionable: pending (will be rewritten),
// applied (rewritten this cycle) and error (failed, needs attention). Skipped cards
// (exact match or over-declared — the majority, no change) are omitted from the
// listing to keep the diff scannable; their count appears in the summary line.
func showDiff(ctx context.Context, db *sql.DB, f ArticleFilter) error {
	pending, err := selectByStatus(ctx, db, "pending", f)
	if err != nil {
		return err
	}
	applied, err := selectByStatus(ctx, db, "applied", f)
	if err != nil {
		return err
	}
	errored, err := selectByStatus(ctx, db, "error", f)
	if err != nil {
		return err
	}
	counts, err := stagingCounts(ctx, db, f)
	if err != nil {
		return err
	}

	rows := append(append(pending, applied...), errored...)
	skipped := counts["skipped"]

	if len(rows) == 0 {
		fmt.Printf("no changes pending — %d staged card(s) all skipped (exact match or over-declared)\n", skipped)
		return nil
	}

	fmt.Printf("=== fix-penalties-dims: diff — %d actionable (%d pending, %d applied, %d error); %d skipped (hidden) ===\n",
		len(rows), len(pending), len(applied), len(errored), skipped)
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
	return nil
}
