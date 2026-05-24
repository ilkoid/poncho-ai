package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
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

// runDryRun shows update payloads without sending to WB API.
func runDryRun(ctx context.Context, cfg *Config, _ string) error {
	dllog.PrintHeader("fix-card-dimensions-v1.5: dry-run",
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

	updater := cardupdate.NewCardUpdater(db)

	fmt.Printf("Building payloads for %d cards (dry-run)\n\n", len(pending))

	for i, r := range pending {
		item, err := buildDimensionUpdatePayload(ctx, updater, r)
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
	dllog.PrintHeader("fix-card-dimensions-v1.5: apply",
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

	updater := cardupdate.NewCardUpdater(db)

	// Convert staged rows to BatchItems.
	items := make([]cardupdate.BatchItem, len(pending))
	for i, r := range pending {
		items[i] = cardupdate.BatchItem{NmID: r.NmID, VendorCode: r.VendorCode}
	}

	// Build a lookup from nmID to staged data for the buildFn closure.
	stagedMap := make(map[int]stagedDimRow, len(pending))
	for _, r := range pending {
		stagedMap[r.NmID] = r
	}

	fmt.Printf("Applying %d cards (batch=%d)\n", len(pending), cfg.WBUpdate.BatchSize)

	result, err := cardupdate.ApplyBatch(ctx, client, cfg.WBUpdate, items,
		func(ctx context.Context, item cardupdate.BatchItem) (wb.CardUpdateItem, error) {
			staged := stagedMap[item.NmID]
			return buildDimensionUpdatePayload(ctx, updater, staged)
		},
		cardupdate.WithStatusCallback(func(nmID int, status string, errMsg string) {
			if status == "ok" {
				updateStagingStatus(ctx, db, nmID, "applied", "")
			} else {
				updateStagingStatus(ctx, db, nmID, "error", errMsg)
			}
		}),
	)
	if err != nil {
		return result.Sent, err
	}

	fmt.Printf("\nDone: %d sent, %d failed\n", result.Sent, result.Failed)
	return result.Sent, nil
}
