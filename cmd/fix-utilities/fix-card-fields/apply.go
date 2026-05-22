package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// stagingRow — row read from fix_card_fields_staging for apply.
type stagingRow struct {
	NmID         int
	VendorCode   string
	ChangesJSON  string
	AllCharsJSON string
	SizesJSON    string
}

func runApply(ctx context.Context, db *sql.DB, client *wb.Client, cfg *Config, dryRun bool) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, changes_json, all_chars_json, sizes_json
		FROM fix_card_fields_staging
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

	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v)\n", len(batch), cfg.WBUpdate.BatchSize, dryRun)

	protectedSet := make(map[int]bool, len(cfg.ProtectedCharIDs))
	for _, id := range cfg.ProtectedCharIDs {
		protectedSet[id] = true
	}

	sent := 0
	failed := 0
	bs := cfg.WBUpdate.BatchSize

	for i := 0; i < len(batch); i += bs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + bs
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]

		items := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildSmartMergePayload(ctx, db, r, protectedSet)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				db.ExecContext(ctx, `UPDATE fix_card_fields_staging SET status = 'error', error_msg = ? WHERE nm_id = ?`,
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

		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL,
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst, items)
		if err != nil {
			log.Printf("batch %d-%d: %v (WB: %s)", i+1, end, err, errorText)
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_card_fields_staging SET status = 'error', error_msg = ? WHERE nm_id = ?`,
					err.Error(), r.NmID)
			}
			failed += len(chunk)
		} else {
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_card_fields_staging SET status = 'sent' WHERE nm_id = ?`, r.NmID)
			}
			sent += len(chunk)
			fmt.Printf("  batch %d-%d: OK (%d cards)\n", i+1, end, len(chunk))
		}

		if i+bs < len(batch) {
			time.Sleep(time.Duration(cfg.WBUpdate.IntervalSeconds) * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", sent, failed)
	return nil
}

// buildSmartMergePayload constructs the WB API update payload with full characteristic preservation.
func buildSmartMergePayload(ctx context.Context, db *sql.DB, r stagingRow, protectedSet map[int]bool) (wb.CardUpdateItem, error) {
	brand, title, desc, dims, err := loadCardFields(ctx, db, r.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load card fields: %w", err)
	}

	// Parse staged data.
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

	// Build changes map: char_id → replace_value string.
	changesMap := make(map[int]string, len(changes))
	for _, ch := range changes {
		changesMap[ch.CharID] = ch.New
	}

	var finalChars []wb.CardUpdateCharc
	seenIDs := make(map[int]bool, len(allChars))

	// Pass 1: iterate ALL current characteristics.
	for _, curr := range allChars {
		seenIDs[curr.CharID] = true
		var val any
		if err := json.Unmarshal([]byte(curr.Value), &val); err != nil {
			val = curr.Value
		}
		val = unwrapValue(val)

		if protectedSet[curr.CharID] {
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
		} else if newVal, exists := changesMap[curr.CharID]; exists {
			convertedValue := convertCharValue(newVal, curr.Value)
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: convertedValue})
		} else {
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
		}
	}

	// Pass 2: add new fields that were absent from the card.
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
	}, nil
}

// unwrapValue extracts scalar from single-element JSON arrays: [3.0] → 3, [2.5] → 2.5, ["text"] → "text", [true] → true.
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

// convertCharValue converts a string replacement value to match the type of the current JSON value.
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
		return generated // Boolean replacement stays as string (WB will parse it)
	case string:
		// String value in characteristic should be returned as string array for WB API
		return stringToCharArray(generated)
	case []any:
		return stringToCharArray(generated)
	default:
		return stringToCharArray(generated)
	}
}

// stringToCharArray splits a comma-separated string into []string for WB API.
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
