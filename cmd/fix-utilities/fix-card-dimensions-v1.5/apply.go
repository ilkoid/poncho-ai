package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// buildDimensionUpdatePayload creates a complete CardUpdateItem with updated dimensions.
func buildDimensionUpdatePayload(ctx context.Context, u *cardupdate.CardUpdater, staged stagedDimRow) (wb.CardUpdateItem, error) {
	card, err := u.LoadFullCard(ctx, staged.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load full card: %w", err)
	}

	item := cardupdate.ToUpdateItem(card)
	item.Dimensions = &wb.CardDimensions{
		Length:       staged.NewLength,
		Width:        staged.NewWidth,
		Height:       staged.NewHeight,
		WeightBrutto: staged.NewWeight,
		IsValid:      true,
	}
	return item, nil
}

// runApply sends staged dimension updates to WB API (or prints payloads in dry-run mode).
func runApply(ctx context.Context, db *sql.DB, client *wb.Client, cfg cardupdate.WBUpdateConfig, dryRun bool) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, old_length, old_width, old_height, old_weight,
		       new_length, new_width, new_height, new_weight, status, error_msg
		FROM fix_card_dimensions_staging
		WHERE status = 'pending'
		ORDER BY nm_id
	`)
	if err != nil {
		return fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	var batch []stagedDimRow
	for rows.Next() {
		var r stagedDimRow
		var errMsg sql.NullString
		if err := rows.Scan(
			&r.NmID, &r.VendorCode,
			&r.OldLength, &r.OldWidth, &r.OldHeight, &r.OldWeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight, &r.NewWeight,
			&r.Status, &errMsg,
		); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if errMsg.Valid {
			r.ErrorMsg = errMsg.String
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	if len(batch) == 0 {
		fmt.Println("No rows to apply. Run --stage first or check status.")
		return nil
	}

	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v)\n", len(batch), cfg.BatchSize, dryRun)

	updater := cardupdate.NewCardUpdater(db)
	sent := 0
	failed := 0

	for i := 0; i < len(batch); i += cfg.BatchSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + cfg.BatchSize
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]

		items := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildDimensionUpdatePayload(ctx, updater, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				updateStagingStatus(ctx, db, r.NmID, "error", err.Error())
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
			fmt.Printf("\n--- Batch %d-%d (%d cards) ---\n%s\n", i+1, end, len(items), payload)
			sent += len(items)
			continue
		}

		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL, cfg.RatePerMin, cfg.RateBurst, items)
		if err != nil {
			log.Printf("batch %d-%d: %v (WB: %s)", i+1, end, err, errorText)
			for _, r := range chunk {
				updateStagingStatus(ctx, db, r.NmID, "error", err.Error())
			}
			failed += len(chunk)
		} else {
			for _, r := range chunk {
				updateStagingStatus(ctx, db, r.NmID, "applied", "")
			}
			sent += len(chunk)
			fmt.Printf("  batch %d-%d: OK (%d cards)\n", i+1, end, len(chunk))

			if err := checkWBErrors(ctx, client, cfg, chunk); err != nil {
				log.Printf("  STOPPING: %v", err)
				break
			}
		}

		if i+cfg.BatchSize < len(batch) {
			time.Sleep(time.Duration(cfg.IntervalSeconds) * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", sent, failed)
	return nil
}

// checkWBErrors queries WB error list after a successful UpdateCards batch.
func checkWBErrors(ctx context.Context, client *wb.Client, cfg cardupdate.WBUpdateConfig, batch []stagedDimRow) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.RatePerMin, cfg.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		log.Printf("  WARN: error list query failed: %v (non-fatal)", err)
		return nil
	}

	batchVC := make(map[string]int, len(batch))
	for _, r := range batch {
		batchVC[r.VendorCode] = r.NmID
	}

	var foundErrors map[string][]string
	for _, item := range items {
		for vc, errs := range item.Errors {
			if _, ok := batchVC[vc]; ok {
				if foundErrors == nil {
					foundErrors = make(map[string][]string)
				}
				foundErrors[vc] = append(foundErrors[vc], errs...)
			}
		}
	}

	if len(foundErrors) == 0 {
		log.Printf("  WBREPLY: no validation errors in error list")
		return nil
	}

	nmIDs := make([]int, 0, len(foundErrors))
	vcs := make([]string, 0, len(foundErrors))
	for vc := range foundErrors {
		nmIDs = append(nmIDs, batchVC[vc])
		vcs = append(vcs, vc)
	}

	report := struct {
		Timestamp   string              `json:"timestamp"`
		BatchNmIDs  []int               `json:"batch_nm_ids"`
		VendorCodes []string            `json:"vendor_codes"`
		WBErrors    map[string][]string `json:"wb_errors"`
	}{
		Timestamp:   time.Now().Format(time.RFC3339),
		BatchNmIDs:  nmIDs,
		VendorCodes: vcs,
		WBErrors:    foundErrors,
	}

	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	data, _ := json.MarshalIndent(report, "", "  ")
	if writeErr := os.WriteFile(filename, data, 0644); writeErr != nil {
		log.Printf("  ERROR: failed to write error report: %v", writeErr)
	} else {
		log.Printf("  Error report saved: %s", filename)
	}

	for vc, errs := range foundErrors {
		nmID := batchVC[vc]
		for _, e := range errs {
			log.Printf("  WB ERROR vendor_code=%s nm_id=%d: %q", vc, nmID, e)
		}
	}

	return fmt.Errorf("WB validation errors found for %d vendor_codes — see %s", len(foundErrors), filename)
}

// runCheck queries the WB error list and prints all card validation errors.
func runCheck(ctx context.Context, client *wb.Client, cfg cardupdate.WBUpdateConfig) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.RatePerMin, cfg.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		return fmt.Errorf("get card errors: %w", err)
	}

	if len(items) == 0 {
		fmt.Println("No errors found in WB error list.")
		return nil
	}

	totalErrors := 0
	for i, item := range items {
		fmt.Printf("\n── Batch %d (UUID: %s) ──\n", i+1, item.BatchUUID)
		fmt.Printf("  Vendor codes: %v\n", item.VendorCodes)
		for vc, sub := range item.Subjects {
			fmt.Printf("  Subject: %s → %s (id=%d)\n", vc, sub.Name, sub.ID)
		}
		for vc, errs := range item.Errors {
			for _, e := range errs {
				fmt.Printf("  ERROR vendor_code=%s: %s\n", vc, e)
				totalErrors++
			}
		}
	}
	fmt.Printf("\nTotal: %d error batches, %d individual errors\n", len(items), totalErrors)

	allVCs := make(map[string]bool)
	allErrors := make(map[string][]string)
	for _, item := range items {
		for _, vc := range item.VendorCodes {
			allVCs[vc] = true
		}
		for vc, errs := range item.Errors {
			allErrors[vc] = append(allErrors[vc], errs...)
		}
	}
	report := struct {
		Timestamp   string              `json:"timestamp"`
		VendorCodes []string            `json:"vendor_codes"`
		WBErrors    map[string][]string `json:"wb_errors"`
		RawResponse []wb.CardErrorItem  `json:"raw_response"`
	}{
		Timestamp:   time.Now().Format(time.RFC3339),
		RawResponse: items,
		WBErrors:    allErrors,
	}
	for vc := range allVCs {
		report.VendorCodes = append(report.VendorCodes, vc)
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("  WARN: failed to write report: %v", err)
	} else {
		log.Printf("Full report saved: %s", filename)
	}

	if totalErrors > 0 {
		return fmt.Errorf("WB error list contains %d errors", totalErrors)
	}
	return nil
}
