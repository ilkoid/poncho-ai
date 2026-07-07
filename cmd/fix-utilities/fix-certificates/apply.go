package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)


// stagingRow — row read from fix_certificates_staging for apply.
type stagingRow struct {
	NmID        int
	VendorCode  string
	ChangesJSON string
	AllCharsJSON string
	SizesJSON   string
}

func runApply(ctx context.Context, db *sql.DB, client *wb.Client, cfg cardupdate.WBUpdateConfig, dryRun bool) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, changes_json, all_chars_json, sizes_json
		FROM fix_certificates_staging
		WHERE status = 'new' AND changes_json != '[]'
		ORDER BY nm_id
	`)
	if err != nil {
		return fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	var batch []stagingRow
	for rows.Next() {
		var r stagingRow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.ChangesJSON, &r.AllCharsJSON, &r.SizesJSON); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	if len(batch) == 0 {
		fmt.Println("No rows to apply. Run --stage first or check status/changes_json.")
		return nil
	}

	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v)\n", len(batch), cfg.BatchSize, dryRun)

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
			item, err := buildSmartMergePayload(ctx, db, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				db.ExecContext(ctx, `UPDATE fix_certificates_staging SET status = 'error', error_msg = ? WHERE nm_id = ?`,
					err.Error(), r.NmID)
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

		body, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL, cfg.RatePerMin, cfg.RateBurst, items)
			if body != "" {
				log.Printf("  WB RESPONSE body: %s", body)
			}
		if err != nil {
			log.Printf("batch %d-%d: %v (WB: %s)", i+1, end, err, errorText)
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_certificates_staging SET status = 'error', error_msg = ? WHERE nm_id = ?`,
					err.Error(), r.NmID)
			}
			failed += len(chunk)
		} else {
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_certificates_staging SET status = 'sent' WHERE nm_id = ?`, r.NmID)
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

// buildSmartMergePayload constructs the full WB API update payload with complete card preservation.
// This is the CRITICAL function — it ensures ALL card fields are sent, not just the changed ones.
func buildSmartMergePayload(ctx context.Context, db *sql.DB, r stagingRow) (wb.CardUpdateItem, error) {
	brand, title, desc, dims, kizMarked, err := loadCardFields(ctx, db, r.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load card fields: %w", err)
	}

	var changes []changeEntry
	if err := json.Unmarshal([]byte(r.ChangesJSON), &changes); err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("parse changes: %w", err)
	}
	var allChars []CardChar
	if err := json.Unmarshal([]byte(r.AllCharsJSON), &allChars); err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("parse all_chars: %w", err)
	}
	var sizes []wb.CardSize
	if err := json.Unmarshal([]byte(r.SizesJSON), &sizes); err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("parse sizes: %w", err)
	}

	// Build changes map: char_id → new value.
	// Empty New means "remove this characteristic" (used by --fix-type).
	changesMap := make(map[int]string, len(changes))
	for _, ch := range changes {
		changesMap[ch.CharID] = ch.New
	}

	var finalChars []wb.CardUpdateCharc
	seenIDs := make(map[int]bool, len(allChars))

	// Pass 1: iterate ALL current characteristics.
	// Certificate fields (15001136-15001138) are in changesMap — replace or remove them.
	// All other fields — preserve as-is.
	for _, curr := range allChars {
		seenIDs[curr.CharID] = true

		newVal, exists := changesMap[curr.CharID]
		if exists && newVal == "" {
			// Removal: skip this characteristic entirely (--fix-type swap).
			continue
		}

		var val any
		if err := json.Unmarshal([]byte(curr.Value), &val); err != nil {
			val = curr.Value
		}
		val = unwrapValue(val)

		if exists {
			convertedValue := convertCharValue(newVal, curr.Value)
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: convertedValue})
		} else {
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
		}
	}

	// Pass 2: add new certificate characteristics that were absent from the card.
	for charID, newVal := range changesMap {
		if !seenIDs[charID] {
			finalChars = append(finalChars, wb.CardUpdateCharc{
				ID:    charID,
				Value: stringToCharArray(newVal),
			})
		}
	}

	return wb.CardUpdateItem{
		NmID:            r.NmID,
		VendorCode:      r.VendorCode,
		Brand:           brand,
		Title:           title,
		Description:     desc,
		Dimensions:      &dims,
		Characteristics: finalChars,
		Sizes:           sizes,
		KizMarked:       kizMarked,
	}, nil
}

// --- Helpers (copied from fix-card-fields per Rule 6: cmd/ cannot import cmd/) ---

func unwrapValue(val any) any {
	arr, ok := val.([]any)
	if !ok || len(arr) != 1 {
		return val
	}
	switch v := arr[0].(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v)
		}
		return v
	case string, bool:
		return v
	default:
		return val
	}
}

func convertCharValue(generated string, currentJSON string) any {
	var current any
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return stringToCharArray(generated)
	}

	unwrapped := unwrapValue(current)
	switch unwrapped.(type) {
	case int, float64:
		if n, err := strconv.Atoi(generated); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(generated, 64); err == nil {
			return f
		}
		return generated
	case bool:
		return generated
	case string:
		return stringToCharArray(generated)
	case []any:
		return stringToCharArray(generated)
	default:
		return stringToCharArray(generated)
	}
}

func stringToCharArray(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return []string{s}
	}
	return result
}

// checkWBErrors queries WB error list after a successful UpdateCards batch.
// Matches returned errors against vendor_codes from the batch.
// Returns error if validation errors are found for our cards.
func checkWBErrors(ctx context.Context, client *wb.Client, cfg cardupdate.WBUpdateConfig, batch []stagingRow) error {
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

	// Build vendor_code → nm_id map from batch.
	batchVC := make(map[string]int, len(batch))
	for _, r := range batch {
		batchVC[r.VendorCode] = r.NmID
	}

	// Search for errors matching our vendor_codes.
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

	// Save error report.
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

	// Save full report.
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
