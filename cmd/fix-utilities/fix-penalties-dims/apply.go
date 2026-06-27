package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// buildPenaltiesDimPayload builds a COMPLETE CardUpdateItem with only the
// dimensions replaced by the WB measurement. The safe-rewrite invariant:
// LoadFullCard (all fields) → ToUpdateItem (full payload) → mutate L/W/H only.
// WeightBrutto is preserved from the loaded card (penalties data has no weight).
func buildPenaltiesDimPayload(ctx context.Context, u *cardupdate.CardUpdater, r stagedRow) (wb.CardUpdateItem, error) {
	card, err := u.LoadFullCard(ctx, r.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load full card: %w", err)
	}
	item := cardupdate.ToUpdateItem(card)
	item.Dimensions = &wb.CardDimensions{
		Length:       r.NewLength,
		Width:        r.NewWidth,
		Height:       r.NewHeight,
		WeightBrutto: card.Dimensions.WeightBrutto, // preserved — no omitempty on the field
		IsValid:      true,
	}
	return item, nil
}

// runApply sends pending dimension updates to WB API (or prints payloads in dry-run).
// Custom loop (NOT cardupdate.ApplyBatch) so we can read-after-write per batch and
// STOP on the first WB validation error. Transport errors mark the chunk 'error'
// and continue; only WB *validation* errors halt the run.
func runApply(ctx context.Context, db *sql.DB, client *wb.Client, cfg *Config, audit *Auditor, dryRun bool) error {
	rows, err := selectPending(ctx, db, cfg.Filter)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No pending rows. Run --stage (or --auto) first.")
		return nil
	}

	bs := cfg.WBUpdate.BatchSize
	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v)\n", len(rows), bs, dryRun)

	updater := cardupdate.NewCardUpdater(db)
	sent, failed := 0, 0

	for i := 0; i < len(rows); i += bs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + bs
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]
		batchNo := i/bs + 1

		items := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildPenaltiesDimPayload(ctx, updater, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				_ = updateStagingStatus(ctx, db, r.NmID, "error", err.Error())
				if audit != nil {
					audit.Error(r, err.Error())
				}
				failed++
				continue
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			continue
		}

		if dryRun {
			payload, _ := json.MarshalIndent(items, "", "  ")
			fmt.Printf("\n--- Batch %d (%d cards) ---\n%s\n", batchNo, len(items), payload)
			sent += len(items)
			continue
		}

		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL,
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst, items)
		if err != nil {
			log.Printf("batch %d: %v (WB: %s)", batchNo, err, errorText)
			for _, r := range chunk {
				_ = updateStagingStatus(ctx, db, r.NmID, "error", err.Error())
				if audit != nil {
					audit.Error(r, fmt.Sprintf("UpdateCards: %v (WB: %s)", err, errorText))
				}
			}
			failed += len(chunk)
		} else {
			for _, r := range chunk {
				_ = updateStagingStatus(ctx, db, r.NmID, "applied", "")
				if audit != nil {
					audit.Fix(r)
				}
			}
			sent += len(chunk)
			fmt.Printf("  batch %d: OK (%d cards)\n", batchNo, len(chunk))

			if err := checkWBErrors(ctx, client, cfg, chunk, batchNo, audit); err != nil {
				log.Printf("  STOPPING: %v", err)
				break
			}
		}

		if i+bs < len(rows) && cfg.WBUpdate.IntervalSeconds > 0 {
			time.Sleep(time.Duration(cfg.WBUpdate.IntervalSeconds) * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", sent, failed)
	if audit != nil {
		audit.Run(fmt.Sprintf("apply: sent=%d failed=%d dry_run=%v", sent, failed, dryRun))
	}
	return nil
}

// checkWBErrors queries the WB error list after a successful UpdateCards batch and
// matches errors by vendor_code. The error list is global (no per-card filter), so
// a lingering error from a prior run can re-trigger the stop — documented behavior.
// Returns nil on clean; returns an error (halting the run) if validation errors match.
func checkWBErrors(ctx context.Context, client *wb.Client, cfg *Config, chunk []stagedRow, batchNo int, audit *Auditor) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		log.Printf("  WARN: error list query failed: %v (non-fatal)", err)
		return nil
	}

	batchVC := make(map[string]int, len(chunk))
	for _, r := range chunk {
		batchVC[r.VendorCode] = r.NmID
	}

	found := make(map[string][]string)
	for _, item := range items {
		for vc, errs := range item.Errors {
			if _, ok := batchVC[vc]; ok {
				found[vc] = append(found[vc], errs...)
			}
		}
	}

	if len(found) == 0 {
		if audit != nil {
			audit.WBOK(batchNo, len(chunk))
		}
		log.Printf("  batch %d: no validation errors", batchNo)
		return nil
	}

	count := 0
	for vc, errs := range found {
		nmID := batchVC[vc]
		for _, e := range errs {
			if audit != nil {
				audit.WBError(batchNo, vc, nmID, e)
			}
			log.Printf("  WB ERROR vendor_code=%s nm_id=%d: %q", vc, nmID, e)
			count++
		}
	}
	if audit != nil {
		audit.WBStop(batchNo, count)
	}
	return fmt.Errorf("batch %d: %d WB validation errors across %d vendor_codes", batchNo, count, len(found))
}

// runCheck queries the WB error list and prints recent card validation errors.
func runCheck(ctx context.Context, client *wb.Client, cfg *Config) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		return fmt.Errorf("get card errors: %w", err)
	}
	if len(items) == 0 {
		fmt.Println("No errors in WB error list.")
		return nil
	}

	total := 0
	for _, item := range items {
		for vc, errs := range item.Errors {
			for _, e := range errs {
				fmt.Printf("  WB ERROR vendor_code=%s: %s\n", vc, e)
				total++
			}
		}
	}
	fmt.Printf("\nTotal: %d error batches, %d individual errors\n", len(items), total)
	if total > 0 {
		return fmt.Errorf("WB error list contains %d errors", total)
	}
	return nil
}
