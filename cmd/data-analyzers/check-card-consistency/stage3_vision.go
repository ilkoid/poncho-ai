package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/progress"
)

// runStage3 выполняет Vision анализ рискованных карточек (этап 3).
func runStage3(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	// Загружаем nm_id карточек с text_has_discrepancy = 1
	nmIDs, err := results.LoadTextDiscrepancies(ctx)
	if err != nil {
		return fmt.Errorf("load text discrepancies: %w", err)
	}
	if len(nmIDs) == 0 {
		log.Println("Stage 3: no text discrepancies found. Run stage 1 first.")
		return nil
	}

	log.Printf("Stage 3: %d cards with text discrepancies", len(nmIDs))

	if cfg.Analysis.Limit > 0 && len(nmIDs) > cfg.Analysis.Limit {
		nmIDs = nmIDs[:cfg.Analysis.Limit]
		log.Printf("  Limit: %d cards", len(nmIDs))
	}

	log.Printf("Stage 3: Vision analysis for %d cards with %s", len(nmIDs), cfg.Vision.Model)

	// Загружаем карточки для получения title/description (только нужные nm_id)
	cards, err := source.LoadCardsForAnalysis(ctx, FilterConfig{NmIDs: nmIDs})
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	cardMap := make(map[int]CardData, len(cards))
	for _, c := range cards {
		cardMap[c.NmID] = c
	}

	// Загружаем характеристики
	charsMap, err := source.LoadCharacteristics(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	// Загружаем фото
	photosMap, err := source.LoadPhotos(ctx, nmIDs, cfg.Vision.PhotosPerCard)
	if err != nil {
		return fmt.Errorf("load photos: %w", err)
	}

	tracker := progress.NewCLITrackerWithConfig(progress.CLITrackerConfig{
		Total:  len(nmIDs),
		Prefix: "Stage 3",
	})

	var (
		wg         sync.WaitGroup
		semaphore  = make(chan struct{}, cfg.Analysis.Concurrency)
		discrepant int
		errors     int
		mu         sync.Mutex
	)

	for _, nmID := range nmIDs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		card, ok := cardMap[nmID]
		if !ok {
			log.Printf("  WARN: nm_id=%d not found in source cards, skipping", nmID)
			continue
		}

		photoURLs := photosMap[nmID]
		if len(photoURLs) == 0 {
			log.Printf("  WARN: nm_id=%d has no photos, skipping", nmID)
			continue
		}

		wg.Add(1)
		go func(nmID int, card CardData) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			chars := charsMap[nmID]
			photoURLs := photosMap[nmID]

			start := time.Now()
			productType, attrs, summary, hasDisc, err := analyzeCardVision(ctx, provider, card, chars, photoURLs, cfg.Vision.ModelConfig, cfg.Prompts)
			dur := time.Since(start)

			if err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR nm_id=%d: %v", nmID, err)
				return
			}

			attrsJSON, _ := json.Marshal(attrs)
			photosJSON, _ := json.Marshal(photoURLs)

			if err := results.SaveVisionAnalysis(ctx, nmID, productType, string(attrsJSON), string(photosJSON), summary, hasDisc); err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR save nm_id=%d: %v", nmID, err)
				return
			}
			if err := results.MarkVisionDone(ctx, nmID); err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR mark vision done nm_id=%d: %v", nmID, err)
				return
			}

			tracker.Update(1)

			mu.Lock()
			n := tracker.Current()
			if hasDisc {
				discrepant++
			}
			discStr := "ok"
			if hasDisc {
				discStr = "DISCREPANCY"
			}
			log.Printf("  [%d/%d] %s | %.1fs | %s | ETA %s",
				n, tracker.Total(),
				time.Now().Format("15:04:05"),
				dur.Seconds(),
				discStr,
				tracker.ETA())
			mu.Unlock()
		}(nmID, card)
	}

	wg.Wait()
	tracker.Done()

	log.Printf("Stage 3 complete: %d checked, %d confirmed discrepancies (%.0f%%), %d errors",
		tracker.Current(), discrepant,
		percent(int64(discrepant), int64(tracker.Current())),
		errors)

	return nil
}

// analyzeCardVision отправляет карточку с фото в Vision модель.
func analyzeCardVision(ctx context.Context, provider llm.Provider, card CardData, chars []CardChar, photoURLs []string, modelCfg ModelConfig, prompts PromptConfig) (string, map[string]string, string, bool, error) {
	messages := buildVisionMessages(card.Title, card.Description, chars, photoURLs, prompts)

	resp, err := generateWithRetry(ctx, provider, messages,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	)
	if err != nil {
		return "", nil, "", false, fmt.Errorf("Vision LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", nil, "", false, fmt.Errorf("empty Vision response")
	}

	var result visionAnalysisResult
	if err := json.Unmarshal([]byte(extractJSON(content)), &result); err != nil {
		return "", nil, truncate(content, 200), false, nil
	}

	return result.ProductType, result.Attributes, result.Summary, result.Discrepancy, nil
}
