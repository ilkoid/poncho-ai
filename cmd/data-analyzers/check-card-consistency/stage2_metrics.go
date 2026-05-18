package main

import (
	"context"
	"fmt"
	"log"
)

// runStage2Metrics обновляет метрики в card_analysis из source DB (без LLM).
func runStage2Metrics(ctx context.Context, source *SourceRepo, results *ResultsRepo, cfg CLIConfig) error {
	cards, err := source.LoadCardsForAnalysis(ctx, cfg.Filter)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	if len(cards) == 0 {
		log.Println("No cards found for metrics refresh")
		return nil
	}
	log.Printf("Stage 2: refreshing metrics for %d cards", len(cards))

	inserted, err := results.EnsureRows(ctx, cards)
	if err != nil {
		return fmt.Errorf("ensure rows: %w", err)
	}
	if inserted > 0 {
		log.Printf("  Created %d new rows in card_analysis", inserted)
	}

	n, err := results.BackfillMetrics(ctx, cfg.Source.DBPath)
	if err != nil {
		return fmt.Errorf("backfill metrics: %w", err)
	}
	log.Printf("  Backfilled metrics: %d updates", n)
	log.Printf("Stage 2 complete: %d cards processed", len(cards))
	return nil
}
