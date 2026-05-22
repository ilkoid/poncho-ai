package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// loadCardCore loads card-level fields from cards table.
func loadCardCore(ctx context.Context, db *sql.DB, nmID int) (brand, title, desc string, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(brand,''), COALESCE(title,''), COALESCE(description,'')
		FROM cards WHERE nm_id = ?
	`, nmID).Scan(&brand, &title, &desc)
	return
}

// loadCharacteristics loads all characteristics for a card as CardUpdateCharc slice.
// The DB stores one row per (nm_id, char_id) with json_value as the value field.
func loadCharacteristics(ctx context.Context, db *sql.DB, nmID int) ([]wb.CardUpdateCharc, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT char_id, json_value FROM card_characteristics WHERE nm_id = ?
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query chars: %w", err)
	}
	defer rows.Close()

	var chars []wb.CardUpdateCharc
	for rows.Next() {
		var charID int
		var jsonValue string
		if err := rows.Scan(&charID, &jsonValue); err != nil {
			return nil, fmt.Errorf("scan char: %w", err)
		}

		// json_value is a JSON array like ["текст"] or [42]
		var val any
		if err := json.Unmarshal([]byte(jsonValue), &val); err != nil {
			// Fallback: treat as string
			val = jsonValue
		}
		// Unwrap single-element arrays: [42] → 42
		if arr, ok := val.([]any); ok && len(arr) == 1 {
			val = arr[0]
		}
		chars = append(chars, wb.CardUpdateCharc{ID: charID, Value: val})
	}

	return chars, rows.Err()
}

// loadSizes loads all sizes for a card from card_sizes table.
func loadSizes(ctx context.Context, db *sql.DB, nmID int) ([]wb.CardSize, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT chrt_id, tech_size, wb_size, skus_json FROM card_sizes WHERE nm_id = ?
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query sizes: %w", err)
	}
	defer rows.Close()

	var sizes []wb.CardSize
	for rows.Next() {
		var s wb.CardSize
		var skusJSON string
		if err := rows.Scan(&s.ChrtID, &s.TechSize, &s.WBSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan size: %w", err)
		}
		if err := json.Unmarshal([]byte(skusJSON), &s.Skus); err != nil {
			s.Skus = nil
		}
		sizes = append(sizes, s)
	}

	return sizes, rows.Err()
}

// buildDimensionUpdatePayload creates a complete CardUpdateItem with updated dimensions.
func buildDimensionUpdatePayload(ctx context.Context, db *sql.DB, staged stagedDimRow) (wb.CardUpdateItem, error) {
	brand, title, desc, err := loadCardCore(ctx, db, staged.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load card core: %w", err)
	}

	chars, err := loadCharacteristics(ctx, db, staged.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load chars: %w", err)
	}

	sizes, err := loadSizes(ctx, db, staged.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load sizes: %w", err)
	}

	return wb.CardUpdateItem{
		NmID:        staged.NmID,
		VendorCode:  staged.VendorCode,
		Brand:       brand,
		Title:       title,
		Description: desc,
		Dimensions: &wb.CardDimensions{
			Length:       staged.NewLength,
			Width:        staged.NewWidth,
			Height:       staged.NewHeight,
			WeightBrutto: staged.NewWeight,
			IsValid:      true,
		},
		Characteristics: chars,
		Sizes:           sizes,
	}, nil
}

// runDryRun shows update payloads without sending to WB API.
func runDryRun(ctx context.Context, cfg *Config, _ string) error {
	dllog.PrintHeader("fix-card-dimensions: dry-run",
		dllog.HeaderField{Key: "DB", Value: cfg.DBPath},
	)

	repo, err := openDB(cfg.DBPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	db := repo.DB()
	pending, err := loadPendingStagedRows(ctx, db)
	if err != nil {
		return fmt.Errorf("load pending: %w", err)
	}

	if len(pending) == 0 {
		dllog.Log("no pending cards in staging table")
		return nil
	}

	fmt.Printf("Building payloads for %d cards (dry-run)\n\n", len(pending))

	for i, r := range pending {
		item, err := buildDimensionUpdatePayload(ctx, db, r)
		if err != nil {
			fmt.Printf("--- Card %d/%d nm_id=%d: ERROR: %v ---\n", i+1, len(pending), r.NmID, err)
			continue
		}

		payload, _ := json.MarshalIndent(item, "", "  ")
		fmt.Printf("--- Card %d/%d: %s (nm_id=%d) ---\n", i+1, len(pending), r.VendorCode, r.NmID)
		fmt.Printf("%s\n\n", payload)
	}

	dllog.Done(0, "dry-run complete: %d payloads generated", len(pending))
	return nil
}

// runApply sends staged dimension updates to WB API.
func runApply(ctx context.Context, cfg *Config, apiKey string) (int, error) {
	dllog.PrintHeader("fix-card-dimensions: apply",
		dllog.HeaderField{Key: "DB", Value: cfg.DBPath},
	)

	repo, err := openDB(cfg.DBPath)
	if err != nil {
		return 0, err
	}
	defer repo.Close()

	db := repo.DB()
	pending, err := loadPendingStagedRows(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("load pending: %w", err)
	}

	if len(pending) == 0 {
		dllog.Log("no pending cards in staging table")
		return 0, nil
	}

	client := wb.New(apiKey)
	client.SetRateLimit("fix-card-dimensions",
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)

	fmt.Printf("Applying %d cards (batch=%d)\n", len(pending), cfg.WBUpdate.BatchSize)

	sent := 0
	failed := 0
	bs := cfg.WBUpdate.BatchSize

	for i := 0; i < len(pending); i += bs {
		select {
		case <-ctx.Done():
			return sent, ctx.Err()
		default:
		}

		end := min(i+bs, len(pending))
		chunk := pending[i:end]

		items := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildDimensionUpdatePayload(ctx, db, r)
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

		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL,
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst, items)
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
		}

		if i+bs < len(pending) {
			time.Sleep(8 * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", sent, failed)
	return sent, nil
}
