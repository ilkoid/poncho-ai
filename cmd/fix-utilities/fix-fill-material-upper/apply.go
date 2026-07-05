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

const batchSize = 30

type applyRow struct {
	nmID        int
	vendorCode  string
	mappedValue string
	charID      int
}

func runApply(ctx context.Context, db *sql.DB, client *wb.Client, dryRun bool) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, mapped_value, char_id
		FROM fix_material_upper
		WHERE status = 'new' AND mapped_value != 'UNMAPPED'
		ORDER BY nm_id
	`)
	if err != nil {
		return fmt.Errorf("query staged rows: %w", err)
	}
	defer rows.Close()

	var batch []applyRow
	for rows.Next() {
		var r applyRow
		if err := rows.Scan(&r.nmID, &r.vendorCode, &r.mappedValue, &r.charID); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	if len(batch) == 0 {
		fmt.Println("No rows to apply. Run --stage first or check status/mapped_value.")
		return nil
	}

	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v)\n", len(batch), batchSize, dryRun)

	sent := 0
	failed := 0
	updater := cardupdate.NewCardUpdater(db)

	for i := 0; i < len(batch); i += batchSize {
		end := i + batchSize
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]

		items := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildMaterialPayload(ctx, updater, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.nmID, err)
				db.ExecContext(ctx, `UPDATE fix_material_upper SET status = 'error', error_msg = ? WHERE nm_id = ?`,
					err.Error(), r.nmID)
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
			fmt.Printf("\n--- Batch %d-%d (%d cards) ---\n%s\n", i+1, end, len(chunk), payload)
			sent += len(chunk)
			continue
		}

		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL, 8, 2, items)
		if err != nil {
			log.Printf("batch %d-%d: %v (WB: %s)", i+1, end, err, errorText)
			// Mark individual cards as error
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_material_upper SET status = 'error', error_msg = ? WHERE nm_id = ?`,
					err.Error(), r.nmID)
			}
			failed += len(chunk)
		} else {
			for _, r := range chunk {
				db.ExecContext(ctx, `UPDATE fix_material_upper SET status = 'sent' WHERE nm_id = ?`, r.nmID)
			}
			sent += len(chunk)
			fmt.Printf("  batch %d-%d: OK (%d cards)\n", i+1, end, len(chunk))
		}

		// Rate limit: ~8 req/min → ~7.5s between batches
		if i+batchSize < len(batch) {
			time.Sleep(8 * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", sent, failed)
	return nil
}

// buildMaterialPayload строит ПОЛНЫЙ CardUpdateItem с одной мутированной
// характеристикой — «Материал верха» (char_id берётся из staging-строки).
// Инвариант безопасного rewrite:
// LoadFullCard (все поля) → ToUpdateItem (полный payload) → мутация только материала.
// Частичный payload {NmID, Characteristics} обнулил бы карточку — WB делает полную замену.
func buildMaterialPayload(ctx context.Context, u *cardupdate.CardUpdater, r applyRow) (wb.CardUpdateItem, error) {
	card, err := u.LoadFullCard(ctx, r.nmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load full card nm_id=%d: %w", r.nmID, err)
	}
	item := cardupdate.ToUpdateItem(card)
	for i, c := range item.Characteristics {
		if c.ID == r.charID {
			item.Characteristics[i].Value = r.mappedValue
			return item, nil
		}
	}
	item.Characteristics = append(item.Characteristics, wb.CardUpdateCharc{ID: r.charID, Value: r.mappedValue})
	return item, nil
}
